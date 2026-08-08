package fileee

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"time"
)

const defaultBaseURL = "https://my.fileee.com"

// defaultTransport liefert einen eigenen *http.Transport (Clone von http.DefaultTransport) mit
// einem zusätzlichen ResponseHeaderTimeout — NIE den globalen http.DefaultTransport selbst
// mutieren, das wäre ein prozessweiter Seiteneffekt auf jeden anderen Code im selben Prozess.
// http.DefaultTransport hat bereits einen Dial-Timeout (30s) und TLSHandshakeTimeout (10s), aber
// KEINEN ResponseHeaderTimeout — eine Verbindung, die erfolgreich aufgebaut wurde, aber danach nie
// eine Response sendet (hängender Fileee-Endpunkt), blockiert damit unbegrenzt, sofern der
// Aufrufer nicht selbst einen Context mit Deadline übergibt (Whole-Codebase-Review Finding I5).
// BEWUSST kein pauschales http.Client.Timeout: das würde die GESAMTE Requestdauer begrenzen und
// große Uploads (Documents.Upload) oder ZIP-Exports (ExportZIP/ExportAll) mitten im Transfer
// abschneiden — bodyProvider (transport.go) ist extra darauf ausgelegt, solche Bodies zu streamen.
// ResponseHeaderTimeout begrenzt dagegen NUR die Time-to-First-Byte der Response-Header, nicht die
// Dauer des nachfolgenden Body-Transfers.
func defaultTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	if t.ResponseHeaderTimeout <= 0 {
		t.ResponseHeaderTimeout = 30 * time.Second
	}
	return t
}

// defaultStaticBaseURL ist der Static-Host, über den geteilte Dokumente als Voll-PDF ausgeliefert
// werden (GET /shares/get/:shareId/:documentId/pdf) — ein ANDERER Host als das API-baseURL.
const defaultStaticBaseURL = "https://static.fileee.com"

type clientConfig struct {
	httpClient    *http.Client
	baseURL       string
	staticBaseURL string
	sessionStore  SessionStore
	rps           float64
	burst         int
	backoff       BackoffPolicy
	logger        *slog.Logger
	userAgent     string
	freshness     time.Duration
}

// Option konfiguriert einen Client bei NewClient oder NewShareClient — beide Konstruktoren
// nehmen dieselben Options entgegen (Auth-bezogene Optionen sind bei NewShareClient wirkungslos,
// siehe dessen Godoc).
type Option func(*clientConfig)

// WithHTTPClient übernimmt Timeout und Transport eines eigenen *http.Client. Der Cookie-Jar wird
// von der Lib gestellt und nicht überschrieben.
func WithHTTPClient(hc *http.Client) Option { return func(c *clientConfig) { c.httpClient = hc } }

// WithBaseURL überschreibt die Basis-URL (Default: https://my.fileee.com), etwa für Tests.
func WithBaseURL(url string) Option { return func(c *clientConfig) { c.baseURL = url } }

// WithStaticBaseURL überschreibt den Static-Host (Default: https://static.fileee.com), über den der
// ShareClient geteilte Voll-PDFs lädt. Nur für den ShareClient relevant, v. a. für Tests.
func WithStaticBaseURL(url string) Option { return func(c *clientConfig) { c.staticBaseURL = url } }

// WithSessionFreshness aktiviert das Session-Freshness-Fenster: nach einem erfolgreichen Verify/
// Login überspringt EnsureSession den user-session-Round-Trip, solange d nicht abgelaufen ist
// (Default 0 = aus, jeder Aufruf verifiziert). Für langlaufende Consumer (fileee-server) sinnvoll,
// zusammen mit StartKeepAlive.
func WithSessionFreshness(d time.Duration) Option {
	return func(c *clientConfig) { c.freshness = d }
}

// WithSessionStore setzt den Persistenz-Store für den Session-Cookie-Jar (Default: Datei im
// Nutzerprofil).
func WithSessionStore(s SessionStore) Option { return func(c *clientConfig) { c.sessionStore = s } }

// WithRateLimit konfiguriert den Token-Bucket: rps Requests pro Sekunde mit der Spitze burst.
func WithRateLimit(rps float64, burst int) Option {
	return func(c *clientConfig) { c.rps = rps; c.burst = burst }
}

// WithBackoff setzt die Retry-Strategie für 429/5xx und Netzwerkfehler.
func WithBackoff(policy BackoffPolicy) Option { return func(c *clientConfig) { c.backoff = policy } }

// WithLogger setzt den Logger der Lib (Default: verwirft alle Ausgaben).
func WithLogger(l *slog.Logger) Option { return func(c *clientConfig) { c.logger = l } }

// WithUserAgent setzt einen Konsumenten-User-Agent (z. B. "paperless-scan-bridge/2.0"). Die
// Lib-Kennung "go-fileee/<version>" wird IMMER angehängt — Fileee sieht Konsument UND Lib.
func WithUserAgent(ua string) Option { return func(c *clientConfig) { c.userAgent = ua } }

// Client ist der Einstiegspunkt der Lib (Umbrella-Spec §3.1). Zustandslos bis auf die
// SessionStore-Referenz (ADR-0001).
type Client struct {
	Documents           *DocumentService
	Tags                ReadService[Tag]
	Companies           ReadService[Company]
	Contacts            WriteService[Contact]
	DocumentTypes       ReadService[DocumentType]
	DocumentTypeSchemes ReadService[DocumentTypeScheme]
	Reminders           ReminderService
	Boxes               BoxService
	Processes           ProcessService
	Conversations       ConversationService

	auth       *authClient
	httpClient *http.Client
	transport  *rateLimitedTransport
	baseURL    string
	logger     *slog.Logger
}

// NewClient erstellt einen Client. Es wird NICHT sofort eingeloggt — der erste Request via
// EnsureSession/Login stellt die Session her (Umbrella-Spec §3.1).
func NewClient(creds Credentials, opts ...Option) (*Client, error) {
	if creds.Username == "" || creds.Password == "" {
		return nil, fmt.Errorf("fileee: Credentials.Username und Password sind Pflichtfelder")
	}
	cfg := &clientConfig{
		baseURL: defaultBaseURL,
		rps:     1,
		burst:   3,
		backoff: NewExponentialBackoff(),
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("fileee: cookie jar: %w", err)
	}

	// base ist der zugrundeliegende RoundTripper, den rateLimitedTransport umwickelt. Wird über
	// WithHTTPClient ein *http.Client mit eigenem Transport übergeben (z. B. für Custom-TLS oder
	// Proxy), MUSS dieser als Basis übernommen werden — sonst wird er beim Wrappen stillschweigend
	// verworfen und WithHTTPClient hätte keinerlei Effekt auf das Transport-Verhalten. Nur wenn der
	// Aufrufer GAR KEINEN eigenen *http.Client übergeben hat, kommt defaultTransport() mit dem
	// defensiven ResponseHeaderTimeout zum Einsatz (Finding I5) — hat der Aufrufer WithHTTPClient
	// genutzt (auch ohne eigenen Transport), hat er sich bewusst für die Kontrolle entschieden und
	// die Lib mischt sich nicht ein.
	base := http.RoundTripper(http.DefaultTransport)
	switch {
	case cfg.httpClient != nil && cfg.httpClient.Transport != nil:
		base = cfg.httpClient.Transport
	case cfg.httpClient == nil:
		base = defaultTransport()
	}

	transport := &rateLimitedTransport{
		base:      base,
		limiter:   newLimiter(cfg.rps, cfg.burst),
		backoff:   cfg.backoff,
		jar:       jar,
		baseURL:   cfg.baseURL,
		userAgent: composeUserAgent(cfg.userAgent),
		logger:    cfg.logger,
	}

	// Die Lib baut ihren EIGENEN *http.Client statt den vom Aufrufer übergebenen zu mutieren — ein
	// Aufrufer-Objekt zu verändern (Transport/Jar überschreiben) ist ein Seiteneffekt, den eine
	// Library nicht auslösen darf (der Aufrufer könnte denselben *http.Client anderweitig nutzen).
	// Der Timeout des Aufrufers wird übernommen, falls einer übergeben wurde.
	hc := &http.Client{
		Transport: transport,
		Jar:       jar,
	}
	if cfg.httpClient != nil {
		hc.Timeout = cfg.httpClient.Timeout
	}

	store := cfg.sessionStore
	if store == nil {
		store = NewFileSessionStore(defaultSessionPath())
	}

	auth := &authClient{hc: hc, baseURL: cfg.baseURL, creds: creds, store: store, freshness: cfg.freshness}
	transport.reauth = auth.reauthenticate

	c := &Client{
		auth:       auth,
		httpClient: hc,
		transport:  transport,
		baseURL:    cfg.baseURL,
		logger:     cfg.logger,
	}
	c.Documents = newDocumentService(c)
	c.Tags = newTagService(c)
	c.Companies = newCompanyService(c)
	c.Contacts = newContactService(c)
	c.DocumentTypes = newDocumentTypeService(c)
	c.DocumentTypeSchemes = newDocumentTypeSchemeService(c)
	c.Reminders = newReminderService(c)
	c.Boxes = newBoxService(c)
	c.Processes = newProcessService(c)
	c.Conversations = newConversationService(c)

	if sess, err := store.Load(context.Background()); err == nil && sess != nil {
		loadCookiesIntoJar(jar, cfg.baseURL, sess.Cookies)
	}

	return c, nil
}

// Login führt einen vollständigen Passwort- (und ggf. TOTP-)Login durch.
func (c *Client) Login(ctx context.Context) error { return c.auth.login(ctx) }

// Logout beendet die Session serverseitig und leert den lokalen Cookie-Jar.
func (c *Client) Logout(ctx context.Context) error { return c.auth.logout(ctx) }

// EnsureSession stellt sicher, dass eine gültige Session besteht, und authentifiziert bei Bedarf neu.
func (c *Client) EnsureSession(ctx context.Context) error { return c.auth.EnsureSession(ctx) }

// AccountStatus liefert Abo- und Lizenzinformationen des Kontos.
func (c *Client) AccountStatus(ctx context.Context) (*AccountStatus, error) {
	return c.auth.accountStatus(ctx)
}

func (c *Client) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("fileee: get request %s: %w", path, err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fileee: get %s: %w", path, err)
	}
	return resp, nil
}

func (c *Client) postJSON(ctx context.Context, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("fileee: post request %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fileee: post %s: %w", path, err)
	}
	return resp, nil
}

func (c *Client) deleteReq(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("fileee: delete request %s: %w", path, err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fileee: delete %s: %w", path, err)
	}
	return resp, nil
}

func (c *Client) putJSON(ctx context.Context, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("fileee: put request %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fileee: put %s: %w", path, err)
	}
	return resp, nil
}

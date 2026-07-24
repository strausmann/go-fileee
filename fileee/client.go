package fileee

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
)

const defaultBaseURL = "https://my.fileee.com"

type clientConfig struct {
	httpClient   *http.Client
	baseURL      string
	sessionStore SessionStore
	rps          float64
	burst        int
	backoff      BackoffPolicy
	logger       *slog.Logger
	userAgent    string
}

type Option func(*clientConfig)

func WithHTTPClient(hc *http.Client) Option  { return func(c *clientConfig) { c.httpClient = hc } }
func WithBaseURL(url string) Option          { return func(c *clientConfig) { c.baseURL = url } }
func WithSessionStore(s SessionStore) Option { return func(c *clientConfig) { c.sessionStore = s } }
func WithRateLimit(rps float64, burst int) Option {
	return func(c *clientConfig) { c.rps = rps; c.burst = burst }
}
func WithBackoff(policy BackoffPolicy) Option { return func(c *clientConfig) { c.backoff = policy } }
func WithLogger(l *slog.Logger) Option        { return func(c *clientConfig) { c.logger = l } }

// WithUserAgent setzt einen Konsumenten-User-Agent (z. B. "paperless-scan-bridge/2.0"). Die
// Lib-Kennung "go-fileee/<version>" wird IMMER angehängt — Fileee sieht Konsument UND Lib.
func WithUserAgent(ua string) Option { return func(c *clientConfig) { c.userAgent = ua } }

// Client ist der Einstiegspunkt der Lib (Umbrella-Spec §3.1). Zustandslos bis auf die
// SessionStore-Referenz (ADR-0001).
type Client struct {
	Documents     *DocumentService
	Tags          ReadService[Tag]
	Companies     ReadService[Company]
	Contacts      WriteService[Contact]
	DocumentTypes ReadService[DocumentType]
	Reminders     ReminderService

	auth       *authClient
	httpClient *http.Client
	transport  *rateLimitedTransport
	baseURL    string
	logger     *slog.Logger
}

// New erstellt einen Client. Es wird NICHT sofort eingeloggt — der erste Request via
// EnsureSession/Login stellt die Session her (Umbrella-Spec §3.1).
func New(creds Credentials, opts ...Option) (*Client, error) {
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
	// verworfen und WithHTTPClient hätte keinerlei Effekt auf das Transport-Verhalten.
	base := http.RoundTripper(http.DefaultTransport)
	if cfg.httpClient != nil && cfg.httpClient.Transport != nil {
		base = cfg.httpClient.Transport
	}

	transport := &rateLimitedTransport{
		base:      base,
		limiter:   newLimiter(cfg.rps, cfg.burst),
		backoff:   cfg.backoff,
		jar:       jar,
		baseURL:   cfg.baseURL,
		userAgent: composeUserAgent(cfg.userAgent),
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

	auth := &authClient{hc: hc, baseURL: cfg.baseURL, creds: creds, store: store}
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
	c.Reminders = newReminderService(c)

	if sess, err := store.Load(context.Background()); err == nil && sess != nil {
		loadCookiesIntoJar(jar, cfg.baseURL, sess.Cookies)
	}

	return c, nil
}

func (c *Client) Login(ctx context.Context) error         { return c.auth.login(ctx) }
func (c *Client) Logout(ctx context.Context) error        { return c.auth.logout(ctx) }
func (c *Client) EnsureSession(ctx context.Context) error { return c.auth.EnsureSession(ctx) }
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

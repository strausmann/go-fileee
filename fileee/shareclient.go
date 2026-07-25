package fileee

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
)

// ShareClient greift OHNE Login auf ein per Share-Link geteiltes Fileee-Dokument zu. Er ist für
// Empfänger gedacht, die nur den Freigabe-Token haben (z. B. ein N8N-Workflow, der einen Link per
// Webhook erhält) und keine Konto-Credentials besitzen.
type ShareClient struct {
	httpClient    *http.Client
	baseURL       string
	staticBaseURL string
	jar           http.CookieJar
}

// NewShareClient erstellt einen credential-losen Client für den Zugriff auf geteilte Dokumente.
// Es gelten dieselben With…-Optionen wie bei New (relevant v. a. WithBaseURL, WithRateLimit,
// WithUserAgent, WithHTTPClient); Auth-bezogene Optionen sind wirkungslos.
func NewShareClient(opts ...Option) *ShareClient {
	cfg := &clientConfig{
		baseURL:       defaultBaseURL,
		staticBaseURL: defaultStaticBaseURL,
		rps:           1,
		burst:         3,
		backoff:       NewExponentialBackoff(),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.staticBaseURL == "" {
		cfg.staticBaseURL = defaultStaticBaseURL
	}
	jar, _ := cookiejar.New(nil)
	// Siehe defaultTransport()-Kommentar (client.go) und New(): derselbe I5-Fix gilt hier — ohne
	// eigenen WithHTTPClient hätte der ShareClient sonst ebenfalls keinerlei Absicherung gegen
	// einen hängenden Endpunkt.
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
		// kein reauth: der ShareClient hat keine Session, die neu aufgebaut werden könnte.
	}
	hc := &http.Client{Transport: transport, Jar: jar}
	if cfg.httpClient != nil {
		hc.Timeout = cfg.httpClient.Timeout
	}
	return &ShareClient{httpClient: hc, baseURL: cfg.baseURL, staticBaseURL: cfg.staticBaseURL, jar: jar}
}

// SharedObject ist die aufgelöste Freigabe: wer geteilt hat (SharedBy/SharedByID) und die
// enthaltenen Dokumente. ID ist die shareId — zusammen mit SharedByID die Parameter für den
// anonymen Seiten-/PDF-Abruf (DownloadSharedPage/DownloadSharedPDF).
type SharedObject struct {
	ID         string           `json:"id"`
	SharedBy   string           `json:"sharedBy"`
	SharedByID string           `json:"sharedById"`
	Created    string           `json:"created"`
	Documents  []SharedDocument `json:"documents"`
}

// SharedDocument ist ein geteiltes Dokument mit den für den anonymen Abruf nötigen typisierten
// Feldern; Raw behält das vollständige Dokument-JSON, da die Struktur je nach Freigabe variiert
// (sender/receiver/type/… selbst dekodieren, wenn benötigt).
type SharedDocument struct {
	ID      string          `json:"id"`
	Title   string          `json:"title"`
	PageIDs []string        `json:"pageIds"`
	Raw     json.RawMessage `json:"-"`
}

// UnmarshalJSON dekodiert die typisierten Felder und bewahrt zusätzlich das komplette Dokument-JSON
// in Raw (für Felder, die SharedDocument nicht modelliert).
func (d *SharedDocument) UnmarshalJSON(b []byte) error {
	type alias SharedDocument
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*d = SharedDocument(a)
	d.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// ShareTokenFromLink extrahiert den Freigabe-Token aus einem Share-Link
// (https://my.fileee.com/shared/<token>). Ein bereits nackter Token wird unverändert zurückgegeben.
func ShareTokenFromLink(link string) string {
	link = strings.TrimRight(link, "/")
	if i := strings.LastIndex(link, "/shared/"); i >= 0 {
		return link[i+len("/shared/"):]
	}
	if i := strings.LastIndex(link, "/"); i >= 0 {
		return link[i+1:]
	}
	return link
}

// ensureXSRF holt über GET /api/f/start ein XSRF-Cookie, das für den nachfolgenden POST (Double-
// Submit) gebraucht wird — auch anonym.
func (s *ShareClient) ensureXSRF(ctx context.Context) error {
	u, _ := url.Parse(s.baseURL)
	for _, c := range s.jar.Cookies(u) {
		if c.Name == "XSRF-TOKEN" {
			return nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/api/f/start", nil)
	if err != nil {
		return fmt.Errorf("fileee: share start request: %w", err)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fileee: share start: %w", err)
	}
	resp.Body.Close()
	return nil
}

// Resolve löst einen Freigabe-Token auf und liefert die geteilten Dokumente. Kein Login nötig.
func (s *ShareClient) Resolve(ctx context.Context, token string) (*SharedObject, error) {
	if err := s.ensureXSRF(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/share-objects/"+token, strings.NewReader("{}"))
	if err != nil {
		return nil, fmt.Errorf("fileee: share resolve request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fileee: share resolve: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: share resolve read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}
	var obj SharedObject
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, fmt.Errorf("fileee: share resolve decode: %w", err)
	}
	return &obj, nil
}

// DownloadPageImage lädt das Bild EINER Seite eines geteilten Dokuments (anonym, über den Token) —
// die seitenweise Viewer-Darstellung. Für das komplette Dokument als PDF siehe DownloadSharedPDF.
func (s *ShareClient) DownloadPageImage(ctx context.Context, token, pageID string, size ImageSize) (io.ReadCloser, error) {
	q := url.Values{"size": {string(size)}}
	return s.getStream(ctx, s.baseURL+"/api/v1/sharing/"+token+"/"+pageID+"?"+q.Encode(), "shared page image")
}

// DownloadSharedPage lädt den Seiten-Inhalt/OCR einer Seite eines geteilten Dokuments als JSON
// (anonym). shareID (= SharedObject.ID) und sharedByID (= SharedObject.SharedByID) stammen aus der
// vorherigen Resolve-Antwort.
func (s *ShareClient) DownloadSharedPage(ctx context.Context, pageID, shareID, sharedByID string) (io.ReadCloser, error) {
	q := url.Values{"share_id": {shareID}, "shared_by": {sharedByID}}
	return s.getStream(ctx, s.baseURL+"/api/pages/"+pageID+"?"+q.Encode(), "shared page")
}

// DownloadSharedPDF lädt ein geteiltes Dokument als komplettes PDF (anonym, ohne Login) — der
// „Download"-Button der Share-Ansicht. WICHTIG: Das PDF liegt auf dem Static-Host
// (static.fileee.com/shares/get/:shareId/:documentId/pdf), NICHT auf dem API-Host. shareID (=
// SharedObject.ID) und documentID (= SharedDocument.ID) stammen aus der Resolve-Antwort.
func (s *ShareClient) DownloadSharedPDF(ctx context.Context, shareID, documentID string, mode PDFMode) (io.ReadCloser, error) {
	q := url.Values{"mode": {string(mode)}}
	return s.getStream(ctx, s.staticBaseURL+"/shares/get/"+shareID+"/"+documentID+"/pdf?"+q.Encode(), "shared pdf")
}

// getStream führt einen anonymen GET aus und gibt bei 200 den offenen Response-Body zurück (der
// Aufrufer schließt ihn); bei Fehlerstatus wird der Body gelesen und als APIError zurückgegeben.
func (s *ShareClient) getStream(ctx context.Context, fullURL, what string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("fileee: %s request: %w", what, err)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fileee: %s: %w", what, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, parseAPIError(resp.StatusCode, body)
	}
	return resp.Body, nil
}

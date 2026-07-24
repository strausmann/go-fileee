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
	httpClient *http.Client
	baseURL    string
	jar        http.CookieJar
}

// NewShareClient erstellt einen credential-losen Client für den Zugriff auf geteilte Dokumente.
// Es gelten dieselben With…-Optionen wie bei New (relevant v. a. WithBaseURL, WithRateLimit,
// WithUserAgent, WithHTTPClient); Auth-bezogene Optionen sind wirkungslos.
func NewShareClient(opts ...Option) *ShareClient {
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
	jar, _ := cookiejar.New(nil)
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
		logger:    cfg.logger,
		// kein reauth: der ShareClient hat keine Session, die neu aufgebaut werden könnte.
	}
	hc := &http.Client{Transport: transport, Jar: jar}
	if cfg.httpClient != nil {
		hc.Timeout = cfg.httpClient.Timeout
	}
	return &ShareClient{httpClient: hc, baseURL: cfg.baseURL, jar: jar}
}

// SharedObject ist die aufgelöste Freigabe: wer geteilt hat und die enthaltenen Dokumente (roh, da
// die Struktur je nach Freigabe variiert — für gezielten Zugriff die Felder selbst dekodieren).
type SharedObject struct {
	ID         string            `json:"id"`
	SharedBy   string            `json:"sharedBy"`
	SharedByID string            `json:"sharedById"`
	Created    string            `json:"created"`
	Documents  []json.RawMessage `json:"documents"`
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

// DownloadPageImage lädt das Bild einer Seite eines geteilten Dokuments (anonym, über den Token).
func (s *ShareClient) DownloadPageImage(ctx context.Context, token, pageID string, size ImageSize) (io.ReadCloser, error) {
	q := url.Values{"size": {string(size)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/api/v1/sharing/"+token+"/"+pageID+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("fileee: shared page image request: %w", err)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fileee: shared page image: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, parseAPIError(resp.StatusCode, body)
	}
	return resp.Body, nil
}

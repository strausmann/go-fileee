package fileee

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// captureUAServer merkt sich den User-Agent-Header des LETZTEN nicht-Auth-Requests.
func captureUAServer(t *testing.T) (*httptest.Server, func() string) {
	t.Helper()
	var mu sync.Mutex
	var lastUA string
	base := jsonHandler(t, mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"GET /api/f/user-session": {Status: 200, Body: []byte(`{"authorized":true}`)},
	}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/f/user-session" {
			mu.Lock()
			lastUA = r.Header.Get("User-Agent")
			mu.Unlock()
		}
		base(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, func() string { mu.Lock(); defer mu.Unlock(); return lastUA }
}

// TestUserAgent_DefaultIsSet: ohne Konfiguration trägt jeder Request den go-fileee-User-Agent,
// damit die Fileee-Server-Logs Client-Name + Version sehen (nicht das generische Go-http-client).
func TestUserAgent_DefaultIsSet(t *testing.T) {
	srv, lastUA := captureUAServer(t)
	c, err := NewClient(Credentials{Username: "u@example.invalid", Password: "pw"},
		WithBaseURL(srv.URL),
		WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "s.json"))),
		WithRateLimit(1000, 1000),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.EnsureSession(context.Background()); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if _, err := c.auth.userSession(context.Background()); err != nil {
		t.Fatalf("userSession: %v", err)
	}
	ua := lastUA()
	if !strings.HasPrefix(ua, "go-fileee/") {
		t.Fatalf("User-Agent sollte mit go-fileee/ beginnen, war: %q", ua)
	}
	if !strings.Contains(ua, Version) {
		t.Fatalf("User-Agent sollte die Version %q enthalten, war: %q", Version, ua)
	}
	if !strings.Contains(ua, "github.com/strausmann/go-fileee") {
		t.Fatalf("User-Agent sollte die Projekt-URL enthalten, war: %q", ua)
	}
}

// TestUserAgent_Custom: ein Konsument (z. B. Scanner-Upload) kann einen eigenen User-Agent setzen —
// die Lib hängt ihre Kennung an, damit Fileee beide sieht.
func TestUserAgent_Custom(t *testing.T) {
	srv, lastUA := captureUAServer(t)
	c, err := NewClient(Credentials{Username: "u@example.invalid", Password: "pw"},
		WithBaseURL(srv.URL),
		WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "s.json"))),
		WithRateLimit(1000, 1000),
		WithUserAgent("paperless-scan-bridge/2.0"),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.EnsureSession(context.Background()); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if _, err := c.auth.userSession(context.Background()); err != nil {
		t.Fatalf("userSession: %v", err)
	}
	ua := lastUA()
	if !strings.Contains(ua, "paperless-scan-bridge/2.0") {
		t.Fatalf("eigener User-Agent fehlt: %q", ua)
	}
	if !strings.Contains(ua, "go-fileee/") {
		t.Fatalf("Lib-Kennung sollte trotzdem enthalten sein: %q", ua)
	}
}

package fileee

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

// TestWithLogger_EmitsRequestDebugEvents: der über WithLogger injizierte slog.Logger erhält je
// Request ein Debug-Event mit Methode, Pfad und Status — die Grundlage fürs Debuggen. Es dürfen
// KEINE Secrets (Cookies/Header/Token) im Log erscheinen.
func TestWithLogger_EmitsRequestDebugEvents(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"GET /api/f/user-session": {Status: 200, Body: []byte(`{"authorized":true}`)},
	})
	c, err := NewClient(Credentials{Username: "u@example.invalid", Password: "pw"},
		WithBaseURL(newMockServer(t, jsonHandler(t, routes)).URL),
		WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "s.json"))),
		WithRateLimit(1000, 1000),
		WithLogger(logger),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.EnsureSession(context.Background()); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "/api/f/login") || !strings.Contains(out, "status=200") {
		t.Fatalf("erwartet Debug-Event mit Pfad + Status, Log war:\n%s", out)
	}
	// Secret-Hygiene: keine Cookie-/XSRF-Werte im Log.
	for _, forbidden := range []string{"XSRF", "JSESSIONID", "Cookie", "two-factor-token", "password"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("Log enthält potenzielles Secret %q:\n%s", forbidden, out)
		}
	}
}

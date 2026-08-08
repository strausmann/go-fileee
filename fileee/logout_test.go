package fileee

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
)

// jarCookieNames liefert die Namen aller Cookies, die der Client-Jar aktuell für die Basis-URL hält.
func jarCookieNames(t *testing.T, c *Client) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	u, _ := url.Parse(c.baseURL)
	for _, ck := range c.httpClient.Jar.Cookies(u) {
		out[ck.Name] = true
	}
	return out
}

// TestLogout_ClearsCookieJar: Logout muss das (serverseitig widerrufene) Session- UND
// rememberMe-Cookie aus dem In-Memory-Jar entfernen — sonst versucht ein späteres
// reauthenticate erst einen sinnlosen token/login mit dem toten rememberMe-Token.
func TestLogout_ClearsCookieJar(t *testing.T) {
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"GET /api/f/user-session": {Status: 200, Body: []byte(`{"authorized":true}`)},
		"POST /api/f/logout":      {Status: 200},
	})
	// rememberMe zusätzlich zum JSESSIONID setzen (login-Route liefert nur JSESSIONID) — wir
	// injizieren rememberMe über eine login-Antwort mit beiden Cookies.
	routes["POST /api/f/login"] = mockRoute{
		Status: 200, Body: []byte(`{"loggedIn":true}`),
		Cookies: []*http.Cookie{
			{Name: "JSESSIONID", Value: "sess-abc", Path: "/"},
			{Name: "rememberMe", Value: "remember-abc", Path: "/"},
		},
	}
	srv := newMockServer(t, jsonHandler(t, routes))
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
	before := jarCookieNames(t, c)
	if !before["JSESSIONID"] {
		t.Fatalf("Vorbedingung: JSESSIONID sollte nach Login im Jar sein, war: %v", before)
	}

	if err := c.Logout(context.Background()); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	after := jarCookieNames(t, c)
	if after["JSESSIONID"] {
		t.Errorf("JSESSIONID noch im Jar nach Logout: %v", after)
	}
	if after["rememberMe"] {
		t.Errorf("rememberMe noch im Jar nach Logout: %v", after)
	}
}

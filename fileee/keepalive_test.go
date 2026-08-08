package fileee

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// countingAuthServer bedient den vollen Login-Handshake + user-session und zählt die
// user-session-Verifies (über einen Kanal signalisiert).
func countingAuthServer(t *testing.T, verifyHits *int64, sig chan<- struct{}) *httptest.Server {
	t.Helper()
	return newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/f/start":
			http.SetCookie(w, &http.Cookie{Name: "XSRF-TOKEN", Value: "x"})
			w.WriteHeader(204)
		case "/api/f/existent":
			w.Write([]byte(`{"existent":true,"twoFactorAuthEnabled":false}`))
		case "/api/f/token/login":
			w.WriteHeader(401) // kein rememberMe -> voller Login
		case "/api/f/login":
			http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "s"})
			w.Write([]byte(`{"loggedIn":true}`))
		case "/api/f/user-session":
			atomic.AddInt64(verifyHits, 1)
			if sig != nil {
				select {
				case sig <- struct{}{}:
				default:
				}
			}
			w.Write([]byte(`{"authorized":true}`))
		default:
			w.WriteHeader(404)
		}
	}))
}

func freshTestClient(t *testing.T, srv *httptest.Server, freshness time.Duration, now func() time.Time) *Client {
	t.Helper()
	c, err := NewClient(Credentials{Username: "u@example.invalid", Password: "p"},
		WithBaseURL(srv.URL), WithRateLimit(1000, 1000),
		WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "s.json"))),
		WithSessionFreshness(freshness))
	if err != nil {
		t.Fatal(err)
	}
	if now != nil {
		c.auth.now = now
	}
	return c
}

func TestEnsureSession_FreshnessSkipsVerify(t *testing.T) {
	var hits int64
	srv := countingAuthServer(t, &hits, nil)
	base := time.Unix(1000, 0)
	clock := base
	c := freshTestClient(t, srv, 15*time.Minute, func() time.Time { return clock })
	ctx := context.Background()

	// 1. Login (keine gespeicherte Session -> voller Login, kein user-session-Verify)
	if err := c.EnsureSession(ctx); err != nil {
		t.Fatalf("login: %v", err)
	}
	// 2. innerhalb des Fensters -> Verify wird übersprungen
	clock = base.Add(1 * time.Minute)
	if err := c.EnsureSession(ctx); err != nil {
		t.Fatalf("fresh: %v", err)
	}
	if got := atomic.LoadInt64(&hits); got != 0 {
		t.Fatalf("erwartet 0 user-session-Verifies im Fenster, bekommen %d", got)
	}
	// 3. nach Ablauf des Fensters -> Verify läuft
	clock = base.Add(16 * time.Minute)
	if err := c.EnsureSession(ctx); err != nil {
		t.Fatalf("stale: %v", err)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("erwartet 1 Verify nach Fensterablauf, bekommen %d", got)
	}
}

func TestStartKeepAlive_PingsUntilStopped(t *testing.T) {
	var hits int64
	sig := make(chan struct{}, 8)
	srv := countingAuthServer(t, &hits, sig)
	c := freshTestClient(t, srv, 15*time.Minute, nil)
	ctx := context.Background()
	if err := c.EnsureSession(ctx); err != nil { // initialer Login (persistiert Session)
		t.Fatal(err)
	}
	stop := c.StartKeepAlive(ctx, 15*time.Millisecond)
	for i := 0; i < 2; i++ {
		select {
		case <-sig:
		case <-time.After(2 * time.Second):
			t.Fatalf("Keepalive-Ping %d blieb aus", i+1)
		}
	}
	stop()
	// nach stop() drainen und prüfen, dass es aufhört
	time.Sleep(50 * time.Millisecond)
	for len(sig) > 0 {
		<-sig
	}
	before := atomic.LoadInt64(&hits)
	time.Sleep(80 * time.Millisecond)
	if after := atomic.LoadInt64(&hits); after != before {
		t.Fatalf("Keepalive lief nach stop() weiter: %d -> %d", before, after)
	}
}

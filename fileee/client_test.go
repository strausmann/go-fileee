package fileee

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// recordingRoundTripper zeichnet auf, ob RoundTrip aufgerufen wurde, und delegiert danach an
// base — damit lässt sich beweisen, dass ein per WithHTTPClient übergebener Custom-Transport
// tatsächlich als Basis des internen rateLimitedTransport verwendet wird, statt beim Wrappen
// stillschweigend durch http.DefaultTransport ersetzt zu werden.
type recordingRoundTripper struct {
	base  http.RoundTripper
	calls atomic.Int64
}

func (rt *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.calls.Add(1)
	return rt.base.RoundTrip(req)
}

func TestNewValidiertCredentials(t *testing.T) {
	_, err := New(Credentials{})
	if err == nil {
		t.Fatalf("erwartet Fehler bei leeren Credentials, bekommen nil")
	}
}

func TestNewMitOptionsUndLogin(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/f/start":     {Status: 204},
		"POST /api/f/existent": {Status: 200, Body: []byte(`{"existent":true,"twoFactorAuthEnabled":false}`)},
		"POST /api/f/login":    {Status: 200, Body: []byte(`{"loggedIn":true}`), Cookies: []*http.Cookie{{Name: "JSESSIONID", Value: "sess-client"}}},
	}
	srv := newMockServer(t, jsonHandler(t, routes))
	store := NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))

	client, err := New(
		Credentials{Username: "test@example.invalid", Password: "test-pw"},
		WithBaseURL(srv.URL),
		WithSessionStore(store),
		WithRateLimit(1000, 1000),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.Documents == nil || client.Tags == nil || client.Companies == nil || client.Contacts == nil || client.DocumentTypes == nil {
		t.Fatalf("nicht alle Services wurden verdrahtet: %+v", client)
	}
	if err := client.Login(context.Background()); err != nil {
		t.Fatalf("Login über Client: %v", err)
	}
	sess, err := store.Load(context.Background())
	if err != nil || sess == nil {
		t.Fatalf("WithSessionStore wurde nicht verwendet: %v / %+v", err, sess)
	}
}

// TestNewMitWithHTTPClientNutztCustomTransportAlsBasis belegt Ende-zu-Ende, dass ein per
// WithHTTPClient übergebener Custom-Transport tatsächlich als Basis des internen
// rateLimitedTransport dient — statt beim Wrappen stillschweigend durch http.DefaultTransport
// ersetzt zu werden. Ein echter Login-Request muss durch den Custom-RoundTripper laufen.
func TestNewMitWithHTTPClientNutztCustomTransportAlsBasis(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/f/start":     {Status: 204},
		"POST /api/f/existent": {Status: 200, Body: []byte(`{"existent":true,"twoFactorAuthEnabled":false}`)},
		"POST /api/f/login":    {Status: 200, Body: []byte(`{"loggedIn":true}`), Cookies: []*http.Cookie{{Name: "JSESSIONID", Value: "sess-custom-transport"}}},
	}
	srv := newMockServer(t, jsonHandler(t, routes))
	store := NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))

	custom := &recordingRoundTripper{base: http.DefaultTransport}

	client, err := New(
		Credentials{Username: "test@example.invalid", Password: "test-pw"},
		WithBaseURL(srv.URL),
		WithSessionStore(store),
		WithRateLimit(1000, 1000),
		WithHTTPClient(&http.Client{Transport: custom}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := client.Login(context.Background()); err != nil {
		t.Fatalf("Login über Client: %v", err)
	}

	if calls := custom.calls.Load(); calls == 0 {
		t.Fatalf("erwartet dass der Custom-Transport als Basis genutzt und mindestens einmal aufgerufen wird, bekommen 0 Aufrufe")
	}
}

// TestNewMutiertAufruferClientNicht belegt, dass New() den per WithHTTPClient übergebenen
// *http.Client NICHT mutiert — weder dessen Transport noch dessen Jar dürfen nach dem Aufruf
// verändert sein. Eine Library darf ein aufrufer-eigenes Objekt nicht als Seiteneffekt umbauen,
// da der Aufrufer denselben *http.Client evtl. anderweitig weiterverwendet.
func TestNewMutiertAufruferClientNicht(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/f/start": {Status: 204},
	}
	srv := newMockServer(t, jsonHandler(t, routes))
	store := NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))

	callerTransport := &recordingRoundTripper{base: http.DefaultTransport}
	callerJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	callerClient := &http.Client{Transport: callerTransport, Jar: callerJar}

	if _, err := New(
		Credentials{Username: "test@example.invalid", Password: "test-pw"},
		WithBaseURL(srv.URL),
		WithSessionStore(store),
		WithHTTPClient(callerClient),
	); err != nil {
		t.Fatalf("New: %v", err)
	}

	if callerClient.Transport != callerTransport {
		t.Fatalf("Aufrufer-Client wurde mutiert: Transport ist nicht mehr das ursprünglich übergebene Objekt")
	}
	if callerClient.Jar != callerJar {
		t.Fatalf("Aufrufer-Client wurde mutiert: Jar ist nicht mehr das ursprünglich übergebene Objekt")
	}
}

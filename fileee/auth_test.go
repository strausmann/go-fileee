package fileee

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestCurrentTOTPLeererSeedLiefertLeerenCode(t *testing.T) {
	code, err := currentTOTP("")
	if err != nil {
		t.Fatalf("currentTOTP(\"\"): %v", err)
	}
	if code != "" {
		t.Fatalf("erwartet leeren Code bei leerem Seed, bekommen %q", code)
	}
}

func TestCurrentTOTPGueltigerSeedLiefertPasssendenCode(t *testing.T) {
	// Test-Seed, kein echter Fileee-Account-Seed (secret-safe).
	const testSeed = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	code, err := currentTOTP(testSeed)
	if err != nil {
		t.Fatalf("currentTOTP: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("Code hat Länge %d, erwartet 6", len(code))
	}
	want, err := totp.GenerateCode(testSeed, time.Now())
	if err != nil {
		t.Fatalf("Referenz-GenerateCode: %v", err)
	}
	if code != want {
		t.Fatalf("code = %q, Referenz-Code = %q", code, want)
	}
}

func TestCurrentTOTPUngueltigerSeedLiefertFehler(t *testing.T) {
	// "1" ist kein gültiges Base32: die Ziffer "1" gehört nicht zum RFC-4648-Base32-
	// Alphabet (A–Z, 2–7) und muss beim Dekodieren fehlschlagen statt still einen
	// Code zu erzeugen.
	code, err := currentTOTP("1")
	if err == nil {
		t.Fatalf("erwartet Fehler bei ungültigem Seed, bekommen Code %q", code)
	}
	if code != "" {
		t.Fatalf("erwartet leeren Code bei Fehler, bekommen %q", code)
	}
}

func newTestAuthClient(t *testing.T, srv *httptest.Server, creds Credentials) *authClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	return &authClient{
		hc:      &http.Client{Jar: jar},
		baseURL: srv.URL,
		creds:   creds,
		store:   NewFileSessionStore(filepath.Join(t.TempDir(), "session.json")),
	}
}

func TestAuthClientLogin(t *testing.T) {
	cases := []struct {
		name       string
		creds      Credentials
		routes     map[string]mockRoute
		wantErr    error
		wantErrNil bool
	}{
		{
			name:  "happy path ohne 2FA",
			creds: Credentials{Username: "test@example.invalid", Password: "test-pw"},
			routes: map[string]mockRoute{
				"GET /api/f/start":     {Status: 204, Cookies: []*http.Cookie{{Name: "XSRF-TOKEN", Value: "xsrf-1"}}},
				"POST /api/f/existent": {Status: 200, Body: []byte(`{"existent":true,"twoFactorAuthEnabled":false}`)},
				"POST /api/f/login":    {Status: 200, Body: []byte(`{"loggedIn":true,"userId":"user-1"}`), Cookies: []*http.Cookie{{Name: "JSESSIONID", Value: "sess-1"}, {Name: "rememberMe", Value: "remember-1"}}},
			},
			wantErrNil: true,
		},
		{
			name:  "happy path mit 2FA",
			creds: Credentials{Username: "test2@example.invalid", Password: "test-pw", TOTPSeed: "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"},
			routes: map[string]mockRoute{
				"GET /api/f/start":     {Status: 204},
				"POST /api/f/existent": {Status: 200, Body: []byte(`{"existent":true,"twoFactorAuthEnabled":true}`)},
				"POST /api/f/login":    {Status: 200, Body: []byte(`{"loggedIn":true}`), Cookies: []*http.Cookie{{Name: "JSESSIONID", Value: "sess-2"}}},
			},
			wantErrNil: true,
		},
		{
			name:  "Konto existiert nicht",
			creds: Credentials{Username: "unbekannt@example.invalid", Password: "test-pw"},
			routes: map[string]mockRoute{
				"GET /api/f/start":     {Status: 204},
				"POST /api/f/existent": {Status: 200, Body: []byte(`{"existent":false,"twoFactorAuthEnabled":false}`)},
			},
			wantErr: ErrInvalidCredentials,
		},
		{
			name:  "2FA-Code falsch -> 403",
			creds: Credentials{Username: "test3@example.invalid", Password: "test-pw", TOTPSeed: "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"},
			routes: map[string]mockRoute{
				"GET /api/f/start":     {Status: 204},
				"POST /api/f/existent": {Status: 200, Body: []byte(`{"existent":true,"twoFactorAuthEnabled":true}`)},
				"POST /api/f/login":    {Status: 403, Body: []byte(`{"apiError":"forbidden","errorMessage":"bad totp"}`)},
			},
			wantErr: ErrTwoFactorInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newMockServer(t, jsonHandler(t, tc.routes))
			a := newTestAuthClient(t, srv, tc.creds)
			err := a.login(context.Background())
			if tc.wantErrNil {
				if err != nil {
					t.Fatalf("login: unerwarteter Fehler %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("login error = %v, erwartet %v", err, tc.wantErr)
			}
		})
	}
}

func TestAuthClientLoginNetworkError(t *testing.T) {
	a := &authClient{
		hc:      &http.Client{Timeout: 50 * time.Millisecond},
		baseURL: "http://127.0.0.1:1", // Port 1 ist praktisch garantiert nicht erreichbar
		creds:   Credentials{Username: "test@example.invalid", Password: "test-pw"},
		store:   NewFileSessionStore(filepath.Join(t.TempDir(), "session.json")),
	}
	err := a.login(context.Background())
	if err == nil {
		t.Fatalf("erwartet Network-Error, bekommen nil")
	}
}

// TestAuthClientLoginServerError5xx deckt den in den Standing Rules geforderten Serverfehler-Pfad
// ab (ergänzend zum Brief, der nur den Verbindungsfehler in TestAuthClientLoginNetworkError
// abdeckt): ein 5xx auf /api/f/login ist weder ErrInvalidCredentials noch ErrTwoFactorInvalid,
// sondern läuft in den default-Zweig von login() und liefert einen *APIError mit dem Original-
// Status/-Body.
func TestAuthClientLoginServerError5xx(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/f/start":     {Status: 204},
		"POST /api/f/existent": {Status: 200, Body: []byte(`{"existent":true,"twoFactorAuthEnabled":false}`)},
		"POST /api/f/login":    {Status: 500, Body: []byte(`{"apiError":"internal_error","errorMessage":"boom"}`)},
	}
	srv := newMockServer(t, jsonHandler(t, routes))
	a := newTestAuthClient(t, srv, Credentials{Username: "test@example.invalid", Password: "test-pw"})
	err := a.login(context.Background())
	if err == nil {
		t.Fatalf("erwartet Serverfehler, bekommen nil")
	}
	if errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrTwoFactorInvalid) {
		t.Fatalf("5xx darf nicht als Credential-/2FA-Fehler interpretiert werden, bekommen %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("erwartet *APIError, bekommen %T (%v)", err, err)
	}
	if apiErr.HTTPStatus != 500 {
		t.Fatalf("apiErr.HTTPStatus = %d, erwartet 500", apiErr.HTTPStatus)
	}
	if apiErr.Code != "internal_error" {
		t.Fatalf("apiErr.Code = %q, erwartet internal_error", apiErr.Code)
	}
}

func TestPersistSessionUndCookieValue(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/f/start":     {Status: 204},
		"POST /api/f/existent": {Status: 200, Body: []byte(`{"existent":true,"twoFactorAuthEnabled":false}`)},
		"POST /api/f/login":    {Status: 200, Body: []byte(`{"loggedIn":true}`), Cookies: []*http.Cookie{{Name: "rememberMe", Value: "remember-test"}}},
	}
	srv := newMockServer(t, jsonHandler(t, routes))
	a := newTestAuthClient(t, srv, Credentials{Username: "test@example.invalid", Password: "test-pw"})
	if err := a.login(context.Background()); err != nil {
		t.Fatalf("login: %v", err)
	}
	if got := a.cookieValue("rememberMe"); got != "remember-test" {
		t.Fatalf("cookieValue(rememberMe) = %q, erwartet remember-test", got)
	}
	sess, err := a.store.Load(context.Background())
	if err != nil || sess == nil {
		t.Fatalf("persistSession hat die Session nicht gespeichert: %v / %+v", err, sess)
	}
}

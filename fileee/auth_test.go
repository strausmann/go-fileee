package fileee

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
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

// TestAuthClientLogin2FAErforderlichAberSeedFehltFailtFastOhneLoginPost deckt das
// Copilot-Review-Finding PR#7 ab: meldet /f/existent 2FA-Pflicht, aber Credentials.TOTPSeed ist
// leer, darf login() NICHT erst ein leeres "two-factor-token" an POST /api/f/login senden und
// den Fehlschlag über das generische ErrTwoFactorInvalid aus der Server-Antwort ableiten —
// stattdessen MUSS VOR dem Login-Request abgebrochen werden, mit einer erklärenden Meldung.
func TestAuthClientLogin2FAErforderlichAberSeedFehltFailtFastOhneLoginPost(t *testing.T) {
	var loginHit bool
	routes := map[string]mockRoute{
		"GET /api/f/start":     {Status: 204},
		"POST /api/f/existent": {Status: 200, Body: []byte(`{"existent":true,"twoFactorAuthEnabled":true}`)},
		// Bewusst mit Erfolgs-Body hinterlegt: würde login() den Fail-Fast NICHT vor dem
		// Login-Request auslösen, käme hier fälschlich ein "erfolgreicher" Login zurück statt
		// eines 401/403 — der Test soll die fehlende Vor-Prüfung über loginHit erkennen, nicht
		// über einen zufällig passenden Fehler-Statuscode.
		"POST /api/f/login": {Status: 200, Body: []byte(`{"loggedIn":true}`)},
	}
	base := jsonHandler(t, routes)
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/f/login" {
			loginHit = true
		}
		base(w, r)
	}))

	// TOTPSeed bewusst leer, obwohl der Server twoFactorAuthEnabled:true meldet.
	creds := Credentials{Username: "test4@example.invalid", Password: "test-pw"}
	a := newTestAuthClient(t, srv, creds)
	err := a.login(context.Background())

	if !errors.Is(err, ErrTwoFactorInvalid) {
		t.Fatalf("login error = %v, erwartet errors.Is(err, ErrTwoFactorInvalid)", err)
	}
	if err.Error() == ErrTwoFactorInvalid.Error() {
		t.Fatalf("Fehlermeldung ist nur die generische Sentinel-Meldung %q, erwartet einen erklärenden Fail-Fast-Text (fehlender TOTP-Seed)", err.Error())
	}
	if loginHit {
		t.Fatalf("POST /api/f/login wurde aufgerufen — der fehlende TOTP-Seed muss VOR dem Login-Request abgefangen werden, nicht erst über die Server-Antwort")
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

func TestEnsureSessionMitGueltigGespeicherterSession(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/f/user-session": {Status: 200, Body: []byte(`{"authorized":true,"secondsBlocked":0}`)},
	}
	srv := newMockServer(t, jsonHandler(t, routes))
	a := newTestAuthClient(t, srv, Credentials{Username: "test@example.invalid", Password: "test-pw"})
	if err := a.store.Save(context.Background(), &Session{Cookies: []*http.Cookie{{Name: "JSESSIONID", Value: "sess-x"}}}); err != nil {
		t.Fatalf("Setup Save: %v", err)
	}
	if err := a.EnsureSession(context.Background()); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
}

func TestEnsureSessionSecondsBlocked(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/f/user-session": {Status: 200, Body: []byte(`{"authorized":true,"secondsBlocked":42}`)},
	}
	srv := newMockServer(t, jsonHandler(t, routes))
	a := newTestAuthClient(t, srv, Credentials{Username: "test@example.invalid", Password: "test-pw"})
	_ = a.store.Save(context.Background(), &Session{Cookies: []*http.Cookie{{Name: "JSESSIONID", Value: "sess-x"}}})

	err := a.EnsureSession(context.Background())
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("erwartet *BlockedError, bekommen %v", err)
	}
	if blocked.SecondsBlocked != 42 {
		t.Fatalf("SecondsBlocked = %d, erwartet 42", blocked.SecondsBlocked)
	}
}

func TestEnsureSessionOhneGespeicherteSessionFuehrtVollenLoginDurch(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/f/start":     {Status: 204},
		"POST /api/f/existent": {Status: 200, Body: []byte(`{"existent":true,"twoFactorAuthEnabled":false}`)},
		"POST /api/f/login":    {Status: 200, Body: []byte(`{"loggedIn":true}`), Cookies: []*http.Cookie{{Name: "JSESSIONID", Value: "sess-neu"}}},
	}
	srv := newMockServer(t, jsonHandler(t, routes))
	a := newTestAuthClient(t, srv, Credentials{Username: "test@example.invalid", Password: "test-pw"})
	if err := a.EnsureSession(context.Background()); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
}

func TestTokenLoginOhneRememberMeCookieLiefertErrSessionExpired(t *testing.T) {
	srv := newMockServer(t, jsonHandler(t, map[string]mockRoute{}))
	a := newTestAuthClient(t, srv, Credentials{Username: "test@example.invalid", Password: "test-pw"})
	err := a.tokenLogin(context.Background())
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("erwartet ErrSessionExpired, bekommen %v", err)
	}
}

// TestTokenLoginHappyPathSendetTokenUndPersistiertSession deckt den bisher ungetesteten
// Erfolgspfad von tokenLogin direkt ab (Review-Finding: der primäre rememberMe-Re-Auth-Pfad war
// nur "durch Lesen verifiziert", nie tatsächlich ausgeführt). Ein eigener Handler (statt
// jsonHandler, das keine Request-Bodies erfasst) stubbt POST /api/f/token/login, prüft das
// gesendete form-Feld "token" und liefert ein Platzhalter-Session-Cookie zurück.
func TestTokenLoginHappyPathSendetTokenUndPersistiertSession(t *testing.T) {
	var sawTokenLoginCall bool
	var capturedToken string
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/f/token/login" {
			sawTokenLoginCall = true
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			capturedToken = r.FormValue("token")
			http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "sess-token-login-placeholder"})
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"apiError":"route_not_mocked","errorMessage":"keine Mock-Route für ` + r.Method + " " + r.URL.Path + `"}`))
	}
	srv := newMockServer(t, http.HandlerFunc(handler))
	a := newTestAuthClient(t, srv, Credentials{Username: "test@example.invalid", Password: "test-pw"})

	// rememberMe-Cookie (Platzhalter-Wert, kein echtes JWT) so in den Jar legen, als stamme er aus
	// einer vorherigen Session — simuliert exakt den Zustand, den tokenLogin über cookieValue()
	// vorfindet.
	scopeURL, err := a.authCookieScopeURL()
	if err != nil {
		t.Fatalf("authCookieScopeURL: %v", err)
	}
	a.hc.Jar.SetCookies(scopeURL, []*http.Cookie{{Name: "rememberMe", Value: "remember-placeholder-token"}})

	if err := a.tokenLogin(context.Background()); err != nil {
		t.Fatalf("tokenLogin: unerwarteter Fehler %v", err)
	}
	if !sawTokenLoginCall {
		t.Fatalf("POST /api/f/token/login wurde nicht aufgerufen")
	}
	if capturedToken != "remember-placeholder-token" {
		t.Fatalf("gesendetes form-Feld token = %q, erwartet remember-placeholder-token", capturedToken)
	}
	sess, err := a.store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load nach tokenLogin: %v", err)
	}
	if sess == nil || len(sess.Cookies) == 0 {
		t.Fatalf("tokenLogin hat die Session nicht persistiert: sess=%+v", sess)
	}
}

// TestEnsureSessionAbgelaufenMitRememberMeNutztTokenLoginNichtVollenLogin deckt den zweiten Teil
// des Review-Findings ab: EnsureSession muss bei einer abgelaufenen, aber mit rememberMe-Cookie
// versehenen gespeicherten Session über reauthenticate() den token/login-Pfad nehmen — NICHT den
// vollen Passwort+TOTP-Login. Die Mock-Routen für /api/f/start, /api/f/existent und /api/f/login
// sind bewusst so gebaut, dass ein Aufruf sofort auffällt (500 + Flag), statt still durchzulaufen.
func TestEnsureSessionAbgelaufenMitRememberMeNutztTokenLoginNichtVollenLogin(t *testing.T) {
	var tokenLoginCalled, fullLoginCalled bool
	var capturedToken string
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/f/user-session":
			// Abgelaufene Session: authorized:false, aber kein Server-Fehler.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"authorized":false,"secondsBlocked":0}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/f/token/login":
			tokenLoginCalled = true
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			capturedToken = r.FormValue("token")
			http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "sess-renewed-placeholder"})
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/f/start" || r.URL.Path == "/api/f/existent" || r.URL.Path == "/api/f/login":
			// Voller Login darf hier NICHT laufen — wird token/login sauber genutzt, kommt dieser
			// Zweig nie zum Zug (reauthenticate kehrt schon bei erfolgreichem tokenLogin zurück).
			fullLoginCalled = true
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"apiError":"route_not_mocked","errorMessage":"keine Mock-Route für ` + r.Method + " " + r.URL.Path + `"}`))
		}
	}
	srv := newMockServer(t, http.HandlerFunc(handler))
	a := newTestAuthClient(t, srv, Credentials{Username: "test@example.invalid", Password: "test-pw"})

	// Gespeicherte, abgelaufene Session mit rememberMe-Cookie (Platzhalter) — wie sie EnsureSession
	// beim Prozessstart aus dem SessionStore lädt.
	if err := a.store.Save(context.Background(), &Session{
		Cookies: []*http.Cookie{
			{Name: "JSESSIONID", Value: "sess-old-expired-placeholder"},
			{Name: "rememberMe", Value: "remember-placeholder-existing"},
		},
	}); err != nil {
		t.Fatalf("Setup Save: %v", err)
	}

	if err := a.EnsureSession(context.Background()); err != nil {
		t.Fatalf("EnsureSession: unerwarteter Fehler %v", err)
	}
	if !tokenLoginCalled {
		t.Fatalf("POST /api/f/token/login wurde nicht aufgerufen — rememberMe-Branch wurde nicht genutzt")
	}
	if fullLoginCalled {
		t.Fatalf("voller Passwort+TOTP-Login wurde aufgerufen, obwohl token/login erfolgreich war")
	}
	if capturedToken != "remember-placeholder-existing" {
		t.Fatalf("gesendetes form-Feld token = %q, erwartet remember-placeholder-existing", capturedToken)
	}
	sess, err := a.store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load nach EnsureSession: %v", err)
	}
	if sess == nil || len(sess.Cookies) == 0 {
		t.Fatalf("Session wurde nach dem token/login-Re-Auth nicht erneuert: sess=%+v", sess)
	}
}

func TestReauthenticateFaelltAufVollenLoginZurueck(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/f/start":     {Status: 204},
		"POST /api/f/existent": {Status: 200, Body: []byte(`{"existent":true,"twoFactorAuthEnabled":false}`)},
		"POST /api/f/login":    {Status: 200, Body: []byte(`{"loggedIn":true}`), Cookies: []*http.Cookie{{Name: "JSESSIONID", Value: "sess-fallback"}}},
	}
	srv := newMockServer(t, jsonHandler(t, routes))
	a := newTestAuthClient(t, srv, Credentials{Username: "test@example.invalid", Password: "test-pw"})
	// Kein rememberMe-Cookie gesetzt -> tokenLogin schlägt fehl -> Fallback auf login().
	if err := a.reauthenticate(context.Background()); err != nil {
		t.Fatalf("reauthenticate: %v", err)
	}
}

// TestReauthenticateBewahrtUrsprungsfehler deckt den zweiten Fix aus dem finalen
// Whole-Branch-Review ab: reauthenticate() darf den Fehler des vollen Logins NICHT mehr maskieren.
// Ein Aufrufer muss über errors.Is BEIDES prüfen können — dass es sich um einen
// Session-Expired-Fall handelt (bestehender Vertrag, ErrSessionExpired) UND welcher konkrete
// Fehler dahintersteckt (hier: ErrInvalidCredentials, weil /f/existent existent:false meldet).
func TestReauthenticateBewahrtUrsprungsfehler(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/f/start":     {Status: 204},
		"POST /api/f/existent": {Status: 200, Body: []byte(`{"existent":false,"twoFactorAuthEnabled":false}`)},
	}
	srv := newMockServer(t, jsonHandler(t, routes))
	a := newTestAuthClient(t, srv, Credentials{Username: "test@example.invalid", Password: "test-pw"})
	// Kein rememberMe-Cookie gesetzt -> tokenLogin schlägt fehl -> Fallback auf login(), das wegen
	// existent:false mit ErrInvalidCredentials fehlschlägt.
	err := a.reauthenticate(context.Background())
	if err == nil {
		t.Fatalf("erwartet Fehler, bekommen nil")
	}
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("erwartet errors.Is(err, ErrSessionExpired) == true (bestehender Vertrag), bekommen %v", err)
	}
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Ursprungsfehler ErrInvalidCredentials nicht mehr über errors.Is auffindbar (maskiert): %v", err)
	}
}

// TestEnsureSessionOhneGespeicherteSessionBewahrtUrsprungsfehler deckt denselben Fix auf
// EnsureSession-Ebene ab: EnsureSession funnelt ohne gespeicherte Session direkt in
// reauthenticate() — der maskierte Fehler darf auch über diesen Aufrufpfad nicht mehr verloren
// gehen.
func TestEnsureSessionOhneGespeicherteSessionBewahrtUrsprungsfehler(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/f/start":     {Status: 204},
		"POST /api/f/existent": {Status: 200, Body: []byte(`{"existent":false,"twoFactorAuthEnabled":false}`)},
	}
	srv := newMockServer(t, jsonHandler(t, routes))
	a := newTestAuthClient(t, srv, Credentials{Username: "test@example.invalid", Password: "test-pw"})
	err := a.EnsureSession(context.Background())
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("erwartet errors.Is(err, ErrSessionExpired) == true, bekommen %v", err)
	}
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Ursprungsfehler ErrInvalidCredentials nicht mehr über errors.Is auffindbar: %v", err)
	}
}

// TestReauthenticateNetzwerkfehlerBleibtUeberErrorsAsAuffindbar belegt, dass auch ein transienter
// Netzwerkfehler (nicht nur ein sentinel-basierter API-Fehler) nach dem Fix erhalten bleibt und
// über errors.As als *url.Error identifizierbar ist — ein Aufrufer kann damit "Netzwerk kurz down"
// von "Credentials sind ungültig/veraltet" unterscheiden, statt beides zu ErrSessionExpired
// zusammenzufalten.
func TestReauthenticateNetzwerkfehlerBleibtUeberErrorsAsAuffindbar(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	a := &authClient{
		hc:      &http.Client{Jar: jar},
		baseURL: "http://127.0.0.1:1", // Port 1: verbindungsverweigert, kein echter Server nötig.
		creds:   Credentials{Username: "test@example.invalid", Password: "test-pw"},
		store:   NewFileSessionStore(filepath.Join(t.TempDir(), "session.json")),
	}
	err = a.reauthenticate(context.Background())
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("erwartet errors.Is(err, ErrSessionExpired) == true, bekommen %v", err)
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		t.Fatalf("erwartet *url.Error über errors.As auffindbar (Netzwerkfehler wurde maskiert): %v", err)
	}
}

func TestLogout(t *testing.T) {
	routes := map[string]mockRoute{"POST /api/f/logout": {Status: 200}}
	srv := newMockServer(t, jsonHandler(t, routes))
	a := newTestAuthClient(t, srv, Credentials{Username: "test@example.invalid", Password: "test-pw"})
	if err := a.logout(context.Background()); err != nil {
		t.Fatalf("logout: %v", err)
	}
	sess, err := a.store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load nach logout: %v", err)
	}
	if sess != nil && len(sess.Cookies) != 0 {
		t.Fatalf("erwartet leere Session nach logout, bekommen %+v", sess)
	}
}

func TestAccountStatusHappyErrorUndNetwork(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		routes := map[string]mockRoute{
			"GET /api/f/user-session":   {Status: 200, Body: []byte(`{"authorized":true,"secondsBlocked":0}`)},
			"GET /api/f/account-status": {Status: 200, Body: []byte(`{"accountTypeId":"premium","currentSubscription":{"name":"Pro","frequency":"monthly","amount":9.99}}`)},
		}
		srv := newMockServer(t, jsonHandler(t, routes))
		a := newTestAuthClient(t, srv, Credentials{Username: "test@example.invalid", Password: "test-pw"})
		_ = a.store.Save(context.Background(), &Session{Cookies: []*http.Cookie{{Name: "JSESSIONID", Value: "sess-x"}}})
		status, err := a.accountStatus(context.Background())
		if err != nil {
			t.Fatalf("accountStatus: %v", err)
		}
		if status.AccountTypeID != "premium" || status.SubscriptionName != "Pro" {
			t.Fatalf("status = %+v", status)
		}
	})

	t.Run("error path 500", func(t *testing.T) {
		routes := map[string]mockRoute{
			"GET /api/f/user-session":   {Status: 200, Body: []byte(`{"authorized":true,"secondsBlocked":0}`)},
			"GET /api/f/account-status": {Status: 500, Body: []byte(`{"apiError":"internal"}`)},
		}
		srv := newMockServer(t, jsonHandler(t, routes))
		a := newTestAuthClient(t, srv, Credentials{Username: "test@example.invalid", Password: "test-pw"})
		_ = a.store.Save(context.Background(), &Session{Cookies: []*http.Cookie{{Name: "JSESSIONID", Value: "sess-x"}}})
		_, err := a.accountStatus(context.Background())
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.HTTPStatus != 500 {
			t.Fatalf("erwartet *APIError HTTP 500, bekommen %v", err)
		}
	})

	t.Run("network error", func(t *testing.T) {
		a := &authClient{
			hc:      &http.Client{Timeout: 50 * time.Millisecond},
			baseURL: "http://127.0.0.1:1",
			creds:   Credentials{Username: "test@example.invalid", Password: "test-pw"},
			store:   NewFileSessionStore(filepath.Join(t.TempDir(), "session.json")),
		}
		_, err := a.accountStatus(context.Background())
		if err == nil {
			t.Fatalf("erwartet Network-Error, bekommen nil")
		}
	})
}

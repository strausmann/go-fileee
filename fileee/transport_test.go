package fileee

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// noopSleep ersetzt time.Sleep in Tests: Backoff-Delays sollen berechnet, aber nicht real
// abgewartet werden (Tests bleiben schnell+deterministisch, unabhängig von BaseDelay/MaxDelay).
func noopSleep(time.Duration) {}

func newTestTransport(t *testing.T, srv *httptest.Server, reauth reauthFunc) *rateLimitedTransport {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	u, _ := url.Parse(srv.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "XSRF-TOKEN", Value: "xsrf-initial"}})
	return &rateLimitedTransport{
		base:    http.DefaultTransport,
		limiter: newLimiter(1000, 1000), // Rate-Limit im Test praktisch deaktivieren
		backoff: &ExponentialBackoff{BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond, MaxAttempts: 3},
		jar:     jar,
		baseURL: srv.URL,
		reauth:  reauth,
		sleep:   noopSleep,
	}
}

func TestRoundTripHappyPathOhneRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	transport := newTestTransport(t, srv, nil)
	client := &http.Client{Transport: transport}
	resp, err := client.Get(srv.URL + "/ok")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
	if calls != 1 {
		t.Fatalf("erwartet 1 Aufruf, bekommen %d", calls)
	}
}

func TestRoundTrip403LoestGenauEinenReauthUndRetryAus(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	var reauthCalls int32
	reauth := func(ctx context.Context) error {
		atomic.AddInt32(&reauthCalls, 1)
		return nil
	}
	transport := newTestTransport(t, srv, reauth)
	client := &http.Client{Transport: transport}
	resp, err := client.Get(srv.URL + "/geschuetzt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("erwartet 200 nach Reauth+Retry, bekommen %d", resp.StatusCode)
	}
	if reauthCalls != 1 {
		t.Fatalf("erwartet genau 1 Reauth-Aufruf, bekommen %d", reauthCalls)
	}
	if callCount != 2 {
		t.Fatalf("erwartet genau 2 HTTP-Aufrufe (1 fehlgeschlagen + 1 Retry), bekommen %d", callCount)
	}
}

func TestRoundTrip403BleibtNachReauthBestehenKeinZweiterLoop(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(403)
	}))
	defer srv.Close()
	var reauthCalls int32
	reauth := func(ctx context.Context) error {
		atomic.AddInt32(&reauthCalls, 1)
		return nil
	}
	transport := newTestTransport(t, srv, reauth)
	client := &http.Client{Transport: transport}
	resp, err := client.Get(srv.URL + "/immer-403")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("erwartet 403 bleibt bestehen, bekommen %d", resp.StatusCode)
	}
	if reauthCalls != 1 {
		t.Fatalf("erwartet genau 1 Reauth-Versuch (kein Loop), bekommen %d", reauthCalls)
	}
	if callCount != 2 {
		t.Fatalf("erwartet genau 2 HTTP-Aufrufe (kein dritter Versuch), bekommen %d", callCount)
	}
}

func TestRoundTripReauthFehlerWirdSofortDurchgereicht(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	defer srv.Close()
	reauth := func(ctx context.Context) error { return ErrSessionExpired }
	transport := newTestTransport(t, srv, reauth)
	client := &http.Client{Transport: transport}
	_, err := client.Get(srv.URL + "/x")
	if err == nil {
		t.Fatalf("erwartet Fehler, bekommen nil")
	}
}

func TestRoundTripIsReauthSkippedLoestKeinenReauthAus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	defer srv.Close()
	var reauthCalls int32
	reauth := func(ctx context.Context) error {
		atomic.AddInt32(&reauthCalls, 1)
		return nil
	}
	transport := newTestTransport(t, srv, reauth)
	client := &http.Client{Transport: transport}
	req, _ := http.NewRequestWithContext(withSkipReauth(context.Background()), http.MethodGet, srv.URL+"/auth-endpoint", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("erwartet 403 unverändert durchgereicht, bekommen %d", resp.StatusCode)
	}
	if reauthCalls != 0 {
		t.Fatalf("erwartet 0 Reauth-Aufrufe bei skip-markiertem Context, bekommen %d", reauthCalls)
	}
}

func Test500LoestBackoffRetriesBisMaxAttemptsAus(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(500)
	}))
	defer srv.Close()
	transport := newTestTransport(t, srv, nil)
	client := &http.Client{Transport: transport}
	resp, err := client.Get(srv.URL + "/kaputt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
	// MaxAttempts=3 im Test-Backoff -> 1 Erstversuch + 3 Retries = 4 Aufrufe insgesamt
	if calls != 4 {
		t.Fatalf("erwartet 4 Aufrufe (1+3 Retries), bekommen %d", calls)
	}
}

func TestNetzwerkfehlerLoestBackoffRetriesAus(t *testing.T) {
	transport := &rateLimitedTransport{
		base:    http.DefaultTransport,
		limiter: newLimiter(1000, 1000),
		backoff: &ExponentialBackoff{BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond, MaxAttempts: 2},
		sleep:   noopSleep,
	}
	client := &http.Client{Transport: transport, Timeout: time.Second}
	_, err := client.Get("http://127.0.0.1:1/nichts")
	if err == nil {
		t.Fatalf("erwartet Netzwerkfehler, bekommen nil")
	}
}

// TestXSRFHeaderNurBeiMutierendenMethoden ist der Regressionstest für Whole-Codebase-Review
// Finding C1: injectXSRF fragte den Cookie-Jar bisher fälschlich an der reinen baseURL (Pfad "/")
// ab. Der reale Fileee-Server setzt das XSRF-TOKEN-Cookie aber ohne explizites Path-Attribut bei
// GET /api/f/start — http.CookieJar leitet daraus per RFC 6265 §5.1.4 den Default-Path "/api/f"
// ab (Verzeichnis des Request-Pfads), NICHT "/". Damit das reale Verhalten statt eines
// künstlichen Test-Setups geprüft wird, holt dieser Test das Cookie über eine ECHTE
// /api/f/start-Antwort (kein explizites Path — genau wie im Live-Handshake), statt es per
// jar.SetCookies direkt an der Root-URL zu platzieren (das hätte den Bug maskiert, weil ein an
// Root gesetztes Cookie zufällig auch die alte, fehlerhafte Root-Abfrage matcht).
func TestXSRFHeaderNurBeiMutierendenMethoden(t *testing.T) {
	var gotHeaderOnPost, gotHeaderOnGet string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/f/start":
			// Bewusst OHNE Cookie.Path — spiegelt exakt das live-verifizierte Verhalten des
			// echten Fileee-Servers (siehe authCookieScopeURL-Kommentar in auth.go).
			http.SetCookie(w, &http.Cookie{Name: "XSRF-TOKEN", Value: "xsrf-echt"})
			w.WriteHeader(200)
		case r.Method == http.MethodPost:
			gotHeaderOnPost = r.Header.Get("x-xsrf-token")
			w.WriteHeader(200)
		default:
			gotHeaderOnGet = r.Header.Get("x-xsrf-token")
			w.WriteHeader(200)
		}
	}))
	defer srv.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	transport := &rateLimitedTransport{
		base:    http.DefaultTransport,
		limiter: newLimiter(1000, 1000),
		backoff: &ExponentialBackoff{BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond, MaxAttempts: 3},
		jar:     jar,
		baseURL: srv.URL,
		sleep:   noopSleep,
	}
	// client.Jar MUSS auf denselben Jar zeigen wie transport.jar — genau wie in der Produktion
	// (client.go: hc.Jar = jar; transport.jar = jar), damit die echte Set-Cookie-Antwort von
	// GET /api/f/start tatsächlich im selben Jar landet, den injectXSRF später abfragt.
	client := &http.Client{Transport: transport, Jar: jar}

	startResp, err := client.Get(srv.URL + "/api/f/start")
	if err != nil {
		t.Fatalf("GET /api/f/start: %v", err)
	}
	startResp.Body.Close()

	getResp, err := client.Get(srv.URL + "/lesen")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	getResp.Body.Close()

	postResp, err := client.Post(srv.URL+"/schreiben", "application/json", nil)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	postResp.Body.Close()

	if gotHeaderOnGet != "" {
		t.Fatalf("x-xsrf-token hätte bei GET NICHT gesetzt sein dürfen, war %q", gotHeaderOnGet)
	}
	if gotHeaderOnPost != "xsrf-echt" {
		t.Fatalf("x-xsrf-token bei POST = %q, erwartet xsrf-echt (aus echtem, pfadlosem /api/f/start-Cookie)", gotHeaderOnPost)
	}
}

// TestRoundTrip403StampedeLoestNurEinenReauthAus deckt den in den Projektregeln verbindlich
// geforderten Stampede-Schutz ab: mehrere gleichzeitige 403-Requests dürfen NICHT je einen
// eigenen Reauth ausloesen (das wuerde den Login-Endpunkt bei einer Session-Ablauf-Situation mit
// N parallelen Requests N-fach treffen). Der Server "entsperrt" sich global erst NACH dem
// (einzigen erwarteten) Reauth-Aufruf. Statt eines realen time.Sleep synchronisiert eine
// Channel-Barriere deterministisch: reauth() wartet, bis ALLE n Requests ihren ersten 403
// gesehen haben (jeder erhöhte firstAttempts-Zähler steht für einen Request, der seinen
// ersten 403 bereits erhalten hat, BEVOR er versucht reauthMu zu sperren) — das erzwingt das
// worst-case Stampede-Szenario ohne reales Warten und ohne Timing-Fragilität.
func TestRoundTrip403StampedeLoestNurEinenReauthAus(t *testing.T) {
	const n = 20

	var mu sync.Mutex
	authorized := false

	var firstAttempts int32
	allFirstAttemptsSeen := make(chan struct{})
	var closeOnce sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ok := authorized
		mu.Unlock()
		if !ok {
			if atomic.AddInt32(&firstAttempts, 1) == int32(n) {
				closeOnce.Do(func() { close(allFirstAttemptsSeen) })
			}
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var reauthCalls int32
	reauth := func(ctx context.Context) error {
		atomic.AddInt32(&reauthCalls, 1)
		// Deterministische Barriere statt time.Sleep: erst wenn alle n Requests ihren ersten
		// 403 gesehen haben, wird authorized gesetzt — garantiert das Stampede-Szenario ohne
		// reales Warten.
		select {
		case <-allFirstAttemptsSeen:
		case <-time.After(5 * time.Second):
			t.Errorf("Timeout: nicht alle %d Requests haben ihren ersten 403 gesehen", n)
		}
		mu.Lock()
		authorized = true
		mu.Unlock()
		return nil
	}
	transport := newTestTransport(t, srv, reauth)
	client := &http.Client{Transport: transport}

	var wg sync.WaitGroup
	errs := make([]error, n)
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := client.Get(srv.URL + "/geschuetzt")
			if err != nil {
				errs[i] = err
				return
			}
			codes[i] = resp.StatusCode
			resp.Body.Close()
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d: unerwarteter Fehler: %v", i, err)
		}
		if codes[i] != 200 {
			t.Fatalf("request %d: erwartet 200 nach Reauth, bekommen %d", i, codes[i])
		}
	}
	if reauthCalls != 1 {
		t.Fatalf("erwartet genau 1 Reauth-Aufruf trotz %d gleichzeitiger 403-Requests (kein Stampede), bekommen %d", n, reauthCalls)
	}
}

// TestRoundTrip403ReauthUeberTransportKeinSelbstDeadlock ist der REGRESSIONSTEST für den
// kritischen Fund: reauth() schickt seinen Handshake bewusst UEBER DENSELBEN Transport (mit
// withSkipReauth im Context) — genau wie es reauthenticate in Task 11 tun wird. Mit dem alten,
// unconditional reauthMu.Lock() am Epoch-Snapshot fuehrte das zu einem Selbst-Deadlock: derselbe
// Goroutine haelt reauthMu (Check-and-Reauth-Zweig) und versucht beim Handshake-Request erneut
// denselben (nicht-reentranten) Mutex zu sperren. Der Test läuft deshalb mit Timeout-Wache in
// einer eigenen Goroutine — ohne den Fix hängt er nach 2 Sekunden und schlägt fehl statt
// (schlimmer) den gesamten Testlauf hängen zu lassen.
func TestRoundTrip403ReauthUeberTransportKeinSelbstDeadlock(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/handshake" {
			w.WriteHeader(200)
			return
		}
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var client *http.Client
	reauth := func(ctx context.Context) error {
		// Genau der kritische Pfad: der Handshake läuft über denselben Transport (derselbe
		// http.Client, withSkipReauth-Context) — reentriert RoundTrip im selben Goroutine-Stack.
		req, err := http.NewRequestWithContext(withSkipReauth(ctx), http.MethodGet, srv.URL+"/auth/handshake", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
	transport := newTestTransport(t, srv, reauth)
	client = &http.Client{Transport: transport}

	done := make(chan struct{})
	var resp *http.Response
	var reqErr error
	go func() {
		defer close(done)
		resp, reqErr = client.Get(srv.URL + "/geschuetzt")
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RoundTrip deadlockte: Reauth-Request über denselben Transport hängt (reauthMu erneut gesperrt)")
	}

	if reqErr != nil {
		t.Fatalf("Get: %v", reqErr)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("erwartet 200 nach Reauth-über-Transport+Retry, bekommen %d", resp.StatusCode)
	}
}

// TestRoundTripXSRFTokenFrischNachReauth deckt die vom Reviewer als ungetestet markierte
// XSRF-Frische nach Reauth ab: rotiert reauth() das XSRF-TOKEN-Cookie im gemeinsamen Jar, MUSS
// der Retry-Request den NEUEN Wert im x-xsrf-token-Header tragen — nicht den alten.
func TestRoundTripXSRFTokenFrischNachReauth(t *testing.T) {
	var callCount int32
	var gotHeaderOnRetry string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			w.WriteHeader(403)
			return
		}
		gotHeaderOnRetry = r.Header.Get("x-xsrf-token")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	var transport *rateLimitedTransport
	reauth := func(ctx context.Context) error {
		u, err := url.Parse(srv.URL)
		if err != nil {
			return err
		}
		transport.jar.SetCookies(u, []*http.Cookie{{Name: "XSRF-TOKEN", Value: "xsrf-nach-reauth"}})
		return nil
	}
	transport = newTestTransport(t, srv, reauth)
	client := &http.Client{Transport: transport}

	resp, err := client.Post(srv.URL+"/schreiben", "application/json", nil)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	resp.Body.Close()

	if gotHeaderOnRetry != "xsrf-nach-reauth" {
		t.Fatalf("x-xsrf-token beim Retry = %q, erwartet frischen Wert xsrf-nach-reauth nach Reauth-Rotation", gotHeaderOnRetry)
	}
}

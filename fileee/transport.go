package fileee

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// reauthFunc wird vom Client (Task 11) gesetzt und ruft authClient.reauthenticate auf.
type reauthFunc func(ctx context.Context) error

// rateLimitedTransport ist der zentrale http.RoundTripper der Lib: Rate-Limit, XSRF-Injektion,
// 403-getriebener Re-Auth (Umbrella-Spec §4.5) und Backoff bei 429/5xx/Netzwerkfehlern (§7).
type rateLimitedTransport struct {
	base    http.RoundTripper
	limiter *rate.Limiter
	backoff BackoffPolicy
	jar     http.CookieJar
	baseURL string

	// reauthMu serialisiert NUR den Check-and-Reauth-Pfad bei einem 403 (siehe RoundTrip unten) —
	// NICHT den Epoch-Snapshot. reauthenticate (Task 11) schickt seinen Handshake bewusst über
	// DENSELBEN Transport (withSkipReauth) — ein Mutex, der auch beim Epoch-Snapshot jeder
	// RoundTrip-Iteration unconditional gehalten würde, deadlockt sich dadurch selbst (derselbe
	// Goroutine-Stack kommt während des Reauth erneut hier an; sync.Mutex ist nicht reentrant).
	// Deshalb ist der Snapshot unten lock-frei über atomic.Uint64 gelöst.
	// userAgent wird bei jedem Request gesetzt (siehe setUserAgent), damit die Fileee-Server-Logs
	// Client-Name + Version sehen statt des generischen "Go-http-client/1.1".
	userAgent string

	// logger emittiert secret-safe Debug-Events (Methode, Pfad, Status, Retry/Reauth) zum Debuggen.
	logger *slog.Logger

	reauthMu sync.Mutex
	reauth   reauthFunc
	// reauthEpoch wird bei jedem ERFOLGREICH abgeschlossenen Reauth erhöht (atomar — der reine
	// Lese-Snapshot braucht dafür KEIN reauthMu, nur der Check-and-Reauth-Vergleich in RoundTrip
	// bleibt mutex-geschützt). Ein RoundTrip merkt sich beim Start seines Versuchs den damals
	// aktuellen Epoch-Stand: sieht er später ein 403 und der Epoch-Stand hat sich in der
	// Zwischenzeit (durch einen PARALLELEN Request) bereits erhöht, hat ein anderer Goroutine
	// den Reauth für ihn bereits erledigt — er ruft t.reauth dann selbst NICHT nochmal auf,
	// sondern retryt direkt mit dem (im gemeinsamen Cookie-Jar bereits aktualisierten) frischen
	// Zustand. Das verhindert einen Stampede von N parallelen Reauth-Aufrufen, wenn N
	// gleichzeitige Requests dieselbe abgelaufene Session treffen — ohne dieses Epoch-Gate würde
	// jeder Request seinen eigenen Reauth auslösen (der Mutex serialisiert die Aufrufe nur
	// zeitlich, verhindert aber nicht deren Anzahl).
	reauthEpoch atomic.Uint64

	// sleep ist die injizierbare Wartefunktion für Backoff-Delays (Default: time.Sleep). Tests
	// setzen hier eine No-Op-Funktion, damit Retry-/Backoff-Pfade nicht real warten.
	sleep func(time.Duration)
}

// sleepFn liefert die zu verwendende Wartefunktion (injizierte Test-Funktion oder time.Sleep).
func (t *rateLimitedTransport) sleepFn() func(time.Duration) {
	if t.sleep != nil {
		return t.sleep
	}
	return time.Sleep
}

// bodyProvider liefert eine Funktion, die den Request-Body für jeden Versuch (Retry/Reauth) frisch
// erzeugt. Setzt der Aufrufer req.GetBody (net/http tut das automatisch für bytes/strings-Bodies),
// wird dieser genutzt und der Body NICHT in den RAM gepuffert — wichtig für große Uploads. Nur wenn
// GetBody fehlt (nicht reproduzierbarer Stream), wird der Body einmalig gepuffert. Ohne Body: nil.
func bodyProvider(req *http.Request) (func() (io.ReadCloser, error), error) {
	if req.Body == nil {
		return nil, nil
	}
	if req.GetBody != nil {
		req.Body.Close()
		return req.GetBody, nil
	}
	b, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return nil, err
	}
	return func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }, nil
}

// cloneRequest klont den Request für einen einzelnen Versuch und hängt einen frischen Body ein.
func cloneRequest(req *http.Request, getBody func() (io.ReadCloser, error)) (*http.Request, error) {
	clone := req.Clone(req.Context())
	if getBody != nil {
		body, err := getBody()
		if err != nil {
			return nil, err
		}
		clone.Body = body
	}
	return clone, nil
}

// apiCookieScopeURL liefert die URL, gegen die der Cookie-Jar nach Handshake-/XSRF-Cookies
// befragt wird. Bewusst NICHT die reine baseURL (Pfad "/"): Set-Cookie-Antworten des Handshakes
// (/api/f/start, /api/f/existent, /api/f/login) tragen kein explizites Path-Attribut, daher
// leitet http.CookieJar den Default-Path nach RFC 6265 §5.1.4 vom Request-Pfad ab — das
// Verzeichnis von z.B. "/api/f/login" ist "/api/f", NICHT "/". Eine Abfrage mit der reinen
// baseURL (Pfad "/") matcht diesen Cookie-Path dann NICHT (path-match schlägt fehl) und liefert
// still eine leere Cookie-Liste. Von authCookieScopeURL (auth.go) UND injectXSRF (hier) genutzt —
// beide brauchen exakt denselben Scope, DRY statt zwei Stellen mit demselben Pfad-Literal zu
// pflegen (Whole-Codebase-Review Finding C1: injectXSRF fragte bisher fälschlich an der reinen
// baseURL ab, wodurch der x-xsrf-token-Header bei JEDER echten mutierenden Anfrage leer blieb).
func apiCookieScopeURL(baseURL string) (*url.URL, error) {
	return url.Parse(baseURL + "/api/f/")
}

// injectXSRF setzt den x-xsrf-token-Header aus dem Cookie-Jar — nur bei mutierenden Methoden
// (POST/PUT/DELETE), da GET-Requests keinen CSRF-Schutz benötigen (Umbrella-Spec §4.5).
func (t *rateLimitedTransport) injectXSRF(req *http.Request) {
	switch req.Method {
	case http.MethodPost, http.MethodPut, http.MethodDelete:
	default:
		return
	}
	if t.jar == nil {
		return
	}
	u, err := apiCookieScopeURL(t.baseURL)
	if err != nil {
		return
	}
	for _, c := range t.jar.Cookies(u) {
		if c.Name == "XSRF-TOKEN" {
			req.Header.Set("x-xsrf-token", c.Value)
			return
		}
	}
}

// setUserAgent setzt den Lib-User-Agent auf dem Request. Überschreibt den Go-Default; ein
// bewusst gesetzter Konsumenten-User-Agent kommt über WithUserAgent/composeUserAgent hier bereits
// vorkombiniert an.
func (t *rateLimitedTransport) setUserAgent(req *http.Request) {
	if t.userAgent != "" {
		req.Header.Set("User-Agent", t.userAgent)
	}
}

// RoundTrip implementiert Rate-Limit, XSRF-Injektion, 403-getriebenen Re-Auth (genau 1 Retry,
// mutex-geschützt + stampede-sicher über reauthEpoch) und Backoff bei 429/5xx/Netzwerkfehlern
// (Umbrella-Spec §4.5/§7).
func (t *rateLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	getBody, err := bodyProvider(req)
	if err != nil {
		return nil, err
	}

	attempt := 0
	reauthed := false
	for {
		if err := t.limiter.Wait(req.Context()); err != nil {
			return nil, err
		}

		// Epoch-Stand VOR dem Versuch snapshotten — Grundlage für den Stampede-Check unten.
		// BEWUSST lock-frei (atomic Load, KEIN reauthMu.Lock()): reauthenticate schickt seinen
		// Handshake über denselben Transport (withSkipReauth), landet also im selben
		// Goroutine-Aufruf erneut genau hier. Ein Mutex-Lock an dieser Stelle wäre ein
		// Selbst-Deadlock, sobald Reauth über den Transport läuft.
		epochAtAttempt := t.reauthEpoch.Load()

		cloned, err := cloneRequest(req, getBody)
		if err != nil {
			return nil, err
		}
		t.injectXSRF(cloned)
		t.setUserAgent(cloned)

		resp, err := t.base.RoundTrip(cloned)
		if err != nil {
			if !t.backoff.ShouldRetry(attempt, nil, err) {
				return nil, err
			}
			t.sleepFn()(t.backoff.NextDelay(attempt))
			attempt++
			continue
		}

		if t.logger != nil {
			t.logger.Debug("fileee request",
				"method", cloned.Method, "path", cloned.URL.Path,
				"status", resp.StatusCode, "attempt", attempt)
		}

		if resp.StatusCode == http.StatusForbidden && !reauthed && t.reauth != nil && !isReauthSkipped(req.Context()) {
			reauthed = true
			resp.Body.Close()

			t.reauthMu.Lock()
			if t.reauthEpoch.Load() == epochAtAttempt {
				// Kein paralleler Request hat seit unserem Versuchsstart bereits
				// reauthentifiziert -> wir übernehmen den (einzigen) Reauth für diese
				// "Generation" abgelaufener Sessions. Edge-Case: läuft die Session GENAU
				// innerhalb dieses In-Flight-Fensters (zwischen Snapshot und hier) ein
				// weiteres Mal ab, führt das bewusst zu einem rohen 403 nach dem einen
				// erlaubten Retry — kein Loop, kein Deadlock, akzeptierter Trade-off.
				reauthErr := t.reauth(req.Context())
				if reauthErr == nil {
					t.reauthEpoch.Add(1)
				}
				t.reauthMu.Unlock()
				if reauthErr != nil {
					return nil, reauthErr
				}
			} else {
				// Ein anderer Request hat bereits reauthentifiziert (Epoch ist weiter) —
				// kein zweiter Reauth-Aufruf nötig, der Cookie-Jar trägt bereits den
				// frischen Zustand (Stampede-Schutz).
				t.reauthMu.Unlock()
			}
			continue
		}

		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return resp, nil
		}
		if !t.backoff.ShouldRetry(attempt, resp, nil) {
			return resp, nil
		}
		resp.Body.Close()
		t.sleepFn()(t.backoff.NextDelay(attempt))
		attempt++
	}
}

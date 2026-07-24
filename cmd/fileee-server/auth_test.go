package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// noExempt lehnt jeden Pfad als nicht-exempt ab — Standard-Fixture für Tests, die die
// Auth-Prüfung selbst testen (kein Pfad soll die Middleware umgehen).
func noExempt(string) bool { return false }

// okHandler ist ein einfacher next-Handler, der 200 OK liefert — Standard-Fixture, um zu
// prüfen, dass die Middleware bei Erfolg tatsächlich an next durchreicht.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// TestAPITokenAuth_NoHeader prüft: fehlt sowohl X-API-Key als auch Authorization, liefert die
// Middleware 401 mit dem exakten JSON-Body und Content-Type application/json — next wird NICHT
// aufgerufen.
func TestAPITokenAuth_NoHeader(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	r := httptest.NewRequest("GET", "/v1/documents", nil)
	w := httptest.NewRecorder()

	APITokenAuth("secret-token", noExempt, next).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	if body := strings.TrimSpace(w.Body.String()); body != `{"error":"unauthorized"}` {
		t.Fatalf("body = %q, want unauthorized JSON", body)
	}
	if called {
		t.Fatal("next wurde trotz fehlendem Token aufgerufen")
	}
}

// TestAPITokenAuth_WrongToken prüft: ein falscher X-API-Key liefert 401, next wird nicht
// aufgerufen.
func TestAPITokenAuth_WrongToken(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	r := httptest.NewRequest("GET", "/v1/documents", nil)
	r.Header.Set("X-API-Key", "wrong-token")
	w := httptest.NewRecorder()

	APITokenAuth("secret-token", noExempt, next).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if called {
		t.Fatal("next wurde trotz falschem Token aufgerufen")
	}
}

// TestAPITokenAuth_ValidXAPIKey prüft: ein korrekter X-API-Key-Header lässt den Request
// durch (200, next wird aufgerufen).
func TestAPITokenAuth_ValidXAPIKey(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/documents", nil)
	r.Header.Set("X-API-Key", "secret-token")
	w := httptest.NewRecorder()

	APITokenAuth("secret-token", noExempt, okHandler()).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// TestAPITokenAuth_ValidBearer prüft: ein korrekter Authorization: Bearer <token>-Header lässt
// den Request ebenfalls durch (200).
func TestAPITokenAuth_ValidBearer(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/documents", nil)
	r.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()

	APITokenAuth("secret-token", noExempt, okHandler()).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// TestAPITokenAuth_ExemptPathSkipsAuth prüft: liefert exempt(path) true, wird next OHNE
// Token-Prüfung aufgerufen — z. B. für /healthz, /openapi.json, /docs.
func TestAPITokenAuth_ExemptPathSkipsAuth(t *testing.T) {
	alwaysExempt := func(string) bool { return true }
	r := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()

	APITokenAuth("secret-token", alwaysExempt, okHandler()).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (exempt path)", w.Code)
	}
}

// TestAPITokenAuth_TokenNeverInResponseBody stellt sicher, dass der konfigurierte Token-Wert
// niemals im 401-Response-Body auftaucht — auch nicht bei einem "fast richtigen" Token, der
// als Präfix oder Teilstring des echten Tokens im Body landen könnte.
func TestAPITokenAuth_TokenNeverInResponseBody(t *testing.T) {
	const token = "super-secret-token-value"
	r := httptest.NewRequest("GET", "/v1/documents", nil)
	r.Header.Set("X-API-Key", "wrong-value-but-similar-super-secret")
	w := httptest.NewRecorder()

	APITokenAuth(token, noExempt, okHandler()).ServeHTTP(w, r)

	if strings.Contains(w.Body.String(), token) {
		t.Fatalf("Token-Wert taucht im 401-Body auf: %q", w.Body.String())
	}
}

// TestAPITokenAuth_DifferentLengthTokens prüft, dass ein Token mit abweichender Länge
// gegenüber dem konfigurierten Token sauber als falsch erkannt wird (kein Panic, kein
// versehentliches Akzeptieren durch die Constant-Time-Vergleichslogik bei Längen-Mismatch).
func TestAPITokenAuth_DifferentLengthTokens(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/documents", nil)
	r.Header.Set("X-API-Key", "short")
	w := httptest.NewRecorder()

	APITokenAuth("a-much-longer-configured-token", noExempt, okHandler()).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

// TestAPITokenAuth_EmptyConfiguredToken_NoHeader prüft die Sicherheitslücke aus dem
// Critical-Review-Finding: Ist die Middleware mit einem leeren konfigurierten Token
// (token == "") aufgesetzt — z. B. durch eine Fehlkonfiguration des Aufrufers — und der Client
// sendet KEINEN X-API-Key/Authorization-Header, MUSS die Middleware trotzdem mit 401
// antworten und next NICHT aufrufen. Ohne Fix liefert subtle.ConstantTimeCompare("", "") == 1
// und die Auth-Prüfung wird für JEDEN Request stillschweigend umgangen (Fail-Open statt
// Fail-Closed).
func TestAPITokenAuth_EmptyConfiguredToken_NoHeader(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	r := httptest.NewRequest("GET", "/v1/documents", nil)
	w := httptest.NewRecorder()

	APITokenAuth("", noExempt, next).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (leerer konfigurierter Token muss fail-closed sein)", w.Code)
	}
	if called {
		t.Fatal("next wurde trotz leerem konfiguriertem Token und fehlendem Header aufgerufen — Auth-Bypass")
	}
}

// TestAPITokenAuth_EmptyConfiguredToken_EmptyHeader prüft dieselbe Fail-Closed-Anforderung für
// den Fall, dass der Client explizit einen leeren X-API-Key-Header mitschickt (statt den Header
// ganz wegzulassen). Auch hier darf ein leerer konfigurierter Token niemals zu einem
// erfolgreichen Auth-Match führen.
func TestAPITokenAuth_EmptyConfiguredToken_EmptyHeader(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	r := httptest.NewRequest("GET", "/v1/documents", nil)
	r.Header.Set("X-API-Key", "")
	w := httptest.NewRecorder()

	APITokenAuth("", noExempt, next).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (leerer konfigurierter Token muss fail-closed sein)", w.Code)
	}
	if called {
		t.Fatal("next wurde trotz leerem konfiguriertem Token und leerem X-API-Key aufgerufen — Auth-Bypass")
	}
}

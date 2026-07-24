package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestClientIP ist der Brief-vorgegebene Ausgangstest: nicht-vertrauenswürdige TCP-Quelle
// ignoriert Header (Anti-Spoofing), vertrauenswürdige TCP-Quelle übernimmt CF-Connecting-IP.
func TestClientIP(t *testing.T) {
	trusted := []string{"10.0.0.0/8"}
	order := []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:5000"
	r.Header.Set("X-Forwarded-For", "1.1.1.1")
	if ip := clientIP(r, trusted, order); ip != "203.0.113.9" {
		t.Fatalf("untrusted proxy: %s", ip)
	}
	r.RemoteAddr = "10.0.0.5:5000"
	r.Header.Set("CF-Connecting-IP", "9.9.9.9")
	if ip := clientIP(r, trusted, order); ip != "9.9.9.9" {
		t.Fatalf("cf: %s", ip)
	}
}

// TestClientIP_UntrustedIgnoresAllHeaders stellt sicher, dass bei nicht-vertrauenswürdiger
// TCP-Quelle ALLE Header ignoriert werden (nicht nur X-Forwarded-For) — auch wenn zusätzlich
// CF-Connecting-IP gesetzt ist, gewinnt die TCP-Quelle.
func TestClientIP_UntrustedIgnoresAllHeaders(t *testing.T) {
	trusted := []string{"10.0.0.0/8"}
	order := []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:5000"
	r.Header.Set("CF-Connecting-IP", "9.9.9.9")
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 10.0.0.5")
	if ip := clientIP(r, trusted, order); ip != "203.0.113.9" {
		t.Fatalf("untrusted source sollte TCP-IP liefern, Header ignorieren: %s", ip)
	}
}

// TestClientIP_TrustedXFFSingleHop prüft den einfachen X-Forwarded-For-Fall: eine
// vertrauenswürdige TCP-Quelle mit genau einem nicht-vertrauenswürdigen Hop liefert dessen IP.
func TestClientIP_TrustedXFFSingleHop(t *testing.T) {
	trusted := []string{"10.0.0.0/8"}
	order := []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 10.0.0.5")
	if ip := clientIP(r, trusted, order); ip != "1.1.1.1" {
		t.Fatalf("xff single hop: %s", ip)
	}
}

// TestClientIP_TrustedXFFMultiHop prüft die vom Review geforderte Mehrfach-Hop-Kette: bei
// mehreren vertrauenswürdigen Hops am rechten Ende von X-Forwarded-For wird von rechts nach
// links gewandert, bis der erste NICHT-vertrauenswürdige Eintrag gefunden wird.
func TestClientIP_TrustedXFFMultiHop(t *testing.T) {
	trusted := []string{"10.0.0.0/8"}
	order := []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 10.0.0.5, 10.0.0.6")
	if ip := clientIP(r, trusted, order); ip != "1.1.1.1" {
		t.Fatalf("xff multi hop: %s", ip)
	}
}

// TestClientIP_TrustedXFFAllTrusted prüft den Fallback, wenn ALLE X-Forwarded-For-Einträge
// vertrauenswürdig sind: dann wird der linkeste (älteste) Eintrag genommen.
func TestClientIP_TrustedXFFAllTrusted(t *testing.T) {
	trusted := []string{"10.0.0.0/8"}
	order := []string{"X-Forwarded-For"}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "10.0.0.4, 10.0.0.5, 10.0.0.6")
	if ip := clientIP(r, trusted, order); ip != "10.0.0.4" {
		t.Fatalf("xff all trusted: %s", ip)
	}
}

// TestClientIP_NoTrustedProxies prüft, dass eine leere trusted-Liste Header IMMER ignoriert —
// unabhängig von der TCP-Quelle.
func TestClientIP_NoTrustedProxies(t *testing.T) {
	order := []string{"CF-Connecting-IP"}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:5000"
	r.Header.Set("CF-Connecting-IP", "9.9.9.9")
	if ip := clientIP(r, nil, order); ip != "203.0.113.9" {
		t.Fatalf("empty trusted: %s", ip)
	}
}

// TestClientIP_BareIPInTrustedList prüft, dass eine bloße IP (ohne /-Suffix) in der
// trusted-Liste als /32 toleriert wird.
func TestClientIP_BareIPInTrustedList(t *testing.T) {
	trusted := []string{"10.0.0.1"}
	order := []string{"CF-Connecting-IP"}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:5000"
	r.Header.Set("CF-Connecting-IP", "9.9.9.9")
	if ip := clientIP(r, trusted, order); ip != "9.9.9.9" {
		t.Fatalf("bare ip trusted: %s", ip)
	}
	r.RemoteAddr = "10.0.0.2:5000"
	if ip := clientIP(r, trusted, order); ip != "10.0.0.2" {
		t.Fatalf("bare ip nicht in Liste sollte nicht trusted sein: %s", ip)
	}
}

// TestClientIP_IPv6Untrusted prüft den IPv6-Pfad für nicht-vertrauenswürdige TCP-Quellen:
// r.RemoteAddr in Kurzform "[ipv6]:port" liefert den reinen IPv6-Host, gesetzte Header werden
// ignoriert (gleiche Anti-Spoofing-Logik wie bei IPv4).
func TestClientIP_IPv6Untrusted(t *testing.T) {
	trusted := []string{"10.0.0.0/8"}
	order := []string{"CF-Connecting-IP"}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[2001:db8::1]:5000"
	r.Header.Set("CF-Connecting-IP", "9.9.9.9")
	if ip := clientIP(r, trusted, order); ip != "2001:db8::1" {
		t.Fatalf("untrusted ipv6 source sollte TCP-IP liefern, Header ignorieren: %s", ip)
	}
}

// TestClientIP_IPv6TrustedCIDR prüft, dass eine vertrauenswürdige IPv6-Quelle (via IPv6-CIDR
// in trusted) genauso wie bei IPv4 den Header übernimmt.
func TestClientIP_IPv6TrustedCIDR(t *testing.T) {
	trusted := []string{"2001:db8::/32"}
	order := []string{"CF-Connecting-IP"}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[2001:db8::1]:5000"
	r.Header.Set("CF-Connecting-IP", "9.9.9.9")
	if ip := clientIP(r, trusted, order); ip != "9.9.9.9" {
		t.Fatalf("trusted ipv6 cidr sollte Header uebernehmen: %s", ip)
	}
}

// TestClientIP_BareIPv6InTrustedList prüft, dass eine bloße IPv6-Adresse (ohne /-Suffix) in
// der trusted-Liste als /128-Host-Netz (exaktes Match) behandelt wird — analog zum
// IPv4-Bare-IP-Fall (TestClientIP_BareIPInTrustedList). Eine andere IPv6-Adresse ausserhalb
// des /128 darf NICHT vertrauenswürdig sein.
func TestClientIP_BareIPv6InTrustedList(t *testing.T) {
	trusted := []string{"2001:db8::1"}
	order := []string{"CF-Connecting-IP"}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[2001:db8::1]:5000"
	r.Header.Set("CF-Connecting-IP", "9.9.9.9")
	if ip := clientIP(r, trusted, order); ip != "9.9.9.9" {
		t.Fatalf("bare ipv6 exact match sollte trusted sein: %s", ip)
	}

	r.RemoteAddr = "[2001:db8::2]:5000"
	if ip := clientIP(r, trusted, order); ip != "2001:db8::2" {
		t.Fatalf("bare ipv6 ausserhalb /128 sollte NICHT trusted sein: %s", ip)
	}
}

// TestClientIP_MalformedTrustedFailsClosed prüft, dass eine trusted-Liste mit ausschliesslich
// nicht parsbaren Einträgen (Tippfehler, ungültiges CIDR-Suffix) NICHT dazu führt, dass
// irgendeine Quelle als vertrauenswürdig gilt — fail closed statt fail open. Ein per
// X-Forwarded-For vorgetäuschter Client darf in diesem Fall NICHT übernommen werden.
func TestClientIP_MalformedTrustedFailsClosed(t *testing.T) {
	trusted := []string{"not-an-ip", "10.0.0.0/999"}
	order := []string{"X-Forwarded-For"}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:5000"
	r.Header.Set("X-Forwarded-For", "1.1.1.1")
	if ip := clientIP(r, trusted, order); ip != "203.0.113.9" {
		t.Fatalf("malformed trusted sollte fail-closed TCP-IP liefern, geforgte XFF ignorieren: %s", ip)
	}
}

// TestClientIP_EmptyOrWhitespaceXFF dokumentiert das tatsächliche Verhalten einer
// vertrauenswürdigen Quelle bei einem leeren bzw. nur aus Leerzeichen bestehenden
// X-Forwarded-For-Wert. Ein wirklich leerer Header-Wert ("") wird von net/http's
// r.Header.Get(...) == "" erkannt und fällt (wie ein fehlender Header) auf den TCP-Quell-Host
// zurück. Ein NUR aus Leerzeichen bestehender Wert ("   ") ist für Header.Get dagegen NICHT
// leer, wird aber von firstUntrustedFromRight beim Trimmen vollständig herausgefiltert und
// liefert einen leeren String zurück — kein Panic, aber ein von "TCP-Host-Fallback"
// abweichendes, aber definiertes Verhalten. Beide Fälle werden hier explizit abgesichert,
// damit sie nicht unbemerkt regressieren.
func TestClientIP_EmptyOrWhitespaceXFF(t *testing.T) {
	trusted := []string{"10.0.0.0/8"}
	order := []string{"X-Forwarded-For"}

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "")
	if ip := clientIP(r, trusted, order); ip != "10.0.0.1" {
		t.Fatalf("leerer XFF-Wert sollte auf TCP-Host zurueckfallen: %q", ip)
	}

	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "10.0.0.1:5000"
	r2.Header.Set("X-Forwarded-For", "   ")
	if ip := clientIP(r2, trusted, order); ip != "" {
		t.Fatalf("nur-leerzeichen XFF-Wert sollte definierten leeren String liefern (kein Panic): %q", ip)
	}
}

// TestAccessLog schreibt eine NGINX-combined-Zeile in einen bytes.Buffer und prüft Status,
// Methode, Pfad und remote_user ("-", niemals ein Token-Wert).
func TestAccessLog(t *testing.T) {
	var buf bytes.Buffer
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hallo"))
	})
	handler := AccessLog(&buf, nil, nil, next)

	r := httptest.NewRequest("GET", "/pfad?geheimtoken=SUPERSECRET123", nil)
	r.RemoteAddr = "203.0.113.9:5000"
	r.Header.Set("Authorization", "Bearer geheimes-api-token-xyz")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	line := buf.String()
	if !strings.HasPrefix(line, "203.0.113.9 - -") {
		t.Fatalf("ip/remote_user Prefix fehlt: %q", line)
	}
	if !strings.Contains(line, `"GET /pfad?geheimtoken=SUPERSECRET123 HTTP/1.1" 418 5`) {
		t.Fatalf("request-line/status/bytes fehlt: %q", line)
	}
	if strings.Contains(line, "geheimes-api-token-xyz") {
		t.Fatalf("Token darf NIE im Access-Log landen: %q", line)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("Zeile muss mit Newline enden: %q", line)
	}
}

// TestAccessLog_DefaultStatus200 prüft, dass ohne expliziten WriteHeader-Aufruf der
// HTTP-Default-Status 200 protokolliert wird (statusRecorder-Default).
func TestAccessLog_DefaultStatus200(t *testing.T) {
	var buf bytes.Buffer
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	handler := AccessLog(&buf, nil, nil, next)

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:5000"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if !strings.Contains(buf.String(), " 200 2 ") {
		t.Fatalf("default status/bytes: %q", buf.String())
	}
}

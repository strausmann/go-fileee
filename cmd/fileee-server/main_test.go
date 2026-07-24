package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRunHealthcheck_OK prüft den Erfolgsfall: ein lokaler Server, der /healthz mit 200
// beantwortet, muss Exit-Code 0 liefern.
func TestRunHealthcheck_OK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Fatalf("unerwarteter Pfad: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	addr := ts.Listener.Addr().String()
	if got := runHealthcheck(addr); got != 0 {
		t.Fatalf("runHealthcheck(%q) = %d, erwartet 0", addr, got)
	}
}

// TestRunHealthcheck_NonOKStatus prüft, dass ein Nicht-2xx-Status (hier 500) als Exit-Code 1
// gewertet wird.
func TestRunHealthcheck_NonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	addr := ts.Listener.Addr().String()
	if got := runHealthcheck(addr); got != 1 {
		t.Fatalf("runHealthcheck(%q) = %d, erwartet 1", addr, got)
	}
}

// TestRunHealthcheck_Unreachable prüft, dass eine nicht erreichbare Adresse (Verbindungsfehler)
// als Exit-Code 1 gewertet wird. Der Listener wird VOR dem Aufruf geschlossen, damit der Port
// mit hoher Wahrscheinlichkeit niemand anderem gehört und die Verbindung sofort abgelehnt wird.
func TestRunHealthcheck_Unreachable(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if got := runHealthcheck(addr); got != 1 {
		t.Fatalf("runHealthcheck(%q) = %d, erwartet 1", addr, got)
	}
}

// TestHealthcheckAddr prüft, dass healthcheckAddr den Port aus FILEEE_LISTEN_ADDR per
// net.SplitHostPort übernimmt, den Host dabei aber IMMER auf 127.0.0.1 setzt — auch wenn
// FILEEE_LISTEN_ADDR einen anderen/keinen Host trägt — und ohne gesetzte Variable bzw. bei einem
// nicht parsbaren Wert auf den LoadConfig-Default-Port (8080) zurückfällt.
func TestHealthcheckAddr(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "leer/nicht gesetzt", env: map[string]string{}, want: "127.0.0.1:8080"},
		{name: "nur Port", env: map[string]string{"FILEEE_LISTEN_ADDR": ":9090"}, want: "127.0.0.1:9090"},
		{name: "Host wird verworfen", env: map[string]string{"FILEEE_LISTEN_ADDR": "0.0.0.0:9090"}, want: "127.0.0.1:9090"},
		{name: "nicht parsbar", env: map[string]string{"FILEEE_LISTEN_ADDR": "keinport"}, want: "127.0.0.1:8080"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			if got := healthcheckAddr(getenv); got != tc.want {
				t.Fatalf("healthcheckAddr() = %q, erwartet %q", got, tc.want)
			}
		})
	}
}

// TestLogLevel prüft die Übersetzung von Config.LogLevel-Strings in slog.Level, inkl. Fallback auf
// Info bei unbekanntem/leerem Wert.
func TestLogLevel(t *testing.T) {
	cases := map[string]string{
		"debug":       "DEBUG",
		"DEBUG":       "DEBUG",
		"warn":        "WARN",
		"warning":     "WARN",
		"error":       "ERROR",
		"info":        "INFO",
		"":            "INFO",
		"unbekannt42": "INFO",
	}
	for in, want := range cases {
		if got := logLevel(in).String(); got != want {
			t.Errorf("logLevel(%q) = %s, erwartet %s", in, got, want)
		}
	}
}

package fileee

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockRoute beschreibt die Antwort einer einzelnen Methode+Pfad-Kombination im Mock-Server.
type mockRoute struct {
	Status  int
	Body    []byte
	Headers map[string]string
	Cookies []*http.Cookie
}

// jsonHandler baut einen http.HandlerFunc aus einer Routing-Tabelle "METHODE PFAD" -> mockRoute.
// Für nicht hinterlegte Routen wird 404 mit einem generischen ApiError-Body geliefert, statt den Test
// stillschweigend durchfallen zu lassen (macht fehlende Fixtures sofort sichtbar).
func jsonHandler(t *testing.T, routes map[string]mockRoute) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		route, ok := routes[key]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"apiError":"route_not_mocked","errorMessage":"keine Mock-Route für ` + key + `"}`))
			return
		}
		for name, value := range route.Headers {
			w.Header().Set(name, value)
		}
		for _, c := range route.Cookies {
			http.SetCookie(w, c)
		}
		if route.Headers["Content-Type"] == "" && len(route.Body) > 0 {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(route.Status)
		if len(route.Body) > 0 {
			_, _ = w.Write(route.Body)
		}
	}
}

// newMockServer startet einen httptest.Server mit dem gegebenen Handler und schließt ihn
// automatisch am Testende. Wird von jeder weiteren Testdatei der Core-Lib genutzt, damit gegen
// Fixtures statt gegen die echte Fileee-Infra getestet wird (ADR-0004).
func newMockServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// TestJSONHandlerRoutesAndFallback ist der Selbsttest des Mock-Helpers: eine hinterlegte Route
// liefert Status+Body+Cookie korrekt, eine nicht hinterlegte Route liefert 404 mit ApiError-Body.
func TestJSONHandlerRoutesAndFallback(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/f/start": {
			Status:  200,
			Cookies: []*http.Cookie{{Name: "XSRF-TOKEN", Value: "test-xsrf-value"}},
		},
	}
	srv := newMockServer(t, jsonHandler(t, routes))

	resp, err := http.Get(srv.URL + "/api/f/start")
	if err != nil {
		t.Fatalf("GET /api/f/start: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("erwartet 200, bekommen %d", resp.StatusCode)
	}
	var sawXSRF bool
	for _, c := range resp.Cookies() {
		if c.Name == "XSRF-TOKEN" && c.Value == "test-xsrf-value" {
			sawXSRF = true
		}
	}
	if !sawXSRF {
		t.Fatalf("erwartetes XSRF-TOKEN-Cookie fehlt in der Antwort")
	}

	resp2, err := http.Get(srv.URL + "/api/unbekannt")
	if err != nil {
		t.Fatalf("GET /api/unbekannt: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Fatalf("erwartet 404 für nicht gemockte Route, bekommen %d", resp2.StatusCode)
	}
}

// TestJSONHandlerCustomHeaderAndBody deckt die Headers-Schleife und den Body-Write-Pfad ab:
// eine Route mit einem Custom-Header und nicht-leerem Body muss beides unverändert in der
// Antwort ausliefern, und der automatisch gesetzte "application/json"-Content-Type darf dabei
// nicht verloren gehen (Headers["Content-Type"] ist in dieser Route nicht explizit gesetzt).
func TestJSONHandlerCustomHeaderAndBody(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	routes := map[string]mockRoute{
		"GET /api/f/documents": {
			Status:  201,
			Body:    body,
			Headers: map[string]string{"X-Custom-Header": "custom-value"},
		},
	}
	srv := newMockServer(t, jsonHandler(t, routes))

	resp, err := http.Get(srv.URL + "/api/f/documents")
	if err != nil {
		t.Fatalf("GET /api/f/documents: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		t.Fatalf("erwartet 201, bekommen %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Custom-Header"); got != "custom-value" {
		t.Fatalf("erwarteter Custom-Header fehlt oder falsch: %q", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("erwartet automatisch gesetzten Content-Type application/json, bekommen %q", got)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Body lesen: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("erwarteter Body %q, bekommen %q", body, got)
	}
}

// TestJSONHandlerRespectsExplicitContentType belegt, dass ein explizit in Headers gesetzter
// Content-Type NICHT vom automatischen "application/json"-Default überschrieben wird.
func TestJSONHandlerRespectsExplicitContentType(t *testing.T) {
	body := []byte("plain text body")
	routes := map[string]mockRoute{
		"GET /api/f/plain": {
			Status:  200,
			Body:    body,
			Headers: map[string]string{"Content-Type": "text/plain"},
		},
	}
	srv := newMockServer(t, jsonHandler(t, routes))

	resp, err := http.Get(srv.URL + "/api/f/plain")
	if err != nil {
		t.Fatalf("GET /api/f/plain: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); got != "text/plain" {
		t.Fatalf("erwartet explizit gesetzten Content-Type text/plain, bekommen %q (wurde überschrieben)", got)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Body lesen: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("erwarteter Body %q, bekommen %q", body, got)
	}
}

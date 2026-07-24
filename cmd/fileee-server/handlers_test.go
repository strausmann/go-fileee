package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/strausmann/go-fileee/fileee"
)

// testAPIToken ist der feste API-Token, mit dem newTestServer den Server aufsetzt — Tests
// authentifizieren sich damit gegen geschützte Routen.
const testAPIToken = "test-token-6"

// newTestFileeeClient baut einen *fileee.Client, der gegen einen leeren httptest-Mock-Server
// (keine hinterlegten Routen) verdrahtet ist — die Lib-eigenen `package fileee`-Test-Helfer
// (newMockServer/newTestClientAgainstMock, mockserver_test.go) sind unexportiert und aus
// `package main` heraus nicht erreichbar; dieser Helfer nutzt deshalb ausschließlich
// exportierte Symbole (fileee.New, fileee.WithBaseURL, fileee.WithSessionStore,
// fileee.NewFileSessionStore). fileee.New führt selbst KEINEN Login/Roundtrip aus ("Es wird
// NICHT sofort eingeloggt", client.go) — für Task 6 (nur /healthz, OpenAPI, Auth-Exempt-Logik,
// keine Domänen-Routen) genügt deshalb ein Mock-Server ganz ohne registrierte Routen; ein
// tatsächlicher Fileee-Roundtrip findet in keinem der drei Testfälle statt.
func newTestFileeeClient(t *testing.T) *fileee.Client {
	t.Helper()

	mockSrv := httptest.NewServer(http.NewServeMux())
	t.Cleanup(mockSrv.Close)

	store := fileee.NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))
	creds := fileee.Credentials{Username: "test@example.invalid", Password: "test-pw"}

	fc, err := fileee.New(creds, fileee.WithBaseURL(mockSrv.URL), fileee.WithSessionStore(store))
	if err != nil {
		t.Fatalf("fileee.New: %v", err)
	}
	return fc
}

// newTestServer baut einen einsatzbereiten *Server (fester API-Token testAPIToken,
// DocsPublic=true, gegen einen leeren Fileee-Mock verdrahtet) und einen httptest.Server, der
// dessen Handler() ausliefert — Handler-Tests schicken echte HTTP-Requests gegen
// zurückgegebenen httptest.Server.URL. Der Logger verwirft alle Ausgaben (io.Discard), damit
// Tests keinen Log-Rausch erzeugen.
func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()

	cfg := Config{
		APIToken:        testAPIToken,
		DocsPublic:      true,
		ClientIPHeaders: defaultClientIPHeaders,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fc := newTestFileeeClient(t)

	s := NewServer(cfg, fc, log)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	return s, ts
}

// TestHealthz_NoTokenRequired prüft: GET /healthz antwortet mit 200, ganz ohne API-Token —
// Monitoring/Health-Checks dürfen nicht am Auth-Gate scheitern.
func TestHealthz_NoTokenRequired(t *testing.T) {
	_, ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestOpenAPIJSON_NoTokenRequired prüft: GET /openapi.json antwortet mit 200 (ohne Token) und
// liefert ein OpenAPI-3.1-Dokument — die maschinenlesbare API-Beschreibung muss ohne Secret
// abrufbar sein (z. B. für Codegenerierung in N8N/CI).
func TestOpenAPIJSON_NoTokenRequired(t *testing.T) {
	_, ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/openapi.json")
	if err != nil {
		t.Fatalf("GET /openapi.json: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Body lesen: %v", err)
	}
	if !strings.Contains(string(body), `"openapi":"3.1`) {
		t.Fatalf("Body enthält kein OpenAPI-3.1-Feld: %s", body)
	}
}

// TestV1PathWithoutToken_Unauthorized prüft: ein /v1/...-Pfad (Domänen-Route, in Task 6 noch
// nicht registriert) liefert OHNE Token 401 — die Auth-Middleware greift bereits VOR dem Mux
// und lehnt jeden nicht-exempten Pfad unabhängig davon ab, ob dahinter überhaupt eine Route
// registriert ist.
func TestV1PathWithoutToken_Unauthorized(t *testing.T) {
	_, ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/v1/documents")
	if err != nil {
		t.Fatalf("GET /v1/documents: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestDocs_NoTokenRequiredWhenPublic prüft: GET /docs antwortet mit 200 ohne Token, solange
// cfg.DocsPublic true ist (newTestServer-Default) — ergänzt die Brief-Pflichtfälle um den
// dritten Exempt-Zweig aus isAuthExempt (server.go).
func TestDocs_NoTokenRequiredWhenPublic(t *testing.T) {
	_, ts := newTestServer(t)

	resp, err := http.Get(ts.URL + "/docs")
	if err != nil {
		t.Fatalf("GET /docs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestDocs_TokenRequiredWhenNotPublic prüft den Kehrfall: ist cfg.DocsPublic false, verlangt
// /docs denselben Token wie jede andere geschützte Route (401 ohne Token).
func TestDocs_TokenRequiredWhenNotPublic(t *testing.T) {
	cfg := Config{
		APIToken:        testAPIToken,
		DocsPublic:      false,
		ClientIPHeaders: defaultClientIPHeaders,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fc := newTestFileeeClient(t)
	s := NewServer(cfg, fc, log)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/docs")
	if err != nil {
		t.Fatalf("GET /docs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (DocsPublic=false)", resp.StatusCode)
	}
}

// TestHealthz_ValidTokenAlsoWorks stellt sicher, dass ein mitgeschickter (korrekter) Token
// /healthz nicht etwa blockiert — exempt bedeutet "Token optional", nicht "Token verboten".
func TestHealthz_ValidTokenAlsoWorks(t *testing.T) {
	_, ts := newTestServer(t)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-API-Key", testAPIToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz mit Token: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

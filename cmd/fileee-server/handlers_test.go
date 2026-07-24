package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/strausmann/go-fileee/fileee"
)

// testAPIToken ist der feste API-Token, mit dem newTestServer den Server aufsetzt — Tests
// authentifizieren sich damit gegen geschützte Routen.
const testAPIToken = "test-token-6"

// mockRoute ist eine Fixture-Antwort für eine "METHODE /pfad"-Kombination des Mock-Fileee-Servers
// in diesem Testpaket — analog zu fileee.mockRoute (fileee/mockserver_test.go), hier eigenständig
// definiert, da das paketinterne `package fileee`-Test-Symbol aus `package main` nicht erreichbar
// ist.
type mockRoute struct {
	// Status ist der HTTP-Statuscode der Fixture-Antwort.
	Status int
	// Body ist der rohe Response-Body (i.d.R. JSON). Leer = kein Body geschrieben.
	Body []byte
}

// newTestFileeeClient baut einen *fileee.Client, der gegen einen httptest-Mock-Server verdrahtet
// ist — die Lib-eigenen `package fileee`-Test-Helfer (newMockServer/jsonHandler,
// mockserver_test.go) sind unexportiert und aus `package main` heraus nicht erreichbar; dieser
// Helfer nutzt deshalb ausschließlich exportierte Symbole (fileee.New, fileee.WithBaseURL,
// fileee.WithSessionStore, fileee.NewFileSessionStore). routes bildet zusätzliche
// "METHODE /pfad"-Kombinationen auf dem Lib-Upstream (z.B. "GET /api/documents/rest/doc-1") auf
// Fixture-Antworten ab — Task 6 brauchte dafür noch nichts (nur /healthz, OpenAPI,
// Auth-Exempt-Logik, keine Domänen-Routen); Task-7-Handler rufen dagegen tatsächlich
// Lib-Methoden auf, die Upstream-Requests auslösen.
//
// EnsureSession-Kurzschluss (fileee/auth.go, ensureSession): die Route "GET /api/f/user-session"
// wird HIER fest verdrahtet und liefert immer {"authorized":true,"secondsBlocked":0}. Kombiniert
// mit einer VORAB in den SessionStore geschriebenen Session mit mindestens einem Cookie (siehe
// store.Save unten) nimmt ensureSession den Zweig "gespeicherte Session vorhanden + Verify OK",
// markiert die Session als frisch und kehrt zurück — OHNE den vollen Login-/TOTP-Reauth-Flow
// auszulösen (der ohne einen dedizierten Login-Mock fehlschlagen würde, siehe
// fileee/auth.go ensureSession: nur bei FEHLENDER/leerer gespeicherter Session oder nicht
// autorisiertem user-session-Ergebnis wird reauthenticate aufgerufen). Ein echter
// Fileee-Login-Roundtrip findet in keinem Handler-Test statt.
func newTestFileeeClient(t *testing.T, routes map[string]mockRoute) *fileee.Client {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/f/user-session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"authorized":true,"secondsBlocked":0}`))
	})
	for pattern, route := range routes {
		route := route
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if len(route.Body) > 0 {
				w.Header().Set("Content-Type", "application/json")
			}
			w.WriteHeader(route.Status)
			if len(route.Body) > 0 {
				_, _ = w.Write(route.Body)
			}
		})
	}

	mockSrv := httptest.NewServer(mux)
	t.Cleanup(mockSrv.Close)

	store := fileee.NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))
	seedSession := &fileee.Session{
		Cookies: []*http.Cookie{{Name: "JSESSIONID", Value: "test-session"}},
		SavedAt: time.Now(),
	}
	if err := store.Save(context.Background(), seedSession); err != nil {
		t.Fatalf("Session-Store vorbefüllen: %v", err)
	}

	creds := fileee.Credentials{Username: "test@example.invalid", Password: "test-pw"}

	fc, err := fileee.New(creds, fileee.WithBaseURL(mockSrv.URL), fileee.WithSessionStore(store))
	if err != nil {
		t.Fatalf("fileee.New: %v", err)
	}
	return fc
}

// newTestServer baut einen einsatzbereiten *Server (fester API-Token testAPIToken,
// DocsPublic=true, gegen einen Fileee-Mock mit den übergebenen routes verdrahtet, siehe
// newTestFileeeClient) und einen httptest.Server, der dessen Handler() ausliefert —
// Handler-Tests schicken echte HTTP-Requests gegen den zurückgegebenen httptest.Server.URL. Der
// Logger verwirft alle Ausgaben (io.Discard), damit Tests keinen Log-Rausch erzeugen. routes darf
// nil sein (Tests ohne Domänen-Roundtrip, z.B. /healthz/OpenAPI/Auth-Exempt-Fälle).
func newTestServer(t *testing.T, routes map[string]mockRoute) (*Server, *httptest.Server) {
	t.Helper()

	cfg := Config{
		APIToken:        testAPIToken,
		DocsPublic:      true,
		ClientIPHeaders: defaultClientIPHeaders,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fc := newTestFileeeClient(t, routes)

	s := NewServer(cfg, fc, log)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	return s, ts
}

// TestHealthz_NoTokenRequired prüft: GET /healthz antwortet mit 200, ganz ohne API-Token —
// Monitoring/Health-Checks dürfen nicht am Auth-Gate scheitern.
func TestHealthz_NoTokenRequired(t *testing.T) {
	_, ts := newTestServer(t, nil)

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
	_, ts := newTestServer(t, nil)

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

// TestV1PathWithoutToken_Unauthorized prüft: ein /v1/...-Pfad (seit Task 7 als Domänen-Route
// registriert) liefert OHNE Token 401 — die Auth-Middleware greift bereits VOR dem Mux und lehnt
// jeden nicht-exempten Pfad ab, unabhängig davon, ob dahinter überhaupt eine Route registriert
// ist.
func TestV1PathWithoutToken_Unauthorized(t *testing.T) {
	_, ts := newTestServer(t, nil)

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
	_, ts := newTestServer(t, nil)

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
	fc := newTestFileeeClient(t, nil)
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
	_, ts := newTestServer(t, nil)

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

// TestGetDocument_ReturnsJSONWithID prüft den Brief-Pflichtfall für GET /v1/documents/{id}: der
// Mock-Fileee liefert unter dem von restService[Document].Get erwarteten Upstream-Pfad
// ("GET /api/documents/rest/doc-1", fileee/service.go) ein Dokument-JSON, der Handler antwortet
// mit 200 und demselben "id"-Feld im Body.
func TestGetDocument_ReturnsJSONWithID(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/documents/rest/doc-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"doc-1","version":1,"status":"DONE","attributes":{"data":{}}}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/documents/doc-1", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-API-Key", testAPIToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/documents/doc-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Body lesen: %v", err)
	}
	if !strings.Contains(string(body), `"id":"doc-1"`) {
		t.Fatalf("Body enthält kein id=doc-1: %s", body)
	}
}

// TestGetPageOCR_ReturnsOCRTokenList prüft den Brief-Pflichtfall für GET /v1/pages/{id}/ocr: der
// Mock-Fileee liefert unter dem von Documents.PageOCR erwarteten Upstream-Pfad
// ("GET /api/pages/page-1", fileee/ocr.go) ein JSON-Array von OCR-Tokens, der Handler antwortet
// mit 200 und liefert diese Liste unverändert im Body.
func TestGetPageOCR_ReturnsOCRTokenList(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/pages/page-1": {
			Status: http.StatusOK,
			Body:   []byte(`[{"text":"Hallo","webappId":"w1","left":1,"top":2,"right":3,"bottom":4,"width":2,"height":2}]`),
		},
	}
	_, ts := newTestServer(t, routes)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/pages/page-1/ocr", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-API-Key", testAPIToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/pages/page-1/ocr: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var toks []fileee.OCRToken
	if err := json.NewDecoder(resp.Body).Decode(&toks); err != nil {
		t.Fatalf("Body als []OCRToken dekodieren: %v", err)
	}
	if len(toks) != 1 || toks[0].Text != "Hallo" {
		t.Fatalf("toks = %+v, want ein Token mit Text=Hallo", toks)
	}
}

// TestListTags_ReturnsList prüft den Brief-Pflichtfall für GET /v1/tags: der Mock-Fileee liefert
// unter dem von restService[Tag].Query erwarteten Upstream-Pfad ("POST /api/tags/rest/query",
// fileee/service.go, Wire-Form queryResultWire) eine Zeile, der Handler antwortet mit 200 und
// listet sie im "items"-Feld.
func TestListTags_ReturnsList(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/tags/rest/query": {
			Status: http.StatusOK,
			Body:   []byte(`{"rows":[{"id":"tag-1","name":"Rechnung"}],"totalRows":1}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/tags", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-API-Key", testAPIToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/tags: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var out entityListBody[fileee.Tag]
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body als entityListBody[Tag] dekodieren: %v", err)
	}
	if out.TotalRows != 1 || len(out.Items) != 1 || out.Items[0].Name != "Rechnung" {
		t.Fatalf("out = %+v, want ein Tag Name=Rechnung, TotalRows=1", out)
	}
}

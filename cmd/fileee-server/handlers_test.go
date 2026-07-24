package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
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

// testJWTWithSub baut ein UNSIGNIERTES, JWT-förmiges Token mit dem gegebenen "sub"-Claim (Header/
// Payload Base64-URL-kodiertes JSON, dritter Teil ein fester Platzhalter "sig") — analog zu
// jwtWithSub in fileee/conversations_write_test.go, hier eigenständig definiert, da unexportierte
// Test-Helfer aus `package fileee` von `package main` aus nicht erreichbar sind. Client.UserID
// (fileee/auth.go, jwtSub) liest NUR den "sub"-Claim aus, OHNE die Signatur zu prüfen — für Tests
// genügt deshalb ein unsigniertes Token.
func testJWTWithSub(sub string) string {
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	return "jwt " + enc(map[string]string{"typ": "JWT"}) + "." + enc(map[string]string{"sub": sub}) + ".sig"
}

// mockRoute ist eine Fixture-Antwort für eine "METHODE /pfad"-Kombination des Mock-Fileee-Servers
// in diesem Testpaket — analog zu fileee.mockRoute (fileee/mockserver_test.go), hier eigenständig
// definiert, da das paketinterne `package fileee`-Test-Symbol aus `package main` nicht erreichbar
// ist.
type mockRoute struct {
	// Status ist der HTTP-Statuscode der Fixture-Antwort.
	Status int
	// Body ist der rohe Response-Body (i.d.R. JSON). Leer = kein Body geschrieben.
	Body []byte
	// ContentType überschreibt den automatisch gesetzten "application/json"-Header (nur gesetzt,
	// wenn Body nicht leer ist) — gebraucht für Task-9-Fixtures, deren Body kein JSON ist (z. B.
	// "image/jpeg" für ein Seitenbild, "application/pdf" für ein Voll-PDF). Leer = Auto-Verhalten
	// wie bisher (application/json bei nicht-leerem Body, sonst kein Header).
	ContentType string
}

// newTestFileeeClient baut EINEN gemeinsamen httptest-Mock-Server und darauf verdrahtet sowohl
// einen *fileee.Client (fc) als auch einen credential-losen *fileee.ShareClient (sc, Task 9) — die
// Lib-eigenen `package fileee`-Test-Helfer (newMockServer/jsonHandler, mockserver_test.go) sind
// unexportiert und aus `package main` heraus nicht erreichbar; dieser Helfer nutzt deshalb
// ausschließlich exportierte Symbole (fileee.New, fileee.NewShareClient, fileee.WithBaseURL,
// fileee.WithStaticBaseURL, fileee.WithSessionStore, fileee.NewFileSessionStore,
// fileee.WithRateLimit). routes bildet zusätzliche "METHODE /pfad"-Kombinationen auf dem
// Lib-Upstream (z.B. "GET /api/documents/rest/doc-1", oder ein Go-1.22+-Wildcard-Pattern wie
// "POST /api/share-objects/{token}") auf Fixture-Antworten ab — Task 6 brauchte dafür noch nichts
// (nur /healthz, OpenAPI, Auth-Exempt-Logik, keine Domänen-Routen); Task-7/9-Handler rufen dagegen
// tatsächlich Lib-Methoden auf, die Upstream-Requests auslösen.
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
//
// ShareClient braucht KEINE Session (credential-los, fileee/shareclient.go) — die einzige von ihm
// benötigte feste Route ist "GET /api/f/start" (ensureXSRF holt darüber ein XSRF-Cookie; der
// zurückgegebene Status/Body ist dafür irrelevant, ensureXSRF prüft nur, dass der Roundtrip ohne
// Transport-Fehler durchläuft). sc zeigt mit BEIDEN Basis-URLs (baseURL UND staticBaseURL) auf
// DENSELBEN mockSrv — analog zu fileee.shareMockServer (fileee/shareclient_test.go), das ebenfalls
// einen einzigen Mock-Host für API- und Static-Pfad verwendet.
func newTestFileeeClient(t *testing.T, routes map[string]mockRoute) (*fileee.Client, *fileee.ShareClient) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/f/user-session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"authorized":true,"secondsBlocked":0}`))
	})
	mux.HandleFunc("GET /api/f/start", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	for pattern, route := range routes {
		route := route
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			ct := route.ContentType
			if ct == "" && len(route.Body) > 0 {
				ct = "application/json"
			}
			if ct != "" {
				w.Header().Set("Content-Type", ct)
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
	// JSESSIONID trägt bewusst ein JWT-förmiges Token MIT "sub"-Claim (testJWTWithSub, analog
	// jwtWithSub in fileee/conversations_write_test.go) statt eines beliebigen Strings (Vorzustand
	// vor Task 11: "test-session") — Client.UserID (fileee/auth.go) liest bevorzugt das
	// "userId"-Cookie, sonst den "sub"-Claim GENAU dieses Cookies, OHNE Signaturprüfung. Task-11-
	// Handler (SendMessage/ShareDocument/UnshareDocument, handlers_conversations.go) rufen
	// UserID intern auf; ein nicht-JWT-förmiger Wert ließ sie zuvor mit einem generischen,
	// nicht auf einen Sentinel-Fehler abbildbaren "user id not available in session"-Fehler
	// scheitern (mapError-Default-Fall, 500) — der EnsureSession-Kurzschluss selbst (siehe unten)
	// bleibt davon unberührt, da ensureSession nur die gespeicherten Cookies + user-session-Mock
	// prüft, nicht deren Inhalt.
	seedSession := &fileee.Session{
		Cookies: []*http.Cookie{{Name: "JSESSIONID", Value: testJWTWithSub("test-user-1")}},
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

	sc := fileee.NewShareClient(
		fileee.WithBaseURL(mockSrv.URL), fileee.WithStaticBaseURL(mockSrv.URL),
		fileee.WithRateLimit(1000, 1000),
	)

	return fc, sc
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
	fc, sc := newTestFileeeClient(t, routes)

	s := NewServer(cfg, fc, sc, log)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	return s, ts
}

// newTestServerWithConfig baut wie newTestServer einen einsatzbereiten *Server samt httptest.Server,
// erlaubt aber zusätzliche Config-Felder zu setzen, die newTestServer nicht exponiert (v.a.
// WaitTimeout/WaitMax für die Wait-Handler-Tests und MaxUploadBytes für die Upload-Size-Limit-Tests)
// — APIToken/DocsPublic/ClientIPHeaders werden trotzdem IMMER wie bei newTestServer belegt, damit
// die Token-Middleware in jedem Test funktioniert, unabhängig davon, was der Aufrufer in cfg
// mitgibt.
func newTestServerWithConfig(t *testing.T, cfg Config, routes map[string]mockRoute) (*Server, *httptest.Server) {
	t.Helper()

	cfg.APIToken = testAPIToken
	cfg.DocsPublic = true
	cfg.ClientIPHeaders = defaultClientIPHeaders

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fc, sc := newTestFileeeClient(t, routes)

	s := NewServer(cfg, fc, sc, log)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	return s, ts
}

// buildUploadMultipart baut den multipart/form-data-Body für POST /v1/documents: ein "file"-Feld
// (Inhalt content, Dateiname filename) und — falls title nicht leer ist — ein zusätzliches
// "title"-Textfeld. Liefert den fertigen Body sowie den passenden Content-Type-Header
// (inkl. Boundary).
func buildUploadMultipart(t *testing.T, filename, content, title string) (*bytes.Buffer, string) {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("Datei-Inhalt schreiben: %v", err)
	}
	if title != "" {
		if err := mw.WriteField("title", title); err != nil {
			t.Fatalf("title-Feld schreiben: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart schließen: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

// newAuthedRequest baut einen http.Request mit gesetztem API-Token-Header (X-API-Key) — kleine
// Abkürzung für die vielen Write-Handler-Tests dieser Datei, die alle denselben Header brauchen.
func newAuthedRequest(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("NewRequest %s %s: %v", method, url, err)
	}
	req.Header.Set("X-API-Key", testAPIToken)
	return req
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
	fc, sc := newTestFileeeClient(t, nil)
	s := NewServer(cfg, fc, sc, log)
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

// ---------------------------------------------------------------------------
// Task 8: Write-Handler (Upload/Update/Share/Boxes/Reminders/Contacts/Export/Wait)
// ---------------------------------------------------------------------------

// TestUploadDocument_Success prüft den Happy-Path von POST /v1/documents: der Mock-Fileee echot
// die vom Client generierte id unverändert zurück (kein Duplikat, analog
// fileee.TestUpload_NewDocumentNoError für dasselbe Muster in der Lib) — der Handler antwortet mit
// 200 und einem nicht-leeren "id"-Feld, ohne "isDuplicate". Braucht eine eigene, echoende
// Mock-Route statt des statischen mockRoute-Musters (newTestFileeeClient), weil die client-id
// zufällig von der Lib generiert wird (fileee.newObjectID) und daher nicht vorab bekannt ist.
func TestUploadDocument_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/f/user-session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"authorized":true,"secondsBlocked":0}`))
	})
	mux.HandleFunc("POST /api/documents/rest", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("Mock: multipart form parsen: %v", err)
		}
		id := r.FormValue("id")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"` + id + `","status":"CLASSIFIED"}`))
	})
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
	fc, err := fileee.New(
		fileee.Credentials{Username: "test@example.invalid", Password: "test-pw"},
		fileee.WithBaseURL(mockSrv.URL), fileee.WithSessionStore(store),
	)
	if err != nil {
		t.Fatalf("fileee.New: %v", err)
	}
	// sc wird von diesem Test nicht angesprochen (reiner Upload-Test) — trotzdem Pflichtfeld von
	// NewServer, daher minimal gegen denselben Mock verdrahtet (kein /api/f/start-Handler nötig,
	// da ensureXSRF hier nie aufgerufen wird).
	sc := fileee.NewShareClient(fileee.WithBaseURL(mockSrv.URL))

	cfg := Config{APIToken: testAPIToken, DocsPublic: true, ClientIPHeaders: defaultClientIPHeaders, MaxUploadBytes: 32 << 20}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewServer(cfg, fc, sc, log)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	body, contentType := buildUploadMultipart(t, "hello.txt", "PDFDATA", "Mein Titel")
	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/documents", body)
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/documents: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var doc fileee.Document
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("Body als fileee.Document dekodieren: %v", err)
	}
	if doc.ID == "" {
		t.Fatalf("doc.ID ist leer, want eine generierte client-id")
	}
}

// TestUploadDocument_DuplicateReturns409 ist der Brief-Pflichtfall: erkennt der Mock-Fileee ein
// Duplikat (liefert eine andere id als die client-generierte, siehe
// fileee.TestUpload_DuplicateReturnsError), antwortet der Handler mit 409 und einem Body, der
// zusätzlich zu error/code die id des bestehenden Dokuments sowie isDuplicate:true trägt
// (Design-Spec §12) — NICHT das generische {error,code}-Schema aus mapError.
func TestUploadDocument_DuplicateReturns409(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/documents/rest": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"existing-server-id","status":"CLASSIFIED"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	body, contentType := buildUploadMultipart(t, "hello.txt", "PDFDATA", "")
	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/documents", body)
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/documents: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 409, body=%s", resp.StatusCode, respBody)
	}
	var out struct {
		Error       string `json:"error"`
		Code        string `json:"code"`
		ID          string `json:"id"`
		IsDuplicate bool   `json:"isDuplicate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body dekodieren: %v", err)
	}
	if out.Code != "duplicate" || out.ID != "existing-server-id" || !out.IsDuplicate {
		t.Fatalf("out = %+v, want code=duplicate, id=existing-server-id, isDuplicate=true", out)
	}
}

// TestUploadDocument_ExceedsMaxUploadBytes prüft, dass uploadSizeLimit (server.go/
// handlers_documents.go) greift: bei einem auf 8 Bytes gedeckelten MaxUploadBytes lässt ein
// Multipart-Body, der diese Grenze überschreitet, r.ParseMultipartForm mit
// "http: request body too large" fehlschlagen — huma antwortet mit einem Validierungsfehler
// (422, siehe huma@v2.35.0 huma.go errStatus-Default), nicht mit 200/500.
func TestUploadDocument_ExceedsMaxUploadBytes(t *testing.T) {
	cfg := Config{MaxUploadBytes: 8}
	_, ts := newTestServerWithConfig(t, cfg, nil)

	body, contentType := buildUploadMultipart(t, "hello.txt", "DIESER INHALT IST GROESSER ALS ACHT BYTES", "")
	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/documents", body)
	req.Header.Set("Content-Type", contentType)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/documents: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("status = 200, want einen Fehlerstatus (Upload-Limit überschritten)")
	}
}

// TestUpdateDocument_Success prüft den Happy-Path von PUT /v1/documents/{id}: der Mock-Fileee
// liefert das aktualisierte Dokument, der Handler antwortet mit 200 und demselben Body.
func TestUpdateDocument_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"PUT /api/documents/rest/doc-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"doc-1","version":2,"status":"DONE"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPut, ts.URL+"/v1/documents/doc-1", strings.NewReader(`{"version":1,"status":"DONE"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /v1/documents/doc-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"version":2`) {
		t.Fatalf("Body enthält kein version=2: %s", body)
	}
}

// TestUpdateDocument_BackendError prüft, dass ein sonstiger Fileee-APIError (hier 400) unverändert
// mit seinem eigenen Status durchgereicht wird (mapError, Design-Spec §12 "sonstiger APIError →
// dessen HTTPStatus").
func TestUpdateDocument_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"PUT /api/documents/rest/doc-1": {
			Status: http.StatusBadRequest,
			Body:   []byte(`{"apiError":"BAD_REQUEST","errorMessage":"invalid data"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPut, ts.URL+"/v1/documents/doc-1", strings.NewReader(`{"version":1}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /v1/documents/doc-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// TestShareDocuments_Success ist der Brief-Pflichtfall: POST /v1/share liefert {link,shareId}
// unverändert aus fileee.Share (json:"link"/"shareId", siehe fileee/share.go) durch.
func TestShareDocuments_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/documents/rest/share": {
			Status: http.StatusOK,
			Body:   []byte(`{"link":"https://my.fileee.com/shared/abc123","shareId":"abc123"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/share", strings.NewReader(`{"documentIds":["doc-1"]}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/share: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var out fileee.Share
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body als fileee.Share dekodieren: %v", err)
	}
	if out.Link != "https://my.fileee.com/shared/abc123" || out.ShareID != "abc123" {
		t.Fatalf("out = %+v, want link/shareId aus der Mock-Antwort", out)
	}
}

// TestShareDocuments_BackendError prüft den Fehlerpfad von POST /v1/share (Backend-4xx passt
// unverändert durch mapError durch). Bewusst NICHT 403: ein 403-Antwortstatus löst in der Core-Lib
// automatisch genau einen Re-Auth-Versuch aus (fileee/transport.go RoundTrip,
// "resp.StatusCode == http.StatusForbidden && !reauthed && t.reauth != nil") — das würde hier den
// eigentlichen Test verfälschen (der Mock-Server implementiert keinen Login-Flow).
func TestShareDocuments_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/documents/rest/share": {
			Status: http.StatusBadRequest,
			Body:   []byte(`{"apiError":"BAD_REQUEST","errorMessage":"invalid documentIds"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/share", strings.NewReader(`{"documentIds":["doc-1"]}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/share: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// TestUnshareDocument_Success prüft den Happy-Path von POST /v1/documents/{id}/unshare — die
// Fileee-Mock-Antwort ist ein leerer 200er (closeAndCheck, fileee/boxes.go, akzeptiert 200/204),
// der Handler liefert dementsprechend 204 (kein Response-Body).
func TestUnshareDocument_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/documents/rest/doc-1/unshare": {Status: http.StatusOK},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/documents/doc-1/unshare", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/documents/doc-1/unshare: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 204, body=%s", resp.StatusCode, respBody)
	}
}

// TestUnshareDocument_BackendError prüft den Fehlerpfad von POST /v1/documents/{id}/unshare.
func TestUnshareDocument_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/documents/rest/doc-1/unshare": {
			Status: http.StatusNotFound,
			Body:   []byte(`{"apiError":"NOT_FOUND","errorMessage":"unknown document"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/documents/doc-1/unshare", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/documents/doc-1/unshare: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, respBody)
	}
}

// TestAddBoxDocument_Success prüft den Happy-Path von POST /v1/boxes/{boxId}/documents/{docId} —
// dünner Durchgriff auf Boxes.AddDocument, kein Destruktiv-Gate (Design-Spec §4.2), 204 ohne Body.
func TestAddBoxDocument_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/fileeeboxes/box-1/doc-1": {Status: http.StatusOK},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/boxes/box-1/documents/doc-1", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/boxes/box-1/documents/doc-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 204, body=%s", resp.StatusCode, respBody)
	}
}

// TestAddBoxDocument_BackendError prüft den Fehlerpfad von
// POST /v1/boxes/{boxId}/documents/{docId}.
func TestAddBoxDocument_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/fileeeboxes/box-1/doc-1": {
			Status: http.StatusNotFound,
			Body:   []byte(`{"apiError":"NOT_FOUND","errorMessage":"unknown box"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/boxes/box-1/documents/doc-1", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/boxes/box-1/documents/doc-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, respBody)
	}
}

// TestRemoveBoxDocument_Success prüft den Happy-Path von
// DELETE /v1/boxes/{boxId}/documents/{docId} — dünner Durchgriff auf Boxes.RemoveDocument.
func TestRemoveBoxDocument_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"DELETE /api/fileeeboxes/box-1/doc-1": {Status: http.StatusNoContent},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodDelete, ts.URL+"/v1/boxes/box-1/documents/doc-1", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /v1/boxes/box-1/documents/doc-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 204, body=%s", resp.StatusCode, respBody)
	}
}

// TestRemoveBoxDocument_BackendError prüft den Fehlerpfad von
// DELETE /v1/boxes/{boxId}/documents/{docId}.
func TestRemoveBoxDocument_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"DELETE /api/fileeeboxes/box-1/doc-1": {
			Status: http.StatusBadRequest,
			Body:   []byte(`{"apiError":"BAD_REQUEST","errorMessage":"cannot remove"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodDelete, ts.URL+"/v1/boxes/box-1/documents/doc-1", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /v1/boxes/box-1/documents/doc-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// TestCreateReminder_Success prüft den Happy-Path von POST /v1/reminders.
func TestCreateReminder_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/reminders/rest": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"rem-1","description":"Zahlung fällig","documentId":"doc-1","startDate":"2026-08-01","version":1}`),
		},
	}
	_, ts := newTestServer(t, routes)

	reqBody := `{"description":"Zahlung fällig","documentId":"doc-1","startDate":"2026-08-01"}`
	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/reminders", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/reminders: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var out fileee.Reminder
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body als fileee.Reminder dekodieren: %v", err)
	}
	if out.ID != "rem-1" {
		t.Fatalf("out.ID = %q, want rem-1", out.ID)
	}
}

// TestCreateReminder_BackendError prüft den Fehlerpfad von POST /v1/reminders.
func TestCreateReminder_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/reminders/rest": {
			Status: http.StatusBadRequest,
			Body:   []byte(`{"apiError":"BAD_REQUEST","errorMessage":"missing documentId"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/reminders", strings.NewReader(`{"description":"x"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/reminders: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// TestUpdateReminder_Success prüft den Happy-Path von PUT /v1/reminders/{id}.
func TestUpdateReminder_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"PUT /api/reminders/rest/rem-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"rem-1","description":"Zahlung erledigt","done":true,"version":2}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPut, ts.URL+"/v1/reminders/rem-1", strings.NewReader(`{"description":"Zahlung erledigt","done":true,"version":1}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /v1/reminders/rem-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"done":true`) {
		t.Fatalf("Body enthält kein done=true: %s", body)
	}
}

// TestUpdateReminder_BackendError prüft den Fehlerpfad von PUT /v1/reminders/{id}.
func TestUpdateReminder_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"PUT /api/reminders/rest/rem-1": {
			Status: http.StatusNotFound,
			Body:   []byte(`{"apiError":"NOT_FOUND","errorMessage":"unknown reminder"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPut, ts.URL+"/v1/reminders/rem-1", strings.NewReader(`{"version":1}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /v1/reminders/rem-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, respBody)
	}
}

// TestCreateContact_Success prüft den Happy-Path von POST /v1/contacts.
func TestCreateContact_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/contacts/rest": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"con-1","firstName":"Max","lastName":"Mustermann","version":1}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/contacts", strings.NewReader(`{"firstName":"Max","lastName":"Mustermann"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/contacts: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var out fileee.Contact
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body als fileee.Contact dekodieren: %v", err)
	}
	if out.ID != "con-1" {
		t.Fatalf("out.ID = %q, want con-1", out.ID)
	}
}

// TestCreateContact_BackendError prüft den Fehlerpfad von POST /v1/contacts.
func TestCreateContact_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/contacts/rest": {
			Status: http.StatusBadRequest,
			Body:   []byte(`{"apiError":"IllegalConditions","errorMessage":"invalid contact"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/contacts", strings.NewReader(`{"firstName":"Max"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/contacts: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// TestUpdateContact_Success prüft den Happy-Path von PUT /v1/contacts/{id}.
func TestUpdateContact_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"PUT /api/contacts/rest/con-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"con-1","firstName":"Maxine","lastName":"Mustermann","version":2}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPut, ts.URL+"/v1/contacts/con-1", strings.NewReader(`{"firstName":"Maxine","lastName":"Mustermann","version":1}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /v1/contacts/con-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"firstName":"Maxine"`) {
		t.Fatalf("Body enthält kein firstName=Maxine: %s", body)
	}
}

// TestUpdateContact_BackendError prüft den Fehlerpfad von PUT /v1/contacts/{id}.
func TestUpdateContact_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"PUT /api/contacts/rest/con-1": {
			Status: http.StatusNotFound,
			Body:   []byte(`{"apiError":"NOT_FOUND","errorMessage":"unknown contact"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPut, ts.URL+"/v1/contacts/con-1", strings.NewReader(`{"version":1}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /v1/contacts/con-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, respBody)
	}
}

// TestExportZip_Success prüft den Happy-Path von POST /v1/documents/export-zip: der Handler
// antwortet mit dem gestarteten Process-Objekt.
func TestExportZip_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/documents/rest/zip": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"proc-1","status":"Waiting","type":"io.fileee.shared.process.DownloadAllProcess"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/documents/export-zip", strings.NewReader(`{"documentIds":["doc-1"],"zipPassword":"geheim"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/documents/export-zip: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var out fileee.Process
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body als fileee.Process dekodieren: %v", err)
	}
	if out.ID != "proc-1" {
		t.Fatalf("out.ID = %q, want proc-1", out.ID)
	}
}

// TestExportZip_BackendError prüft den Fehlerpfad von POST /v1/documents/export-zip.
func TestExportZip_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/documents/rest/zip": {
			Status: http.StatusBadRequest,
			Body:   []byte(`{"apiError":"BAD_REQUEST","errorMessage":"invalid password"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/documents/export-zip", strings.NewReader(`{"zipPassword":""}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/documents/export-zip: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// TestGetProcess_Success prüft den Happy-Path von GET /v1/processes/{id} (einmaliger Poll).
func TestGetProcess_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/processes/proc-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"proc-1","status":"Done"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodGet, ts.URL+"/v1/processes/proc-1", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/processes/proc-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var out fileee.Process
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body als fileee.Process dekodieren: %v", err)
	}
	if out.Status != "Done" {
		t.Fatalf("out.Status = %q, want Done", out.Status)
	}
}

// TestWaitProcess_TerminalReturnsImmediately ist der Happy-Path von
// POST /v1/processes/{id}/wait: ist der Vorgang bereits beim ersten Poll terminal, liefert
// WaitForProcess sofort (ohne die Deadline abzuwarten) den finalen Status.
func TestWaitProcess_TerminalReturnsImmediately(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/processes/proc-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"proc-1","status":"Done"}`),
		},
	}
	cfg := Config{WaitTimeout: 5 * time.Second, WaitMax: 10 * time.Second}
	_, ts := newTestServerWithConfig(t, cfg, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/processes/proc-1/wait?timeout=5s", nil)

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/processes/proc-1/wait: %v", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var out fileee.Process
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body als fileee.Process dekodieren: %v", err)
	}
	if out.Status != "Done" {
		t.Fatalf("out.Status = %q, want Done", out.Status)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("elapsed = %v, want deutlich unter der 5s-Deadline (Vorgang war sofort terminal)", elapsed)
	}
}

// TestWaitProcess_NonTerminalTimeoutReturns200 ist der Brief-Pflichtfall: bleibt der Vorgang bis
// zum Ablauf der (auf timeout=1s gedeckelten) Deadline nicht-terminal ("Running"), antwortet der
// Handler MIT 200 und dem letzten Status — kein Fehler (Design-Spec §4.4). Das belegt den
// DeadlineExceeded-Zweig in handleWaitProcess (handlers_share.go): WaitForProcess selbst liefert in
// diesem Fall (nil, context.DeadlineExceeded) OHNE den letzten Status (fileee/export.go), der
// Handler muss also selbst nachpollen.
func TestWaitProcess_NonTerminalTimeoutReturns200(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/processes/proc-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"proc-1","status":"Running"}`),
		},
	}
	cfg := Config{WaitTimeout: 5 * time.Second, WaitMax: 5 * time.Second}
	_, ts := newTestServerWithConfig(t, cfg, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/processes/proc-1/wait?timeout=1s", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/processes/proc-1/wait: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (Nicht-Terminal + Timeout ist KEIN Fehler), body=%s", resp.StatusCode, respBody)
	}
	var out fileee.Process
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body als fileee.Process dekodieren: %v", err)
	}
	if out.Status != "Running" {
		t.Fatalf("out.Status = %q, want Running (letzter Poll-Status)", out.Status)
	}
}

// TestWaitProcess_TimeoutCappedByWaitMax prüft die Deckelung aus Design-Spec §4.4: fordert der
// Client ein Timeout von 10s an, cfg.WaitMax aber nur 1s, MUSS die effektive Wartezeit auf 1s
// gedeckelt werden — der Test belegt das indirekt über die Laufzeit (deutlich unter 10s) UND den
// 200-mit-Running-Body-Rückgabewert (identisch zum Nicht-Terminal-Timeout-Fall).
func TestWaitProcess_TimeoutCappedByWaitMax(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/processes/proc-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"proc-1","status":"Running"}`),
		},
	}
	cfg := Config{WaitTimeout: 5 * time.Second, WaitMax: 1 * time.Second}
	_, ts := newTestServerWithConfig(t, cfg, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/processes/proc-1/wait?timeout=10s", nil)

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/processes/proc-1/wait: %v", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("elapsed = %v, want deutlich unter den angeforderten 10s (WaitMax=1s muss gedeckelt haben)", elapsed)
	}
}

// TestWaitProcess_BackendError prüft, dass ein Fehler, der NICHT von der Deadline stammt (hier ein
// Backend-4xx beim allerersten Poll), unverändert über mapError durchgereicht wird — anders als der
// DeadlineExceeded-Fall antwortet der Handler hier NICHT mit 200.
func TestWaitProcess_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/processes/proc-1": {
			Status: http.StatusNotFound,
			Body:   []byte(`{"apiError":"NOT_FOUND","errorMessage":"unknown process"}`),
		},
	}
	cfg := Config{WaitTimeout: 5 * time.Second, WaitMax: 5 * time.Second}
	_, ts := newTestServerWithConfig(t, cfg, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/processes/proc-1/wait?timeout=1s", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/processes/proc-1/wait: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, respBody)
	}
}

// TestWaitProcess_InvalidTimeoutReturns400 prüft, dass ein nicht als Go-Duration parsbarer
// timeout-Query-Parameter mit 400 abgelehnt wird, statt z. B. den Default stillschweigend zu
// verwenden oder mit 500 zu scheitern.
func TestWaitProcess_InvalidTimeoutReturns400(t *testing.T) {
	cfg := Config{WaitTimeout: 5 * time.Second, WaitMax: 5 * time.Second}
	_, ts := newTestServerWithConfig(t, cfg, nil)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/processes/proc-1/wait?timeout=notaduration", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/processes/proc-1/wait: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// ---------------------------------------------------------------------------
// Task 9: Anonymer Share-Proxy (resolve/image/ocr/pdf über s.sc)
// ---------------------------------------------------------------------------

// TestResolveShare_Success prüft den Brief-Pflichtfall für POST /v1/share-objects/{token}: der
// Mock-Fileee liefert unter dem von ShareClient.Resolve erwarteten Upstream-Pfad
// ("POST /api/share-objects/tok123", fileee/shareclient.go) die aufgelöste Freigabe, der Handler
// antwortet mit 200 und liefert sharedBy sowie die Dokumentliste unverändert im Body.
func TestResolveShare_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/share-objects/tok123": {
			Status: http.StatusOK,
			Body: []byte(`{"id":"sh1","sharedBy":"Max Mustermann","sharedById":"u1",
				"created":"2026-07-24T00:00:00Z",
				"documents":[{"id":"doc1","title":"Rechnung","pageIds":["pg1","pg2"]}]}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/share-objects/tok123", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/share-objects/tok123: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}

	var obj fileee.SharedObject
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		t.Fatalf("Body als fileee.SharedObject dekodieren: %v", err)
	}
	if obj.SharedBy != "Max Mustermann" || len(obj.Documents) != 1 || obj.Documents[0].Title != "Rechnung" {
		t.Fatalf("obj = %+v, want SharedBy=Max Mustermann mit einem Dokument Title=Rechnung", obj)
	}
}

// TestResolveShare_BackendErrorReturns404 prüft den Fehlerpfad von POST /v1/share-objects/{token}:
// ein unbekannter/abgelaufener Token liefert vom Mock-Fileee 404, mapError übersetzt das
// 1:1 in den HTTP-Status der fileee-server-Antwort (Spec §12 "sonstiger *fileee.APIError → dessen
// eigener HTTPStatus").
func TestResolveShare_BackendErrorReturns404(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/share-objects/tok-unknown": {
			Status: http.StatusNotFound,
			Body:   []byte(`{"apiError":"NOT_FOUND","errorMessage":"unknown share token"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/share-objects/tok-unknown", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/share-objects/tok-unknown: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, respBody)
	}
}

// TestDownloadSharedPageImage_Success prüft GET /v1/share-objects/{token}/pages/{pageId}/image:
// ShareClient.DownloadPageImage braucht (anders als SharedPageOCR/DownloadSharedPDF) KEINEN
// vorherigen Resolve-Aufruf — der Handler streamt direkt vom eigenen Token-Endpunkt
// ("GET /api/v1/sharing/tok123/pg1", fileee/shareclient.go) auf den Huma-BodyWriter, ohne die
// Bytes im RAM zu puffern (analog handleDownloadPageImage, handlers_documents.go).
func TestDownloadSharedPageImage_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/v1/sharing/tok123/pg1": {
			Status:      http.StatusOK,
			Body:        []byte("JPEGDATA"),
			ContentType: "image/jpeg",
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodGet, ts.URL+"/v1/share-objects/tok123/pages/pg1/image", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/share-objects/tok123/pages/pg1/image: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Body lesen: %v", err)
	}
	if string(body) != "JPEGDATA" {
		t.Fatalf("body = %q, want %q", body, "JPEGDATA")
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg", ct)
	}
}

// sharedPageOCRMockServer baut einen Mock-Fileee-Server für den zweistufigen Ablauf von
// GET /v1/share-objects/{token}/pages/{pageId}/ocr: der Handler MUSS zuerst per Resolve die
// shareId/sharedById auflösen (fileee.SharedObject.ID/SharedByID), bevor er SharedPageOCR
// aufrufen kann (fileee/ocr.go SharedPageOCR verlangt shareID/sharedByID als Parameter — anders
// als DownloadPageImage kennt dieser Fileee-Endpunkt keinen direkten Token-Pfad). Die Mock-Route
// für /api/pages/{pageId} prüft deshalb explizit, dass share_id/shared_by aus der VORHERIGEN
// Resolve-Antwort ankommen — ein reiner mockRoute-Fixture-Eintrag könnte das nicht verifizieren.
func sharedPageOCRMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/f/start", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /api/share-objects/tok123", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"sh1","sharedBy":"Max","sharedById":"u1","created":"2026-07-24T00:00:00Z","documents":[{"id":"doc1","title":"Rechnung","pageIds":["pg1"]}]}`))
	})
	mux.HandleFunc("GET /api/pages/pg1", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("share_id") != "sh1" || r.URL.Query().Get("shared_by") != "u1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"text":"Hallo","webappId":"w1","left":1,"top":2,"right":3,"bottom":4,"width":2,"height":2}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestGetSharedPageOCR_Success prüft den Brief-Pflichtfall für
// GET /v1/share-objects/{token}/pages/{pageId}/ocr: Resolve + SharedPageOCR liefern zusammen die
// OCR-Tokenliste, der Handler antwortet mit 200 und der unveränderten Token-Liste im Body.
func TestGetSharedPageOCR_Success(t *testing.T) {
	srv := sharedPageOCRMockServer(t)
	sc := fileee.NewShareClient(fileee.WithBaseURL(srv.URL), fileee.WithStaticBaseURL(srv.URL), fileee.WithRateLimit(1000, 1000))
	fc, _ := newTestFileeeClient(t, nil)

	cfg := Config{APIToken: testAPIToken, DocsPublic: true, ClientIPHeaders: defaultClientIPHeaders}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewServer(cfg, fc, sc, log)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	req := newAuthedRequest(t, http.MethodGet, ts.URL+"/v1/share-objects/tok123/pages/pg1/ocr", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/share-objects/tok123/pages/pg1/ocr: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var toks []fileee.OCRToken
	if err := json.NewDecoder(resp.Body).Decode(&toks); err != nil {
		t.Fatalf("Body als []OCRToken dekodieren: %v", err)
	}
	if len(toks) != 1 || toks[0].Text != "Hallo" {
		t.Fatalf("toks = %+v, want ein Token mit Text=Hallo", toks)
	}
}

// sharedPDFMockServer baut einen Mock-Fileee-Server für den zweistufigen Ablauf von
// GET /v1/share-objects/{token}/documents/{docId}/pdf: der Handler löst den Token zuerst per
// Resolve auf (shareId aus fileee.SharedObject.ID), bevor er DownloadSharedPDF gegen den
// Static-Host aufruft (fileee/shareclient.go DownloadSharedPDF verlangt shareID, nicht token).
// pdfStatus/pdfBody steuern die Antwort der PDF-Route — so kann derselbe Aufbau sowohl für den
// Erfolgs- als auch für den Fehlerfall (TestDownloadSharedDocumentPDF_BackendErrorReturns404)
// wiederverwendet werden.
func sharedPDFMockServer(t *testing.T, pdfStatus int, pdfBody []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/f/start", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /api/share-objects/tok123", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"sh1","sharedBy":"Max","sharedById":"u1","created":"2026-07-24T00:00:00Z","documents":[{"id":"doc1","title":"Rechnung","pageIds":["pg1"]}]}`))
	})
	mux.HandleFunc("GET /shares/get/sh1/doc1/pdf", func(w http.ResponseWriter, r *http.Request) {
		if pdfStatus == http.StatusOK {
			w.Header().Set("Content-Type", "application/pdf")
		}
		w.WriteHeader(pdfStatus)
		if len(pdfBody) > 0 {
			_, _ = w.Write(pdfBody)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newTestServerWithShareClient baut einen Server, dessen sc auf srv zeigt (baseURL UND
// staticBaseURL, analog fileee.shareMockServer) — gemeinsamer Aufbau für die PDF-Tests unten.
func newTestServerWithShareClient(t *testing.T, srv *httptest.Server) (*Server, *httptest.Server) {
	t.Helper()
	sc := fileee.NewShareClient(fileee.WithBaseURL(srv.URL), fileee.WithStaticBaseURL(srv.URL), fileee.WithRateLimit(1000, 1000))
	fc, _ := newTestFileeeClient(t, nil)

	cfg := Config{APIToken: testAPIToken, DocsPublic: true, ClientIPHeaders: defaultClientIPHeaders}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewServer(cfg, fc, sc, log)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

// TestDownloadSharedDocumentPDF_Success prüft den Brief-Pflichtfall "PDF-Route streamt Bytes" für
// GET /v1/share-objects/{token}/documents/{docId}/pdf: Resolve liefert die shareId, die PDF-Route
// streamt die vom Static-Host gelieferten Bytes unverändert auf den Huma-BodyWriter.
func TestDownloadSharedDocumentPDF_Success(t *testing.T) {
	srv := sharedPDFMockServer(t, http.StatusOK, []byte("%PDF-1.7 fake"))
	_, ts := newTestServerWithShareClient(t, srv)

	req := newAuthedRequest(t, http.MethodGet, ts.URL+"/v1/share-objects/tok123/documents/doc1/pdf", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/share-objects/tok123/documents/doc1/pdf: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Body lesen: %v", err)
	}
	if string(body) != "%PDF-1.7 fake" {
		t.Fatalf("body = %q, want %q", body, "%PDF-1.7 fake")
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("Content-Type = %q, want application/pdf", ct)
	}
}

// TestDownloadSharedDocumentPDF_BackendErrorReturns404 prüft den Brief-Pflichtfall "download 4xx →
// gemappter Status": Resolve gelingt, aber die PDF-Route auf dem Static-Host meldet 404 (Dokument
// zwischenzeitlich gelöscht/nicht mehr Teil der Freigabe) — mapError übersetzt das in denselben
// HTTP-Status der fileee-server-Antwort.
func TestDownloadSharedDocumentPDF_BackendErrorReturns404(t *testing.T) {
	srv := sharedPDFMockServer(t, http.StatusNotFound, []byte(`{"apiError":"NOT_FOUND","errorMessage":"document not in share"}`))
	_, ts := newTestServerWithShareClient(t, srv)

	req := newAuthedRequest(t, http.MethodGet, ts.URL+"/v1/share-objects/tok123/documents/doc1/pdf", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/share-objects/tok123/documents/doc1/pdf: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, respBody)
	}
}

// ---------------------------------------------------------------------------
// Task 10: Unified Resolver POST /v1/resolve
// ---------------------------------------------------------------------------

// resolveDocumentFixture ist die Mock-Fileee-Antwort für "GET /api/documents/rest/doc-1"
// (Documents.Get): zwei Seiten (pg1, pg2) und typisierte Attribute (title/documentTypeId im
// wrapper-Format {"value":...}, siehe fileee/types.go DocumentAttributes.UnmarshalJSON) — Grundlage
// für TestResolve_InternalDocument_Success und TestResolve_IncludeOCR_InlinesTokens.
const resolveDocumentFixture = `{"id":"doc-1","version":1,"status":"DONE",` +
	`"pages":[{"id":"pg1","imageVersion":1,"contentVersion":1},{"id":"pg2","imageVersion":1,"contentVersion":1}],` +
	`"attributes":{"data":{"title":{"value":"Rechnung Test"},"documentTypeId":{"value":"bill"}}}}`

// TestResolve_InternalDocument_Success prüft den Brief-Pflichtfall für einen internen Link:
// {"url":".../documents/doc-1"} löst über Documents.Get auf und liefert kind="internal" mit einem
// downloadUrl, der auf /v1/documents/doc-1/pdf (dieser Server selbst) zeigt — NICHT auf Fileee.
func TestResolve_InternalDocument_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/documents/rest/doc-1": {
			Status: http.StatusOK,
			Body:   []byte(resolveDocumentFixture),
		},
	}
	_, ts := newTestServer(t, routes)

	body := `{"url":"https://my.fileee.com/documents/doc-1"}`
	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/resolve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/resolve: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var got ResolvedDocument
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("Body als ResolvedDocument dekodieren: %v", err)
	}
	if got.Kind != "internal" {
		t.Fatalf("Kind = %q, want %q", got.Kind, "internal")
	}
	if got.ID != "doc-1" {
		t.Fatalf("ID = %q, want %q", got.ID, "doc-1")
	}
	if got.DownloadUrl != "/v1/documents/doc-1/pdf?mode=download" {
		t.Fatalf("DownloadUrl = %q, want %q", got.DownloadUrl, "/v1/documents/doc-1/pdf?mode=download")
	}
	if got.OCRUrl != "/v1/pages/pg1/ocr" {
		t.Fatalf("OCRUrl = %q, want %q", got.OCRUrl, "/v1/pages/pg1/ocr")
	}
	if len(got.PageIDs) != 2 || got.PageIDs[0] != "pg1" || got.PageIDs[1] != "pg2" {
		t.Fatalf("PageIDs = %v, want [pg1 pg2]", got.PageIDs)
	}
	if got.Metadata.Title != "Rechnung Test" || got.Metadata.Type != "bill" {
		t.Fatalf("Metadata = %+v, want Title=Rechnung Test Type=bill", got.Metadata)
	}
	if got.OCR != nil {
		t.Fatalf("OCR = %+v, want nil (kein include=ocr gesetzt)", got.OCR)
	}
}

// TestResolve_SharedLink_Success prüft den Brief-Pflichtfall für einen Share-Link:
// {"url":".../shared/tok123"} löst anonym über ShareClient.Resolve auf und liefert kind="shared"
// mit URLs, die auf die /v1/share-objects/{token}/…-Proxy-Routen DIESES Servers zeigen.
func TestResolve_SharedLink_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/share-objects/tok123": {
			Status: http.StatusOK,
			Body: []byte(`{"id":"sh1","sharedBy":"Max Mustermann","sharedById":"u1",` +
				`"created":"2026-07-24T00:00:00Z",` +
				`"documents":[{"id":"doc1","title":"Rechnung","pageIds":["pg1"]}]}`),
		},
	}
	_, ts := newTestServer(t, routes)

	body := `{"url":"https://my.fileee.com/shared/tok123"}`
	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/resolve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/resolve: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var got ResolvedDocument
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("Body als ResolvedDocument dekodieren: %v", err)
	}
	if got.Kind != "shared" {
		t.Fatalf("Kind = %q, want %q", got.Kind, "shared")
	}
	if got.ID != "doc1" {
		t.Fatalf("ID = %q, want %q", got.ID, "doc1")
	}
	if got.DownloadUrl != "/v1/share-objects/tok123/documents/doc1/pdf?mode=download" {
		t.Fatalf("DownloadUrl = %q, want %q", got.DownloadUrl, "/v1/share-objects/tok123/documents/doc1/pdf?mode=download")
	}
	if got.OCRUrl != "/v1/share-objects/tok123/pages/pg1/ocr" {
		t.Fatalf("OCRUrl = %q, want %q", got.OCRUrl, "/v1/share-objects/tok123/pages/pg1/ocr")
	}
	if got.Metadata.Title != "Rechnung" {
		t.Fatalf("Metadata.Title = %q, want %q", got.Metadata.Title, "Rechnung")
	}
}

// TestResolve_UnknownLink_Returns400 prüft den Brief-Pflichtfall "Müll-URL → 400": eine URL, die
// weder auf .../documents/<id> noch auf .../shared/<token> passt, liefert LinkKindUnknown
// (fileee.ParseDocumentLink) — der Handler antwortet mit 400, OHNE einen Fileee-Roundtrip
// auszulösen (keine Mock-Route registriert, würde also mit 404 vom Mock-Mux scheitern, wenn der
// Handler fälschlich doch einen Upstream-Call versuchte).
func TestResolve_UnknownLink_Returns400(t *testing.T) {
	_, ts := newTestServer(t, nil)

	body := `{"url":"https://example.com/nothing/here"}`
	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/resolve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/resolve: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// TestResolve_IncludeOCR_InlinesTokens prüft "?include=ocr": zusätzlich zu den Basisfeldern lädt
// der Handler die OCR-Tokens JEDER Seite (hier: pg1 und pg2) und bettet sie im Feld "ocr"
// (pageId -> Tokens) ein, statt nur den Verweis ocrUrl zu liefern.
func TestResolve_IncludeOCR_InlinesTokens(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/documents/rest/doc-1": {
			Status: http.StatusOK,
			Body:   []byte(resolveDocumentFixture),
		},
		"GET /api/pages/pg1": {
			Status: http.StatusOK,
			Body:   []byte(`[{"text":"Hallo","webappId":"w1","left":1,"top":2,"right":3,"bottom":4,"width":2,"height":2}]`),
		},
		"GET /api/pages/pg2": {
			Status: http.StatusOK,
			Body:   []byte(`[{"text":"Welt","webappId":"w2","left":1,"top":2,"right":3,"bottom":4,"width":2,"height":2}]`),
		},
	}
	_, ts := newTestServer(t, routes)

	body := `{"url":"https://my.fileee.com/documents/doc-1"}`
	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/resolve?include=ocr", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/resolve?include=ocr: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var got ResolvedDocument
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("Body als ResolvedDocument dekodieren: %v", err)
	}
	if got.OCR == nil {
		t.Fatalf("OCR = nil, want inline-Tokens (include=ocr gesetzt)")
	}
	pg1Toks, ok := got.OCR["pg1"]
	if !ok || len(pg1Toks) != 1 || pg1Toks[0].Text != "Hallo" {
		t.Fatalf("OCR[pg1] = %+v, want ein Token mit Text=Hallo", pg1Toks)
	}
	pg2Toks, ok := got.OCR["pg2"]
	if !ok || len(pg2Toks) != 1 || pg2Toks[0].Text != "Welt" {
		t.Fatalf("OCR[pg2] = %+v, want ein Token mit Text=Welt", pg2Toks)
	}
}

// TestResolve_InternalDocument_BackendErrorReturns404 prüft den Fehlerpfad: ein interner Link auf
// eine Dokument-ID, die der Mock-Fileee mit 404 beantwortet (Documents.Get → fileee.ErrNotFound),
// wird über mapError 1:1 auf den HTTP-Status der fileee-server-Antwort abgebildet (Design-Spec
// §12 "sonstiger *fileee.APIError → dessen eigener HTTPStatus", hier der ErrNotFound-Sonderfall
// → 404 "not_found").
func TestResolve_InternalDocument_BackendErrorReturns404(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/documents/rest/doc-404": {
			Status: http.StatusNotFound,
		},
	}
	_, ts := newTestServer(t, routes)

	body := `{"url":"https://my.fileee.com/documents/doc-404"}`
	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/resolve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/resolve: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, respBody)
	}
}

// TestResolve_InternalDocument_ZeroPages_NoOCRUrl prüft den Guard in resolveInternal
// (cmd/fileee-server/resolve.go: "if len(pageIDs) > 0 { ocrURL = ... }"): ein Dokument OHNE Seiten
// (leere "pages"-Liste in der Documents.Get-Antwort) darf NICHT versuchen, pageIDs[0] zu indizieren
// (das würde ohne den Guard mit index-out-of-range panicen) — stattdessen bleibt OCRUrl leer und die
// Antwort liefert trotzdem 200 mit kind="internal" und einer leeren PageIDs-Liste.
func TestResolve_InternalDocument_ZeroPages_NoOCRUrl(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/documents/rest/doc-nopages": {
			Status: http.StatusOK,
			Body: []byte(`{"id":"doc-nopages","version":1,"status":"DONE","pages":[],` +
				`"attributes":{"data":{"title":{"value":"Ohne Seiten"}}}}`),
		},
	}
	_, ts := newTestServer(t, routes)

	body := `{"url":"https://my.fileee.com/documents/doc-nopages"}`
	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/resolve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/resolve: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var got ResolvedDocument
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("Body als ResolvedDocument dekodieren: %v", err)
	}
	if got.Kind != "internal" {
		t.Fatalf("Kind = %q, want %q", got.Kind, "internal")
	}
	if len(got.PageIDs) != 0 {
		t.Fatalf("PageIDs = %v, want leer (Dokument ohne Seiten)", got.PageIDs)
	}
	if got.OCRUrl != "" {
		t.Fatalf("OCRUrl = %q, want leer (Guard len(pageIDs) > 0 greift nicht)", got.OCRUrl)
	}
}

// TestResolve_SharedDocument_ZeroDocuments_Returns404 prüft den Guard in resolveShared
// (cmd/fileee-server/resolve.go: "if len(obj.Documents) == 0 { return 404 }"): eine Freigabe, deren
// ShareClient.Resolve-Antwort eine LEERE documents-Liste enthält, darf NICHT versuchen,
// obj.Documents[0] zu indizieren (Panic-Kandidat ohne den Guard) — stattdessen liefert der Handler
// den in resolve.go dokumentierten Status 404 ("not_found").
func TestResolve_SharedDocument_ZeroDocuments_Returns404(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/share-objects/tok-empty": {
			Status: http.StatusOK,
			Body: []byte(`{"id":"sh-empty","sharedBy":"Max Mustermann","sharedById":"u1",` +
				`"created":"2026-07-24T00:00:00Z","documents":[]}`),
		},
	}
	_, ts := newTestServer(t, routes)

	body := `{"url":"https://my.fileee.com/shared/tok-empty"}`
	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/resolve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/resolve: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, respBody)
	}
}

// ---------------------------------------------------------------------------
// Task 11: Konversations-Handler (Chat/Doc/Teilnehmer/Einladungen)
// ---------------------------------------------------------------------------

// TestListConversations_Success prüft den Happy-Path von GET /v1/conversations: der Mock-Fileee
// liefert unter dem von Conversations.Diff erwarteten Upstream-Pfad ("POST /api/conversations/rest/
// diff", fileee/service.go) eine Zeile, der Handler antwortet mit 200 und listet sie im
// "items"-Feld samt nicht-leerem Folge-Cursor.
func TestListConversations_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/conversations/rest/diff": {
			Status: http.StatusOK,
			Body:   []byte(`{"rows":[{"id":"conv-1","version":1,"title":"Rechnung teilen"}],"totalRows":1}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodGet, ts.URL+"/v1/conversations", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/conversations: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var out conversationListBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body als conversationListBody dekodieren: %v", err)
	}
	if out.TotalRows != 1 || len(out.Items) != 1 || out.Items[0].ID != "conv-1" {
		t.Fatalf("out = %+v, want eine Konversation ID=conv-1, TotalRows=1", out)
	}
	if out.Cursor == "" {
		t.Fatalf("Cursor ist leer, want einen kodierten Folge-Cursor")
	}
}

// TestListConversations_InvalidCursorReturns400 prüft, dass ein nicht dekodierbarer `cursor`-
// Parameter mit 400 abgelehnt wird, BEVOR ein Upstream-Request ausgelöst wird (analog
// decodeCursor-Verhalten bei GET /v1/documents) — routes bleibt bewusst leer, ein Roundtrip darf
// gar nicht erst stattfinden.
func TestListConversations_InvalidCursorReturns400(t *testing.T) {
	_, ts := newTestServer(t, nil)

	req := newAuthedRequest(t, http.MethodGet, ts.URL+"/v1/conversations?cursor=not-valid-base64!!!", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/conversations: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// TestGetConversation_Success prüft den Happy-Path von GET /v1/conversations/{id} — dünner
// Durchgriff auf Conversations.Get.
func TestGetConversation_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/conversations/rest/conv-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"conv-1","version":1,"title":"Rechnung teilen"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodGet, ts.URL+"/v1/conversations/conv-1", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/conversations/conv-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var out fileee.Conversation
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body als fileee.Conversation dekodieren: %v", err)
	}
	if out.ID != "conv-1" || out.Title != "Rechnung teilen" {
		t.Fatalf("out = %+v, want ID=conv-1, Title=Rechnung teilen", out)
	}
}

// TestGetConversation_BackendError prüft den Fehlerpfad von GET /v1/conversations/{id}.
func TestGetConversation_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/conversations/rest/conv-1": {
			Status: http.StatusNotFound,
			Body:   []byte(`{"apiError":"NOT_FOUND","errorMessage":"unknown conversation"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodGet, ts.URL+"/v1/conversations/conv-1", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/conversations/conv-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, respBody)
	}
}

// TestListDocumentConversations_Success prüft den Happy-Path von GET /v1/documents/{id}/
// conversations: Documents.Conversations filtert intern über Conversations.Diff (POST
// /api/conversations/rest/diff) nach ConversationState.SharedDocumentIDs — von zwei Diff-Zeilen
// darf nur die mit passender sharedDocumentIds im Ergebnis landen.
func TestListDocumentConversations_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/conversations/rest/diff": {
			Status: http.StatusOK,
			Body: []byte(`{"rows":[` +
				`{"id":"conv-a","version":1,"state":{"sharedDocumentIds":["doc-1"]}},` +
				`{"id":"conv-b","version":1,"state":{"sharedDocumentIds":["doc-2"]}}` +
				`],"totalRows":2}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodGet, ts.URL+"/v1/documents/doc-1/conversations", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/documents/doc-1/conversations: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var out entityListBody[fileee.Conversation]
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body als entityListBody[Conversation] dekodieren: %v", err)
	}
	if out.TotalRows != 1 || len(out.Items) != 1 || out.Items[0].ID != "conv-a" {
		t.Fatalf("out = %+v, want genau eine Konversation ID=conv-a (nur doc-1 geteilt)", out)
	}
}

// TestSendMessage_Success prüft den Brief-Pflichtfall für POST /v1/conversations/{id}/messages:
// Conversations.SendMessage lädt zuerst die Konversation (GET /api/conversations/rest/conv-1,
// für lastMessageID), postet dann die Chatnachricht (POST .../conv-1/message) und der Handler
// liefert die vom Mock-Fileee quittierte fileee.SentMessage unverändert im Body.
func TestSendMessage_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/conversations/rest/conv-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"conv-1","version":1,"messages":[]}`),
		},
		"POST /api/conversations/rest/conv-1/message": {
			Status: http.StatusOK,
			Body:   []byte(`{"conversationId":"conv-1","messageId":"msg-1","messageIndex":0}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/conversations/conv-1/messages", strings.NewReader(`{"text":"Hallo"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/conversations/conv-1/messages: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var out fileee.SentMessage
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body als fileee.SentMessage dekodieren: %v", err)
	}
	if out.ConversationID != "conv-1" || out.MessageID != "msg-1" {
		t.Fatalf("out = %+v, want ConversationID=conv-1, MessageID=msg-1", out)
	}
}

// TestSendMessage_BackendError prüft den Fehlerpfad von POST /v1/conversations/{id}/messages: das
// vorgeschaltete Get gelingt, aber der eigentliche message-POST scheitert — Backend-4xx passt
// unverändert durch mapError durch.
func TestSendMessage_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/conversations/rest/conv-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"conv-1","version":1,"messages":[]}`),
		},
		"POST /api/conversations/rest/conv-1/message": {
			Status: http.StatusBadRequest,
			Body:   []byte(`{"apiError":"BAD_REQUEST","errorMessage":"empty message"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/conversations/conv-1/messages", strings.NewReader(`{"text":""}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/conversations/conv-1/messages: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// TestShareConversationDocument_Success prüft den Happy-Path von
// POST /v1/conversations/{id}/documents/{docId} — dünner Durchgriff auf Conversations.
// ShareDocument (dieselbe message-Route wie SendMessage, aber eine DOCUMENT- statt CHAT-Nachricht).
func TestShareConversationDocument_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/conversations/rest/conv-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"conv-1","version":1,"messages":[]}`),
		},
		"POST /api/conversations/rest/conv-1/message": {
			Status: http.StatusOK,
			Body:   []byte(`{"conversationId":"conv-1","messageId":"msg-2","messageIndex":1}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/conversations/conv-1/documents/doc-9", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/conversations/conv-1/documents/doc-9: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var out fileee.SentMessage
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body als fileee.SentMessage dekodieren: %v", err)
	}
	if out.MessageID != "msg-2" {
		t.Fatalf("out = %+v, want MessageID=msg-2", out)
	}
}

// TestShareConversationDocument_BackendError prüft den Fehlerpfad von
// POST /v1/conversations/{id}/documents/{docId}.
func TestShareConversationDocument_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/conversations/rest/conv-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"conv-1","version":1,"messages":[]}`),
		},
		"POST /api/conversations/rest/conv-1/message": {
			Status: http.StatusNotFound,
			Body:   []byte(`{"apiError":"NOT_FOUND","errorMessage":"unknown document"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/conversations/conv-1/documents/doc-9", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/conversations/conv-1/documents/doc-9: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, respBody)
	}
}

// TestUnshareConversationDocument_Success prüft den Happy-Path von
// DELETE /v1/conversations/{id}/documents/{docId} — dünner Durchgriff auf Conversations.
// UnshareDocument (dieselbe message-Route, remove=true statt remove=false).
func TestUnshareConversationDocument_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/conversations/rest/conv-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"conv-1","version":1,"messages":[]}`),
		},
		"POST /api/conversations/rest/conv-1/message": {
			Status: http.StatusOK,
			Body:   []byte(`{"conversationId":"conv-1","messageId":"msg-3","messageIndex":2}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodDelete, ts.URL+"/v1/conversations/conv-1/documents/doc-9", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /v1/conversations/conv-1/documents/doc-9: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var out fileee.SentMessage
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body als fileee.SentMessage dekodieren: %v", err)
	}
	if out.MessageID != "msg-3" {
		t.Fatalf("out = %+v, want MessageID=msg-3", out)
	}
}

// TestUnshareConversationDocument_BackendError prüft den Fehlerpfad von
// DELETE /v1/conversations/{id}/documents/{docId}.
func TestUnshareConversationDocument_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/conversations/rest/conv-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"conv-1","version":1,"messages":[]}`),
		},
		"POST /api/conversations/rest/conv-1/message": {
			Status: http.StatusBadRequest,
			Body:   []byte(`{"apiError":"BAD_REQUEST","errorMessage":"cannot unshare"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodDelete, ts.URL+"/v1/conversations/conv-1/documents/doc-9", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /v1/conversations/conv-1/documents/doc-9: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// TestAddParticipant_Success prüft den Happy-Path von POST /v1/conversations/{id}/participants —
// dünner Durchgriff auf Conversations.AddParticipant (POST /api/conversations/conv-1/participants/
// add, fileee/conversations.go postParticipants), 200 ohne Body auf Fileee-Seite → 204 hier.
func TestAddParticipant_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/conversations/conv-1/participants/add": {Status: http.StatusOK},
	}
	_, ts := newTestServer(t, routes)

	body := `{"email":"empfaenger@example.invalid","role":"VIEWER"}`
	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/conversations/conv-1/participants", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/conversations/conv-1/participants: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 204, body=%s", resp.StatusCode, respBody)
	}
}

// TestAddParticipant_InvalidRoleReturns400 prüft, dass eine unbekannte Rolle MIT 400 abgelehnt
// wird, BEVOR Conversations.AddParticipant aufgerufen wird — routes bleibt bewusst leer, ein
// Upstream-Roundtrip darf gar nicht erst stattfinden (isValidConversationRole-Guard).
func TestAddParticipant_InvalidRoleReturns400(t *testing.T) {
	_, ts := newTestServer(t, nil)

	body := `{"email":"empfaenger@example.invalid","role":"SUPERADMIN"}`
	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/conversations/conv-1/participants", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/conversations/conv-1/participants: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// TestAddParticipant_BackendError prüft den Fehlerpfad von POST /v1/conversations/{id}/participants
// mit einer gültigen Rolle (der Upstream-Request findet also statt und scheitert dort).
func TestAddParticipant_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/conversations/conv-1/participants/add": {
			Status: http.StatusBadRequest,
			Body:   []byte(`{"apiError":"BAD_REQUEST","errorMessage":"invalid email"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	body := `{"email":"not-an-email","role":"VIEWER"}`
	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/conversations/conv-1/participants", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/conversations/conv-1/participants: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// TestRemoveParticipant_Success prüft den Happy-Path von DELETE /v1/conversations/{id}/
// participants/{participantId}: Conversations.RemoveParticipant lädt zuerst die Konversation (GET
// /api/conversations/rest/conv-1, um das volle Participant-Objekt zu finden) und postet dann POST
// /api/conversations/conv-1/participants/remove.
func TestRemoveParticipant_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/conversations/rest/conv-1": {
			Status: http.StatusOK,
			Body: []byte(`{"id":"conv-1","version":1,"participants":[` +
				`{"id":"participant-1","name":"Max Mustermann","type":"EXTERNAL","invited":true,"joined":false}` +
				`]}`),
		},
		"POST /api/conversations/conv-1/participants/remove": {Status: http.StatusOK},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodDelete, ts.URL+"/v1/conversations/conv-1/participants/participant-1", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /v1/conversations/conv-1/participants/participant-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 204, body=%s", resp.StatusCode, respBody)
	}
}

// TestRemoveParticipant_BackendError prüft den Fehlerpfad von DELETE /v1/conversations/{id}/
// participants/{participantId}: das vorgeschaltete Get gelingt (Teilnehmer existiert), aber der
// eigentliche remove-POST scheitert.
func TestRemoveParticipant_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/conversations/rest/conv-1": {
			Status: http.StatusOK,
			Body: []byte(`{"id":"conv-1","version":1,"participants":[` +
				`{"id":"participant-1","name":"Max Mustermann","type":"EXTERNAL","invited":true,"joined":false}` +
				`]}`),
		},
		"POST /api/conversations/conv-1/participants/remove": {
			Status: http.StatusBadRequest,
			Body:   []byte(`{"apiError":"BAD_REQUEST","errorMessage":"cannot remove"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodDelete, ts.URL+"/v1/conversations/conv-1/participants/participant-1", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /v1/conversations/conv-1/participants/participant-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

// TestRemoveParticipant_UnknownParticipantReturns404 prüft den in fileee/conversations.go
// dokumentierten Sonderfall: ist die angefragte participantId NICHT in Conversation.Participants
// enthalten, liefert RemoveParticipant einen ErrNotFound-wrappenden Fehler, OHNE den remove-POST
// überhaupt abzusetzen — mapError übersetzt das wie jeden anderen ErrNotFound-Fall zu 404.
func TestRemoveParticipant_UnknownParticipantReturns404(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/conversations/rest/conv-1": {
			Status: http.StatusOK,
			Body:   []byte(`{"id":"conv-1","version":1,"participants":[]}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodDelete, ts.URL+"/v1/conversations/conv-1/participants/unknown-participant", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /v1/conversations/conv-1/participants/unknown-participant: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, respBody)
	}
}

// TestListInvitations_Success prüft den Brief-Pflichtfall für GET /v1/conversations/invitations:
// PendingInvitations liefert (über Conversations.Diff) eine offene Einladung inkl. Conversation.
// Token — der Handler muss dieses Token unverändert im Response-Body durchreichen, damit ein
// Aufrufer es direkt an POST /v1/conversations/invitations/accept/{token} weitergeben kann.
func TestListInvitations_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/conversations/rest/diff": {
			Status: http.StatusOK,
			Body: []byte(`{"rows":[` +
				`{"id":"conv-2","version":1,"invitation":true,"token":"inv-tok-1"},` +
				`{"id":"conv-3","version":1,"invitation":false}` +
				`],"totalRows":2}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodGet, ts.URL+"/v1/conversations/invitations", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/conversations/invitations: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, respBody)
	}
	var out entityListBody[fileee.Conversation]
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Body als entityListBody[Conversation] dekodieren: %v", err)
	}
	if out.TotalRows != 1 || len(out.Items) != 1 {
		t.Fatalf("out = %+v, want genau eine offene Einladung (conv-3 ist keine Einladung)", out)
	}
	if out.Items[0].ID != "conv-2" || out.Items[0].Token != "inv-tok-1" {
		t.Fatalf("out.Items[0] = %+v, want ID=conv-2, Token=inv-tok-1", out.Items[0])
	}
}

// TestAcceptInvitation_Success prüft den Brief-Pflichtfall für
// POST /v1/conversations/invitations/accept/{token} — dünner Durchgriff auf
// Conversations.AcceptInvitation, 204 ohne Body. Der SERVER-eigene Pfad trägt "accept" VOR {token}
// (Begründung: registerConversationRoutes, Go-ServeMux-Pattern-Konflikt mit
// "/v1/conversations/{id}/documents/{docId}") — die Fileee-UPSTREAM-Route bleibt unverändert
// "POST /api/conversations/invitations/{token}/accept" (fileee/conversations.go).
func TestAcceptInvitation_Success(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/conversations/invitations/inv-tok-1/accept": {Status: http.StatusOK},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/conversations/invitations/accept/inv-tok-1", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/conversations/invitations/accept/inv-tok-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 204, body=%s", resp.StatusCode, respBody)
	}
}

// TestAcceptInvitation_BackendError prüft den Fehlerpfad von
// POST /v1/conversations/invitations/accept/{token}.
func TestAcceptInvitation_BackendError(t *testing.T) {
	routes := map[string]mockRoute{
		"POST /api/conversations/invitations/inv-tok-1/accept": {
			Status: http.StatusBadRequest,
			Body:   []byte(`{"apiError":"BAD_REQUEST","errorMessage":"invalid or expired token"}`),
		},
	}
	_, ts := newTestServer(t, routes)

	req := newAuthedRequest(t, http.MethodPost, ts.URL+"/v1/conversations/invitations/accept/inv-tok-1", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/conversations/invitations/accept/inv-tok-1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, respBody)
	}
}

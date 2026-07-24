package fileee

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentServiceQueryUndDiff(t *testing.T) {
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"POST /api/documents/rest/query": {Status: 200, Body: []byte(`{"rows":[{"id":"doc-1","status":"DONE"}],"totalRows":1}`)},
		"POST /api/documents/rest/diff":  {Status: 200, Body: []byte(`{"rows":[{"id":"doc-1","version":2,"status":"DONE"}],"idsToDelete":[],"totalRows":1}`)},
	})
	client := newTestClientAgainstMock(t, routes)

	queried, err := client.Documents.Query(context.Background(), QueryOptions{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(queried.Rows) != 1 || queried.Rows[0].Status != StatusDone {
		t.Fatalf("Query-Ergebnis falsch: %+v", queried.Rows)
	}

	diffed, err := client.Documents.Diff(context.Background(), NewCursor("Document"))
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diffed.NextCursor.Known["doc-1"] != 2 {
		t.Fatalf("Diff-Cursor falsch: %+v", diffed.NextCursor.Known)
	}
}

func TestDocumentServiceGetHappyUndNotFound(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"GET /api/documents/rest/doc-1": {Status: 200, Body: []byte(`{"id":"doc-1","status":"DONE"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		doc, err := client.Documents.Get(context.Background(), "doc-1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if doc.ID != "doc-1" {
			t.Fatalf("Get-Ergebnis falsch: %+v", doc)
		}
	})

	t.Run("not found", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"GET /api/documents/rest/unbekannt": {Status: 404, Body: []byte(`{"errorCode":"NOT_FOUND"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		_, err := client.Documents.Get(context.Background(), "unbekannt")
		if !errorsIsNotFound(err) {
			t.Fatalf("erwartet ErrNotFound, bekommen %v", err)
		}
	})
}

func TestDocumentServiceUpdateHappyErrorNetwork(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"PUT /api/documents/rest/doc-1": {Status: 200, Body: []byte(`{"id":"doc-1","status":"DONE","version":4}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		updated, err := client.Documents.Update(context.Background(), &Document{ID: "doc-1", Version: 3, Status: StatusDone})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.Version != 4 {
			t.Fatalf("Update-Ergebnis falsch: %+v", updated)
		}
	})

	t.Run("error path 409 version conflict", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"PUT /api/documents/rest/doc-1": {Status: 409, Body: []byte(`{"errorCode":"VERSION_CONFLICT"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		_, err := client.Documents.Update(context.Background(), &Document{ID: "doc-1", Version: 1})
		if err == nil {
			t.Fatalf("erwartet Fehler bei 409, bekommen nil")
		}
	})

	t.Run("network error", func(t *testing.T) {
		client, err := New(
			Credentials{Username: "test@example.invalid", Password: "test-pw"},
			WithBaseURL("http://127.0.0.1:1"),
			WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = client.Documents.Update(context.Background(), &Document{ID: "doc-1"})
		if err == nil {
			t.Fatalf("erwartet Network-Error, bekommen nil")
		}
	})
}

func TestDocumentServiceUploadHappyDuplikatErrorNetwork(t *testing.T) {
	t.Run("happy path kein Duplikat", func(t *testing.T) {
		var gotContentType string
		var capturedID string
		routes := ensureSessionRoutes()
		srv := newMockUploadServer(t, routes, func(w http.ResponseWriter, r *http.Request) {
			gotContentType = r.Header.Get("Content-Type")
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("ParseMultipartForm: %v", err)
			}
			capturedID = r.FormValue("id")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"id":"` + capturedID + `","status":"NEW"}`))
		})
		client := newTestClientAgainstMockServer(t, srv)
		result, err := client.Documents.Upload(context.Background(), strings.NewReader("test-inhalt"), UploadMetadata{Title: "Testrechnung.pdf"})
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if result.IsDuplicate {
			t.Fatalf("erwartet kein Duplikat, IsDuplicate=true")
		}
		if !strings.HasPrefix(gotContentType, "multipart/form-data") {
			t.Fatalf("Content-Type = %q, erwartet multipart/form-data-Praefix", gotContentType)
		}
		if capturedID == "" {
			t.Fatalf("erwartet client-generierte id im Formfeld, war leer")
		}
	})

	t.Run("Duplikat erkannt (id weicht ab)", func(t *testing.T) {
		routes := ensureSessionRoutes()
		srv := newMockUploadServer(t, routes, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"id":"bereits-existierendes-doc","status":"DONE"}`))
		})
		client := newTestClientAgainstMockServer(t, srv)
		result, err := client.Documents.Upload(context.Background(), strings.NewReader("test-inhalt"), UploadMetadata{Title: "Testrechnung.pdf"})
		if !errors.Is(err, ErrDuplicateDocument) {
			t.Fatalf("erwartet ErrDuplicateDocument, bekam %v", err)
		}
		if !result.IsDuplicate || result.Document == nil {
			t.Fatalf("Result soll trotz Fehler befüllt sein (IsDuplicate + Document): %+v", result)
		}
	})

	t.Run("error path 400", func(t *testing.T) {
		routes := ensureSessionRoutes()
		srv := newMockUploadServer(t, routes, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"errorCode":"BAD_UPLOAD"}`))
		})
		client := newTestClientAgainstMockServer(t, srv)
		_, err := client.Documents.Upload(context.Background(), strings.NewReader("x"), UploadMetadata{Title: "x.pdf"})
		if err == nil {
			t.Fatalf("erwartet Fehler bei 400, bekommen nil")
		}
	})

	t.Run("network error (EnsureSession schlägt fehl)", func(t *testing.T) {
		client, err := New(
			Credentials{Username: "test@example.invalid", Password: "test-pw"},
			WithBaseURL("http://127.0.0.1:1"),
			WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = client.Documents.Upload(context.Background(), strings.NewReader("x"), UploadMetadata{Title: "x.pdf"})
		if err == nil {
			t.Fatalf("erwartet Fehler aus EnsureSession, bekommen nil")
		}
	})

	t.Run("network error beim Upload-POST selbst (EnsureSession erfolgreich)", func(t *testing.T) {
		// EnsureSession läuft gegen die normalen Auth-Routen durch (erfolgreich); erst der
		// eigentliche POST /api/documents/rest schlägt auf Transport-Ebene fehl — die Gegenstelle
		// hijackt die TCP-Verbindung und schließt sie ohne HTTP-Antwort, sodass httpClient.Do()
		// hier (documents.go:132-134) einen Netzwerkfehler liefert statt eines *APIError.
		routes := ensureSessionRoutes()
		srv := newMockUploadServer(t, routes, func(w http.ResponseWriter, r *http.Request) {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatalf("Test-ResponseWriter unterstützt kein http.Hijacker")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatalf("Hijack: %v", err)
			}
			conn.Close()
		})
		client := newTestClientAgainstMockServer(t, srv)
		_, err := client.Documents.Upload(context.Background(), strings.NewReader("test-inhalt"), UploadMetadata{Title: "x.pdf"})
		if err == nil {
			t.Fatalf("erwartet Network-Error beim Upload-POST, bekommen nil")
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("erwartet Transport-/Network-Error, bekommen *APIError: %v", apiErr)
		}
	})

	t.Run("leerer Titel verwendet Default-Dateiname", func(t *testing.T) {
		var gotFilename, gotTitleField string
		routes := ensureSessionRoutes()
		srv := newMockUploadServer(t, routes, func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("ParseMultipartForm: %v", err)
			}
			if files := r.MultipartForm.File["file"]; len(files) == 1 {
				gotFilename = files[0].Filename
			}
			gotTitleField = r.FormValue("attributes.data.title.value")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"id":"` + r.FormValue("id") + `","status":"NEW"}`))
		})
		client := newTestClientAgainstMockServer(t, srv)
		if _, err := client.Documents.Upload(context.Background(), strings.NewReader("test-inhalt"), UploadMetadata{}); err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if gotFilename != "upload" {
			t.Fatalf("Default-Dateiname = %q, erwartet %q", gotFilename, "upload")
		}
		if gotTitleField != "upload" {
			t.Fatalf("attributes.data.title.value = %q, erwartet %q", gotTitleField, "upload")
		}
	})

	t.Run("Document-Metadaten werden als document-Feld gesendet", func(t *testing.T) {
		var gotDocumentField, gotTitleField string
		routes := ensureSessionRoutes()
		srv := newMockUploadServer(t, routes, func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("ParseMultipartForm: %v", err)
			}
			gotDocumentField = r.FormValue("document")
			gotTitleField = r.FormValue("attributes.data.title.value")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"id":"` + r.FormValue("id") + `","status":"NEW"}`))
		})
		client := newTestClientAgainstMockServer(t, srv)
		meta := UploadMetadata{Title: "Testrechnung.pdf", Document: &Document{Status: StatusNew}}
		if _, err := client.Documents.Upload(context.Background(), strings.NewReader("test-inhalt"), meta); err != nil {
			t.Fatalf("Upload: %v", err)
		}
		if gotDocumentField == "" {
			t.Fatalf("erwartet befülltes document-Feld, war leer")
		}
		if gotTitleField != "" {
			t.Fatalf("attributes.data.title.value hätte bei gesetztem Document NICHT gesendet werden dürfen, war %q", gotTitleField)
		}
	})

	t.Run("defekter Reader liefert Fehler", func(t *testing.T) {
		routes := ensureSessionRoutes()
		client := newTestClientAgainstMock(t, routes)
		_, err := client.Documents.Upload(context.Background(), failingReader{}, UploadMetadata{Title: "x.pdf"})
		if err == nil {
			t.Fatalf("erwartet Fehler bei defektem Reader, bekommen nil")
		}
	})

	t.Run("ungültige JSON-Antwort liefert Fehler", func(t *testing.T) {
		routes := ensureSessionRoutes()
		srv := newMockUploadServer(t, routes, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`das-ist-kein-json`))
		})
		client := newTestClientAgainstMockServer(t, srv)
		_, err := client.Documents.Upload(context.Background(), strings.NewReader("test-inhalt"), UploadMetadata{Title: "x.pdf"})
		if err == nil {
			t.Fatalf("erwartet Fehler bei ungültiger JSON-Antwort, bekommen nil")
		}
	})
}

// failingReader liefert bei jedem Read einen Fehler (simuliert einen defekten Quell-Reader beim
// Upload, secret-safe: transportiert keine echten Daten).
type failingReader struct{}

var errFailingReader = errors.New("fileee-test: kaputter Reader")

func (failingReader) Read([]byte) (int, error) {
	return 0, errFailingReader
}

func TestDocumentServiceDownloadPDFHappyUndNotFound(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"GET /api/v1/documents/doc-1/pdf": {Status: 200, Headers: map[string]string{"Content-Type": "application/pdf"}, Body: []byte("%PDF-test-inhalt")},
		})
		client := newTestClientAgainstMock(t, routes)
		rc, err := client.Documents.DownloadPDF(context.Background(), "doc-1", PDFModeDownload)
		if err != nil {
			t.Fatalf("DownloadPDF: %v", err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if string(data) != "%PDF-test-inhalt" {
			t.Fatalf("PDF-Inhalt falsch: %q", data)
		}
	})

	t.Run("not found", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"GET /api/v1/documents/unbekannt/pdf": {Status: 404, Body: []byte(`{"errorCode":"NOT_FOUND"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		_, err := client.Documents.DownloadPDF(context.Background(), "unbekannt", PDFModeDownload)
		if !errorsIsNotFound(err) {
			t.Fatalf("erwartet ErrNotFound, bekommen %v", err)
		}
	})

	t.Run("server error 500", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"GET /api/v1/documents/doc-2/pdf": {Status: 500, Body: []byte(`{"errorCode":"SERVER_ERROR"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		_, err := client.Documents.DownloadPDF(context.Background(), "doc-2", PDFModeDownload)
		if err == nil {
			t.Fatalf("erwartet Fehler bei 500, bekommen nil")
		}
		if errorsIsNotFound(err) {
			t.Fatalf("erwartet *APIError statt ErrNotFound bei 500, bekommen %v", err)
		}
	})

	t.Run("EnsureSession schlägt fehl, Download-Request wird nie abgesetzt", func(t *testing.T) {
		client, err := New(
			Credentials{Username: "test@example.invalid", Password: "test-pw"},
			WithBaseURL("http://127.0.0.1:1"),
			WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = client.Documents.DownloadPDF(context.Background(), "doc-1", PDFModeDownload)
		if err == nil {
			t.Fatalf("erwartet Fehler aus EnsureSession, bekommen nil")
		}
	})
}

func TestDocumentServiceDownloadPageImage(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"GET /api/v1/pages/page-1/image": {Status: 200, Headers: map[string]string{"Content-Type": "image/jpeg"}, Body: []byte("jpeg-test-inhalt")},
		})
		client := newTestClientAgainstMock(t, routes)
		rc, err := client.Documents.DownloadPageImage(context.Background(), "page-1", ImageSizeSmedium, 3)
		if err != nil {
			t.Fatalf("DownloadPageImage: %v", err)
		}
		defer rc.Close()
		data, _ := io.ReadAll(rc)
		if string(data) != "jpeg-test-inhalt" {
			t.Fatalf("Bild-Inhalt falsch: %q", data)
		}
	})

	t.Run("EnsureSession schlägt fehl, Download-Request wird nie abgesetzt", func(t *testing.T) {
		client, err := New(
			Credentials{Username: "test@example.invalid", Password: "test-pw"},
			WithBaseURL("http://127.0.0.1:1"),
			WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = client.Documents.DownloadPageImage(context.Background(), "page-1", ImageSizeSmedium, 3)
		if err == nil {
			t.Fatalf("erwartet Fehler aus EnsureSession, bekommen nil")
		}
	})
}

// newMockUploadServer baut einen httptest.Server, der für EnsureSession-Routen normal antwortet
// und für POST /api/documents/rest den custom handler nutzt (Multipart lässt sich nicht über
// die einfache mockRoute-Tabelle abbilden, da der Body dynamisch/binär ist).
func newMockUploadServer(t *testing.T, authRoutes map[string]mockRoute, uploadHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	base := jsonHandler(t, authRoutes)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/documents/rest" {
			uploadHandler(w, r)
			return
		}
		base(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestClientAgainstMockServer(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	client, err := New(
		Credentials{Username: "test@example.invalid", Password: "test-pw"},
		WithBaseURL(srv.URL),
		WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		WithRateLimit(1000, 1000),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

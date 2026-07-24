package fileee

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestUpload_DuplicateReturnsError: erkennt der Server ein bereits existierendes Dokument (gibt eine
// andere id zurück als die gesendete client-id), liefert Upload einen ErrDuplicateDocument — damit
// ein Aufrufer, der nur err prüft, nicht unbemerkt auf einem BESTEHENDEN Dokument weiterarbeitet.
// Das Result ist trotzdem befüllt (IsDuplicate + das existierende Document), für Aufrufer, die
// Duplikate bewusst behandeln wollen.
func TestUpload_DuplicateReturnsError(t *testing.T) {
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/documents/rest" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			// Server gibt eine ANDERE id zurück als die client-generierte -> Duplikat.
			_, _ = w.Write([]byte(`{"id":"existing-server-id","status":"CLASSIFIED"}`))
			return
		}
		jsonHandler(t, ensureSessionRoutes())(w, r)
	}))
	c := newTestClientAgainstMockServer(t, srv)

	res, err := c.Documents.Upload(context.Background(), strings.NewReader("PDFDATA"), UploadMetadata{Title: "x.pdf"})
	if !errors.Is(err, ErrDuplicateDocument) {
		t.Fatalf("erwartet ErrDuplicateDocument, bekam %v", err)
	}
	if res == nil || !res.IsDuplicate || res.Document == nil || res.Document.ID != "existing-server-id" {
		t.Fatalf("Result soll trotz Fehler befüllt sein: %+v", res)
	}
}

// TestUpload_NewDocumentNoError: bei einem neuen Dokument (Server behält die client-id) kein Fehler.
func TestUpload_NewDocumentNoError(t *testing.T) {
	var sentID string
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/documents/rest" {
			_ = r.ParseMultipartForm(1 << 20)
			sentID = r.FormValue("id")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"id":"` + sentID + `","status":"CLASSIFIED"}`))
			return
		}
		jsonHandler(t, ensureSessionRoutes())(w, r)
	}))
	c := newTestClientAgainstMockServer(t, srv)

	res, err := c.Documents.Upload(context.Background(), strings.NewReader("PDFDATA"), UploadMetadata{Title: "x.pdf"})
	if err != nil {
		t.Fatalf("neues Dokument soll keinen Fehler liefern: %v", err)
	}
	if res.IsDuplicate {
		t.Errorf("IsDuplicate soll false sein bei neuem Dokument")
	}
}

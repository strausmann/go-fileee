package fileee

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
)

func TestDocuments_Share(t *testing.T) {
	var gotQuery string
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/documents/rest/share" {
			gotQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"link":"https://my.fileee.com/shared/tok","shareId":"s1"}`))
			return
		}
		jsonHandler(t, ensureSessionRoutes())(w, r)
	}))
	c := newTestClientAgainstMockServer(t, srv)

	share, err := c.Documents.Share(context.Background(), []string{"d1", "d2"})
	if err != nil {
		t.Fatalf("Share: %v", err)
	}
	if share.Link == "" || share.ShareID != "s1" {
		t.Fatalf("Share falsch dekodiert: %+v", share)
	}
	q, _ := url.ParseQuery(gotQuery)
	if q.Get("documentIds") != "d1,d2" {
		t.Errorf("documentIds = %q, erwartet Komma-Liste d1,d2", q.Get("documentIds"))
	}
}

func TestDocuments_Unshare(t *testing.T) {
	var gotPath string
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/documents/rest/d1/unshare" {
			gotPath = r.URL.Path
			w.WriteHeader(200)
			return
		}
		jsonHandler(t, ensureSessionRoutes())(w, r)
	}))
	c := newTestClientAgainstMockServer(t, srv)
	if err := c.Documents.Unshare(context.Background(), "d1"); err != nil {
		t.Fatalf("Unshare: %v", err)
	}
	if gotPath != "/api/documents/rest/d1/unshare" {
		t.Errorf("falscher Pfad: %s", gotPath)
	}
}

// TestDocuments_Share_EmptyDocumentIDs deckt Whole-Codebase-Review Finding M1 ab: eine
// nil/leere documentIDs-Liste erzeugte bisher stillschweigend "documentIds=" (leer) im Request
// statt eines klaren Fehlers — unklar, ob der reale Server das als "nichts teilen", "alles
// teilen" oder 400 interpretiert (anders als ExportZIP/ExportAll, wo eine leere Liste explizit
// "alle Dokumente" bedeutet und getestet ist). Share() MUSS bei nil/leerer Liste einen Fehler
// zurückgeben, BEVOR überhaupt ein Request rausgeht.
func TestDocuments_Share_EmptyDocumentIDs(t *testing.T) {
	var called bool
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/documents/rest/share" {
			called = true
			w.WriteHeader(200)
			return
		}
		jsonHandler(t, ensureSessionRoutes())(w, r)
	}))
	c := newTestClientAgainstMockServer(t, srv)

	if _, err := c.Documents.Share(context.Background(), nil); err == nil {
		t.Fatal("erwartet Fehler bei nil documentIDs, bekommen nil")
	}
	if _, err := c.Documents.Share(context.Background(), []string{}); err == nil {
		t.Fatal("erwartet Fehler bei leerem documentIDs-Slice, bekommen nil")
	}
	if called {
		t.Fatal("Share darf bei leerer ID-Liste keinen Request an den Server schicken")
	}
}

// TestDocuments_ShareErrorNetwork deckt Share() als Mutation-Funktion vollständig ab
// (Test-Coverage-Pflicht Finding I2): Happy-Path ist bereits durch TestDocuments_Share
// abgedeckt, hier folgen Error-Path (echter Server-4xx) und Network-Error.
func TestDocuments_ShareErrorNetwork(t *testing.T) {
	t.Run("error path 400", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"POST /api/documents/rest/share": {Status: 400, Body: []byte(`{"errorCode":"BAD_REQUEST"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		_, err := client.Documents.Share(context.Background(), []string{"d1"})
		if err == nil {
			t.Fatalf("erwartet Fehler bei 400, bekommen nil")
		}
	})

	t.Run("network error", func(t *testing.T) {
		client, err := NewClient(
			Credentials{Username: "test@example.invalid", Password: "test-pw"},
			WithBaseURL("http://127.0.0.1:1"),
			WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		)
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		_, err = client.Documents.Share(context.Background(), []string{"d1"})
		if err == nil {
			t.Fatalf("erwartet Network-Error, bekommen nil")
		}
	})
}

// TestDocuments_UnshareErrorNetwork deckt Unshare() als Mutation-Funktion vollständig ab
// (Test-Coverage-Pflicht Finding I2): Happy-Path ist bereits durch TestDocuments_Unshare
// abgedeckt, hier folgen Error-Path (echter Server-5xx) und Network-Error.
func TestDocuments_UnshareErrorNetwork(t *testing.T) {
	t.Run("error path 500", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"POST /api/documents/rest/d1/unshare": {Status: 500, Body: []byte(`{"apiError":"boom"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		err := client.Documents.Unshare(context.Background(), "d1")
		if err == nil {
			t.Fatalf("erwartet Fehler bei 500, bekommen nil")
		}
	})

	t.Run("network error", func(t *testing.T) {
		client, err := NewClient(
			Credentials{Username: "test@example.invalid", Password: "test-pw"},
			WithBaseURL("http://127.0.0.1:1"),
			WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		)
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		err = client.Documents.Unshare(context.Background(), "d1")
		if err == nil {
			t.Fatalf("erwartet Network-Error, bekommen nil")
		}
	})
}

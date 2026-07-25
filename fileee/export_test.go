package fileee

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestDocuments_ExportZIP_WireForm(t *testing.T) {
	var captured map[string]any
	srv := newMockJSONCaptureServer(t, ensureSessionRoutes(),
		"POST", "/api/documents/rest/zip",
		func(raw []byte) (int, []byte) {
			_ = json.Unmarshal(raw, &captured)
			return 200, []byte(`{"id":"p1","status":"Waiting","type":"io.fileee.shared.process.DownloadAllProcess","documents":["d1","d2"]}`)
		})
	c := newTestClientAgainstMockServer(t, srv)

	proc, err := c.Documents.ExportZIP(context.Background(), []string{"d1", "d2"}, "geheim")
	if err != nil {
		t.Fatalf("ExportZIP: %v", err)
	}
	if proc.ID != "p1" || proc.Status != "Waiting" {
		t.Fatalf("Prozess falsch dekodiert: %+v", proc)
	}
	if captured["zipPassword"] != "geheim" {
		t.Errorf("zipPassword = %v, erwartet geheim", captured["zipPassword"])
	}
	ids, _ := captured["documentIds"].([]any)
	if len(ids) != 2 {
		t.Errorf("documentIds = %v, erwartet 2 IDs", captured["documentIds"])
	}
}

func TestDocuments_ExportAll_EmptyDocumentIDs(t *testing.T) {
	var captured map[string]any
	srv := newMockJSONCaptureServer(t, ensureSessionRoutes(),
		"POST", "/api/documents/rest/zip",
		func(raw []byte) (int, []byte) {
			_ = json.Unmarshal(raw, &captured)
			return 200, []byte(`{"id":"p1","status":"Waiting"}`)
		})
	c := newTestClientAgainstMockServer(t, srv)

	if _, err := c.Documents.ExportAll(context.Background(), "geheim"); err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	ids, ok := captured["documentIds"].([]any)
	if !ok || len(ids) != 0 {
		t.Errorf("documentIds muss ein leeres Array sein (= alle Dokumente), war %v", captured["documentIds"])
	}
}

// TestDocuments_ExportZIPErrorNetwork deckt ExportZIP() (und über die gemeinsame Implementation
// damit auch ExportAll()) als Mutation-Funktion vollständig ab (Test-Coverage-Pflicht
// Finding I2): Happy-Path ist bereits durch TestDocuments_ExportZIP_WireForm/
// TestDocuments_ExportAll_EmptyDocumentIDs abgedeckt, hier folgen Error-Path (echter Server-4xx)
// und Network-Error für beide Einstiegspunkte.
func TestDocuments_ExportZIPErrorNetwork(t *testing.T) {
	t.Run("ExportZIP error path 400", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"POST /api/documents/rest/zip": {Status: 400, Body: []byte(`{"errorCode":"BAD_REQUEST"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		_, err := client.Documents.ExportZIP(context.Background(), []string{"d1"}, "geheim")
		if err == nil {
			t.Fatalf("erwartet Fehler bei 400, bekommen nil")
		}
	})

	t.Run("ExportZIP network error", func(t *testing.T) {
		client, err := New(
			Credentials{Username: "test@example.invalid", Password: "test-pw"},
			WithBaseURL("http://127.0.0.1:1"),
			WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = client.Documents.ExportZIP(context.Background(), []string{"d1"}, "geheim")
		if err == nil {
			t.Fatalf("erwartet Network-Error, bekommen nil")
		}
	})

	t.Run("ExportAll error path 500", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"POST /api/documents/rest/zip": {Status: 500, Body: []byte(`{"apiError":"boom"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		_, err := client.Documents.ExportAll(context.Background(), "geheim")
		if err == nil {
			t.Fatalf("erwartet Fehler bei 500, bekommen nil")
		}
	})

	t.Run("ExportAll network error", func(t *testing.T) {
		client, err := New(
			Credentials{Username: "test@example.invalid", Password: "test-pw"},
			WithBaseURL("http://127.0.0.1:1"),
			WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = client.Documents.ExportAll(context.Background(), "geheim")
		if err == nil {
			t.Fatalf("erwartet Network-Error, bekommen nil")
		}
	})
}

func TestProcesses_Get(t *testing.T) {
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"GET /api/processes/p1": {Status: 200, Body: []byte(`{"id":"p1","status":"Running","type":"io.fileee.shared.process.DownloadAllProcess"}`)},
	})
	c := newTestClientAgainstMockServer(t, newMockServer(t, jsonHandler(t, routes)))
	proc, err := c.Processes.Get(context.Background(), "p1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if proc.Status != "Running" {
		t.Errorf("status = %q, erwartet Running", proc.Status)
	}
}

// statefulProcessServer liefert für GET /api/processes/<id> nacheinander die übergebenen
// JSON-Bodies (Status-Verlauf), damit WaitForProcess über mehrere Polls getestet werden kann.
// EnsureSession-Routen werden vorab bedient.
func statefulProcessServer(t *testing.T, id string, bodies ...string) *httptest.Server {
	t.Helper()
	auth := jsonHandler(t, ensureSessionRoutes())
	var i int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/processes/"+id {
			b := bodies[i]
			if i < len(bodies)-1 {
				i++
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(b))
			return
		}
		auth(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWaitForProcess_PollsUntilTerminal(t *testing.T) {
	srv := statefulProcessServer(t, "p1",
		`{"id":"p1","status":"Waiting"}`,
		`{"id":"p1","status":"Running"}`,
		`{"id":"p1","status":"Finished","documents":{"downloadUrl":"x"}}`,
	)
	c := newTestClientAgainstMockServer(t, srv)

	proc, err := c.WaitForProcess(context.Background(), "p1", WaitOptions{Interval: time.Millisecond})
	if err != nil {
		t.Fatalf("WaitForProcess: %v", err)
	}
	if proc.Status != "Finished" {
		t.Fatalf("erwartet Terminal-Status Finished, bekommen %q", proc.Status)
	}
}

func TestWaitForProcess_ContextCancel(t *testing.T) {
	srv := statefulProcessServer(t, "p1", `{"id":"p1","status":"Running"}`)
	c := newTestClientAgainstMockServer(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.WaitForProcess(ctx, "p1", WaitOptions{Interval: time.Millisecond}); err == nil {
		t.Fatal("erwartet Fehler bei abgebrochenem Context, bekommen nil")
	}
}

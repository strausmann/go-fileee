package fileee

import (
	"context"
	"path/filepath"
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

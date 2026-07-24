package fileee

import (
	"context"
	"encoding/json"
	"testing"
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

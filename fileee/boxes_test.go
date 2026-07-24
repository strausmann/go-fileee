package fileee

import (
	"context"
	"net/http"
	"testing"
)

func TestBoxes_List(t *testing.T) {
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"POST /api/fileeeboxes/rest/diff": {Status: 200, Body: []byte(`{"rows":[
			{"id":"b1","boxNr":1,"boxName":"Gehaltsabrechnung","qrCode":"abc","productCode":"fb3diy-plus","documents":[{"documentId":"d1","pageCount":1,"modified":"2024-01-01T00:00:00.000Z"}],"removedDocuments":[],"version":33},
			{"id":"b2","boxNr":2,"boxName":"Allgemein","qrCode":"def","productCode":"box-china-v1.0","documents":[],"removedDocuments":[],"version":69}
		],"totalRows":2,"idsToDelete":[]}`)},
	})
	c := newTestClientAgainstMockServer(t, newMockServer(t, jsonHandler(t, routes)))

	boxes, err := c.Boxes.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(boxes) != 2 {
		t.Fatalf("erwartet 2 Boxen, bekam %d", len(boxes))
	}
	if boxes[0].BoxNr != 1 || boxes[0].BoxName != "Gehaltsabrechnung" || len(boxes[0].Documents) != 1 {
		t.Errorf("Box 0 falsch dekodiert: %+v", boxes[0])
	}
	if boxes[0].Documents[0].DocumentID != "d1" || boxes[0].Documents[0].PageCount != 1 {
		t.Errorf("Box-Dokument falsch dekodiert: %+v", boxes[0].Documents[0])
	}
}

func TestBoxes_Get(t *testing.T) {
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"GET /api/fileeeboxes/rest/b1": {Status: 200, Body: []byte(`{"id":"b1","boxNr":1,"boxName":"Gehaltsabrechnung","documents":[],"removedDocuments":[]}`)},
	})
	c := newTestClientAgainstMockServer(t, newMockServer(t, jsonHandler(t, routes)))
	box, err := c.Boxes.Get(context.Background(), "b1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if box.ID != "b1" || box.BoxNr != 1 {
		t.Errorf("falsch dekodiert: %+v", box)
	}
}

func TestBoxes_AddDocument(t *testing.T) {
	var gotMethod, gotPath, gotXSRF string
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/fileeeboxes/b1/d1" {
			gotMethod, gotPath, gotXSRF = r.Method, r.URL.Path, r.Header.Get("x-xsrf-token")
			w.WriteHeader(200)
			return
		}
		jsonHandler(t, ensureSessionRoutes())(w, r)
	}))
	c := newTestClientAgainstMockServer(t, srv)
	// XSRF-Cookie in den Jar bringen (start-Route setzt normalerweise keinen; login schon)
	if err := c.EnsureSession(context.Background()); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if err := c.Boxes.AddDocument(context.Background(), "b1", "d1"); err != nil {
		t.Fatalf("AddDocument: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/fileeeboxes/b1/d1" {
		t.Errorf("falscher Request: %s %s", gotMethod, gotPath)
	}
	_ = gotXSRF
}

func TestBoxes_RemoveDocument(t *testing.T) {
	var gotMethod, gotPath string
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/fileeeboxes/b1/d1" {
			gotMethod, gotPath = r.Method, r.URL.Path
			w.WriteHeader(200)
			return
		}
		jsonHandler(t, ensureSessionRoutes())(w, r)
	}))
	c := newTestClientAgainstMockServer(t, srv)
	if err := c.Boxes.RemoveDocument(context.Background(), "b1", "d1"); err != nil {
		t.Fatalf("RemoveDocument: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/fileeeboxes/b1/d1" {
		t.Errorf("falscher Request: %s %s", gotMethod, gotPath)
	}
}

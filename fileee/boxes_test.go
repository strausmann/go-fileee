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

// TestBoxes_AddDocument ist zugleich der end-to-end-Regressionstest für Whole-Codebase-Review
// Finding C1 (injectXSRF fragte den Cookie-Jar bisher an der falschen URL ab, siehe transport.go
// apiCookieScopeURL): die Mock-Routen liefern das XSRF-TOKEN-Cookie über eine ECHTE
// /api/f/start-Antwort OHNE explizites Path-Attribut — exakt wie der reale Fileee-Server — und
// der Test prüft, dass der x-xsrf-token-Header beim nachfolgenden mutierenden Request (POST)
// tatsächlich befüllt ankommt, statt (wie zuvor) die Prüfung mit `_ = gotXSRF` zu verwerfen.
//
// WICHTIG: Es wird bewusst NUR EINMAL eingeloggt (kein separater c.EnsureSession()-Aufruf vor
// AddDocument) — AddDocument ruft EnsureSession intern selbst auf. Ein zweiter, vorgelagerter
// EnsureSession-Aufruf würde einen Session-Persist+Reload-Zyklus durchlaufen
// (authClient.persistSession -> loadCookiesIntoJar), und loadCookiesIntoJar lädt die Cookies über
// jar.SetCookies AN DER ROOT-URL nach (session.go) — das gibt dem XSRF-TOKEN-Cookie zusätzlich
// einen Root-Pfad "/" und würde damit selbst die alte, fehlerhafte Root-Abfrage in injectXSRF
// erfüllen und den C1-Bug maskieren, statt ihn zu belegen.
func TestBoxes_AddDocument(t *testing.T) {
	var gotMethod, gotPath, gotXSRF string
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"GET /api/f/start": {Status: 204, Cookies: []*http.Cookie{{Name: "XSRF-TOKEN", Value: "xsrf-echt"}}},
	})
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/fileeeboxes/b1/d1" {
			gotMethod, gotPath, gotXSRF = r.Method, r.URL.Path, r.Header.Get("x-xsrf-token")
			w.WriteHeader(200)
			return
		}
		jsonHandler(t, routes)(w, r)
	}))
	c := newTestClientAgainstMockServer(t, srv)
	if err := c.Boxes.AddDocument(context.Background(), "b1", "d1"); err != nil {
		t.Fatalf("AddDocument: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/fileeeboxes/b1/d1" {
		t.Errorf("falscher Request: %s %s", gotMethod, gotPath)
	}
	if gotXSRF == "" {
		t.Fatal("x-xsrf-token Header war leer — XSRF-Cookie aus /api/f/start wurde nicht injiziert (Review-Finding C1 Regression)")
	}
	if gotXSRF != "xsrf-echt" {
		t.Errorf("x-xsrf-token = %q, erwartet xsrf-echt", gotXSRF)
	}
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

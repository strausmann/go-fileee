package fileee

import (
	"context"
	"net/http"
	"net/url"
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

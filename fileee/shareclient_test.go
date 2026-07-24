package fileee

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShareTokenFromLink(t *testing.T) {
	cases := map[string]string{
		"https://my.fileee.com/shared/abc123":  "abc123",
		"https://my.fileee.com/shared/abc123/": "abc123",
		"abc123":                               "abc123",
	}
	for in, want := range cases {
		if got := ShareTokenFromLink(in); got != want {
			t.Errorf("ShareTokenFromLink(%q) = %q, erwartet %q", in, got, want)
		}
	}
}

func TestShareClient_Resolve(t *testing.T) {
	var gotXSRF, gotMethod, gotPath string
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/f/start":
			http.SetCookie(w, &http.Cookie{Name: "XSRF-TOKEN", Value: "xsrf-abc", Path: "/"})
			w.WriteHeader(204)
		case r.URL.Path == "/api/share-objects/tok123":
			gotMethod, gotPath, gotXSRF = r.Method, r.URL.Path, r.Header.Get("x-xsrf-token")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"id":"share1","sharedBy":"Max","sharedById":"u1","created":"2026-07-24T00:00:00Z","documents":[{"id":"d1"}]}`))
		default:
			w.WriteHeader(404)
		}
	}))

	sc := NewShareClient(WithBaseURL(srv.URL), WithRateLimit(1000, 1000))
	obj, err := sc.Resolve(context.Background(), "tok123")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if obj.SharedBy != "Max" || obj.SharedByID != "u1" || len(obj.Documents) != 1 {
		t.Fatalf("SharedObject falsch dekodiert: %+v", obj)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/share-objects/tok123" {
		t.Errorf("falscher Request: %s %s", gotMethod, gotPath)
	}
	if gotXSRF != "xsrf-abc" {
		t.Errorf("x-xsrf-token nicht gesetzt (aus /api/f/start): %q", gotXSRF)
	}
}

// shareMockServer bedient den kompletten anonymen Share-Flow inkl. Voll-PDF auf dem Static-Host.
func shareMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/f/start":
			http.SetCookie(w, &http.Cookie{Name: "XSRF-TOKEN", Value: "xsrf-abc", Path: "/"})
			w.WriteHeader(204)
		case r.Method == http.MethodPost && r.URL.Path == "/api/share-objects/tok123":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"sh1","sharedBy":"Max","sharedById":"u1","created":"2026-07-24T00:00:00Z",
				"documents":[{"id":"doc1","title":"Rechnung","pageIds":["pg1","pg2"]}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/pages/pg1":
			if r.URL.Query().Get("share_id") != "sh1" || r.URL.Query().Get("shared_by") != "u1" {
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ocr":"text"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/shares/get/sh1/doc1/pdf":
			if r.URL.Query().Get("mode") != "download" {
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.7 fake"))
		default:
			w.WriteHeader(404)
		}
	}))
}

func TestShareClient_Resolve_TypedDocuments(t *testing.T) {
	srv := shareMockServer(t)
	sc := NewShareClient(WithBaseURL(srv.URL), WithStaticBaseURL(srv.URL), WithRateLimit(1000, 1000))
	obj, err := sc.Resolve(context.Background(), "tok123")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if obj.ID != "sh1" || obj.SharedByID != "u1" || len(obj.Documents) != 1 {
		t.Fatalf("SharedObject falsch: %+v", obj)
	}
	d := obj.Documents[0]
	if d.ID != "doc1" || d.Title != "Rechnung" || len(d.PageIDs) != 2 || d.PageIDs[0] != "pg1" {
		t.Fatalf("SharedDocument falsch dekodiert: %+v", d)
	}
	if len(d.Raw) == 0 {
		t.Error("Raw sollte das volle Dokument-JSON behalten")
	}
}

func TestShareClient_DownloadSharedPDF(t *testing.T) {
	srv := shareMockServer(t)
	sc := NewShareClient(WithBaseURL(srv.URL), WithStaticBaseURL(srv.URL), WithRateLimit(1000, 1000))
	rc, err := sc.DownloadSharedPDF(context.Background(), "sh1", "doc1", PDFModeDownload)
	if err != nil {
		t.Fatalf("DownloadSharedPDF: %v", err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "%PDF-1.7 fake" {
		t.Fatalf("PDF-Body falsch: %q", b)
	}
}

func TestShareClient_DownloadSharedPage(t *testing.T) {
	srv := shareMockServer(t)
	sc := NewShareClient(WithBaseURL(srv.URL), WithStaticBaseURL(srv.URL), WithRateLimit(1000, 1000))
	rc, err := sc.DownloadSharedPage(context.Background(), "pg1", "sh1", "u1")
	if err != nil {
		t.Fatalf("DownloadSharedPage: %v", err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != `{"ocr":"text"}` {
		t.Fatalf("OCR-Body falsch: %q", b)
	}
}

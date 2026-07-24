package fileee

import (
	"context"
	"net/http"
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

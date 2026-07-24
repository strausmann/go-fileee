package fileee

import (
	"context"
	"net/http"
	"testing"
)

const ocrArrayBody = `[
  {"text":"Rechnung","webappId":"w1","left":10,"top":20,"right":110,"bottom":40,"width":100,"height":20},
  {"text":"217,84","webappId":"w2","left":50,"top":60,"right":120,"bottom":80,"width":70,"height":20}
]`

func assertOCR(t *testing.T, toks []OCRToken) {
	t.Helper()
	if len(toks) != 2 {
		t.Fatalf("erwartet 2 Tokens, bekommen %d", len(toks))
	}
	if toks[0].Text != "Rechnung" || toks[0].WebappID != "w1" || toks[0].Width != 100 || toks[0].Bottom != 40 {
		t.Fatalf("Token[0] falsch dekodiert: %+v", toks[0])
	}
}

func TestDocuments_PageOCR_Authenticated(t *testing.T) {
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"GET /api/pages/pg1": {Status: 200, Body: []byte(ocrArrayBody)},
	})
	c := newTestClientAgainstMock(t, routes)
	toks, err := c.Documents.PageOCR(context.Background(), "pg1")
	if err != nil {
		t.Fatalf("PageOCR: %v", err)
	}
	assertOCR(t, toks)
}

func TestShareClient_SharedPageOCR_Typed(t *testing.T) {
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/pages/pg1" && r.URL.Query().Get("share_id") == "sh1" && r.URL.Query().Get("shared_by") == "u1" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(ocrArrayBody))
			return
		}
		w.WriteHeader(404)
	}))
	sc := NewShareClient(WithBaseURL(srv.URL), WithRateLimit(1000, 1000))
	toks, err := sc.SharedPageOCR(context.Background(), "pg1", "sh1", "u1")
	if err != nil {
		t.Fatalf("SharedPageOCR: %v", err)
	}
	assertOCR(t, toks)
}

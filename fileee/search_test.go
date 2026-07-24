package fileee

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// TestDocuments_Search_WireForm prüft die aus dem Live-HAR verifizierte Volltextsuche-Wire-Form:
// criteria[] mit field=DocumentQueries:FULLTEXT (type Enum) + operator FUZZY + value (type String),
// plus die Status-Ausschluss-Filter (NEQ gegen UPLOADING/DELETED/ERROR/NEW).
func TestDocuments_Search_WireForm(t *testing.T) {
	var captured map[string]any
	srv := newMockJSONCaptureServer(t, ensureSessionRoutes(),
		"POST", "/api/documents/rest/query",
		func(raw []byte) (int, []byte) {
			_ = json.Unmarshal(raw, &captured)
			return 200, []byte(`{"rows":["6a628532079173000191633d"],"totalRows":1}`)
		})
	c := newTestClientAgainstMockServer(t, srv)

	res, err := c.Documents.Search(context.Background(), "Rechnung", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.TotalRows != 1 || len(res.IDs) != 1 {
		t.Fatalf("erwartet 1 Treffer, bekam TotalRows=%d IDs=%v", res.TotalRows, res.IDs)
	}

	crit, _ := captured["criteria"].([]any)
	// mind. der FULLTEXT-Filter muss vorhanden sein
	var found bool
	for _, cc := range crit {
		m, _ := cc.(map[string]any)
		field, _ := m["field"].(map[string]any)
		if field["value"] != "DocumentQueries:FULLTEXT" {
			continue
		}
		found = true
		fi, _ := field["serializeInformation"].(map[string]any)
		if fi["type"] != "Enum" {
			t.Errorf("FULLTEXT field.type = %v, erwartet Enum", fi["type"])
		}
		if m["operator"] != "FUZZY" {
			t.Errorf("operator = %v, erwartet FUZZY", m["operator"])
		}
		val, _ := m["value"].(map[string]any)
		if val["value"] != "Rechnung" {
			t.Errorf("value.value = %v, erwartet Rechnung", val["value"])
		}
		vi, _ := val["serializeInformation"].(map[string]any)
		if vi["type"] != "String" {
			t.Errorf("value.type = %v, erwartet String", vi["type"])
		}
	}
	if !found {
		t.Fatalf("FULLTEXT-Kriterium fehlt im Request; criteria=%v", crit)
	}
	if captured["onlyIds"] != true {
		t.Errorf("onlyIds sollte true sein (Suche liefert IDs), war %v", captured["onlyIds"])
	}
}

// TestDocuments_Search_StatusFilters: die Suche schließt die nicht-nutzbaren Status aus (wie die UI).
func TestDocuments_Search_StatusFilters(t *testing.T) {
	var captured map[string]any
	srv := newMockJSONCaptureServer(t, ensureSessionRoutes(),
		"POST", "/api/documents/rest/query",
		func(raw []byte) (int, []byte) {
			_ = json.Unmarshal(raw, &captured)
			return 200, []byte(`{"rows":[],"totalRows":0}`)
		})
	c := newTestClientAgainstMockServer(t, srv)
	if _, err := c.Documents.Search(context.Background(), "x", SearchOptions{}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	crit, _ := captured["criteria"].([]any)
	want := map[string]bool{
		"PublicDocumentStatus:UPLOADING": false, "PublicDocumentStatus:DELETED": false,
		"PublicDocumentStatus:ERROR": false, "PublicDocumentStatus:NEW": false,
	}
	for _, cc := range crit {
		m, _ := cc.(map[string]any)
		if m["operator"] != "NEQ" {
			continue
		}
		val, _ := m["value"].(map[string]any)
		if s, ok := val["value"].(string); ok {
			if _, exists := want[s]; exists {
				want[s] = true
			}
		}
	}
	for s, seen := range want {
		if !seen {
			t.Errorf("Status-Ausschluss %s fehlt", s)
		}
	}
}

// TestDocuments_Search_ServerError: ein Server-Fehler wird als *APIError durchgereicht.
func TestDocuments_Search_ServerError(t *testing.T) {
	srv := newMockJSONCaptureServer(t, ensureSessionRoutes(),
		"POST", "/api/documents/rest/query",
		func(raw []byte) (int, []byte) {
			return 500, []byte(`{"apiError":"boom","errorMessage":"interner Fehler"}`)
		})
	c := newTestClientAgainstMockServer(t, srv)
	_, err := c.Documents.Search(context.Background(), "x", SearchOptions{})
	if err == nil {
		t.Fatal("erwartet Fehler bei HTTP 500, bekam nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("erwartet *APIError, bekam %T: %v", err, err)
	}
}

var _ = http.MethodPost

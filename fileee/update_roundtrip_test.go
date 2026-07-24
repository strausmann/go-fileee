package fileee

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// TestDocumentUpdate_PreservesGroupAttributes: ein Dokument mit einem Gruppen-Attribut (amount)
// muss beim Update den amount-Wrapper VERBATIM zurücksenden — die typisierte Dekodierung darf ihn
// nicht mit einem ungültigen Format (type "AttributeGroup") überschreiben. Ein geänderter Titel
// wird dabei mitgesendet.
func TestDocumentUpdate_PreservesGroupAttributes(t *testing.T) {
	docJSON := `{
		"id":"d1","version":3,"status":"CLASSIFIED",
		"attributes":{"type":"COMPOSED","source":"USER","modified":"2026-07-24T00:00:00.000Z","data":{
			"title":{"value":"Alt","type":"TEXT","source":"USER","modified":"2026-07-24T00:00:00.000Z"},
			"amount":{"type":"COMPOSED","attributeGroup":"COMPOSED","source":"SYSTEM","modified":"2026-07-24T00:00:00.000Z","data":{
				"currency":{"value":"EUR","type":"ENUMERATION","source":"SYSTEM","modified":"2026-07-24T00:00:00.000Z","enumClassName":"io.fileee.Currency"},
				"value":{"value":148.75,"type":"DOUBLE","source":"SYSTEM","modified":"2026-07-24T00:00:00.000Z"}
			}}
		}}
	}`
	var captured map[string]any
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/api/documents/rest/d1" {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &captured)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"id":"d1","version":4}`))
			return
		}
		jsonHandler(t, ensureSessionRoutes())(w, r)
	}))
	c := newTestClientAgainstMockServer(t, srv)

	var doc Document
	if err := json.Unmarshal([]byte(docJSON), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Attributes.Amount == nil || doc.Attributes.Amount.Currency != "EUR" {
		t.Fatalf("amount nicht typisiert dekodiert: %+v", doc.Attributes.Amount)
	}
	doc.Attributes.Title = "Neu"

	if _, err := c.Documents.Update(context.Background(), &doc); err != nil {
		t.Fatalf("Update: %v", err)
	}

	attrs, _ := captured["attributes"].(map[string]any)
	data, _ := attrs["data"].(map[string]any)
	// Titel geändert:
	title, _ := data["title"].(map[string]any)
	if title["value"] != "Neu" {
		t.Errorf("title.value = %v, erwartet Neu", title["value"])
	}
	// amount verbatim: type COMPOSED (NICHT AttributeGroup), enumClassName erhalten:
	amount, _ := data["amount"].(map[string]any)
	if amount["type"] != "COMPOSED" {
		t.Errorf("amount.type = %v, erwartet COMPOSED (nicht AttributeGroup)", amount["type"])
	}
	adata, _ := amount["data"].(map[string]any)
	currency, _ := adata["currency"].(map[string]any)
	if currency["enumClassName"] != "io.fileee.Currency" {
		t.Errorf("amount.data.currency.enumClassName ging verloren: %v", currency["enumClassName"])
	}
	if currency["value"] != "EUR" {
		t.Errorf("amount.data.currency.value = %v, erwartet EUR", currency["value"])
	}
}

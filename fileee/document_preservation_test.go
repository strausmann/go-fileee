package fileee

import (
	"encoding/json"
	"reflect"
	"testing"
)

const preservationDocument = `{
	"id":"000000000000000000000001","version":0,"created":"2026-01-02T03:04:05.123Z","modified":"2026-01-02T03:04:05.456Z","deleted":false,
	"status":"CLASSIFIED","type":"example.DocumentApiDTO","futureDocumentField":{"keep":true},
	"pages":[{"id":"000000000000000000000002","imageVersion":"7","contentVersion":3,"rotation":90}],
	"uploadAttribute":{"originalFileName":"synthetic.pdf","futureUploadField":"keep"},
	"attributes":{"type":"COMPOSED","source":"USER","futureEnvelopeField":"keep","data":{
		"title":{"type":"TEXT","value":"Synthetic title","source":"SYSTEM","modified":"2026-01-02T03:04:05.123Z"},
		"senderId":{"type":"TEXT","value":"000000000000000000000003","source":"SYSTEM","confidence":0.75},
		"receiverId":{"type":"TEXT","value":"","source":"USER"},
		"issueDate":{"type":"DATE","value":"2026-01-01","source":"SYSTEM"},
		"read":{"type":"BOOLEAN","value":false,"source":"USER"},
		"reviewed":{"type":"BOOLEAN","value":false,"source":"SYSTEM"},
		"tagIds":{"type":"LIST","containedType":"TEXT","value":[],"source":"USER"},
		"amount":{"type":"COMPOSED","attributeGroup":"COMPOSED","source":"SYSTEM","data":{"currency":{"type":"ENUMERATION","enumClassName":"Currency","value":"EURO","source":"SYSTEM"},"value":{"type":"DOUBLE","value":12.5,"source":"SYSTEM"}}},
		"futureAttribute":{"type":"TEXT","value":"keep","source":"SYSTEM"}
	}}
}`

func preservationJSON(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDocumentRoundTripPreservesCompleteCurrentJSON(t *testing.T) {
	var doc Document
	if err := json.Unmarshal([]byte(preservationDocument), &doc); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preservationJSON(t, encoded), preservationJSON(t, []byte(preservationDocument))) {
		t.Fatalf("an untouched document changed during its JSON round trip: %s", encoded)
	}
	// An existing typed-field consumer must still be able to change a title.
	doc.Attributes.Title = "Changed title"
	encoded, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	got := preservationJSON(t, encoded)
	want := preservationJSON(t, []byte(preservationDocument))
	gotAttrs := got["attributes"].(map[string]any)["data"].(map[string]any)
	wantAttrs := want["attributes"].(map[string]any)["data"].(map[string]any)
	if gotAttrs["title"].(map[string]any)["value"] != "Changed title" {
		t.Fatal("typed title modification was lost")
	}
	delete(gotAttrs, "title")
	delete(wantAttrs, "title")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("a title update changed untouched document fields: %s", encoded)
	}
}

func TestAttributePatchDistinguishesOmittedNullAndEmptyList(t *testing.T) {
	var doc Document
	if err := json.Unmarshal([]byte(preservationDocument), &doc); err != nil {
		t.Fatal(err)
	}
	before, err := doc.Attributes.AttributeFields()
	if err != nil {
		t.Fatal(err)
	}
	patch := map[string]json.RawMessage{
		"amount":     json.RawMessage(`null`),
		"receiverId": json.RawMessage(`{"type":"TEXT","value":"000000000000000000000004","source":"USER"}`),
		"tagIds":     json.RawMessage(`{"type":"LIST","containedType":"TEXT","value":[],"source":"USER"}`),
	}
	if err := doc.Attributes.PatchAttributeFields(patch); err != nil {
		t.Fatal(err)
	}
	after, err := doc.Attributes.AttributeFields()
	if err != nil {
		t.Fatal(err)
	}
	if string(after["amount"]) != "null" || doc.Attributes.Amount != nil {
		t.Fatalf("explicit null did not clear amount: %s", after["amount"])
	}
	if doc.Attributes.ReceiverID != "000000000000000000000004" || len(doc.Attributes.TagIDs) != 0 {
		t.Fatalf("patch was not decoded into typed fields: %+v", doc.Attributes)
	}
	for key, raw := range before {
		if _, touched := patch[key]; !touched && !reflect.DeepEqual(preservationJSONValue(t, raw), preservationJSONValue(t, after[key])) {
			t.Fatalf("omitted field %s changed", key)
		}
	}
	// Returned raw values must not alias the model's preservation snapshot.
	after["senderId"][0] = '['
	again, err := doc.Attributes.AttributeFields()
	if err != nil || !json.Valid(again["senderId"]) {
		t.Fatal("AttributeFields exposed mutable model storage")
	}
	// Malformed JSON cannot partially mutate the document.
	if err := doc.Attributes.PatchAttributeFields(map[string]json.RawMessage{"title": json.RawMessage(`{`)}); err == nil {
		t.Fatal("expected malformed patch rejection")
	}
	if doc.Attributes.Title != "Synthetic title" {
		t.Fatal("rejected patch changed the model")
	}
}

func preservationJSONValue(t *testing.T, raw []byte) any {
	t.Helper()
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPatchAttributeFieldsRejectsNilReceiver(t *testing.T) {
	var attributes *DocumentAttributes
	if err := attributes.PatchAttributeFields(map[string]json.RawMessage{"title": json.RawMessage(`null`)}); err == nil {
		t.Fatal("nil attributes must return an error instead of panicking")
	}
}

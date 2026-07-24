package fileee

import (
	"encoding/json"
	"testing"
)

func TestQueryOptionsToWireEinfacheGleichheit(t *testing.T) {
	opts := QueryOptions{
		Criteria: []Criterion{{Field: "DocumentField:DOCUMENT_INFORMATION__ISREAD", Operator: OpEQ, Value: true}},
	}
	wire := opts.toWire()
	if len(wire.Criteria) != 1 {
		t.Fatalf("erwartet 1 Criterion, bekommen %d", len(wire.Criteria))
	}
	c := wire.Criteria[0]
	if c.Field.Value != "DocumentField:DOCUMENT_INFORMATION__ISREAD" {
		t.Errorf("Field.Value = %q", c.Field.Value)
	}
	if c.Operator != OpEQ {
		t.Errorf("Operator = %q, erwartet EQ", c.Operator)
	}
	if c.Field.SerializeInformation.Type != "Boolean" || c.Value.SerializeInformation.Type != "Boolean" {
		t.Errorf("serializeInformation.type = %q/%q, erwartet Boolean/Boolean", c.Field.SerializeInformation.Type, c.Value.SerializeInformation.Type)
	}
	if wire.Limit != defaultPageLimit {
		t.Errorf("Limit-Default = %d, erwartet %d", wire.Limit, defaultPageLimit)
	}
}

func TestQueryOptionsToWireListeUndSortOrder(t *testing.T) {
	opts := QueryOptions{
		Criteria:  []Criterion{{Field: "EntityObjectField:ID", Operator: OpIn, Value: []string{"a", "b"}}},
		SortOrder: []SortField{{Field: "DocumentField:DOCUMENT_INFORMATION__CREATED", Desc: true}},
		Limit:     50,
		Start:     10,
	}
	wire := opts.toWire()
	if wire.Criteria[0].Field.SerializeInformation.Type != "List" {
		t.Errorf("serializeInformation.type für Liste = %q, erwartet List", wire.Criteria[0].Field.SerializeInformation.Type)
	}
	if wire.Limit != 50 || wire.Start != 10 {
		t.Errorf("Limit/Start = %d/%d, erwartet 50/10", wire.Limit, wire.Start)
	}
	if len(wire.SortOrder) != 1 || !wire.SortOrder[0].Descending {
		t.Fatalf("SortOrder falsch: %+v", wire.SortOrder)
	}
}

func TestQueryOptionsToWireExistsOhneValue(t *testing.T) {
	opts := QueryOptions{Criteria: []Criterion{{Field: "EntityObjectField:ID", Operator: OpExists}}}
	wire := opts.toWire()
	b, err := json.Marshal(wire.Criteria[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	valueField, ok := decoded["value"].(map[string]any)
	if !ok {
		t.Fatalf("value-Feld fehlt oder falscher Typ: %+v", decoded)
	}
	if v, present := valueField["value"]; present && v != nil {
		t.Errorf("erwartet kein value.value bei EXISTS ohne Criterion.Value, bekommen %v", v)
	}
}

func TestSerializeInformationType(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{true, "Boolean"},
		{"text", "String"},
		{[]string{"a"}, "List"},
		{42, "Enum"},
		{nil, "Enum"},
	}
	for _, tc := range cases {
		if got := serializeInformationType(tc.in); got != tc.want {
			t.Errorf("serializeInformationType(%#v) = %q, erwartet %q", tc.in, got, tc.want)
		}
	}
}

func TestCursorNewCloneSindUnabhaengig(t *testing.T) {
	c := NewCursor("Document")
	c.Known["doc-1"] = 1
	clone := c.Clone()
	clone.Known["doc-2"] = 2
	if _, ok := c.Known["doc-2"]; ok {
		t.Fatalf("Clone() teilt die Known-Map mit dem Original (keine echte Kopie)")
	}
	if len(c.Known) != 1 {
		t.Fatalf("Original wurde durch Clone-Mutation verändert: %+v", c.Known)
	}
}

func TestBuildLocalResultsDeterministischSortiert(t *testing.T) {
	c := NewCursor("Tag")
	c.Known["tag-b"] = 2
	c.Known["tag-a"] = 1
	local := buildLocalResults(c)
	if len(local) != 2 || local[0].ID != "tag-a" || local[1].ID != "tag-b" {
		t.Fatalf("buildLocalResults nicht deterministisch sortiert: %+v", local)
	}
}

func TestDecodeDiffLeererCursor(t *testing.T) {
	body := []byte(`{"rows":[{"id":"tag-1","version":1,"name":"Rechnung"},{"id":"tag-2","version":1,"name":"Vertrag"}],"idsToDelete":[],"totalRows":2}`)
	result, err := decodeDiff[Tag](body, NewCursor("Tag"))
	if err != nil {
		t.Fatalf("decodeDiff: %v", err)
	}
	if len(result.Rows) != 2 || result.TotalRows != 2 {
		t.Fatalf("Rows/TotalRows falsch: %+v", result)
	}
	if result.NextCursor.Known["tag-1"] != 1 || result.NextCursor.Known["tag-2"] != 1 {
		t.Fatalf("NextCursor.Known falsch befüllt: %+v", result.NextCursor.Known)
	}
}

func TestDecodeDiffCursorMitVorwissenUndIdsToDelete(t *testing.T) {
	cursor := NewCursor("Tag")
	cursor.Known["tag-1"] = 1
	cursor.Known["tag-alt"] = 5

	body := []byte(`{"rows":[{"id":"tag-1","version":2,"name":"Rechnung"}],"idsToDelete":["tag-alt"],"totalRows":1}`)
	result, err := decodeDiff[Tag](body, cursor)
	if err != nil {
		t.Fatalf("decodeDiff: %v", err)
	}
	if result.NextCursor.Known["tag-1"] != 2 {
		t.Fatalf("tag-1 wurde nicht auf Version 2 aktualisiert: %+v", result.NextCursor.Known)
	}
	if _, stillThere := result.NextCursor.Known["tag-alt"]; stillThere {
		t.Fatalf("tag-alt hätte durch idsToDelete entfernt werden müssen: %+v", result.NextCursor.Known)
	}
	if len(result.DeletedIDs) != 1 || result.DeletedIDs[0] != "tag-alt" {
		t.Fatalf("DeletedIDs falsch: %+v", result.DeletedIDs)
	}
	// ursprünglicher Cursor darf NICHT mutiert worden sein (Clone-Semantik).
	if _, stillInOriginal := cursor.Known["tag-alt"]; !stillInOriginal {
		t.Fatalf("Original-Cursor wurde fälschlich mutiert")
	}
}

func TestDecodeDiffKaputtesJSON(t *testing.T) {
	_, err := decodeDiff[Tag]([]byte("nicht-json"), NewCursor("Tag"))
	if err == nil {
		t.Fatalf("erwartet Fehler bei kaputtem JSON, bekommen nil")
	}
}

// TestDecodeDiffDocumentBatchToleriertStringVersionInEinerZeile ist die Batch-Level-Regression aus
// dem finalen Whole-Branch-Review: EINE Dokumentzeile mit einem Page-imageVersion als JSON-String
// (openapi.json: DocumentPage.imageVersion = ["string","integer"]) durfte den kompletten
// Query/Diff-Batch NICHT abbrechen. Vor dem Fix ließ das reine int64-Feld encoding/json bei dieser
// einen Zeile mit einem Typfehler abbrechen — decodeDiff[Document] gab dann gar keine Dokumente
// zurück, obwohl nur eine von zwei Zeilen betroffen war. Nach dem Fix müssen BEIDE Dokumente
// vollständig dekodiert im Ergebnis auftauchen.
func TestDecodeDiffDocumentBatchToleriertStringVersionInEinerZeile(t *testing.T) {
	body := []byte(`{
		"rows": [
			{
				"id": "doc-1", "version": 1, "created": "2026-01-01T00:00:00Z", "modified": "2026-01-01T00:00:00Z",
				"deleted": false, "status": "DONE", "type": "Document",
				"pages": [{"id": "page-1", "imageVersion": "5", "contentVersion": "5"}],
				"attributes": {"data": {}}, "uploadAttribute": {}, "sharedSpaceIds": [], "forbiddenActions": []
			},
			{
				"id": "doc-2", "version": 1, "created": "2026-01-02T00:00:00Z", "modified": "2026-01-02T00:00:00Z",
				"deleted": false, "status": "DONE", "type": "Document",
				"pages": [{"id": "page-2", "imageVersion": 7, "contentVersion": 3}],
				"attributes": {"data": {}}, "uploadAttribute": {}, "sharedSpaceIds": [], "forbiddenActions": []
			}
		],
		"idsToDelete": [],
		"totalRows": 2
	}`)
	result, err := decodeDiff[Document](body, NewCursor("Document"))
	if err != nil {
		t.Fatalf("decodeDiff[Document]: %v (der gesamte Batch wäre vor dem Fix an EINER Zeile mit String-Version abgebrochen)", err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("erwartet 2 Dokumente im Batch, bekommen %d: %+v", len(result.Rows), result.Rows)
	}
	if result.Rows[0].ID != "doc-1" || int64(result.Rows[0].Pages[0].ImageVersion) != 5 {
		t.Errorf("doc-1 falsch dekodiert: %+v", result.Rows[0])
	}
	if result.Rows[1].ID != "doc-2" || int64(result.Rows[1].Pages[0].ImageVersion) != 7 {
		t.Errorf("doc-2 falsch dekodiert: %+v", result.Rows[1])
	}
	if result.NextCursor.Known["doc-1"] != 1 || result.NextCursor.Known["doc-2"] != 1 {
		t.Errorf("NextCursor nach Batch falsch: %+v", result.NextCursor.Known)
	}
}

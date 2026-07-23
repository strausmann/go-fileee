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

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

package fileee

import "testing"

func TestQueryOptionsToWireEnumSortField(t *testing.T) {
	options := QueryOptions{SortOrder: []SortField{
		{Field: "ContactinyObjectField:COMPANY_NAME", FieldType: "Enum"},
		{Field: "EntityObjectField:ID"},
	}}
	fields := options.toWire().SortOrder
	if fields[0].BaseAttribute.SerializeInformation.Type != "Enum" {
		t.Errorf("expliziter Typ = %q, erwartet Enum", fields[0].BaseAttribute.SerializeInformation.Type)
	}
	if fields[1].BaseAttribute.SerializeInformation.Type != "String" {
		t.Errorf("Standardtyp = %q, erwartet String", fields[1].BaseAttribute.SerializeInformation.Type)
	}
}

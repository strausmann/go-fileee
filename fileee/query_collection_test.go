package fileee

import "testing"

func TestQuerySerializesCollectionElementType(t *testing.T) {
	legacy := Criterion{Field: "DocumentField:TAG_IDS", Operator: OpHasAll, Value: []string{"tag-example"}, FieldType: "Enum", ValueType: "List"}
	typed := legacy
	typed.CollectionValueType = "String"
	filters := (QueryOptions{Criteria: []Criterion{legacy, typed}}).toWire().Criteria
	if filters[0].Value.SerializeInformation.CollectionSerializeInformation != nil {
		t.Fatal("legacy filter unexpectedly specifies an element type")
	}
	typedInfo := filters[1].Value.SerializeInformation
	if typedInfo.Type != "List" || typedInfo.CollectionSerializeInformation == nil || typedInfo.CollectionSerializeInformation.Type != "String" {
		t.Fatalf("typed filter did not serialize a list of strings: %+v", typedInfo)
	}
}

package fileee

import (
	"encoding/json"
	"reflect"
)

// jsonRawRow ist ein sprechender Alias für json.RawMessage, verwendet in queryResultWire (dieser
// Task) und diffWire (Task 13) — die generische decodeDiff/Query-Dekodierung braucht rohe
// Zeilen-Bytes, bevor sie typspezifisch (T) entschlüsselt werden.
type jsonRawRow = json.RawMessage

// defaultPageLimit ist der Standard-Seitenlimit für Query/Diff-Requests (Umbrella-Spec §3.3).
// Bewusst hier statt in client.go definiert: query.go (Task 12) wird vor client.go (Task 11)
// implementiert (Batch-Reihenfolge 12->13->14->11, siehe Task-11-Brief "Hinweis zur Reihenfolge"),
// client.go referenziert diese Konstante nur noch.
const defaultPageLimit = 100

// QueryOptions steuert POST .../rest/query (Umbrella-Spec §3.3, API.md §3).
type QueryOptions struct {
	Criteria  []Criterion
	SortOrder []SortField
	Start     int
	Limit     int
	OnlyIDs   bool
}

// Criterion ist eine typisierte Filterbedingung; Field ist eine EntityField-Konstante
// (z.B. "DocumentField:DOCUMENT_INFORMATION__ISREAD", API.md §3.3).
type Criterion struct {
	Field    string
	Operator Operator
	Value    any
	Optional bool
}

// SortField steuert die Sortierung (API.md §3.2).
type SortField struct {
	Field      string
	Desc       bool
	NullsFirst bool
}

type QueryResult[T any] struct {
	Rows      []T
	TotalRows int
}

type serializeInfoWire struct {
	Type string `json:"type"`
}

type criterionFieldWire struct {
	Value                string            `json:"value"`
	SerializeInformation serializeInfoWire `json:"serializeInformation"`
}

type criterionValueWire struct {
	Value                any               `json:"value,omitempty"`
	SerializeInformation serializeInfoWire `json:"serializeInformation"`
}

type criterionWire struct {
	Field    criterionFieldWire `json:"field"`
	Operator Operator           `json:"operator"`
	Optional bool               `json:"optional"`
	Value    criterionValueWire `json:"value"`
}

type sortOrderWire struct {
	BaseAttribute criterionFieldWire `json:"baseAttribute"`
	Descending    bool               `json:"descending"`
	NullsFirst    bool               `json:"nullsFirst"`
}

type queryRequestWire struct {
	Criteria  []criterionWire `json:"criteria"`
	SortOrder []sortOrderWire `json:"sortOrder"`
	Limit     int             `json:"limit"`
	Start     int             `json:"start"`
	OnlyIDs   bool            `json:"onlyIds"`
}

type queryResultWire struct {
	Rows      []jsonRawRow `json:"rows"`
	TotalRows int          `json:"totalRows"`
}

// serializeInformationType leitet den Query-DSL-Typdiskriminator (API.md §3.2/§6.10) aus dem
// Go-Laufzeittyp von v ab — dokumentierte Annahme, siehe Design-Entscheidung in Task-12-Brief und
// Self-Review (Umbrella-Spec §10.3 Punkt C: fail-safe, falscher Diskriminator führt schlimmstenfalls
// zu einem vom Server ignorierten Filter, nicht zu Datenverlust).
func serializeInformationType(v any) string {
	switch v.(type) {
	case bool:
		return "Boolean"
	case string:
		return "String"
	case nil:
		return "Enum"
	}
	rv := reflect.ValueOf(v)
	if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
		return "List"
	}
	return "Enum"
}

func (o QueryOptions) toWire() queryRequestWire {
	w := queryRequestWire{
		Criteria:  make([]criterionWire, 0, len(o.Criteria)),
		SortOrder: make([]sortOrderWire, 0, len(o.SortOrder)),
		Limit:     o.Limit,
		Start:     o.Start,
		OnlyIDs:   o.OnlyIDs,
	}
	if w.Limit == 0 {
		w.Limit = defaultPageLimit
	}
	for _, c := range o.Criteria {
		typ := serializeInformationType(c.Value)
		w.Criteria = append(w.Criteria, criterionWire{
			Field:    criterionFieldWire{Value: c.Field, SerializeInformation: serializeInfoWire{Type: typ}},
			Operator: c.Operator,
			Optional: c.Optional,
			Value:    criterionValueWire{Value: c.Value, SerializeInformation: serializeInfoWire{Type: typ}},
		})
	}
	for _, s := range o.SortOrder {
		w.SortOrder = append(w.SortOrder, sortOrderWire{
			BaseAttribute: criterionFieldWire{Value: s.Field, SerializeInformation: serializeInfoWire{Type: "String"}},
			Descending:    s.Desc,
			NullsFirst:    s.NullsFirst,
		})
	}
	return w
}

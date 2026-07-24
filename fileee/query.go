package fileee

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
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
// (z.B. "DocumentField:DOCUMENT_INFORMATION__ISREAD").
//
// FieldType und ValueType überschreiben optional den serializeInformation.type von field bzw.
// value; ohne Override wird er aus dem Go-Wert abgeleitet. Nötig, wenn beide Typen auseinanderfallen
// (z.B. Volltextsuche: field "Enum", value "String").
type Criterion struct {
	Field     string
	Operator  Operator
	Value     any
	Optional  bool
	FieldType string
	ValueType string
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
		derived := serializeInformationType(c.Value)
		fieldType := derived
		if c.FieldType != "" {
			fieldType = c.FieldType
		}
		valueType := derived
		if c.ValueType != "" {
			valueType = c.ValueType
		}
		w.Criteria = append(w.Criteria, criterionWire{
			Field:    criterionFieldWire{Value: c.Field, SerializeInformation: serializeInfoWire{Type: fieldType}},
			Operator: c.Operator,
			Optional: c.Optional,
			Value:    criterionValueWire{Value: c.Value, SerializeInformation: serializeInfoWire{Type: valueType}},
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

// Cursor hält den Sync-Zustand EINER Entity-Art (Umbrella-Spec §5.1). Der Aufrufer
// erzeugt/speichert/übergibt Cursor-Werte — die Lib mutiert sie nur innerhalb eines
// Diff()-Aufrufs (über eine Kopie, siehe Clone) und gibt das Ergebnis zurück.
type Cursor struct {
	EntityType string
	Known      map[string]int64
}

func NewCursor(entityType string) Cursor {
	return Cursor{EntityType: entityType, Known: map[string]int64{}}
}

func (c Cursor) Clone() Cursor {
	known := make(map[string]int64, len(c.Known))
	for k, v := range c.Known {
		known[k] = v
	}
	return Cursor{EntityType: c.EntityType, Known: known}
}

type DiffResult[T any] struct {
	Rows       []T
	DeletedIDs []string
	TotalRows  int
	NextCursor Cursor
}

type localResultWire struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

type diffRequestWire struct {
	Criteria     []criterionWire   `json:"criteria"`
	SortOrder    []sortOrderWire   `json:"sortOrder"`
	Limit        int               `json:"limit"`
	Start        int               `json:"start"`
	LocalResults []localResultWire `json:"localResults"`
}

type diffWire struct {
	Rows        []jsonRawRow `json:"rows"`
	IdsToDelete []string     `json:"idsToDelete"`
	TotalRows   int          `json:"totalRows"`
}

type idVersionWire struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

// buildLocalResults baut den Request-Body-Teil localResults aus cursor.Known (Umbrella-Spec §5.2,
// angenommene Wire-Form — siehe Umbrella-Spec §10.3 Punkt C: fail-safe, ein unerwartetes Format
// führt schlimmstenfalls zu einem Voll-Snapshot statt Delta, kein Datenverlust). Deterministisch
// sortiert nach ID für reproduzierbare Requests/Tests.
func buildLocalResults(c Cursor) []localResultWire {
	out := make([]localResultWire, 0, len(c.Known))
	for id, v := range c.Known {
		out = append(out, localResultWire{ID: id, Version: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// decodeDiff dekodiert eine diffWire-Antwort generisch in ein DiffResult[T] und berechnet
// NextCursor gemäß §5.2: jede Zeile -> Known[id]=version, idsToDelete entfernt Einträge. T wird
// sowohl in die typisierten Rows als auch (separat, über idVersionWire) für die Cursor-Merge-Logik
// dekodiert, damit die Merge-Logik unabhängig vom konkreten Zeilentyp bleibt.
func decodeDiff[T any](body []byte, cursor Cursor) (*DiffResult[T], error) {
	var w diffWire
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("fileee: diff response decode: %w", err)
	}
	rows := make([]T, 0, len(w.Rows))
	next := cursor.Clone()
	for _, raw := range w.Rows {
		var row T
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, fmt.Errorf("fileee: diff row decode: %w", err)
		}
		rows = append(rows, row)

		var iv idVersionWire
		if err := json.Unmarshal(raw, &iv); err != nil {
			return nil, fmt.Errorf("fileee: diff row id/version decode: %w", err)
		}
		next.Known[iv.ID] = iv.Version
	}
	for _, id := range w.IdsToDelete {
		delete(next.Known, id)
	}
	return &DiffResult[T]{
		Rows:       rows,
		DeletedIDs: w.IdsToDelete,
		TotalRows:  w.TotalRows,
		NextCursor: next,
	}, nil
}

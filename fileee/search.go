package fileee

import "context"

const (
	fieldFulltext    = "DocumentQueries:FULLTEXT"
	fieldDocStatus   = "DocumentField:DOCUMENT_INFORMATION__STATUS"
	statusEnumPrefix = "PublicDocumentStatus:"
)

// nonSearchableStatuses sind die Dokument-Status, die eine Suche ausschließt (unfertig, gelöscht,
// fehlerhaft).
var nonSearchableStatuses = []string{"UPLOADING", "DELETED", "ERROR", "NEW"}

// SearchOptions steuert Documents.Search.
type SearchOptions struct {
	Limit int
	Start int
	// IncludeAllStatuses sucht auch über unfertige/gelöschte/fehlerhafte Dokumente.
	IncludeAllStatuses bool
}

// SearchResult enthält die Dokument-IDs der Treffer und deren Gesamtzahl.
type SearchResult struct {
	IDs       []string
	TotalRows int
}

// Search führt eine Volltextsuche (FULLTEXT/FUZZY) aus und liefert die IDs der Treffer; Details je
// Treffer über Documents.Get. Nicht durchsuchbare Status werden ausgeschlossen, sofern nicht
// SearchOptions.IncludeAllStatuses gesetzt ist.
func (s *DocumentService) Search(ctx context.Context, term string, opts SearchOptions) (*SearchResult, error) {
	criteria := make([]Criterion, 0, len(nonSearchableStatuses)+1)
	if !opts.IncludeAllStatuses {
		for _, st := range nonSearchableStatuses {
			criteria = append(criteria, Criterion{
				Field:     fieldDocStatus,
				Operator:  OpNEQ,
				Value:     statusEnumPrefix + st,
				FieldType: "Enum",
				ValueType: "Enum",
			})
		}
	}
	criteria = append(criteria, Criterion{
		Field:     fieldFulltext,
		Operator:  OpFuzzy,
		Value:     term,
		FieldType: "Enum",
		ValueType: "String",
	})

	res, err := s.inner.queryIDs(ctx, QueryOptions{
		Criteria: criteria,
		Limit:    opts.Limit,
		Start:    opts.Start,
		OnlyIDs:  true,
	})
	if err != nil {
		return nil, err
	}
	return &SearchResult{IDs: res.IDs, TotalRows: res.TotalRows}, nil
}

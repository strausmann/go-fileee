package fileee

import "context"

// Query-DSL-Konstanten der Volltextsuche (live via HAR verifiziert, siehe Skill fileee
// troubleshooting „Volltextsuche"). Feldnamen sind DSL-Konstanten `<Entity>Queries|Field:<KONST>`,
// KEIN Attribut-Pfad wie `attributes.data.title`.
const (
	fieldFulltext    = "DocumentQueries:FULLTEXT"
	fieldDocStatus   = "DocumentField:DOCUMENT_INFORMATION__STATUS"
	statusEnumPrefix = "PublicDocumentStatus:"
)

// nonSearchableStatuses sind die Dokument-Status, die die Web-UI bei einer Suche ausschließt
// (noch nicht fertig verarbeitet / gelöscht / fehlerhaft) — als NEQ-Filter mitgeschickt.
var nonSearchableStatuses = []string{"UPLOADING", "DELETED", "ERROR", "NEW"}

// SearchOptions steuert Documents.Search.
type SearchOptions struct {
	// Limit begrenzt die Trefferzahl pro Seite (0 = Lib-Default).
	Limit int
	// Start ist der Offset für Paginierung.
	Start int
	// IncludeAllStatuses deaktiviert die Standard-Status-Ausschlüsse (UPLOADING/DELETED/ERROR/NEW),
	// falls wirklich über ALLE Dokumente inkl. unfertiger gesucht werden soll.
	IncludeAllStatuses bool
}

// SearchResult ist das Ergebnis einer Volltextsuche: die Dokument-IDs der Treffer (die Suche läuft
// mit onlyIds=true, wie die Web-UI) plus die Gesamttrefferzahl.
type SearchResult struct {
	IDs       []string
	TotalRows int
}

// Search führt eine Volltextsuche über alle Dokumente aus (FULLTEXT/FUZZY). Die exakte
// Kriterien-Wire-Form ist live gegen my.fileee.com verifiziert: field `DocumentQueries:FULLTEXT`
// (type Enum), operator FUZZY, value = Suchbegriff (type String); zusätzlich schließt die Suche —
// wie die Web-UI — die nicht-durchsuchbaren Status per NEQ aus. Ergebnis sind Dokument-IDs
// (onlyIds=true); Details je Treffer über Documents.Get.
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

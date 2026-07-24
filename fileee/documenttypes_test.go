package fileee

import (
	"context"
	"testing"
)

// TestDocumentTypeServiceDiffDirekt200KeinFallback deckt den bisher ungetesteten Zweig von
// documentTypeService.Diff() ab, in dem der generische diff-Endpunkt direkt mit 200 antwortet.
// In diesem Fall darf KEIN Fallback auf Query() erfolgen — die Query-Route ist hier bewusst
// NICHT gemockt, sodass ein fälschlich ausgelöster Fallback sofort an der 404-Fallback-Route
// des Mock-Servers scheitern würde.
func TestDocumentTypeServiceDiffDirekt200KeinFallback(t *testing.T) {
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"POST /api/document-types/rest/diff": {
			Status: 200,
			Body:   []byte(`{"rows":[{"id":"dt-1","i18NName":"Rechnung","version":2}],"idsToDelete":["dt-alt"],"totalRows":1}`),
		},
	})
	client := newTestClientAgainstMock(t, routes)

	cursor := NewCursor("DocumentType")
	cursor.Known["dt-alt"] = 1

	result, err := client.DocumentTypes.Diff(context.Background(), cursor)
	if err != nil {
		t.Fatalf("Diff (direkt, kein Fallback): %v", err)
	}
	if len(result.Rows) != 1 || result.Rows[0].ID != "dt-1" || result.Rows[0].I18NName != "Rechnung" {
		t.Fatalf("Diff-Rows falsch übernommen (müssen 1:1 aus dem diff-Endpunkt stammen): %+v", result.Rows)
	}
	if len(result.DeletedIDs) != 1 || result.DeletedIDs[0] != "dt-alt" {
		t.Fatalf("DeletedIDs falsch: %+v", result.DeletedIDs)
	}
	if _, stillKnown := result.NextCursor.Known["dt-alt"]; stillKnown {
		t.Fatalf("dt-alt hätte laut idsToDelete aus NextCursor.Known entfernt werden müssen: %+v", result.NextCursor.Known)
	}
	if v, ok := result.NextCursor.Known["dt-1"]; !ok || v != 2 {
		t.Fatalf("dt-1 hätte mit Version 2 in NextCursor.Known stehen müssen: %+v", result.NextCursor.Known)
	}
}

// TestDocumentTypeServiceQueryFallbackDiffEntferntFehlendeKnownIDs deckt den Fallback-Zweig
// (queryFallbackDiff) für den Fall ab, dass der übergebene Cursor bereits bekannte IDs enthält,
// die im Query-Ergebnis NICHT mehr auftauchen (serverseitig gelöschte DocumentTypes). Diese IDs
// müssen als gelöscht erkannt werden: in der berechneten DeletedIDs-Liste auftauchen und aus
// NextCursor.Known verschwinden.
func TestDocumentTypeServiceQueryFallbackDiffEntferntFehlendeKnownIDs(t *testing.T) {
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"POST /api/document-types/rest/diff":  {Status: 404, Body: []byte(`{"errorCode":"NOT_FOUND"}`)},
		"POST /api/document-types/rest/query": {Status: 200, Body: []byte(`{"rows":[{"id":"dt-1","i18NName":"Rechnung","version":1}],"totalRows":1}`)},
	})
	client := newTestClientAgainstMock(t, routes)

	// Cursor kennt bereits "dt-1" (bleibt) UND "dt-verschwunden" (fehlt im Query-Ergebnis -> muss
	// als gelöscht erkannt werden).
	cursor := NewCursor("DocumentType")
	cursor.Known["dt-1"] = 1
	cursor.Known["dt-verschwunden"] = 3

	result, err := client.DocumentTypes.Diff(context.Background(), cursor)
	if err != nil {
		t.Fatalf("Diff mit Fallback: %v", err)
	}
	if len(result.Rows) != 1 || result.Rows[0].ID != "dt-1" {
		t.Fatalf("Fallback-Rows falsch: %+v", result.Rows)
	}

	foundDeleted := false
	for _, id := range result.DeletedIDs {
		if id == "dt-verschwunden" {
			foundDeleted = true
		}
	}
	if !foundDeleted {
		t.Fatalf("erwartet 'dt-verschwunden' in DeletedIDs, bekommen: %+v", result.DeletedIDs)
	}
	if _, stillKnown := result.NextCursor.Known["dt-verschwunden"]; stillKnown {
		t.Fatalf("'dt-verschwunden' hätte aus NextCursor.Known entfernt werden müssen: %+v", result.NextCursor.Known)
	}
	if v, ok := result.NextCursor.Known["dt-1"]; !ok || v != 1 {
		t.Fatalf("'dt-1' hätte mit Version 1 in NextCursor.Known bleiben müssen: %+v", result.NextCursor.Known)
	}
}

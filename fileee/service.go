package fileee

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ReadService deckt die drei lesenden REST-Konventionen ab (Umbrella-Spec §3.3).
type ReadService[T any] interface {
	Query(ctx context.Context, opts QueryOptions) (*QueryResult[T], error)
	Diff(ctx context.Context, cursor Cursor) (*DiffResult[T], error)
	Get(ctx context.Context, id string) (*T, error)
}

// WriteService erweitert ReadService um Create/Update (aktuell nur Contacts, Umbrella-Spec §3.3).
type WriteService[T any] interface {
	ReadService[T]
	Create(ctx context.Context, entity *T) (*T, error)
	Update(ctx context.Context, entity *T) (*T, error)
}

// restService implementiert das generische Query/Diff/Get-Muster (API.md §1/§3), das alle
// Sync-fähigen Ressourcen teilen. T ist der konkrete Zeilentyp (Tag, Company, DocumentType).
type restService[T any] struct {
	client       *Client
	resourcePath string
}

// Query listet Entitäten über den Query-Endpunkt (paginiert).
func (s *restService[T]) Query(ctx context.Context, opts QueryOptions) (*QueryResult[T], error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	body, err := json.Marshal(opts.toWire())
	if err != nil {
		return nil, fmt.Errorf("fileee: query request encode: %w", err)
	}
	resp, err := s.client.postJSON(ctx, "/api/"+s.resourcePath+"/rest/query", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: query response read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}
	var wire queryResultWire
	if err := json.Unmarshal(respBody, &wire); err != nil {
		return nil, fmt.Errorf("fileee: query response decode: %w", err)
	}
	rows := make([]T, 0, len(wire.Rows))
	for _, raw := range wire.Rows {
		var row T
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, fmt.Errorf("fileee: query row decode: %w", err)
		}
		rows = append(rows, row)
	}
	return &QueryResult[T]{Rows: rows, TotalRows: wire.TotalRows}, nil
}

// idQueryResult ist das Ergebnis einer onlyIds-Query: reine Entity-IDs statt voller Objekte.
type idQueryResult struct {
	IDs       []string
	TotalRows int
}

// queryIDs führt eine onlyIds-Query aus; der Server liefert dabei pro Zeile eine ID statt eines
// vollen Objekts.
func (s *restService[T]) queryIDs(ctx context.Context, opts QueryOptions) (*idQueryResult, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	opts.OnlyIDs = true
	body, err := json.Marshal(opts.toWire())
	if err != nil {
		return nil, fmt.Errorf("fileee: queryIDs request encode: %w", err)
	}
	resp, err := s.client.postJSON(ctx, "/api/"+s.resourcePath+"/rest/query", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: queryIDs response read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}
	var wire queryResultWire
	if err := json.Unmarshal(respBody, &wire); err != nil {
		return nil, fmt.Errorf("fileee: queryIDs response decode: %w", err)
	}
	ids := make([]string, 0, len(wire.Rows))
	for _, raw := range wire.Rows {
		var id string
		if err := json.Unmarshal(raw, &id); err != nil {
			return nil, fmt.Errorf("fileee: queryIDs row decode: %w", err)
		}
		ids = append(ids, id)
	}
	return &idQueryResult{IDs: ids, TotalRows: wire.TotalRows}, nil
}

// Diff liefert die Änderungen seit dem übergebenen Cursor.
func (s *restService[T]) Diff(ctx context.Context, cursor Cursor) (*DiffResult[T], error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	body, err := json.Marshal(diffRequestWire{
		Criteria:     []criterionWire{},
		SortOrder:    []sortOrderWire{},
		LocalResults: buildLocalResults(cursor),
		Limit:        defaultPageLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("fileee: diff request encode: %w", err)
	}
	resp, err := s.client.postJSON(ctx, "/api/"+s.resourcePath+"/rest/diff", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: diff response read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}
	return decodeDiff[T](respBody, cursor)
}

// Get lädt eine einzelne Entität anhand ihrer ID.
func (s *restService[T]) Get(ctx context.Context, id string) (*T, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	resp, err := s.client.get(ctx, "/api/"+s.resourcePath+"/rest/"+id)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: get response read: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}
	var row T
	if err := json.Unmarshal(respBody, &row); err != nil {
		return nil, fmt.Errorf("fileee: get response decode: %w", err)
	}
	return &row, nil
}

// queryAllPages blaettert Query() vollstaendig durch (Start/Limit) — genutzt vom
// DocumentType-Diff-Fallback (§10.3 D) und generell für Voll-Exporte.
func (s *restService[T]) queryAllPages(ctx context.Context) ([]T, error) {
	var all []T
	start := 0
	for {
		page, err := s.Query(ctx, QueryOptions{Start: start, Limit: defaultPageLimit})
		if err != nil {
			return nil, err
		}
		all = append(all, page.Rows...)
		start += len(page.Rows)
		if len(page.Rows) == 0 || start >= page.TotalRows {
			break
		}
	}
	return all, nil
}

// randRead ist als Package-Var austauschbar (Standard: crypto/rand.Read), damit Tests eine
// Entropie-Fehlersituation ohne echten OS-Fehler simulieren können (siehe newObjectID-Tests in
// service_test.go).
var randRead = rand.Read

// newObjectID erzeugt eine clientseitige ID für Contact.Create-`id` und Document.Upload-`id`
// (API.md §4.1) — bewusst ohne externe UUID-Dependency (Global Constraints: keine zusätzlichen
// Runtime-Deps). Vollständig hier implementiert (nicht erst in Task 16), da Task 14 vor Task 16
// läuft; Task 16 (Dokument-Upload) nutzt dieselbe Funktion.
//
// Format: 24 Hex-Zeichen (12 zufällige Bytes), NICHT eine RFC-4122-UUID (36 Zeichen mit
// Bindestrichen). LIVE VERIFIZIERT (2026-07-23, Contacts.Create gegen Testkonto): eine gesendete
// UUID wird vom Server MIT 400 "IllegalConditions" / "Invalid Id format" abgelehnt; eine echte,
// vom Server vergebene Contact-ID hat live beobachtet len=24 (z.B. "6a62852e079173000191609d") —
// das entspricht dem MongoDB-ObjectId-Format. Ein 24-Hex-Zeichen-Wert wurde live erfolgreich
// akzeptiert (200, Contact angelegt). Die ursprüngliche Annahme "clientseitig generierte UUID"
// (API.md §4.1, aus der Kotlin/JS-Code-Analyse von `InstanceHelper.newObjectId()` abgeleitet) war
// zu diesem Punkt falsch/ungenau — der tatsächlich erwartete ID-Raum ist ObjectId-förmig.
//
// Gibt (string, error) zurück statt den Fehler von rand.Read stillschweigend zu verschlucken
// (Copilot-Review PR#7): bei Entropie-Erschöpfung wäre sonst ein All-Null-Byte-Slice ("000...0")
// als ID möglich — mit Kollisionsrisiko über mehrere Aufrufe hinweg statt eines lauten Fehlers.
func newObjectID() (string, error) {
	var b [12]byte
	if _, err := randRead(b[:]); err != nil {
		return "", fmt.Errorf("fileee: objectId generieren: %w", err)
	}
	return fmt.Sprintf("%x", b), nil
}

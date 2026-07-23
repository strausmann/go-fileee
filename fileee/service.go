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

func (s *restService[T]) Diff(ctx context.Context, cursor Cursor) (*DiffResult[T], error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	body, err := json.Marshal(diffRequestWire{LocalResults: buildLocalResults(cursor), Limit: defaultPageLimit})
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

// newObjectID erzeugt eine clientseitige RFC-4122-v4-UUID (für Contact.Create-`id` und
// Document.Upload-`id`, API.md §4.1) — bewusst ohne externe UUID-Dependency (Global Constraints:
// keine zusätzlichen Runtime-Deps). Vollständig hier implementiert (nicht erst in Task 16), da
// Task 14 vor Task 16 läuft; Task 16 (Dokument-Upload) nutzt dieselbe Funktion.
func newObjectID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

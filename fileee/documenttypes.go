package fileee

import (
	"context"
	"errors"
	"net/http"
)

// documentTypeService bettet restService[DocumentType] für Query/Get ein und überschreibt Diff
// um den Query-Fallback (Umbrella-Spec §10.3 D) zu implementieren.
type documentTypeService struct {
	inner restService[DocumentType]
}

func newDocumentTypeService(c *Client) ReadService[DocumentType] {
	return &documentTypeService{inner: restService[DocumentType]{client: c, resourcePath: "document-types"}}
}

// Query listet die Dokumenttypen.
func (s *documentTypeService) Query(ctx context.Context, opts QueryOptions) (*QueryResult[DocumentType], error) {
	return s.inner.Query(ctx, opts)
}

// Get lädt einen einzelnen Dokumenttyp anhand seiner ID.
func (s *documentTypeService) Get(ctx context.Context, id string) (*DocumentType, error) {
	return s.inner.Get(ctx, id)
}

// Diff versucht den generischen diff-Endpunkt; bei 404/405 (nicht vorhanden, §10.3 D) degradiert
// er auf einen Voll-Query als Fallback und baut den Cursor daraus neu auf.
func (s *documentTypeService) Diff(ctx context.Context, cursor Cursor) (*DiffResult[DocumentType], error) {
	result, err := s.inner.Diff(ctx, cursor)
	if err == nil {
		return result, nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && (apiErr.HTTPStatus == http.StatusNotFound || apiErr.HTTPStatus == http.StatusMethodNotAllowed) {
		return s.queryFallbackDiff(ctx, cursor)
	}
	return nil, err
}

func (s *documentTypeService) queryFallbackDiff(ctx context.Context, cursor Cursor) (*DiffResult[DocumentType], error) {
	all, err := s.inner.queryAllPages(ctx)
	if err != nil {
		return nil, err
	}
	next := NewCursor(cursor.EntityType)
	for _, dt := range all {
		next.Known[dt.ID] = dt.Version
	}
	var deleted []string
	for id := range cursor.Known {
		if _, ok := next.Known[id]; !ok {
			deleted = append(deleted, id)
		}
	}
	return &DiffResult[DocumentType]{Rows: all, DeletedIDs: deleted, TotalRows: len(all), NextCursor: next}, nil
}

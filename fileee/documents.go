package fileee

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// DocumentService kapselt die zentrale Ressource (Umbrella-Spec §3.4). Query/Diff/Get/Update
// nutzen intern restService[Document] (Wiederverwendung des generischen Musters), Upload/
// DownloadPDF/DownloadPageImage (Task 16) sind bewusst eigene, konkrete Methoden.
type DocumentService struct {
	inner  restService[Document]
	client *Client
}

func newDocumentService(c *Client) *DocumentService {
	return &DocumentService{inner: restService[Document]{client: c, resourcePath: "documents"}, client: c}
}

func (s *DocumentService) Query(ctx context.Context, opts QueryOptions) (*QueryResult[Document], error) {
	return s.inner.Query(ctx, opts)
}

func (s *DocumentService) Diff(ctx context.Context, cursor Cursor) (*DiffResult[Document], error) {
	return s.inner.Diff(ctx, cursor)
}

func (s *DocumentService) Get(ctx context.Context, id string) (*Document, error) {
	return s.inner.Get(ctx, id)
}

// Update ändert ein Dokument (API.md §4.1, PUT .../rest/:id, Optimistic-Locking über version —
// ein Versionskonflikt liefert einen serverseitigen Fehlerstatus, der hier unverändert als
// *APIError durchgereicht wird; der exakte Statuscode ist nicht code-belegt, siehe API.md §9 Punkt b).
func (s *DocumentService) Update(ctx context.Context, doc *Document) (*Document, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("fileee: document update encode: %w", err)
	}
	resp, err := s.client.putJSON(ctx, "/api/documents/rest/"+doc.ID, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: document update read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}
	var updated Document
	if err := json.Unmarshal(respBody, &updated); err != nil {
		return nil, fmt.Errorf("fileee: document update decode: %w", err)
	}
	return &updated, nil
}

package fileee

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Share ist eine erzeugte Dokument-Freigabe. Link ist die Freigabe-URL, die der Empfänger öffnet;
// der Zugriff darüber erfordert einen eingeloggten Fileee-Empfänger.
type Share struct {
	Link    string `json:"link"`
	ShareID string `json:"shareId"`
}

// Share erzeugt eine Freigabe für die angegebenen Dokumente und liefert deren Link.
func (s *DocumentService) Share(ctx context.Context, documentIDs []string) (*Share, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	q := url.Values{"documentIds": {strings.Join(documentIDs, ",")}}
	resp, err := s.client.postJSON(ctx, "/api/documents/rest/share?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: share read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}
	var out Share
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("fileee: share decode: %w", err)
	}
	return &out, nil
}

// Unshare hebt die Freigabe eines Dokuments wieder auf.
func (s *DocumentService) Unshare(ctx context.Context, documentID string) error {
	if err := s.client.EnsureSession(ctx); err != nil {
		return err
	}
	resp, err := s.client.postJSON(ctx, "/api/documents/rest/"+documentID+"/unshare", nil)
	if err != nil {
		return err
	}
	return closeAndCheck(resp)
}

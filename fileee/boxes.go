package fileee

import (
	"context"
	"io"
	"net/http"
)

// BoxDocument verweist auf ein in einer FileeeBox abgelegtes Dokument.
type BoxDocument struct {
	DocumentID string `json:"documentId"`
	PageCount  int    `json:"pageCount"`
	Modified   string `json:"modified"`
}

// FileeeBox ist eine physische Ablagebox (mit QR-Code), deren Dokument-Zuordnung digital verwaltet
// wird. BoxNr ist die Nummer, die beim Mail-Upload im Betreff als "@box<N>" referenziert wird.
type FileeeBox struct {
	ID               string        `json:"id"`
	BoxNr            int           `json:"boxNr"`
	BoxName          string        `json:"boxName"`
	QRCode           string        `json:"qrCode"`
	ProductCode      string        `json:"productCode"`
	Documents        []BoxDocument `json:"documents"`
	RemovedDocuments []BoxDocument `json:"removedDocuments"`
	Version          int64         `json:"version"`
	Created          string        `json:"created,omitempty"`
	Modified         string        `json:"modified,omitempty"`
	Deleted          bool          `json:"deleted,omitempty"`
}

// BoxService liest FileeeBoxen und verwaltet ihre Dokument-Zuordnung.
type BoxService interface {
	// List liefert alle FileeeBoxen des Kontos.
	List(ctx context.Context) ([]FileeeBox, error)
	// Get lädt eine einzelne Box anhand ihrer ID.
	Get(ctx context.Context, id string) (*FileeeBox, error)
	// AddDocument heftet ein Dokument in eine Box ein.
	AddDocument(ctx context.Context, boxID, documentID string) error
	// RemoveDocument entfernt ein Dokument aus einer Box.
	RemoveDocument(ctx context.Context, boxID, documentID string) error
}

type boxService struct {
	restService[FileeeBox]
	client *Client
}

func newBoxService(c *Client) BoxService {
	return &boxService{restService: restService[FileeeBox]{client: c, resourcePath: "fileeeboxes"}, client: c}
}

// List liefert alle FileeeBoxen des Kontos.
func (s *boxService) List(ctx context.Context) ([]FileeeBox, error) {
	res, err := s.Diff(ctx, NewCursor("FileeeBox"))
	if err != nil {
		return nil, err
	}
	return res.Rows, nil
}

// documentInBoxPath baut den Pfad für die Dokument-in-Box-Operationen. Er nutzt bewusst NICHT das
// /rest-Präfix.
func documentInBoxPath(boxID, documentID string) string {
	return "/api/fileeeboxes/" + boxID + "/" + documentID
}

// AddDocument heftet ein Dokument in eine Box ein.
func (s *boxService) AddDocument(ctx context.Context, boxID, documentID string) error {
	if err := s.client.EnsureSession(ctx); err != nil {
		return err
	}
	resp, err := s.client.postJSON(ctx, documentInBoxPath(boxID, documentID), nil)
	if err != nil {
		return err
	}
	return closeAndCheck(resp)
}

// RemoveDocument entfernt ein Dokument aus einer Box.
func (s *boxService) RemoveDocument(ctx context.Context, boxID, documentID string) error {
	if err := s.client.EnsureSession(ctx); err != nil {
		return err
	}
	resp, err := s.client.deleteReq(ctx, documentInBoxPath(boxID, documentID))
	if err != nil {
		return err
	}
	return closeAndCheck(resp)
}

// closeAndCheck schließt den Response-Body und übersetzt einen Nicht-200-Status in einen *APIError.
func closeAndCheck(resp *http.Response) error {
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return parseAPIError(resp.StatusCode, body)
	}
	return nil
}

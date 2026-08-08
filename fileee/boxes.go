package fileee

import (
	"context"
	"io"
	"net/http"
)

// BoxDocument verweist auf ein in einer Box abgelegtes Dokument.
type BoxDocument struct {
	DocumentID string `json:"documentId"`
	PageCount  int    `json:"pageCount"`
	Modified   string `json:"modified"`
}

// Box ist eine per QR-Code identifizierte Ablagebox, deren Dokument-Zuordnung digital
// verwaltet wird — entweder das physische fileeeBox-Produkt oder eine selbstgebaute fileeeDIY-Box
// (ProductCode unterscheidet die Variante). BoxNr ist die Nummer, die beim Mail-Upload im Betreff
// als "@box<N>" referenziert wird.
type Box struct {
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

// BoxService liest Boxen und verwaltet ihre Dokument-Zuordnung.
type BoxService interface {
	// List liefert alle Boxen des Kontos.
	List(ctx context.Context) ([]Box, error)
	// Get lädt eine einzelne Box anhand ihrer ID.
	Get(ctx context.Context, id string) (*Box, error)
	// AddDocument heftet ein Dokument in eine Box ein.
	AddDocument(ctx context.Context, boxID, documentID string) error
	// RemoveDocument entfernt ein Dokument aus einer Box.
	RemoveDocument(ctx context.Context, boxID, documentID string) error
}

type boxService struct {
	restService[Box]
	client *Client
}

func newBoxService(c *Client) BoxService {
	return &boxService{restService: restService[Box]{client: c, resourcePath: "fileeeboxes"}, client: c}
}

// List liefert alle Boxen des Kontos.
func (s *boxService) List(ctx context.Context) ([]Box, error) {
	res, err := s.Diff(ctx, NewCursor("Box"))
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

// closeAndCheck schließt den Response-Body und übersetzt einen Status außerhalb von 200/204 in
// einen *APIError (204 zusätzlich zu 200 als Erfolg, seit Task 15s Delete-Methoden — ein
// leerer No-Content-DELETE-Response ist die REST-übliche Erfolgsantwort, API.md dokumentiert für
// die reverse-engineerte Fileee-API keinen festen Statuscode je Endpunkt).
func closeAndCheck(resp *http.Response) error {
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return parseAPIError(resp.StatusCode, body)
	}
	return nil
}

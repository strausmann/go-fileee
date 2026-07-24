package fileee

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// OCRToken ist ein einzelnes vom Fileee-OCR erkanntes Text-Token mit seiner Bounding-Box auf der
// Seite. Eine Seite liefert eine flache Liste solcher Tokens (GET /api/pages/:pageId) — geeignet,
// um Text zu rekonstruieren oder Felder positionsbasiert zu extrahieren (z. B. für eine Migration
// nach Paperless-ngx). Koordinaten sind Seiten-relativ; text ist der erkannte Inhalt.
type OCRToken struct {
	Text     string  `json:"text"`
	WebappID string  `json:"webappId"`
	Left     float64 `json:"left"`
	Top      float64 `json:"top"`
	Right    float64 `json:"right"`
	Bottom   float64 `json:"bottom"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
}

// parseOCR dekodiert die OCR-Antwort (JSON-Array von OCRToken) aus einem Reader.
func parseOCR(r io.Reader) ([]OCRToken, error) {
	var toks []OCRToken
	if err := json.NewDecoder(r).Decode(&toks); err != nil {
		return nil, fmt.Errorf("fileee: ocr decode: %w", err)
	}
	return toks, nil
}

// PageOCR liefert die OCR-Tokens einer Seite eines eigenen Dokuments (authentifiziert, über die
// Session): GET /api/pages/:pageId. Für eine per Share-Link geteilte Seite (ohne Login) siehe
// ShareClient.SharedPageOCR.
func (s *DocumentService) PageOCR(ctx context.Context, pageID string) ([]OCRToken, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	resp, err := s.client.get(ctx, "/api/pages/"+pageID)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, parseAPIError(resp.StatusCode, body)
	}
	return parseOCR(resp.Body)
}

// SharedPageOCR liefert die OCR-Tokens einer Seite eines per Share-Link geteilten Dokuments (anonym,
// ohne Login). shareID (= SharedObject.ID) und sharedByID (= SharedObject.SharedByID) stammen aus
// der Resolve-Antwort. Getipptes Pendant zu DownloadSharedPage (das den Rohstrom liefert).
func (s *ShareClient) SharedPageOCR(ctx context.Context, pageID, shareID, sharedByID string) ([]OCRToken, error) {
	rc, err := s.DownloadSharedPage(ctx, pageID, shareID, sharedByID)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return parseOCR(rc)
}

package fileee

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
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

// Query listet Dokumente über den Query-Endpunkt (paginiert).
func (s *DocumentService) Query(ctx context.Context, opts QueryOptions) (*QueryResult[Document], error) {
	return s.inner.Query(ctx, opts)
}

// Diff liefert die Änderungen seit dem übergebenen Cursor.
func (s *DocumentService) Diff(ctx context.Context, cursor Cursor) (*DiffResult[Document], error) {
	return s.inner.Diff(ctx, cursor)
}

// Get lädt ein einzelnes Dokument anhand seiner ID.
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

// Delete löscht ein Dokument unwiderruflich (Hard-DELETE, DELETE /api/documents/rest/:id) — es
// gibt serverseitig keinen Papierkorb/Undo für diese Operation. Die Lib bietet Delete bewusst als
// geguardete Opt-in-Methode an (ADR-0007/ADR-0008); der fileee-server registriert die zugehörige
// HTTP-Route nur, wenn beim Start FILEEE_ALLOW_DESTRUCTIVE gesetzt ist. Ein fehlendes Dokument
// liefert ErrNotFound (per errors.Is prüfbar), jeder andere Fehlerstatus ein *APIError.
func (s *DocumentService) Delete(ctx context.Context, id string) error {
	return s.inner.delete(ctx, id)
}

// UploadMetadata steuert Document.Upload (API.md §4.1).
type UploadMetadata struct {
	Title    string
	Document *Document
}

func (m UploadMetadata) filename() string {
	if m.Title != "" {
		return m.Title
	}
	return "upload"
}

// UploadResult meldet, ob der Server ein bereits existierendes Dokument erkannt hat
// (zurückgegebene id weicht von der gesendeten Client-id ab, API.md §4.1 "Serverseitige
// Duplikaterkennung"). Bei einem Duplikat liefert Upload zusätzlich ErrDuplicateDocument.
type UploadResult struct {
	Document    *Document
	IsDuplicate bool
}

// Upload lädt eine neue Datei hoch (POST /api/documents/rest, multipart, API.md §4.1). Die id ist
// CLIENT-generiert (newObjectID) und wird IMMER mitgeschickt — weicht die vom Server
// zurückgegebene id davon ab, hat der Server ein bereits existierendes Dokument erkannt
// (UploadResult.IsDuplicate).
func (s *DocumentService) Upload(ctx context.Context, r io.Reader, meta UploadMetadata) (*UploadResult, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	clientID, err := newObjectID()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", meta.filename())
	if err != nil {
		return nil, fmt.Errorf("fileee: multipart Datei-Feld: %w", err)
	}
	if _, err := io.Copy(fw, r); err != nil {
		return nil, fmt.Errorf("fileee: multipart Kopie: %w", err)
	}
	if err := mw.WriteField("id", clientID); err != nil {
		return nil, fmt.Errorf("fileee: multipart id-Feld: %w", err)
	}
	if meta.Document != nil {
		docJSON, err := json.Marshal(meta.Document)
		if err != nil {
			return nil, fmt.Errorf("fileee: Dokument-Metadaten kodieren: %w", err)
		}
		if err := mw.WriteField("document", string(docJSON)); err != nil {
			return nil, fmt.Errorf("fileee: multipart document-Feld: %w", err)
		}
	} else {
		if err := mw.WriteField("attributes.data.title.value", meta.filename()); err != nil {
			return nil, fmt.Errorf("fileee: multipart title-Feld: %w", err)
		}
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("fileee: multipart schließen: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.client.baseURL+"/api/documents/rest", &buf)
	if err != nil {
		return nil, fmt.Errorf("fileee: Upload-Request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := s.client.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fileee: Upload: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: Upload-Antwort lesen: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}
	var doc Document
	if err := json.Unmarshal(respBody, &doc); err != nil {
		return nil, fmt.Errorf("fileee: Upload-Antwort dekodieren: %w", err)
	}
	if doc.ID != clientID {
		// Server hat ein bestehendes Dokument erkannt: Result befüllen UND Fehler liefern, damit ein
		// Aufrufer, der nur err prüft, nicht unbemerkt auf einem BESTEHENDEN Dokument weiterarbeitet.
		return &UploadResult{Document: &doc, IsDuplicate: true}, ErrDuplicateDocument
	}
	return &UploadResult{Document: &doc, IsDuplicate: false}, nil
}

// DownloadPDF liefert das Original-PDF (GET /api/v1/documents/:id/pdf?mode=..., API.md §4.1) —
// der primäre Download-Weg (Umbrella-Spec §6.3).
func (s *DocumentService) DownloadPDF(ctx context.Context, id string, mode PDFMode) (io.ReadCloser, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/api/v1/documents/%s/pdf?mode=%s", s.client.baseURL, id, mode)
	return s.downloadBinary(ctx, u)
}

// DownloadPageImage ist der Fallback, falls kein PDF verfügbar ist (GET
// /api/v1/pages/:id/image?size=...&version=..., API.md §4.1). version MUSS immer frisch aus dem
// zuletzt geladenen pages[]-Array kommen (Skill-Troubleshooting) und darf nicht zwischengespeichert
// werden.
func (s *DocumentService) DownloadPageImage(ctx context.Context, pageID string, size ImageSize, version int64) (io.ReadCloser, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/api/v1/pages/%s/image?size=%s&version=%d", s.client.baseURL, pageID, size, version)
	return s.downloadBinary(ctx, u)
}

// downloadBinary führt den GET aus und liefert den Body als ReadCloser (die aufrufende Seite MUSS
// ihn schließen) — 404 wird als ErrNotFound gemeldet, jeder andere Fehlerstatus als *APIError.
func (s *DocumentService) downloadBinary(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("fileee: Download-Request: %w", err)
	}
	resp, err := s.client.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fileee: Download: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, parseAPIError(resp.StatusCode, body)
	}
	return resp.Body, nil
}

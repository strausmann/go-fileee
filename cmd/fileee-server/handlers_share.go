package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/strausmann/go-fileee/fileee"
)

// registerShareRoutes registriert die Freigabe- und Prozess-Operationen (Task 8, Design-Spec
// §4.2/§4.4, docs/superpowers/specs/2026-07-24-fileee-server-design.md im homelab-management-Repo):
// Freigabe erzeugen/widerrufen sowie den asynchronen Prozess-Poll/Wait-Mechanismus, den u.a.
// POST /v1/documents/export-zip (handlers_documents.go) nutzt. Jeder Handler delegiert direkt an
// s.fc und übersetzt Lib-Fehler ausschließlich über mapError (errors.go).
func (s *Server) registerShareRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "share-documents",
		Method:      http.MethodPost,
		Path:        "/v1/share",
		Summary:     "Freigabe für Dokumente erzeugen",
	}, s.handleShareDocuments)

	huma.Register(api, huma.Operation{
		OperationID: "unshare-document",
		Method:      http.MethodPost,
		Path:        "/v1/documents/{id}/unshare",
		Summary:     "Freigabe eines Dokuments widerrufen",
	}, s.handleUnshareDocument)

	huma.Register(api, huma.Operation{
		OperationID: "get-process",
		Method:      http.MethodGet,
		Path:        "/v1/processes/{id}",
		Summary:     "Status eines asynchronen Vorgangs abfragen (Poll)",
	}, s.handleGetProcess)

	huma.Register(api, huma.Operation{
		OperationID: "wait-process",
		Method:      http.MethodPost,
		Path:        "/v1/processes/{id}/wait",
		Summary:     "Synchron auf einen asynchronen Vorgang warten (Timeout gedeckelt auf cfg.WaitMax)",
	}, s.handleWaitProcess)
}

// shareDocumentsRequest ist der Body von POST /v1/share (Design-Spec §4.2:
// "POST /v1/share {documentIds}").
type shareDocumentsRequest struct {
	DocumentIDs []string `json:"documentIds" doc:"IDs der freizugebenden Dokumente."`
}

// shareDocumentsInput steuert POST /v1/share.
type shareDocumentsInput struct {
	Body shareDocumentsRequest
}

// shareDocumentsOutput ist der Response-Body von POST /v1/share: fileee.Share trägt bereits die
// vom Design-Spec geforderten Felder link/shareId (json:"link"/"shareId", siehe fileee/share.go) —
// kein eigenes Wire-Mapping nötig.
type shareDocumentsOutput struct {
	Body fileee.Share
}

// handleShareDocuments implementiert POST /v1/share — dünner Durchgriff auf Documents.Share.
func (s *Server) handleShareDocuments(ctx context.Context, in *shareDocumentsInput) (*shareDocumentsOutput, error) {
	share, err := s.fc.Documents.Share(ctx, in.Body.DocumentIDs)
	if err != nil {
		return nil, mapError(err)
	}
	return &shareDocumentsOutput{Body: *share}, nil
}

// unshareDocumentInput steuert POST /v1/documents/{id}/unshare.
type unshareDocumentInput struct {
	ID string `path:"id" doc:"Dokument-ID, deren Freigabe aufgehoben wird."`
}

// handleUnshareDocument implementiert POST /v1/documents/{id}/unshare — dünner Durchgriff auf
// Documents.Unshare. Kein Response-Body (204 No Content); das Widerrufen einer Freigabe verliert
// keine Daten (Design-Spec §4.2 "Freigabe widerrufen, kein Datenverlust").
func (s *Server) handleUnshareDocument(ctx context.Context, in *unshareDocumentInput) (*struct{}, error) {
	if err := s.fc.Documents.Unshare(ctx, in.ID); err != nil {
		return nil, mapError(err)
	}
	return nil, nil
}

// getProcessInput steuert GET /v1/processes/{id}.
type getProcessInput struct {
	ID string `path:"id" doc:"Prozess-ID."`
}

// processOutput ist der gemeinsame Response-Body von GET /v1/processes/{id} und
// POST /v1/processes/{id}/wait — beide liefern denselben fileee.Process-Snapshot.
type processOutput struct {
	Body fileee.Process
}

// handleGetProcess implementiert GET /v1/processes/{id} — dünner Durchgriff auf Processes.Get
// (einmaliger Poll, im Gegensatz zum blockierenden handleWaitProcess).
func (s *Server) handleGetProcess(ctx context.Context, in *getProcessInput) (*processOutput, error) {
	proc, err := s.fc.Processes.Get(ctx, in.ID)
	if err != nil {
		return nil, mapError(err)
	}
	return &processOutput{Body: *proc}, nil
}

// waitProcessInput steuert POST /v1/processes/{id}/wait. Timeout bleibt bewusst ein roher String
// statt eines getippten time.Duration-Query-Parameters — huma@v2.35.0 kennt keine eingebaute
// Duration-Parsing-Unterstützung für Query-Parameter (parseInto, huma.go), der Handler parst daher
// selbst per time.ParseDuration (Design-Spec §4.4 "Query ?timeout=<dauer>").
type waitProcessInput struct {
	ID      string `path:"id" doc:"Prozess-ID."`
	Timeout string `query:"timeout" doc:"Maximale Wartezeit dieses Aufrufs (Go-Duration-Syntax, z. B. \"30s\"). Leer = Server-Default (FILEEE_WAIT_TIMEOUT); wird IMMER auf FILEEE_WAIT_MAX gedeckelt."`
}

// handleWaitProcess implementiert POST /v1/processes/{id}/wait (Design-Spec §4.4): das
// angeforderte Timeout wird auf cfg.WaitMax gedeckelt (min(parsedTimeout, cfg.WaitMax)), fehlt es,
// gilt cfg.WaitTimeout als Default. WaitForProcess pollt bis der Vorgang terminal ist oder der
// abgeleitete Context abläuft — läuft die Deadline zuerst ab (ctx.Err() == context.DeadlineExceeded,
// fileee/export.go WaitForProcess), ist das laut Spec KEIN Fehler: der Handler pollt EINMALIG mit
// dem URSPRÜNGLICHEN (nicht abgelaufenen) Request-Context nach und liefert 200 mit dem aktuellen
// (nicht-terminalen) Status, damit der Client weiterpollen kann. waitCtx selbst ist zu diesem
// Zeitpunkt bereits abgelaufen und für einen weiteren Fileee-Request unbrauchbar — deshalb
// verwendet der Nachpoll bewusst ctx (den Handler-Parameter), nicht waitCtx.
func (s *Server) handleWaitProcess(ctx context.Context, in *waitProcessInput) (*processOutput, error) {
	effective := s.cfg.WaitTimeout
	if in.Timeout != "" {
		d, err := time.ParseDuration(in.Timeout)
		if err != nil {
			return nil, newStatusError(http.StatusBadRequest, "invalid_timeout", "invalid timeout parameter")
		}
		effective = d
	}
	if s.cfg.WaitMax > 0 && effective > s.cfg.WaitMax {
		effective = s.cfg.WaitMax
	}

	waitCtx, cancel := context.WithTimeout(ctx, effective)
	defer cancel()

	proc, err := s.fc.WaitForProcess(waitCtx, in.ID, fileee.WaitOptions{})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			current, getErr := s.fc.Processes.Get(ctx, in.ID)
			if getErr != nil {
				return nil, mapError(getErr)
			}
			return &processOutput{Body: *current}, nil
		}
		return nil, mapError(err)
	}
	return &processOutput{Body: *proc}, nil
}

// ---------------------------------------------------------------------------
// Task 9: Anonymer Share-Proxy
// ---------------------------------------------------------------------------

// registerShareProxyRoutes registriert die anonymen, credential-losen Share-Proxy-Operationen
// (Task 9, Design-Spec §4.1 Share-Proxy-Routen, docs/superpowers/specs/2026-07-24-fileee-server-design.md
// im homelab-management-Repo): einen Empfänger, der nur den Freigabe-Token besitzt (z. B. ein N8N-
// Workflow), kann damit den Token auflösen sowie Seitenbild, OCR-Tokens und das Voll-PDF eines
// geteilten Dokuments abrufen — OHNE einen Fileee-Login. Jeder Handler delegiert direkt an s.sc
// (den credential-losen fileee.ShareClient, siehe server.go) statt an s.fc, und übersetzt
// Lib-Fehler ausschließlich über mapError (errors.go) — dieselbe Fehler-Übersetzung wie bei den
// authentifizierten Routen, da fileee.ShareClient dieselben Sentinel-Fehler/*fileee.APIError
// zurückgibt wie fileee.Client (fileee/errors.go gilt paketweit, nicht clientspezifisch).
//
// Zwei der vier Operationen (OCR, PDF) brauchen intern EINEN zusätzlichen Resolve-Aufruf: Anders
// als DownloadPageImage (das einen eigenen Token-Endpunkt kennt, "GET /api/v1/sharing/:token/:pageId")
// verlangen SharedPageOCR und DownloadSharedPDF die shareId/sharedById aus der Resolve-Antwort
// (fileee.SharedObject.ID/SharedByID) statt des rohen Tokens — das ist eine Eigenheit der
// zugrunde liegenden Fileee-API (fileee/shareclient.go-Doku), kein Design dieses Servers.
func (s *Server) registerShareProxyRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "resolve-share",
		Method:      http.MethodPost,
		Path:        "/v1/share-objects/{token}",
		Summary:     "Freigabe-Token auflösen (anonym, ohne Fileee-Login)",
	}, s.handleResolveShare)

	huma.Register(api, huma.Operation{
		OperationID: "download-shared-page-image",
		Method:      http.MethodGet,
		Path:        "/v1/share-objects/{token}/pages/{pageId}/image",
		Summary:     "Seitenbild eines geteilten Dokuments herunterladen (anonym, Stream)",
	}, s.handleDownloadSharedPageImage)

	huma.Register(api, huma.Operation{
		OperationID: "get-shared-page-ocr",
		Method:      http.MethodGet,
		Path:        "/v1/share-objects/{token}/pages/{pageId}/ocr",
		Summary:     "OCR-Tokens einer Seite eines geteilten Dokuments laden (anonym)",
	}, s.handleGetSharedPageOCR)

	huma.Register(api, huma.Operation{
		OperationID: "download-shared-document-pdf",
		Method:      http.MethodGet,
		Path:        "/v1/share-objects/{token}/documents/{docId}/pdf",
		Summary:     "Voll-PDF eines geteilten Dokuments herunterladen (anonym, vom Static-Host, Stream)",
	}, s.handleDownloadSharedDocumentPDF)
}

// resolveShareInput steuert POST /v1/share-objects/{token}.
type resolveShareInput struct {
	Token string `path:"token" doc:"Freigabe-Token (aus dem Share-Link, z. B. https://my.fileee.com/shared/<token>)."`
}

// resolveShareOutput ist der Response-Body von POST /v1/share-objects/{token}: fileee.SharedObject
// trägt bereits die vom Design-Spec geforderten Felder (sharedBy/sharedById/documents,
// fileee/shareclient.go) — kein eigenes Wire-Mapping nötig.
type resolveShareOutput struct {
	Body fileee.SharedObject
}

// handleResolveShare implementiert POST /v1/share-objects/{token} — dünner Durchgriff auf
// ShareClient.Resolve. Kein Fileee-Login nötig: der Handler nutzt s.sc statt s.fc.
func (s *Server) handleResolveShare(ctx context.Context, in *resolveShareInput) (*resolveShareOutput, error) {
	obj, err := s.sc.Resolve(ctx, in.Token)
	if err != nil {
		return nil, mapError(err)
	}
	return &resolveShareOutput{Body: *obj}, nil
}

// downloadSharedPageImageInput steuert GET /v1/share-objects/{token}/pages/{pageId}/image.
type downloadSharedPageImageInput struct {
	Token  string           `path:"token" doc:"Freigabe-Token."`
	PageID string           `path:"pageId" doc:"Seiten-ID (aus SharedDocument.PageIDs einer vorherigen Resolve-Antwort)."`
	Size   fileee.ImageSize `query:"size" doc:"Bildgröße (smedium/medium)." default:"medium"`
}

// handleDownloadSharedPageImage implementiert GET /v1/share-objects/{token}/pages/{pageId}/image
// als Stream (analog handleDownloadPageImage, handlers_documents.go, aber anonym über s.sc statt
// s.fc). Braucht KEINEN vorherigen Resolve-Aufruf — ShareClient.DownloadPageImage nimmt Token und
// pageId direkt entgegen (eigener Token-Endpunkt "GET /api/v1/sharing/:token/:pageId").
func (s *Server) handleDownloadSharedPageImage(ctx context.Context, in *downloadSharedPageImageInput) (*huma.StreamResponse, error) {
	size := in.Size
	if size == "" {
		size = fileee.ImageSizeMedium
	}
	rc, err := s.sc.DownloadPageImage(ctx, in.Token, in.PageID, size)
	if err != nil {
		return nil, mapError(err)
	}
	return &huma.StreamResponse{
		Body: func(sctx huma.Context) {
			defer rc.Close()
			sctx.SetHeader("Content-Type", "image/jpeg")
			if _, err := io.Copy(sctx.BodyWriter(), rc); err != nil {
				s.log.Error("shared page-image stream copy fehlgeschlagen", "token", in.Token, "page_id", in.PageID, "error", err)
			}
		},
	}, nil
}

// getSharedPageOCRInput steuert GET /v1/share-objects/{token}/pages/{pageId}/ocr.
type getSharedPageOCRInput struct {
	Token  string `path:"token" doc:"Freigabe-Token."`
	PageID string `path:"pageId" doc:"Seiten-ID (aus SharedDocument.PageIDs einer vorherigen Resolve-Antwort)."`
}

// getSharedPageOCROutput ist der Response-Body von GET /v1/share-objects/{token}/pages/{pageId}/ocr:
// die flache Liste der erkannten Text-Tokens mit Bounding-Box (fileee.OCRToken, analog
// getPageOCROutput, handlers_documents.go).
type getSharedPageOCROutput struct {
	Body []fileee.OCRToken
}

// handleGetSharedPageOCR implementiert GET /v1/share-objects/{token}/pages/{pageId}/ocr. Anders
// als handleDownloadSharedPageImage braucht dieser Endpunkt EINEN vorgeschalteten Resolve-Aufruf:
// ShareClient.SharedPageOCR verlangt shareId/sharedById (fileee.SharedObject.ID/SharedByID) statt
// des rohen Tokens (fileee/ocr.go-Doku).
func (s *Server) handleGetSharedPageOCR(ctx context.Context, in *getSharedPageOCRInput) (*getSharedPageOCROutput, error) {
	obj, err := s.sc.Resolve(ctx, in.Token)
	if err != nil {
		return nil, mapError(err)
	}
	toks, err := s.sc.SharedPageOCR(ctx, in.PageID, obj.ID, obj.SharedByID)
	if err != nil {
		return nil, mapError(err)
	}
	return &getSharedPageOCROutput{Body: toks}, nil
}

// downloadSharedDocumentPDFInput steuert GET /v1/share-objects/{token}/documents/{docId}/pdf.
type downloadSharedDocumentPDFInput struct {
	Token string         `path:"token" doc:"Freigabe-Token."`
	DocID string         `path:"docId" doc:"Dokument-ID (fileee.SharedDocument.ID aus einer vorherigen Resolve-Antwort)."`
	Mode  fileee.PDFMode `query:"mode" doc:"download (Originaldatei) oder print (druckoptimierte Fassung)." default:"download"`
}

// handleDownloadSharedDocumentPDF implementiert GET /v1/share-objects/{token}/documents/{docId}/pdf
// als Stream (analog handleDownloadDocumentPDF, handlers_documents.go, aber anonym über s.sc statt
// s.fc). Braucht EINEN vorgeschalteten Resolve-Aufruf: ShareClient.DownloadSharedPDF verlangt die
// shareId (fileee.SharedObject.ID) statt des rohen Tokens, UND das PDF liegt auf dem Static-Host
// (fileee/shareclient.go DownloadSharedPDF-Doku), nicht auf dem API-Host — beides erledigt s.sc
// intern, der Handler selbst kennt den Static-Host nicht.
func (s *Server) handleDownloadSharedDocumentPDF(ctx context.Context, in *downloadSharedDocumentPDFInput) (*huma.StreamResponse, error) {
	mode := in.Mode
	if mode == "" {
		mode = fileee.PDFModeDownload
	}
	obj, err := s.sc.Resolve(ctx, in.Token)
	if err != nil {
		return nil, mapError(err)
	}
	rc, err := s.sc.DownloadSharedPDF(ctx, obj.ID, in.DocID, mode)
	if err != nil {
		return nil, mapError(err)
	}
	return &huma.StreamResponse{
		Body: func(sctx huma.Context) {
			defer rc.Close()
			sctx.SetHeader("Content-Type", "application/pdf")
			if _, err := io.Copy(sctx.BodyWriter(), rc); err != nil {
				s.log.Error("shared pdf stream copy fehlgeschlagen", "token", in.Token, "doc_id", in.DocID, "error", err)
			}
		},
	}, nil
}

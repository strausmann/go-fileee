package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/strausmann/go-fileee/fileee"
)

// defaultDocumentListLimit ist das Default-Limit für GET /v1/documents, wenn der Aufrufer keinen
// (oder einen nicht-positiven) `limit`-Query-Parameter mitschickt. Bewusst ein eigener,
// server-lokaler Wert statt der unexportierten `fileee`-internen Konstante (die Lib exportiert
// ihr Default-Page-Limit nicht) — beide sind zufällig identisch (100), das ist aber keine
// Voraussetzung, nur eine sinnvolle Übereinstimmung.
const defaultDocumentListLimit = 100

// documentCursorEntityType ist der EntityType-Diskriminator, den ein leerer `cursor`-Parameter für
// GET /v1/documents initialisiert (fileee.NewCursor-Konvention, siehe fileee/query.go). Der Wert
// ist reine Client-Bookkeeping-Metadatik der Lib (fließt in keine Diff-Logik ein) und wird beim
// Roundtrip über encodeCursor/decodeCursor unverändert mitgeführt.
const documentCursorEntityType = "Document"

// uploadSizeLimit begrenzt den Request-Body von POST /v1/documents auf maxBytes
// (FILEEE_MAX_UPLOAD_SIZE, Config.MaxUploadBytes) — siehe Verdrahtung + Begründung in
// server.go Handler(). maxBytes <= 0 deaktiviert das Limit (kein sinnvoller Konfigurationswert,
// aber defensiv statt eines Panics/Endlos-Limits). http.MaxBytesReader lässt einen nachfolgenden
// r.ParseMultipartForm (huma@v2.35.0 adapters/humago GetMultipartForm) mit
// "http: request body too large" abbrechen, sobald die Grenze überschritten wird.
func uploadSizeLimit(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if maxBytes > 0 && r.Method == http.MethodPost && r.URL.Path == "/v1/documents" {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// registerDocumentRoutes registriert alle Dokument-/Seiten-bezogenen Operationen (Task 7 Read +
// Task 8 Write, Design-Spec §4.1/§4.2, docs/superpowers/specs/2026-07-24-fileee-server-design.md
// im homelab-management-Repo) auf der übergebenen Huma-API: Liste/Suche, Einzelabruf, PDF-/Bild-
// Stream, OCR-Tokens, Upload, Update und ZIP-Export. Jeder Handler delegiert direkt an s.fc
// (Core-Lib) und übersetzt Lib-Fehler ausschließlich über mapError (errors.go) — Ausnahme ist der
// Upload-Duplikat-Fall (uploadDuplicateError), der laut Design-Spec §12 zusätzliche Felder (id,
// isDuplicate) braucht, die das generische {error,code}-Schema nicht abdeckt.
func (s *Server) registerDocumentRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-documents",
		Method:      http.MethodGet,
		Path:        "/v1/documents",
		Summary:     "Dokumente auflisten oder per Volltextsuche durchsuchen",
	}, s.handleListDocuments)

	huma.Register(api, huma.Operation{
		OperationID: "upload-document",
		Method:      http.MethodPost,
		Path:        "/v1/documents",
		Summary:     "Neues Dokument hochladen (multipart)",
	}, s.handleUploadDocument)

	huma.Register(api, huma.Operation{
		OperationID: "get-document",
		Method:      http.MethodGet,
		Path:        "/v1/documents/{id}",
		Summary:     "Einzelnes Dokument laden",
	}, s.handleGetDocument)

	huma.Register(api, huma.Operation{
		OperationID: "update-document",
		Method:      http.MethodPut,
		Path:        "/v1/documents/{id}",
		Summary:     "Dokument-Metadaten aktualisieren",
		// SkipValidateBody: fileee.Document ist ein Wire-Typ der Core-Lib OHNE omitempty-Tags
		// (er MUSS 1:1 mit der Fileee-Antwort roundtripen können, siehe MarshalJSON/UnmarshalJSON
		// in fileee/types.go). Humas Default-Schemagenerierung markiert deshalb JEDES Feld ohne
		// omitempty als required — ein Aufrufer, der nur geänderte Metadaten schickt (der
		// eigentliche Zweck von PUT), würde an dieser Fremd-Validierung scheitern, obwohl
		// Documents.Update selbst kein vollständiges Objekt verlangt. Die Core-Lib bleibt trotzdem
		// die einzige Validierungsinstanz (serverseitiges Optimistic-Locking über version).
		SkipValidateBody: true,
	}, s.handleUpdateDocument)

	huma.Register(api, huma.Operation{
		OperationID: "download-document-pdf",
		Method:      http.MethodGet,
		Path:        "/v1/documents/{id}/pdf",
		Summary:     "Original-PDF eines Dokuments herunterladen (Stream)",
	}, s.handleDownloadDocumentPDF)

	huma.Register(api, huma.Operation{
		OperationID: "download-page-image",
		Method:      http.MethodGet,
		Path:        "/v1/pages/{pageId}/image",
		Summary:     "Seitenbild herunterladen (Fallback ohne PDF, Stream)",
	}, s.handleDownloadPageImage)

	huma.Register(api, huma.Operation{
		OperationID: "get-page-ocr",
		Method:      http.MethodGet,
		Path:        "/v1/pages/{pageId}/ocr",
		Summary:     "OCR-Tokens einer Seite laden",
	}, s.handleGetPageOCR)

	huma.Register(api, huma.Operation{
		OperationID: "export-documents-zip",
		Method:      http.MethodPost,
		Path:        "/v1/documents/export-zip",
		Summary:     "Passwortgeschützten ZIP-Export starten (asynchroner Process)",
	}, s.handleExportZip)
}

// listDocumentsInput steuert GET /v1/documents. Ist Query gesetzt, läuft der Such-Zweig
// (Documents.Search + Get-Hydration je Treffer — Search liefert laut fileee/search.go nur
// Dokument-IDs, Design-Spec §17 "API/Code"); ist Query leer, läuft der Diff-Zweig (zustandsloser
// Cursor-Sync über Documents.Diff). Beide Zweige teilen sich Limit.
type listDocumentsInput struct {
	Query  string `query:"query" doc:"Volltextsuche (FULLTEXT/FUZZY über Documents.Search). Gesetzt aktiviert den Suchmodus statt des Diff-Modus."`
	Limit  int    `query:"limit" doc:"Max. Anzahl Ergebnisse dieser Seite/dieses Suchlaufs. Wirkt nur im Suchmodus (query gesetzt); im Diff-Modus (leeres query) wird es ignoriert." default:"100"`
	Cursor string `query:"cursor" doc:"Opaques Cursor-Token aus einer vorigen Antwort dieses Endpunkts (nur im Diff-Modus relevant, d.h. wenn query leer ist). Leer = kompletter Sync von vorn."`
}

// documentListBody ist der gemeinsame Response-Body von GET /v1/documents für beide Modi
// (Design-Spec §17: einheitlicher Output {items, cursor, totalRows}). Cursor bleibt im Suchmodus
// leer — Documents.Search kennt keinen Diff-Cursor, nur eine Start/Limit-Pagination.
type documentListBody struct {
	Items     []fileee.Document `json:"items" doc:"Dokumente dieser Seite bzw. dieses Suchlaufs."`
	Cursor    string            `json:"cursor" doc:"Opaques Folge-Cursor-Token für den nächsten Diff-Aufruf (leer im Suchmodus)."`
	TotalRows int               `json:"totalRows" doc:"Von Fileee gemeldete Gesamtzahl (Suchtreffer bzw. Diff-Zeilen)."`
}

// listDocumentsOutput kapselt documentListBody als Huma-Response von GET /v1/documents.
type listDocumentsOutput struct {
	Body documentListBody
}

// handleListDocuments implementiert GET /v1/documents. Ist Query gesetzt, sucht sie per
// Documents.Search (liefert nur Treffer-IDs) und hydriert jeden Treffer per Documents.Get zum
// vollen Dokument (N+1-Zugriffsmuster — bewusst in Kauf genommen, siehe Search-Dokumentation:
// Details kommen bei dieser Fileee-API-Facette ausschließlich über Get). Ohne Query synchronisiert
// sie inkrementell über Documents.Diff mit einem aus dem `cursor`-Parameter dekodierten
// fileee.Cursor und liefert den codierten Folge-Cursor zurück.
func (s *Server) handleListDocuments(ctx context.Context, in *listDocumentsInput) (*listDocumentsOutput, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = defaultDocumentListLimit
	}

	if in.Query != "" {
		res, err := s.fc.Documents.Search(ctx, in.Query, fileee.SearchOptions{Limit: limit})
		if err != nil {
			return nil, mapError(err)
		}
		items := make([]fileee.Document, 0, len(res.IDs))
		for _, id := range res.IDs {
			doc, err := s.fc.Documents.Get(ctx, id)
			if err != nil {
				return nil, mapError(err)
			}
			items = append(items, *doc)
		}
		return &listDocumentsOutput{Body: documentListBody{Items: items, TotalRows: res.TotalRows}}, nil
	}

	cursor, err := decodeCursor(in.Cursor)
	if err != nil {
		return nil, newStatusError(http.StatusBadRequest, "invalid_cursor", "invalid cursor parameter")
	}
	diff, err := s.fc.Documents.Diff(ctx, cursor)
	if err != nil {
		return nil, mapError(err)
	}
	nextCursor, err := encodeCursor(diff.NextCursor)
	if err != nil {
		return nil, mapError(err)
	}
	return &listDocumentsOutput{Body: documentListBody{Items: diff.Rows, Cursor: nextCursor, TotalRows: diff.TotalRows}}, nil
}

// encodeCursor verpackt einen Lib-Cursor (fileee.Cursor) als opakes Web-Token: JSON-Serialisierung,
// anschließend Base64-URL ohne Padding. Aufrufer MÜSSEN das Ergebnis als Blackbox behandeln (siehe
// documentListBody.Cursor) — der Server ist der einzige Ort, der es je wieder decodeCursor
// übergibt.
func encodeCursor(c fileee.Cursor) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("cursor kodieren: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// decodeCursor entpackt ein von encodeCursor erzeugtes Cursor-Token. Ein leerer String liefert
// einen frischen Cursor (documentCursorEntityType, leeres Known) für einen vollständigen Sync von
// vorn — das ist der Normalfall beim allerersten Aufruf ohne `cursor`-Parameter.
func decodeCursor(s string) (fileee.Cursor, error) {
	if s == "" {
		return fileee.NewCursor(documentCursorEntityType), nil
	}
	return decodeCursorToken(s)
}

// decodeCursorToken dekodiert einen NICHT-leeren, von encodeCursor erzeugten Cursor-Token
// (Base64-URL ohne Padding, anschließend JSON) — der ressourcen-unabhängige Kern von decodeCursor.
// Ausgelagert (Task 11), damit decodeConversationsCursor (handlers_conversations.go) dieselbe
// Dekodierung mit einem anderen Default-EntityType ("Conversation" statt "Document") wiederverwenden
// kann, ohne die Base64/JSON-Logik zu duplizieren.
func decodeCursorToken(s string) (fileee.Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return fileee.Cursor{}, fmt.Errorf("cursor dekodieren: %w", err)
	}
	var c fileee.Cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return fileee.Cursor{}, fmt.Errorf("cursor dekodieren: %w", err)
	}
	return c, nil
}

// getDocumentInput steuert GET /v1/documents/{id}.
type getDocumentInput struct {
	ID string `path:"id" doc:"Dokument-ID."`
}

// getDocumentOutput ist der Response-Body von GET /v1/documents/{id}: das vollständige
// fileee.Document (inkl. Attributes/Pages).
type getDocumentOutput struct {
	Body fileee.Document
}

// handleGetDocument implementiert GET /v1/documents/{id} — dünner Durchgriff auf Documents.Get.
func (s *Server) handleGetDocument(ctx context.Context, in *getDocumentInput) (*getDocumentOutput, error) {
	doc, err := s.fc.Documents.Get(ctx, in.ID)
	if err != nil {
		return nil, mapError(err)
	}
	return &getDocumentOutput{Body: *doc}, nil
}

// downloadDocumentPDFInput steuert GET /v1/documents/{id}/pdf.
type downloadDocumentPDFInput struct {
	ID   string         `path:"id" doc:"Dokument-ID."`
	Mode fileee.PDFMode `query:"mode" doc:"download (Originaldatei) oder print (druckoptimierte Fassung)." default:"download"`
}

// handleDownloadDocumentPDF implementiert GET /v1/documents/{id}/pdf als Stream — der PDF-Body
// wird NIE vollständig in den RAM gepuffert: io.Copy kopiert direkt vom Lib-eigenen
// io.ReadCloser (Documents.DownloadPDF) auf den Huma-BodyWriter (Design-Spec §13
// "Streaming-Download ohne RAM-Puffer").
func (s *Server) handleDownloadDocumentPDF(ctx context.Context, in *downloadDocumentPDFInput) (*huma.StreamResponse, error) {
	mode := in.Mode
	if mode == "" {
		mode = fileee.PDFModeDownload
	}
	rc, err := s.fc.Documents.DownloadPDF(ctx, in.ID, mode)
	if err != nil {
		return nil, mapError(err)
	}
	return &huma.StreamResponse{
		Body: func(sctx huma.Context) {
			defer rc.Close()
			sctx.SetHeader("Content-Type", "application/pdf")
			if _, err := io.Copy(sctx.BodyWriter(), rc); err != nil {
				s.log.Error("pdf stream copy fehlgeschlagen", "document_id", in.ID, "error", err)
			}
		},
	}, nil
}

// downloadPageImageInput steuert GET /v1/pages/{pageId}/image. Version wird 1:1 an
// Documents.DownloadPageImage durchgereicht (die Lib-Signatur verlangt sie zwingend) — der
// Aufrufer MUSS den zuletzt aus dem übergeordneten Dokument gelesenen Wert mitschicken
// (Document.Pages[i].ImageVersion), NIE einen zwischengespeicherten (siehe Page-Doku in
// fileee/types.go). Dieser dünne Passthrough-Endpunkt selbst prüft die Frische NICHT — ein
// künftiger Unified-Resolver (Design-Spec §4.1a, spätere Task) kann diesen Wert serverseitig
// vorab aus einem frisch geladenen Dokument beziehen und braucht dafür keine Änderung an dieser
// Route.
type downloadPageImageInput struct {
	PageID  string           `path:"pageId" doc:"Seiten-ID."`
	Size    fileee.ImageSize `query:"size" doc:"Bildgröße (smedium/medium)." default:"medium"`
	Version int64            `query:"v" doc:"Aktuelle imageVersion der Seite (aus Document.Pages), NIE zwischenspeichern."`
}

// handleDownloadPageImage implementiert GET /v1/pages/{pageId}/image als Stream (Fallback-Weg
// ohne PDF, analog handleDownloadDocumentPDF ohne RAM-Puffer).
func (s *Server) handleDownloadPageImage(ctx context.Context, in *downloadPageImageInput) (*huma.StreamResponse, error) {
	size := in.Size
	if size == "" {
		size = fileee.ImageSizeMedium
	}
	rc, err := s.fc.Documents.DownloadPageImage(ctx, in.PageID, size, in.Version)
	if err != nil {
		return nil, mapError(err)
	}
	return &huma.StreamResponse{
		Body: func(sctx huma.Context) {
			defer rc.Close()
			sctx.SetHeader("Content-Type", "image/jpeg")
			if _, err := io.Copy(sctx.BodyWriter(), rc); err != nil {
				s.log.Error("page-image stream copy fehlgeschlagen", "page_id", in.PageID, "error", err)
			}
		},
	}, nil
}

// getPageOCRInput steuert GET /v1/pages/{pageId}/ocr.
type getPageOCRInput struct {
	PageID string `path:"pageId" doc:"Seiten-ID."`
}

// getPageOCROutput ist der Response-Body von GET /v1/pages/{pageId}/ocr: die flache Liste der
// erkannten Text-Tokens mit Bounding-Box (fileee.OCRToken) — Grundlage einer möglichen
// Fileee→Paperless-ngx-Migration (siehe fileee/ocr.go).
type getPageOCROutput struct {
	Body []fileee.OCRToken
}

// handleGetPageOCR implementiert GET /v1/pages/{pageId}/ocr — dünner Durchgriff auf
// Documents.PageOCR (authentifizierter Pfad; der anonyme Share-Pfad über ShareClient.
// SharedPageOCR ist Scope einer späteren Task, Design-Spec §4.1 Share-Proxy-Routen).
func (s *Server) handleGetPageOCR(ctx context.Context, in *getPageOCRInput) (*getPageOCROutput, error) {
	toks, err := s.fc.Documents.PageOCR(ctx, in.PageID)
	if err != nil {
		return nil, mapError(err)
	}
	return &getPageOCROutput{Body: toks}, nil
}

// uploadDocumentInput steuert POST /v1/documents (multipart, Design-Spec §4.2). Huma dekodiert das
// Formular über huma.MultipartFormFiles[T] (huma@v2.35.0 formdata.go) — das "file"-Feld ist
// Pflicht (required:"true"), "title" ist ein optionales Textfeld (form:"title", kein Datei-Feld).
// contentType:"application/octet-stream" auf File akzeptiert JEDEN Dateityp (huma@v2.35.0
// MimeTypeValidator.Validate behandelt "application/octet-stream" als Wildcard) — die eigentliche
// Dateityp-Prüfung übernimmt Fileee selbst (ErrUnsupportedFileType, 415, mapError).
type uploadDocumentInput struct {
	RawBody huma.MultipartFormFiles[struct {
		File  huma.FormFile `form:"file" contentType:"application/octet-stream" required:"true" doc:"Hochzuladende Datei (beliebiger Dateityp; Fileee lehnt nicht unterstützte Typen serverseitig mit 415 ab)."`
		Title string        `form:"title" doc:"Optionaler Dokumenttitel. Fallback: Dateiname der hochgeladenen Datei."`
	}]
}

// uploadDuplicateError signalisiert 409 bei einem Upload-Duplikat (Design-Spec §12: "Upload-
// Duplikat (ErrDuplicateDocument) → 409 {error:"duplicate", id, isDuplicate:true}"). Anders als das
// generische {error,code}-Schema aus errors.go (mapError) MUSS diese Antwort zusätzlich die id des
// bereits bestehenden Dokuments sowie isDuplicate:true enthalten, damit der Aufrufer sofort mit der
// bestehenden id weiterarbeiten kann, statt sie separat nachfragen zu müssen — deshalb ein
// eigenständiger, lokaler Fehlertyp statt eines mapError-Zweigs.
type uploadDuplicateError struct {
	// ErrorMsg ist Feld "error" im JSON-Body — analog zu statusError (errors.go).
	ErrorMsg string `json:"error"`
	// ErrorCode ist Feld "code" im JSON-Body — hier immer "duplicate".
	ErrorCode string `json:"code"`
	// ID ist die id des bereits bestehenden Dokuments (nicht die client-generierte id des Uploads).
	ID string `json:"id"`
	// IsDuplicate ist immer true — Design-Spec §12 verlangt das Feld explizit im Body.
	IsDuplicate bool `json:"isDuplicate"`
}

// Error liefert die menschenlesbare Fehlermeldung und erfüllt damit das error-Interface.
func (e *uploadDuplicateError) Error() string { return e.ErrorMsg }

// GetStatus liefert immer 409 und erfüllt damit huma.StatusError (siehe statusError.GetStatus,
// errors.go, für den zugrunde liegenden Huma-Mechanismus).
func (e *uploadDuplicateError) GetStatus() int { return http.StatusConflict }

// newUploadDuplicateError baut den 409-Fehler aus der id des von Documents.Upload gemeldeten,
// bereits bestehenden Dokuments.
func newUploadDuplicateError(existingID string) *uploadDuplicateError {
	return &uploadDuplicateError{
		ErrorMsg:    "document already exists",
		ErrorCode:   "duplicate",
		ID:          existingID,
		IsDuplicate: true,
	}
}

// handleUploadDocument implementiert POST /v1/documents. Sie delegiert an Documents.Upload
// (client-generierte id, serverseitige Duplikaterkennung — siehe fileee/documents.go) und behandelt
// den Duplikat-Fall gesondert (uploadDuplicateError statt mapError), weil dessen Response-Body
// zusätzliche Felder braucht (Design-Spec §12). Jeder andere Fehler läuft weiterhin über mapError.
func (s *Server) handleUploadDocument(ctx context.Context, in *uploadDocumentInput) (*getDocumentOutput, error) {
	data := in.RawBody.Data()
	defer data.File.Close()

	title := data.Title
	if title == "" {
		title = data.File.Filename
	}

	res, err := s.fc.Documents.Upload(ctx, data.File, fileee.UploadMetadata{Title: title})
	if err != nil {
		if errors.Is(err, fileee.ErrDuplicateDocument) && res != nil && res.Document != nil {
			return nil, newUploadDuplicateError(res.Document.ID)
		}
		return nil, mapError(err)
	}
	return &getDocumentOutput{Body: *res.Document}, nil
}

// updateDocumentInput steuert PUT /v1/documents/{id}. Die Pfad-id ist maßgeblich — sie überschreibt
// ein eventuell abweichendes Body.ID, damit ein Aufrufer nicht versehentlich ein anderes Dokument
// als das in der URL adressierte ändert.
type updateDocumentInput struct {
	ID   string          `path:"id" doc:"Dokument-ID."`
	Body fileee.Document `doc:"Vollständiges, aktualisiertes Dokument (Optimistic Locking über version, siehe Documents.Update)."`
}

// handleUpdateDocument implementiert PUT /v1/documents/{id} — dünner Durchgriff auf
// Documents.Update.
func (s *Server) handleUpdateDocument(ctx context.Context, in *updateDocumentInput) (*getDocumentOutput, error) {
	doc := in.Body
	doc.ID = in.ID
	updated, err := s.fc.Documents.Update(ctx, &doc)
	if err != nil {
		return nil, mapError(err)
	}
	return &getDocumentOutput{Body: *updated}, nil
}

// exportZipRequest ist der Body von POST /v1/documents/export-zip (Design-Spec §4.2). Eine leere
// DocumentIDs-Liste exportiert ALLE Dokumente des Kontos (Documents.ExportAll).
type exportZipRequest struct {
	DocumentIDs []string `json:"documentIds,omitempty" doc:"Zu exportierende Dokument-IDs. Leer/weggelassen = alle Dokumente."`
	ZipPassword string   `json:"zipPassword" doc:"Passwort, mit dem die erzeugte ZIP-Datei geschützt wird."`
}

// exportZipInput steuert POST /v1/documents/export-zip.
type exportZipInput struct {
	Body exportZipRequest
}

// exportZipOutput ist der Response-Body von POST /v1/documents/export-zip: der gestartete
// asynchrone Vorgang (fileee.Process) — sein Fortschritt wird über GET /v1/processes/{id} bzw.
// POST /v1/processes/{id}/wait abgefragt (handlers_share.go).
type exportZipOutput struct {
	Body fileee.Process
}

// handleExportZip implementiert POST /v1/documents/export-zip. Eine leere/weggelassene
// documentIds-Liste läuft über Documents.ExportAll (alle Dokumente), eine nicht-leere Liste über
// Documents.ExportZIP (Teilmenge) — beide liefern denselben Process-Typ.
func (s *Server) handleExportZip(ctx context.Context, in *exportZipInput) (*exportZipOutput, error) {
	var proc *fileee.Process
	var err error
	if len(in.Body.DocumentIDs) == 0 {
		proc, err = s.fc.Documents.ExportAll(ctx, in.Body.ZipPassword)
	} else {
		proc, err = s.fc.Documents.ExportZIP(ctx, in.Body.DocumentIDs, in.Body.ZipPassword)
	}
	if err != nil {
		return nil, mapError(err)
	}
	return &exportZipOutput{Body: *proc}, nil
}

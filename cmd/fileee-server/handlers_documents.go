package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

// registerDocumentRoutes registriert alle Dokument-/Seiten-bezogenen Read-Operationen (Task 7,
// Design-Spec §4.1, docs/superpowers/specs/2026-07-24-fileee-server-design.md im
// homelab-management-Repo) auf der übergebenen Huma-API: Liste/Suche, Einzelabruf, PDF-/Bild-
// Stream und OCR-Tokens. Jeder Handler delegiert direkt an s.fc (Core-Lib) und übersetzt
// Lib-Fehler ausschließlich über mapError (errors.go) — es gibt bewusst KEINE eigene
// Statuscode-Logik in dieser Datei.
func (s *Server) registerDocumentRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-documents",
		Method:      http.MethodGet,
		Path:        "/v1/documents",
		Summary:     "Dokumente auflisten oder per Volltextsuche durchsuchen",
	}, s.handleListDocuments)

	huma.Register(api, huma.Operation{
		OperationID: "get-document",
		Method:      http.MethodGet,
		Path:        "/v1/documents/{id}",
		Summary:     "Einzelnes Dokument laden",
	}, s.handleGetDocument)

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

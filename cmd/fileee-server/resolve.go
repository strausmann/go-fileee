package main

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/strausmann/go-fileee/fileee"
)

// registerResolveRoute registriert POST /v1/resolve (Task 10, Design-Spec §4.1a
// "Unified Resolver", docs/superpowers/specs/2026-07-24-fileee-server-design.md im
// homelab-management-Repo): ein Fileee-Dokument-Link (egal ob interner Login-Link oder anonymer
// Share-Link) rein, ein einheitliches ResolvedDocument raus. handleResolve ist der einzige
// Handler, der fileee.ParseDocumentLink aufruft — jede andere Route kennt Linkarten nicht.
func (s *Server) registerResolveRoute(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "resolve-document-link",
		Method:      http.MethodPost,
		Path:        "/v1/resolve",
		Summary:     "Fileee-Dokument-Link auflösen (interner Link oder anonymer Share-Link)",
	}, s.handleResolve)
}

// resolveRequestBody ist der Body von POST /v1/resolve (Design-Spec §4.1a: "POST /v1/resolve
// {url}").
type resolveRequestBody struct {
	URL string `json:"url" doc:"Fileee-Dokument-Link, z. B. https://my.fileee.com/documents/<id> (intern) oder https://my.fileee.com/shared/<token> (anonyme Freigabe)."`
}

// resolveInput steuert POST /v1/resolve. Include ist der optionale Query-Parameter
// "?include=ocr" (Design-Spec §4.1a): der einzige bisher definierte Wert ist "ocr" — jeder andere
// Wert (inkl. leer) verhält sich wie "nicht gesetzt" (nur ocrUrl als Verweis, kein inline-OCR).
type resolveInput struct {
	Body    resolveRequestBody
	Include string `query:"include" doc:"\"ocr\" bettet die OCR-Tokens ALLER Seiten des aufgelösten Dokuments inline ein (Feld \"ocr\", pageId -> Tokens); jeder andere/fehlende Wert liefert nur ocrUrl als Verweis (Design-Spec §4.1a: OCR nicht inline wegen Größe, außer bei explizitem Opt-in)."`
}

// ResolvedMetadata sind die von fileee-server aus dem jeweiligen Lib-Typ (fileee.Document für
// "internal", fileee.SharedDocument für "shared") extrahierten Kernmetadaten (Design-Spec §4.1a).
//
// Bewusste Scope-Entscheidungen (kein Raten, siehe Task-10-Report):
//   - Type wird aus Document.Attributes.DocumentTypeID befüllt (NICHT aus dem separaten
//     Top-Level-Feld Document.Type) — docs/API.md §"Auto-Klassifizierung"/§827 belegt
//     documentTypeId als die semantische Dokumentkategorie (z. B. "bill"); Document.Type ist ein
//     anderer, hier nicht relevanter Wire-Diskriminator.
//   - SenderID/ReceiverID bleiben rohe Fileee-Kontakt-IDs (Document.Attributes.SenderID/
//     ReceiverID) statt aufgelöster Kontakt-Objekte — eine Auflösung über Contacts.Get ist NICHT
//     Teil der in Task 10 konsumierten Schnittstellen (Documents.Get/PageOCR/DownloadPDF,
//     ShareClient.Resolve/SharedPageOCR/DownloadSharedPDF) und bliebe einer Folge-Task
//     vorbehalten, falls gewünscht.
//   - Für "shared"-Dokumente liefert fileee.SharedDocument nur Title (kein Type/IssueDate/
//     Sender/Receiver) — die entsprechenden Felder bleiben dort leer/nil, statt sie zu erfinden.
type ResolvedMetadata struct {
	// Title ist der Dokumenttitel (Document.Attributes.Title bzw. SharedDocument.Title).
	Title string `json:"title"`
	// Type ist die Dokumentkategorie (Document.Attributes.DocumentTypeID, z. B. "bill"). Bei
	// "shared"-Dokumenten immer leer (SharedDocument kennt keine Kategorie).
	Type string `json:"type,omitempty"`
	// IssueDate ist das Ausstellungsdatum (Document.Attributes.IssueDate). Bei "shared"-
	// Dokumenten immer nil (SharedDocument kennt kein Ausstellungsdatum).
	IssueDate *time.Time `json:"issueDate,omitempty"`
	// SenderID ist die rohe Fileee-Kontakt-ID des Absenders (Document.Attributes.SenderID). Bei
	// "shared"-Dokumenten immer leer.
	SenderID string `json:"senderId,omitempty"`
	// ReceiverID ist die rohe Fileee-Kontakt-ID des Empfängers (Document.Attributes.ReceiverID).
	// Bei "shared"-Dokumenten immer leer.
	ReceiverID string `json:"receiverId,omitempty"`
}

// ResolvedDocument ist die einheitliche Antwort von POST /v1/resolve (Design-Spec §4.1a): egal ob
// der aufgelöste Link intern oder eine anonyme Freigabe war, liefert sie dieselbe Form. OCRUrl und
// DownloadUrl zeigen IMMER auf fileee-server selbst (relative Server-Pfade, kein Fileee-Host, kein
// Static-Host) — ein Aufrufer (z. B. ein N8N-Workflow) braucht dafür nie Fileee-Credentials.
type ResolvedDocument struct {
	// Kind ist "internal" oder "shared" (fileee.LinkKind.String()) — die von ParseDocumentLink
	// erkannte Linkart.
	Kind string `json:"kind"`
	// ID ist die Dokument-ID (bei "shared" die ID des ERSTEN Dokuments der Freigabe, siehe
	// resolveShared-Doku).
	ID string `json:"id"`
	// Metadata sind die Kernmetadaten des aufgelösten Dokuments (siehe ResolvedMetadata-Doku).
	Metadata ResolvedMetadata `json:"metadata"`
	// PageIDs sind alle Seiten-IDs des Dokuments in Fileee-Reihenfolge.
	PageIDs []string `json:"pageIds"`
	// OCRUrl verweist auf die OCR-Tokens der ERSTEN Seite (server-eigener, relativer Pfad) — bei
	// mehrseitigen Dokumenten kann der Aufrufer die OCR-Route für jede weitere Seite selbst aus
	// PageIDs ableiten (Design-Spec §4.1a nennt nur einen einzelnen ocrUrl-Wert im Beispiel).
	// Leer, wenn das Dokument keine Seiten hat.
	OCRUrl string `json:"ocrUrl"`
	// DownloadUrl verweist auf das Voll-PDF des Dokuments (server-eigener, relativer Pfad,
	// mode=download).
	DownloadUrl string `json:"downloadUrl"`
	// OCR enthält, NUR wenn "?include=ocr" gesetzt war, die OCR-Tokens JEDER Seite (Schlüssel =
	// pageId). Ohne include=ocr bleibt das Feld nil und wird wegen omitempty nicht serialisiert
	// (Design-Spec §4.1a: "OCR nicht inline" ist der Default, include=ocr der Opt-in-Bequemfall).
	OCR map[string][]fileee.OCRToken `json:"ocr,omitempty"`
}

// resolveOutput kapselt ResolvedDocument als Huma-Response von POST /v1/resolve.
type resolveOutput struct {
	Body ResolvedDocument
}

// handleResolve implementiert POST /v1/resolve. Sie ruft ParseDocumentLink GENAU EINMAL auf und
// dispatcht anhand des erkannten fileee.LinkKind an resolveInternal (authentifiziert über s.fc)
// bzw. resolveShared (anonym über s.sc). Ein nicht erkannter Link (fileee.LinkKindUnknown) liefert
// einen eigenen Request-Validierungsfehler (newStatusError, KEIN mapError) — ParseDocumentLink
// selbst liefert keinen Lib-Fehler, den mapError übersetzen könnte (Design-Spec §4.1a: "sonst →
// 400").
func (s *Server) handleResolve(ctx context.Context, in *resolveInput) (*resolveOutput, error) {
	kind, id := fileee.ParseDocumentLink(in.Body.URL)
	includeOCR := in.Include == "ocr"

	switch kind {
	case fileee.LinkKindInternal:
		return s.resolveInternal(ctx, id, includeOCR)
	case fileee.LinkKindShared:
		return s.resolveShared(ctx, id, includeOCR)
	default:
		return nil, newStatusError(http.StatusBadRequest, "invalid_link", "unrecognized fileee document link")
	}
}

// resolveInternal löst einen internen Login-Link auf (Design-Spec §4.1a "internal"): Documents.Get
// liefert das vollständige Dokument (authentifiziert, über die Session), aus dem Metadata/PageIDs/
// OCRUrl/DownloadUrl abgeleitet werden. Bei includeOCR=true lädt sie zusätzlich JEDE Seite per
// Documents.PageOCR (ein Roundtrip pro Seite — bewusst in Kauf genommen, analog zum N+1-Muster in
// handleListDocuments, handlers_documents.go, da die Lib keinen Bulk-OCR-Endpunkt kennt).
func (s *Server) resolveInternal(ctx context.Context, id string, includeOCR bool) (*resolveOutput, error) {
	doc, err := s.fc.Documents.Get(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}

	pageIDs := make([]string, 0, len(doc.Pages))
	for _, p := range doc.Pages {
		pageIDs = append(pageIDs, p.ID)
	}

	var ocrURL string
	if len(pageIDs) > 0 {
		ocrURL = "/v1/pages/" + pageIDs[0] + "/ocr"
	}

	resolved := ResolvedDocument{
		Kind: fileee.LinkKindInternal.String(),
		ID:   doc.ID,
		Metadata: ResolvedMetadata{
			Title:      doc.Attributes.Title,
			Type:       doc.Attributes.DocumentTypeID,
			IssueDate:  doc.Attributes.IssueDate,
			SenderID:   doc.Attributes.SenderID,
			ReceiverID: doc.Attributes.ReceiverID,
		},
		PageIDs:     pageIDs,
		OCRUrl:      ocrURL,
		DownloadUrl: "/v1/documents/" + doc.ID + "/pdf?mode=" + string(fileee.PDFModeDownload),
	}

	if includeOCR {
		ocr, err := fetchAllOCR(pageIDs, func(pageID string) ([]fileee.OCRToken, error) {
			return s.fc.Documents.PageOCR(ctx, pageID)
		})
		if err != nil {
			return nil, mapError(err)
		}
		resolved.OCR = ocr
	}

	return &resolveOutput{Body: resolved}, nil
}

// resolveShared löst einen anonymen Share-Link auf (Design-Spec §4.1a "shared"): ShareClient.
// Resolve liefert die Freigabe (fileee.SharedObject) OHNE Fileee-Login. Eine Freigabe kann laut
// SharedObject.Documents mehrere Dokumente bündeln; ResolvedDocument bildet aber genau EIN
// Dokument ab (Design-Spec §4.1a zeigt ein einzelnes ResolvedDocument) — deshalb wird
// SharedObject.Documents[0] als DAS aufgelöste Dokument verwendet (Standardfall: ein Share-Link
// pro Dokument, siehe bestehende Handler-Tests/Fixtures in handlers_test.go, die alle genau ein
// Dokument je Freigabe zurückgeben). Eine leere Documents-Liste ist kein in Design-Spec §12
// vorgesehener Fall und wird konservativ als 404 behandelt statt ein leeres/erfundenes
// ResolvedDocument zu liefern.
func (s *Server) resolveShared(ctx context.Context, token string, includeOCR bool) (*resolveOutput, error) {
	obj, err := s.sc.Resolve(ctx, token)
	if err != nil {
		return nil, mapError(err)
	}
	if len(obj.Documents) == 0 {
		return nil, newStatusError(http.StatusNotFound, "not_found", "share contains no documents")
	}
	doc := obj.Documents[0]

	var ocrURL string
	if len(doc.PageIDs) > 0 {
		ocrURL = "/v1/share-objects/" + token + "/pages/" + doc.PageIDs[0] + "/ocr"
	}

	resolved := ResolvedDocument{
		Kind:        fileee.LinkKindShared.String(),
		ID:          doc.ID,
		Metadata:    ResolvedMetadata{Title: doc.Title},
		PageIDs:     doc.PageIDs,
		OCRUrl:      ocrURL,
		DownloadUrl: "/v1/share-objects/" + token + "/documents/" + doc.ID + "/pdf?mode=" + string(fileee.PDFModeDownload),
	}

	if includeOCR {
		ocr, err := fetchAllOCR(doc.PageIDs, func(pageID string) ([]fileee.OCRToken, error) {
			return s.sc.SharedPageOCR(ctx, pageID, obj.ID, obj.SharedByID)
		})
		if err != nil {
			return nil, mapError(err)
		}
		resolved.OCR = ocr
	}

	return &resolveOutput{Body: resolved}, nil
}

// fetchAllOCR lädt für jede pageID in pageIDs die OCR-Tokens über fetch (das den Unterschied
// zwischen Documents.PageOCR und ShareClient.SharedPageOCR kapselt) und liefert sie als Map
// (pageId -> Tokens) — gemeinsame Umsetzung von "?include=ocr" für resolveInternal und
// resolveShared. Bricht beim ersten Fehler ab und reicht ihn unübersetzt an den Aufrufer weiter
// (der ihn per mapError übersetzt), statt Teilresultate zurückzugeben.
func fetchAllOCR(pageIDs []string, fetch func(pageID string) ([]fileee.OCRToken, error)) (map[string][]fileee.OCRToken, error) {
	ocr := make(map[string][]fileee.OCRToken, len(pageIDs))
	for _, pageID := range pageIDs {
		toks, err := fetch(pageID)
		if err != nil {
			return nil, err
		}
		ocr[pageID] = toks
	}
	return ocr, nil
}

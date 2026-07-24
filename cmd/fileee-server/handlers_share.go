package main

import (
	"context"
	"errors"
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

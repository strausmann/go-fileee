package main

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/strausmann/go-fileee/fileee"
)

// emptyInput ist der Input-Typ für Read-Endpunkte ohne Pfad-/Query-Parameter (die Stammdaten-
// Listen dieser Datei). Huma akzeptiert einen Zeiger auf ein feldloses struct als "kein Input"
// (siehe huma@v2.35.0 huma_test.go, Testfall "response-stream").
type emptyInput struct{}

// entityListBody ist der einheitliche Response-Body der generischen Stammdaten-Listen (Tags,
// Companies, Contacts, DocumentTypes, DocumentTypeSchemes, Reminders) — sie teilen sich alle
// dieselbe Query/Diff/Get-Konvention (fileee.ReadService[T], fileee/service.go) und liefern hier
// deshalb dieselbe {items, totalRows}-Form. Ein Diff-Cursor ist für diese Ressourcen (Stand
// dieser Task) bewusst NICHT exponiert — anders als bei /v1/documents gibt es dafür noch keinen
// dokumentierten Bedarf; die erste Query-Seite (Default-Limit 100 aus fileee.QueryOptions.toWire)
// genügt für den aktuellen Read-Scope. Auch von listBoxesOutput wiederverwendet (Boxes.List kennt
// kein Query/TotalRows-Ergebnis wie die generischen ReadServices — TotalRows wird dort aus
// len(Items) abgeleitet).
type entityListBody[T any] struct {
	Items     []T `json:"items" doc:"Erste Seite der Ressource (Default-Limit 100)."`
	TotalRows int `json:"totalRows" doc:"Von Fileee gemeldete (bzw. bei Boxes aus der Listenlänge abgeleitete) Gesamtzahl."`
}

// entityListOutput kapselt entityListBody[T] als Huma-Response.
type entityListOutput[T any] struct {
	Body entityListBody[T]
}

// registerEntityListRoute registriert eine parameterlose GET-Liste für einen generischen
// fileee.ReadService[T]. Tags/Companies/Contacts/DocumentTypes/DocumentTypeSchemes/Reminders
// teilen sich exakt diese Signatur (fileee/service.go ReadService[T].Query) — query wird deshalb
// direkt als Methodenwert übergeben (z.B. s.fc.Tags.Query), ganz ohne Wrapper-Closure.
func registerEntityListRoute[T any](api huma.API, operationID, path string, query func(ctx context.Context, opts fileee.QueryOptions) (*fileee.QueryResult[T], error)) {
	huma.Register(api, huma.Operation{
		OperationID: operationID,
		Method:      http.MethodGet,
		Path:        path,
	}, func(ctx context.Context, in *emptyInput) (*entityListOutput[T], error) {
		res, err := query(ctx, fileee.QueryOptions{})
		if err != nil {
			return nil, mapError(err)
		}
		return &entityListOutput[T]{Body: entityListBody[T]{Items: res.Rows, TotalRows: res.TotalRows}}, nil
	})
}

// getBoxInput steuert GET /v1/boxes/{id}.
type getBoxInput struct {
	ID string `path:"id" doc:"FileeeBox-ID."`
}

// getBoxOutput ist der Response-Body von GET /v1/boxes/{id}.
type getBoxOutput struct {
	Body fileee.FileeeBox
}

// listBoxesOutput ist der Response-Body von GET /v1/boxes.
type listBoxesOutput struct {
	Body entityListBody[fileee.FileeeBox]
}

// registerEntityRoutes registriert die Stammdaten-Read-Operationen (Task 7, Design-Spec §4.1,
// docs/superpowers/specs/2026-07-24-fileee-server-design.md im homelab-management-Repo): Tags,
// Companies, Contacts, DocumentTypes, DocumentTypeSchemes, Reminders (alle über das generische
// ReadService[T]-Muster) sowie Boxes (eigenes BoxService-Interface mit List/Get statt
// Query/Diff/Get).
func (s *Server) registerEntityRoutes(api huma.API) {
	registerEntityListRoute(api, "list-tags", "/v1/tags", s.fc.Tags.Query)
	registerEntityListRoute(api, "list-companies", "/v1/companies", s.fc.Companies.Query)
	registerEntityListRoute(api, "list-contacts", "/v1/contacts", s.fc.Contacts.Query)
	registerEntityListRoute(api, "list-document-types", "/v1/document-types", s.fc.DocumentTypes.Query)
	registerEntityListRoute(api, "list-document-type-schemes", "/v1/document-type-schemes", s.fc.DocumentTypeSchemes.Query)
	registerEntityListRoute(api, "list-reminders", "/v1/reminders", s.fc.Reminders.Query)

	huma.Register(api, huma.Operation{
		OperationID: "list-boxes",
		Method:      http.MethodGet,
		Path:        "/v1/boxes",
	}, s.handleListBoxes)

	huma.Register(api, huma.Operation{
		OperationID: "get-box",
		Method:      http.MethodGet,
		Path:        "/v1/boxes/{id}",
	}, s.handleGetBox)
}

// handleListBoxes implementiert GET /v1/boxes über Boxes.List (intern ein Diff mit vollem
// FileeeBox-Cursor, fileee/boxes.go) — TotalRows wird hier aus len(boxes) abgeleitet, da
// BoxService.List keinen separaten TotalRows-Wert liefert.
func (s *Server) handleListBoxes(ctx context.Context, in *emptyInput) (*listBoxesOutput, error) {
	boxes, err := s.fc.Boxes.List(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	return &listBoxesOutput{Body: entityListBody[fileee.FileeeBox]{Items: boxes, TotalRows: len(boxes)}}, nil
}

// handleGetBox implementiert GET /v1/boxes/{id} — dünner Durchgriff auf Boxes.Get.
func (s *Server) handleGetBox(ctx context.Context, in *getBoxInput) (*getBoxOutput, error) {
	box, err := s.fc.Boxes.Get(ctx, in.ID)
	if err != nil {
		return nil, mapError(err)
	}
	return &getBoxOutput{Body: *box}, nil
}

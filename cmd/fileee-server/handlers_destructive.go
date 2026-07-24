package main

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// registerDestructiveRoutes registriert die drei echten Hard-DELETE-Routen (Dokument, Kontakt,
// Erinnerung — Design-Spec §4.2/§12, ADR-0007/ADR-0008): DELETE /v1/documents/{id},
// DELETE /v1/contacts/{id}, DELETE /v1/reminders/{id}. Im Unterschied zu
// handleRemoveBoxDocument (DELETE /v1/boxes/{boxId}/documents/{docId}, "Ausheften ≠ Löschen",
// handlers_entities.go) lösen diese drei Routen ein UNWIDERRUFLICHES Hard-DELETE bei Fileee aus
// (fileee.DocumentService.Delete / contactService.Delete / reminderService.Delete — es gibt
// serverseitig keinen Papierkorb/Undo).
//
// Handler() (server.go) ruft diese Methode AUSSCHLIESSLICH innerhalb von
// `if s.cfg.AllowDestructive { … }` auf. Ist das Flag (FILEEE_ALLOW_DESTRUCTIVE) false, wird
// registerDestructiveRoutes gar nicht erst aufgerufen — die drei Pfade bleiben dem
// zugrundeliegenden http.ServeMux (adapters/humago) komplett unbekannt für das DELETE-Verb. Es
// gibt also KEINEN Zwischenzustand "Route existiert, lehnt aber zur Laufzeit mit 403 ab" — die
// bewusste Design-Entscheidung ist "Route abwesend, wenn deaktiviert" (kein
// register-then-403-Anti-Pattern). Da GET/PUT auf denselben drei Pfaden bereits registriert sind
// (Task 7/8), liefert Go 1.22+ http.ServeMux bei deaktiviertem Gate für ein DELETE auf diesen
// Pfaden 405 Method Not Allowed statt eines pauschalen 404 (Pattern-Auflösung des
// Standard-Mux, siehe Doku bei den Gate-OFF-Tests in handlers_test.go) — auch das entsteht
// ausschließlich aus der (Nicht-)Registrierung, nicht aus einem eigenen Handler-Zweig.
func (s *Server) registerDestructiveRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-document",
		Method:      http.MethodDelete,
		Path:        "/v1/documents/{id}",
		Summary:     "Dokument unwiderruflich löschen (Hard-DELETE, nur bei FILEEE_ALLOW_DESTRUCTIVE)",
	}, s.handleDeleteDocument)

	huma.Register(api, huma.Operation{
		OperationID: "delete-contact",
		Method:      http.MethodDelete,
		Path:        "/v1/contacts/{id}",
		Summary:     "Kontakt unwiderruflich löschen (Hard-DELETE, nur bei FILEEE_ALLOW_DESTRUCTIVE)",
	}, s.handleDeleteContact)

	huma.Register(api, huma.Operation{
		OperationID: "delete-reminder",
		Method:      http.MethodDelete,
		Path:        "/v1/reminders/{id}",
		Summary:     "Erinnerung unwiderruflich löschen (Hard-DELETE, nur bei FILEEE_ALLOW_DESTRUCTIVE)",
	}, s.handleDeleteReminder)
}

// deleteDocumentInput steuert DELETE /v1/documents/{id}.
type deleteDocumentInput struct {
	ID string `path:"id" doc:"Dokument-ID."`
}

// handleDeleteDocument implementiert DELETE /v1/documents/{id} — unwiderrufliches Hard-DELETE über
// Documents.Delete (fileee/documents.go). Schreibt VOR dem eigentlichen Löschversuch eine
// strukturierte Audit-Log-Zeile auf Warn-Level (s.log) — Warn statt Info, damit die Zeile in jeder
// Standard-Log-Konfiguration sichtbar bleibt und nicht erst ab einem selten aktivierten
// Debug-Level auftaucht. Die Zeile protokolliert den VERSUCH, nicht nur den Erfolg (Design-Spec
// §12 "jede ausgeführte Destruktiv-Op wird protokolliert") — ein anschließender Fehler (z. B. 404
// vom Backend) wird zusätzlich über mapError abgebildet, die Audit-Zeile bleibt davon unberührt
// stehen. Der leere `*struct{}`-Rückgabetyp ohne Body-Feld lässt Huma automatisch mit 204 No
// Content antworten (huma@v2.35.0 huma.go: op.DefaultStatus wird ohne Body-Feld auf 204 gesetzt,
// analog zum bestehenden handleRemoveBoxDocument-Muster in handlers_entities.go).
func (s *Server) handleDeleteDocument(ctx context.Context, in *deleteDocumentInput) (*struct{}, error) {
	s.log.Warn("destruktive Operation", "op", "delete", "resource", "document", "id", in.ID)
	if err := s.fc.Documents.Delete(ctx, in.ID); err != nil {
		return nil, mapError(err)
	}
	return nil, nil
}

// deleteContactInput steuert DELETE /v1/contacts/{id}.
type deleteContactInput struct {
	ID string `path:"id" doc:"Kontakt-ID."`
}

// handleDeleteContact implementiert DELETE /v1/contacts/{id} — unwiderrufliches Hard-DELETE über
// Contacts.Delete (fileee/contacts.go). Audit-Log und Rückgabeverhalten analog
// handleDeleteDocument.
func (s *Server) handleDeleteContact(ctx context.Context, in *deleteContactInput) (*struct{}, error) {
	s.log.Warn("destruktive Operation", "op", "delete", "resource", "contact", "id", in.ID)
	if err := s.fc.Contacts.Delete(ctx, in.ID); err != nil {
		return nil, mapError(err)
	}
	return nil, nil
}

// deleteReminderInput steuert DELETE /v1/reminders/{id}.
type deleteReminderInput struct {
	ID string `path:"id" doc:"Erinnerungs-ID."`
}

// handleDeleteReminder implementiert DELETE /v1/reminders/{id} — unwiderrufliches Hard-DELETE über
// Reminders.Delete (fileee/reminders.go). Audit-Log und Rückgabeverhalten analog
// handleDeleteDocument.
func (s *Server) handleDeleteReminder(ctx context.Context, in *deleteReminderInput) (*struct{}, error) {
	s.log.Warn("destruktive Operation", "op", "delete", "resource", "reminder", "id", in.ID)
	if err := s.fc.Reminders.Delete(ctx, in.ID); err != nil {
		return nil, mapError(err)
	}
	return nil, nil
}

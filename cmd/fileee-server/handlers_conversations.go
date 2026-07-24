package main

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/strausmann/go-fileee/fileee"
)

// conversationsCursorEntityType ist der EntityType-Diskriminator, den ein leerer `cursor`-Parameter
// für GET /v1/conversations initialisiert (fileee.NewCursor-Konvention, analog
// documentCursorEntityType in handlers_documents.go). Bewusst "Conversation" statt "Document" —
// jede Diff-fähige Ressource führt ihren eigenen EntityType (reine Client-Bookkeeping-Metadatik der
// Lib, fließt in keine Diff-Logik ein).
const conversationsCursorEntityType = "Conversation"

// registerConversationRoutes registriert die Konversations-/Chat-/Freigabe-/Einladungs-Operationen
// (Task 11, Design-Spec §4.1b "Konversationen (Teilen mit Kontakt + Chat)",
// docs/superpowers/specs/2026-07-24-fileee-server-design.md im homelab-management-Repo). Jeder
// Handler delegiert direkt an s.fc.Conversations bzw. s.fc.Documents.Conversations und übersetzt
// Lib-Fehler ausschließlich über mapError (errors.go) — die einzige Ausnahme ist die
// Rollen-Validierung in handleAddParticipant (newStatusError, KEIN Lib-Aufruf bei ungültiger Rolle).
func (s *Server) registerConversationRoutes(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-conversations",
		Method:      http.MethodGet,
		Path:        "/v1/conversations",
		Summary:     "Konversationen auflisten (inkrementeller Diff-Sync)",
	}, s.handleListConversations)

	huma.Register(api, huma.Operation{
		OperationID: "list-conversation-invitations",
		Method:      http.MethodGet,
		Path:        "/v1/conversations/invitations",
		Summary:     "Offene Einladungen an das eigene Konto auflisten (inkl. Invitation-Token)",
	}, s.handleListInvitations)

	huma.Register(api, huma.Operation{
		OperationID: "accept-conversation-invitation",
		Method:      http.MethodPost,
		// Pfad bewusst "invitations/accept/{token}" statt des in Brief/Design-Spec §4.1b
		// wörtlich genannten "invitations/{token}/accept": beide 5-Segment-POST-Pfade
		// "/v1/conversations/{id}/documents/{docId}" (share/unshare, s.u.) und
		// "/v1/conversations/invitations/{token}/accept" sind bei WÖRTLICHER Übernahme
		// ambig für Go's http.ServeMux (huma@v2.35.0 humago-Adapter registriert direkt auf
		// einem *http.ServeMux, Go 1.22+ Pattern-Matching): sie unterscheiden sich an DREI
		// Positionen (3: {id} vs. "invitations", 4: "documents" vs. {token}, 5: {docId} vs.
		// "accept") mit GEMISCHTER Dominanz (an Position 4 ist "documents" spezifischer, an
		// den Positionen 3+5 ist der jeweils andere Pfad spezifischer) — ServeMux.Handle
		// paniced deshalb zur Registrierungszeit mit "pattern ... conflicts with pattern
		// ...: neither is more specific than the other" (empirisch verifiziert, Task 11).
		// Durch den Tausch landet an Position 4 ein LITERALES "accept" statt des Wildcards
		// {token} — "documents" und "accept" sind an derselben Position unterschiedliche
		// Literale, können also NIE denselben konkreten Request-Pfad matchen, wodurch beide
		// Pfade garantiert disjunkt sind (kein Konflikt mehr, ebenfalls empirisch
		// verifiziert). Semantik/Upstream-Aufruf (Conversations.AcceptInvitation) sind
		// unverändert — nur die SERVER-eigene Pfadform (nicht die von Fileee selbst
		// verwendete Upstream-Route "/api/conversations/invitations/:token/accept")
		// verschiebt "accept" vor {token}.
		Path:    "/v1/conversations/invitations/accept/{token}",
		Summary: "Einladung über ihren Invitation-Token annehmen",
	}, s.handleAcceptInvitation)

	huma.Register(api, huma.Operation{
		OperationID: "get-conversation",
		Method:      http.MethodGet,
		Path:        "/v1/conversations/{id}",
		Summary:     "Einzelne Konversation laden (inkl. participants[]/messages[])",
	}, s.handleGetConversation)

	huma.Register(api, huma.Operation{
		OperationID: "list-document-conversations",
		Method:      http.MethodGet,
		Path:        "/v1/documents/{id}/conversations",
		Summary:     "Konversationen laden, in denen ein Dokument geteilt ist",
	}, s.handleListDocumentConversations)

	huma.Register(api, huma.Operation{
		OperationID: "send-conversation-message",
		Method:      http.MethodPost,
		Path:        "/v1/conversations/{id}/messages",
		Summary:     "Text-Chatnachricht in eine Konversation posten",
	}, s.handleSendMessage)

	huma.Register(api, huma.Operation{
		OperationID: "share-conversation-document",
		Method:      http.MethodPost,
		Path:        "/v1/conversations/{id}/documents/{docId}",
		Summary:     "Dokument in eine Konversation teilen (DOCUMENT-Chatnachricht)",
	}, s.handleShareConversationDocument)

	huma.Register(api, huma.Operation{
		OperationID: "unshare-conversation-document",
		Method:      http.MethodDelete,
		Path:        "/v1/conversations/{id}/documents/{docId}",
		Summary:     "Geteiltes Dokument aus einer Konversation entfernen (kein Destruktiv-Gate, Chat-Entfernung ≠ Dokument-Löschung)",
	}, s.handleUnshareConversationDocument)

	huma.Register(api, huma.Operation{
		OperationID: "add-conversation-participant",
		Method:      http.MethodPost,
		Path:        "/v1/conversations/{id}/participants",
		Summary:     "Externen Empfänger per E-Mail in eine Konversation einladen",
	}, s.handleAddParticipant)

	huma.Register(api, huma.Operation{
		OperationID: "remove-conversation-participant",
		Method:      http.MethodDelete,
		Path:        "/v1/conversations/{id}/participants/{participantId}",
		Summary:     "Teilnehmer aus einer Konversation entfernen",
	}, s.handleRemoveParticipant)
}

// listConversationsInput steuert GET /v1/conversations — analog listDocumentsInput
// (handlers_documents.go), aber ohne Suchmodus: Konversationen kennen laut Design-Spec §4.1b nur
// den Diff-Zweig (Conversations.Diff), keine Volltextsuche.
type listConversationsInput struct {
	Cursor string `query:"cursor" doc:"Opaques Cursor-Token aus einer vorigen Antwort dieses Endpunkts. Leer = kompletter Sync von vorn."`
}

// conversationListBody ist der Response-Body von GET /v1/conversations (Design-Spec §17:
// einheitliches {items, cursor, totalRows}-Schema, analog documentListBody).
type conversationListBody struct {
	Items     []fileee.Conversation `json:"items" doc:"Konversationen dieser Diff-Seite."`
	Cursor    string                `json:"cursor" doc:"Opaques Folge-Cursor-Token für den nächsten Aufruf."`
	TotalRows int                   `json:"totalRows" doc:"Von Fileee gemeldete Gesamtzahl der Diff-Zeilen."`
}

// listConversationsOutput kapselt conversationListBody als Huma-Response.
type listConversationsOutput struct {
	Body conversationListBody
}

// decodeConversationsCursor entpackt ein von encodeCursor (handlers_documents.go) erzeugtes
// Cursor-Token für GET /v1/conversations. Ein leerer String liefert einen frischen Cursor mit dem
// EntityType-Diskriminator "Conversation" (conversationsCursorEntityType) statt "Document" — die
// eigentliche Base64/JSON-Dekodierung ist identisch zu decodeCursor, aber bewusst separat
// gehalten, da beide Ressourcen unterschiedliche Default-EntityTypes brauchen.
func decodeConversationsCursor(s string) (fileee.Cursor, error) {
	if s == "" {
		return fileee.NewCursor(conversationsCursorEntityType), nil
	}
	return decodeCursorToken(s)
}

// handleListConversations implementiert GET /v1/conversations — dünner Durchgriff auf
// Conversations.Diff (Design-Spec §4.1b: "GET /v1/conversations | Conversations.Diff | Liste").
func (s *Server) handleListConversations(ctx context.Context, in *listConversationsInput) (*listConversationsOutput, error) {
	cursor, err := decodeConversationsCursor(in.Cursor)
	if err != nil {
		return nil, newStatusError(http.StatusBadRequest, "invalid_cursor", "invalid cursor parameter")
	}
	diff, err := s.fc.Conversations.Diff(ctx, cursor)
	if err != nil {
		return nil, mapError(err)
	}
	nextCursor, err := encodeCursor(diff.NextCursor)
	if err != nil {
		return nil, mapError(err)
	}
	return &listConversationsOutput{Body: conversationListBody{Items: diff.Rows, Cursor: nextCursor, TotalRows: diff.TotalRows}}, nil
}

// getConversationInput steuert GET /v1/conversations/{id}.
type getConversationInput struct {
	ID string `path:"id" doc:"Konversations-ID."`
}

// getConversationOutput ist der Response-Body von GET /v1/conversations/{id}: die vollständige
// fileee.Conversation (inkl. participants[] mit Annahme-Status über Participant.Joined/Accepted()
// und messages[], fileee/conversations.go).
type getConversationOutput struct {
	Body fileee.Conversation
}

// handleGetConversation implementiert GET /v1/conversations/{id} — dünner Durchgriff auf
// Conversations.Get.
func (s *Server) handleGetConversation(ctx context.Context, in *getConversationInput) (*getConversationOutput, error) {
	conv, err := s.fc.Conversations.Get(ctx, in.ID)
	if err != nil {
		return nil, mapError(err)
	}
	return &getConversationOutput{Body: *conv}, nil
}

// listDocumentConversationsInput steuert GET /v1/documents/{id}/conversations.
type listDocumentConversationsInput struct {
	ID string `path:"id" doc:"Dokument-ID."`
}

// listDocumentConversationsOutput ist der Response-Body von GET /v1/documents/{id}/conversations.
// Reicht die generische entityListBody[T] (handlers_entities.go) wieder — analog
// handleListBoxes/listBoxesOutput —, da Documents.Conversations (fileee/documents.go) genau wie
// Boxes.List nur []Conversation statt eines separaten Diff/Query-Ergebnisses mit eigenem
// TotalRows-Feld liefert; TotalRows wird deshalb aus len(items) abgeleitet.
type listDocumentConversationsOutput struct {
	Body entityListBody[fileee.Conversation]
}

// handleListDocumentConversations implementiert GET /v1/documents/{id}/conversations — dünner
// Durchgriff auf Documents.Conversations (Design-Spec §4.1b: "mit wem ein Dokument geteilt ist +
// Annahme", Annahme-Status pro Teilnehmer über Participant.Joined/Accepted()).
func (s *Server) handleListDocumentConversations(ctx context.Context, in *listDocumentConversationsInput) (*listDocumentConversationsOutput, error) {
	convs, err := s.fc.Documents.Conversations(ctx, in.ID)
	if err != nil {
		return nil, mapError(err)
	}
	return &listDocumentConversationsOutput{Body: entityListBody[fileee.Conversation]{Items: convs, TotalRows: len(convs)}}, nil
}

// sendMessageRequest ist der Body von POST /v1/conversations/{id}/messages (Design-Spec §4.1b:
// "POST /v1/conversations/:id/messages | SendMessage | Text-Chat").
type sendMessageRequest struct {
	Text string `json:"text" doc:"Text der Chatnachricht."`
}

// sendMessageInput steuert POST /v1/conversations/{id}/messages.
type sendMessageInput struct {
	ID   string `path:"id" doc:"Konversations-ID."`
	Body sendMessageRequest
}

// sentMessageOutput ist der gemeinsame Response-Body von POST /v1/conversations/{id}/messages,
// POST/DELETE /v1/conversations/{id}/documents/{docId}: die von Fileee quittierte fileee.SentMessage
// (conversationId/messageId/messageIndex, fileee/conversations.go) — alle drei Operationen posten
// intern eine typisierte Chatnachricht (CHAT bzw. DOCUMENT) und bekommen dieselbe Wire-Antwort.
type sentMessageOutput struct {
	Body fileee.SentMessage
}

// handleSendMessage implementiert POST /v1/conversations/{id}/messages — dünner Durchgriff auf
// Conversations.SendMessage.
func (s *Server) handleSendMessage(ctx context.Context, in *sendMessageInput) (*sentMessageOutput, error) {
	msg, err := s.fc.Conversations.SendMessage(ctx, in.ID, in.Body.Text)
	if err != nil {
		return nil, mapError(err)
	}
	return &sentMessageOutput{Body: *msg}, nil
}

// conversationDocumentInput steuert POST/DELETE /v1/conversations/{id}/documents/{docId} —
// dieselben Pfad-Parameter für Teilen und Entfernen (analog boxDocumentInput,
// handlers_entities.go).
type conversationDocumentInput struct {
	ID    string `path:"id" doc:"Konversations-ID."`
	DocID string `path:"docId" doc:"Dokument-ID."`
}

// handleShareConversationDocument implementiert POST /v1/conversations/{id}/documents/{docId} —
// dünner Durchgriff auf Conversations.ShareDocument (postet eine DOCUMENT-Chatnachricht,
// remove=false).
func (s *Server) handleShareConversationDocument(ctx context.Context, in *conversationDocumentInput) (*sentMessageOutput, error) {
	msg, err := s.fc.Conversations.ShareDocument(ctx, in.ID, in.DocID)
	if err != nil {
		return nil, mapError(err)
	}
	return &sentMessageOutput{Body: *msg}, nil
}

// handleUnshareConversationDocument implementiert DELETE /v1/conversations/{id}/documents/{docId}
// — dünner Durchgriff auf Conversations.UnshareDocument (postet eine DOCUMENT-Chatnachricht,
// remove=true; kein Destruktiv-Gate — das zugrunde liegende Dokument bleibt unangetastet, nur die
// Chat-Freigabe wird zurückgenommen, analog boxDocumentInput-Doku).
func (s *Server) handleUnshareConversationDocument(ctx context.Context, in *conversationDocumentInput) (*sentMessageOutput, error) {
	msg, err := s.fc.Conversations.UnshareDocument(ctx, in.ID, in.DocID)
	if err != nil {
		return nil, mapError(err)
	}
	return &sentMessageOutput{Body: *msg}, nil
}

// addParticipantRequest ist der Body von POST /v1/conversations/{id}/participants (Design-Spec
// §4.1b: "per E-Mail einladen (EXTERNAL, Rolle)"). Role MUSS einer der fileee.ConversationRole*-
// Werte sein (VIEWER/EDITOR/ADMIN, fileee/conversations.go) — ungültige Werte werden VOR jedem
// Lib-Aufruf mit 400 abgelehnt (siehe handleAddParticipant).
type addParticipantRequest struct {
	Email string `json:"email" doc:"E-Mail-Adresse des einzuladenden externen Teilnehmers."`
	Role  string `json:"role" doc:"Rolle des Teilnehmers: VIEWER, EDITOR oder ADMIN (fileee.ConversationRole*)."`
}

// addParticipantInput steuert POST /v1/conversations/{id}/participants.
type addParticipantInput struct {
	ID   string `path:"id" doc:"Konversations-ID."`
	Body addParticipantRequest
}

// isValidConversationRole meldet, ob role einer der drei bekannten ConversationRole*-Werte ist
// (fileee.ConversationRoleViewer/Editor/Admin, fileee/conversations.go).
func isValidConversationRole(role string) bool {
	switch role {
	case fileee.ConversationRoleViewer, fileee.ConversationRoleEditor, fileee.ConversationRoleAdmin:
		return true
	default:
		return false
	}
}

// handleAddParticipant implementiert POST /v1/conversations/{id}/participants. Validiert Body.Role
// GEGEN die ConversationRole*-Enum, BEVOR Conversations.AddParticipant aufgerufen wird — eine
// ungültige Rolle ist ein Request-Validierungsfehler (newStatusError, 400), KEIN Lib-/mapError-Fall
// (die Lib selbst validiert die Rolle nicht, sie würde den Wert unverändert an Fileee weiterreichen).
// Kein Response-Body (204 No Content), da AddParticipant selbst nichts zurückliefert.
func (s *Server) handleAddParticipant(ctx context.Context, in *addParticipantInput) (*struct{}, error) {
	if !isValidConversationRole(in.Body.Role) {
		return nil, newStatusError(http.StatusBadRequest, "invalid_role", "invalid conversation role")
	}
	if err := s.fc.Conversations.AddParticipant(ctx, in.ID, in.Body.Email, in.Body.Role); err != nil {
		return nil, mapError(err)
	}
	return nil, nil
}

// removeParticipantInput steuert DELETE /v1/conversations/{id}/participants/{participantId}.
type removeParticipantInput struct {
	ID            string `path:"id" doc:"Konversations-ID."`
	ParticipantID string `path:"participantId" doc:"Teilnehmer-ID (Participant.ID)."`
}

// handleRemoveParticipant implementiert DELETE /v1/conversations/{id}/participants/{participantId}
// — dünner Durchgriff auf Conversations.RemoveParticipant. Kein Response-Body (204 No Content).
// Ein unbekannter Teilnehmer liefert *fileee.APIError, das ErrNotFound wrapped (fileee/conversations.go
// RemoveParticipant) — mapError übersetzt das wie jeden anderen ErrNotFound-Fall zu 404.
func (s *Server) handleRemoveParticipant(ctx context.Context, in *removeParticipantInput) (*struct{}, error) {
	if err := s.fc.Conversations.RemoveParticipant(ctx, in.ID, in.ParticipantID); err != nil {
		return nil, mapError(err)
	}
	return nil, nil
}

// listInvitationsOutput ist der Response-Body von GET /v1/conversations/invitations. Reicht die
// generische entityListBody[T] wieder (analog listDocumentConversationsOutput) — PendingInvitations
// liefert []Conversation ohne eigenes TotalRows, jede Zeile trägt bereits (aus Sicht des
// eingeladenen Kontos) Conversation.Token, das hier UNVERÄNDERT im "items[].token"-Feld landet
// (Design-Spec §4.1b: "liefert offene Einladungen inkl. Conversation.Token").
type listInvitationsOutput struct {
	Body entityListBody[fileee.Conversation]
}

// handleListInvitations implementiert GET /v1/conversations/invitations — dünner Durchgriff auf
// Conversations.PendingInvitations.
func (s *Server) handleListInvitations(ctx context.Context, in *emptyInput) (*listInvitationsOutput, error) {
	invitations, err := s.fc.Conversations.PendingInvitations(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	return &listInvitationsOutput{Body: entityListBody[fileee.Conversation]{Items: invitations, TotalRows: len(invitations)}}, nil
}

// acceptInvitationInput steuert POST /v1/conversations/invitations/accept/{token} (Pfadform-
// Begründung siehe registerConversationRoutes). Token ist der Invitation-Token aus einer vorigen
// GET /v1/conversations/invitations-Antwort (Conversation.Token) — NICHT die Conversation-ID
// (siehe AcceptInvitation-Doku, fileee/conversations.go).
type acceptInvitationInput struct {
	Token string `path:"token" doc:"Invitation-Token (Conversation.Token aus GET /v1/conversations/invitations), NICHT die Konversations-ID."`
}

// handleAcceptInvitation implementiert POST /v1/conversations/invitations/accept/{token} — dünner
// Durchgriff auf Conversations.AcceptInvitation. Kein Response-Body (204 No Content).
func (s *Server) handleAcceptInvitation(ctx context.Context, in *acceptInvitationInput) (*struct{}, error) {
	if err := s.fc.Conversations.AcceptInvitation(ctx, in.Token); err != nil {
		return nil, mapError(err)
	}
	return nil, nil
}

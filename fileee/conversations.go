package fileee

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Conversation ist eine Fileee-Konversation — der Mechanismus hinter „Mit Kontakt teilen": ein
// Dokument wird über eine Konversation mit Teilnehmern geteilt, inkl. Chat (messages) und
// Annahme-/Lesestatus. Endpunkte unter `/api/conversations/rest/...`.
type Conversation struct {
	ID                 string            `json:"id"`
	Title              string            `json:"title"`
	ConversationType   string            `json:"conversationType"`
	Kind               string            `json:"kind"`
	Participants       []Participant     `json:"participants"`
	FormerParticipants []Participant     `json:"formerParticipants"`
	Roles              map[string]string `json:"roles"`
	State              ConversationState `json:"state"`
	Invitation         bool              `json:"invitation"`
	// Token ist der Invitation-Token — NUR bei einer offenen Einladung aus Sicht des eingeladenen
	// Kontos gesetzt (leer, sobald beigetreten). Direkt an AcceptInvitation übergeben.
	Token   string `json:"token,omitempty"`
	Version int64  `json:"version"`
	Created            string            `json:"created,omitempty"`
	Modified           string            `json:"modified,omitempty"`
	// Messages sind die Chat-/System-Nachrichten der Konversation (aufsteigend). Message hält die
	// gemeinsamen Felder typisiert und das vollständige JSON in Raw.
	Messages []Message `json:"messages,omitempty"`
	// Raw enthält das vollständige Konversations-JSON (für Felder, die dieser Typ nicht modelliert).
	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON dekodiert die typisierten Felder und bewahrt zusätzlich das vollständige JSON in Raw.
func (c *Conversation) UnmarshalJSON(b []byte) error {
	type alias Conversation
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*c = Conversation(a)
	c.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// Message ist eine einzelne Nachricht einer Konversation. Type bestimmt die Bedeutung: CHAT
// (Text in Text), DOCUMENT (geteiltes Dokument in DocumentID, Remove=true beim Entfernen),
// PARTICIPANT_STATE (Teilnehmer beigetreten/entfernt), META_INFORMATION (System).
//
// ABSENDER-ZUORDNUNG (wichtig, um Verwechslungen zu vermeiden):
//   - SenderID ist der ABSOLUTE Absender (eine userId oder companyId) — die Quelle der Wahrheit,
//     WER gesendet hat, unabhängig davon, wer eingeloggt ist. Zum Anzeigen des Namens SenderID
//     gegen Conversation.Participants (Participant.ID → Name) auflösen.
//   - Direction ist RELATIV zum eingeloggten Konto: FROM_USER = das eingeloggte Konto hat gesendet,
//     TO_USER = ein anderer hat gesendet (empfangen). Dieselbe Nachricht ist also für den einen
//     Teilnehmer FROM_USER und für den anderen TO_USER — Direction sagt NICHT absolut, wer sendete.
//
// Raw enthält das vollständige Nachrichten-JSON.
type Message struct {
	ID         string          `json:"id"`
	Direction  string          `json:"direction"`
	Timestamp  string          `json:"timestamp"`
	Text       string          `json:"message"`
	Type       string          `json:"type"`
	SenderID   string          `json:"senderId"`
	SenderName string          `json:"senderName"`
	DocumentID string          `json:"documentId,omitempty"`
	Remove     bool            `json:"remove,omitempty"`
	Raw        json.RawMessage `json:"-"`
}

// UnmarshalJSON dekodiert die typisierten Felder und bewahrt das vollständige JSON in Raw.
func (m *Message) UnmarshalJSON(b []byte) error {
	type alias Message
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*m = Message(a)
	m.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// IsReply meldet, ob die Nachricht eine eingehende Text-Antwort ist — Type CHAT und Direction
// TO_USER, also von einem ANDEREN Teilnehmer als dem eingeloggten Konto gesendet (relativ zum
// eingeloggten Konto). Der typische Trigger für einen N8N-Workflow. Für die absolute Absender-
// Identität SenderID verwenden.
func (m Message) IsReply() bool { return m.Type == "CHAT" && m.Direction == "TO_USER" }

// Participant ist ein Teilnehmer einer Konversation. Invited/Joined sind Booleans (LIVE verifiziert
// 2026-07-24): Invited = eingeladen, Joined = beigetreten/angenommen. Type ist USER (Fileee-Nutzer)
// oder EXTERNAL (per E-Mail eingeladen); bei EXTERNAL trägt ExternalID die E-Mail.
type Participant struct {
	ID                      string         `json:"id"`
	Name                    string         `json:"name"`
	Type                    string         `json:"type"`
	Invited                 bool           `json:"invited"`
	Joined                  bool           `json:"joined"`
	ExternalID              string         `json:"externalId,omitempty"`
	PhoneNumber             string         `json:"phoneNumber,omitempty"`
	ConversationPermissions []string       `json:"conversationPermissions,omitempty"`
	Attributes              map[string]any `json:"attributes,omitempty"`
}

// Accepted meldet, ob der Teilnehmer die Freigabe angenommen hat (Joined true). Ein Teilnehmer mit
// Invited=true und Joined=false ist eingeladen, hat aber noch nicht angenommen.
func (p Participant) Accepted() bool { return p.Joined }

// ConversationState hält den nutzerspezifischen Zustand der Konversation: gelesen-Status, eigene
// Rolle und die IDs der in dieser Konversation geteilten Dokumente.
type ConversationState struct {
	Read              bool            `json:"read"`
	Role              string          `json:"role"`
	DateOfLastMessage string          `json:"dateOfLastMessage,omitempty"`
	SharedDocumentIDs []string        `json:"sharedDocumentIds,omitempty"`
	Permissions       json.RawMessage `json:"permissions,omitempty"`
}

// ConversationService liest Konversationen (Query/Diff/Get) und sendet Chat-Nachrichten. Weitere
// schreibende Operationen (Teilnehmer verwalten, Einladung annehmen, Dokument in den Chat teilen)
// folgen, sobald deren Request-Formate verifiziert sind.
type ConversationService interface {
	ReadService[Conversation]
	// SendMessage postet eine Text-Chatnachricht in die Konversation.
	SendMessage(ctx context.Context, conversationID, text string) (*SentMessage, error)
	// ShareDocument teilt ein bestehendes Fileee-Dokument in die Konversation.
	ShareDocument(ctx context.Context, conversationID, documentID string) (*SentMessage, error)
	// UnshareDocument entfernt ein geteiltes Dokument wieder aus der Konversation.
	UnshareDocument(ctx context.Context, conversationID, documentID string) (*SentMessage, error)
	// AddParticipant lädt einen externen Empfänger per E-Mail in die Konversation ein.
	AddParticipant(ctx context.Context, conversationID, email, role string) error
	// RemoveParticipant entfernt einen Teilnehmer (per ID) aus der Konversation.
	RemoveParticipant(ctx context.Context, conversationID, participantID string) error
	// PendingInvitations liefert die Konversationen mit einer offenen Einladung an das eigene Konto;
	// jede trägt in Conversation.Token den Invitation-Token für AcceptInvitation. LIVE verifiziert.
	PendingInvitations(ctx context.Context) ([]Conversation, error)
	// AcceptInvitation nimmt eine Einladung über ihren Invitation-Token an (POST
	// /api/conversations/invitations/:token/accept). Der Token ist Conversation.Token einer offenen
	// Einladung aus PendingInvitations — NICHT die Conversation-ID (die quittiert der Server mit 404).
	// LIVE verifiziert (End-to-End als eingeladenes Konto).
	AcceptInvitation(ctx context.Context, invitationToken string) error
}

// Konversations-Rollen für AddParticipant.
const (
	ConversationRoleViewer = "VIEWER"
	ConversationRoleEditor = "EDITOR"
	ConversationRoleAdmin  = "ADMIN"
)

// SentMessage ist die Server-Antwort auf eine gesendete Nachricht.
type SentMessage struct {
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
	MessageIndex   int    `json:"messageIndex"`
}

type conversationService struct {
	restService[Conversation]
}

func newConversationService(c *Client) ConversationService {
	return &conversationService{restService: restService[Conversation]{client: c, resourcePath: "conversations"}}
}

// chatMessageWire ist der (live verifizierte) Nachrichten-Body. Die vielen null-Felder werden
// bewusst mitgesendet (die Web-App tut das ebenso); Zeiger-Felder marshalln als JSON null.
type chatMessageWire struct {
	ID             string         `json:"id"`
	Direction      string         `json:"direction"`
	Timestamp      string         `json:"timestamp"`
	Message        string         `json:"message"`
	Important      bool           `json:"important"`
	I18nDictionary map[string]any `json:"i18nDictionary"`
	SenderName     *string        `json:"senderName"`
	SourceModule   *string        `json:"sourceModule"`
	Processed      bool           `json:"processed"`
	SenderID       string         `json:"senderId"`
	OnlyVisibleFor *string        `json:"onlyVisibleFor"`
	Type           string         `json:"type"`
	Attributes     *string        `json:"attributes"`
	Channel        *string        `json:"channel"`
	Attachments    []any          `json:"attachments"`
	ContentType    *string        `json:"contentType"`
}

type sendMessageWire struct {
	Message    chatMessageWire `json:"message"`
	LocalState struct {
		LastMessageID string `json:"lastMessageId"`
	} `json:"localState"`
}

// SendMessage postet eine Text-Chatnachricht (type CHAT, direction FROM_USER) in die Konversation.
// senderId wird aus der eigenen Session (Client.UserID) bestimmt; localState.lastMessageId aus der
// zuletzt geladenen Nachricht der Konversation. Live verifiziert (POST /api/conversations/rest/:id/
// message, 2026-07-24).
func (s *conversationService) SendMessage(ctx context.Context, conversationID, text string) (*SentMessage, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	senderID, err := s.client.UserID(ctx)
	if err != nil {
		return nil, err
	}
	conv, err := s.Get(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	id, err := newObjectID()
	if err != nil {
		return nil, err
	}
	body := sendMessageWire{Message: chatMessageWire{
		ID:             id,
		Direction:      "FROM_USER",
		Timestamp:      s.client.auth.nowFn().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Message:        text,
		I18nDictionary: map[string]any{},
		SenderID:       senderID,
		Type:           "CHAT",
		Attachments:    []any{},
	}}
	body.LocalState.LastMessageID = lastMessageID(conv)
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("fileee: send message encode: %w", err)
	}
	resp, err := s.client.postJSON(ctx, "/api/conversations/rest/"+conversationID+"/message", raw)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: send message read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}
	var out SentMessage
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("fileee: send message decode: %w", err)
	}
	return &out, nil
}

// documentMessageWire ist eine DOCUMENT-Nachricht: teilt (remove=false) bzw. entfernt (remove=true)
// ein bestehendes Fileee-Dokument in/aus einer Konversation. Feldstruktur aus Live-Traffic belegt.
type documentMessageWire struct {
	ID                  string         `json:"id"`
	Direction           string         `json:"direction"`
	Timestamp           string         `json:"timestamp"`
	Message             *string        `json:"message"`
	Important           bool           `json:"important"`
	I18nDictionary      map[string]any `json:"i18nDictionary"`
	SenderName          *string        `json:"senderName"`
	SourceModule        *string        `json:"sourceModule"`
	Processed           bool           `json:"processed"`
	SenderID            string         `json:"senderId"`
	OnlyVisibleFor      *string        `json:"onlyVisibleFor"`
	Type                string         `json:"type"`
	Attributes          *string        `json:"attributes"`
	Channel             *string        `json:"channel"`
	Attachments         []any          `json:"attachments"`
	DocumentID          string         `json:"documentId"`
	DocumentType        *string        `json:"documentType"`
	Identifier          *string        `json:"identifier"`
	SubIdentifier       *string        `json:"subIdentifier"`
	Remove              bool           `json:"remove"`
	Status              *string        `json:"status"`
	DontShare           bool           `json:"dontShare"`
	UnsharedIdentifiers []string       `json:"unsharedIdentifiers"`
	ShareIndex          *int           `json:"shareIndex"`
	ShareTotal          *int           `json:"shareTotal"`
	Uploaded            *bool          `json:"uploaded"`
}

type sendDocumentWire struct {
	Message    documentMessageWire `json:"message"`
	LocalState struct {
		LastMessageID string `json:"lastMessageId"`
	} `json:"localState"`
}

// ShareDocument teilt ein bereits in Fileee existierendes Dokument in die Konversation (DOCUMENT-
// Nachricht, remove=false).
func (s *conversationService) ShareDocument(ctx context.Context, conversationID, documentID string) (*SentMessage, error) {
	return s.docMessage(ctx, conversationID, documentID, false)
}

// UnshareDocument entfernt ein zuvor geteiltes Dokument wieder aus der Konversation (DOCUMENT-
// Nachricht, remove=true).
func (s *conversationService) UnshareDocument(ctx context.Context, conversationID, documentID string) (*SentMessage, error) {
	return s.docMessage(ctx, conversationID, documentID, true)
}

func (s *conversationService) docMessage(ctx context.Context, conversationID, documentID string, remove bool) (*SentMessage, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	senderID, err := s.client.UserID(ctx)
	if err != nil {
		return nil, err
	}
	conv, err := s.Get(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	id, err := newObjectID()
	if err != nil {
		return nil, err
	}
	body := sendDocumentWire{Message: documentMessageWire{
		ID:             id,
		Direction:      "FROM_USER",
		Timestamp:      s.client.auth.nowFn().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		I18nDictionary: map[string]any{},
		SenderID:       senderID,
		Type:           "DOCUMENT",
		Attachments:    []any{},
		DocumentID:     documentID,
		Remove:         remove,
	}}
	body.LocalState.LastMessageID = lastMessageID(conv)
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("fileee: doc message encode: %w", err)
	}
	resp, err := s.client.postJSON(ctx, "/api/conversations/rest/"+conversationID+"/message", raw)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: doc message read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}
	var out SentMessage
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("fileee: doc message decode: %w", err)
	}
	return &out, nil
}

// lastMessageID liefert die id der letzten Nachricht einer Konversation (leer, wenn keine).
func lastMessageID(c *Conversation) string {
	if len(c.Messages) == 0 {
		return ""
	}
	return c.Messages[len(c.Messages)-1].ID
}

// addParticipantsWire / addParticipantWire: Body von POST …/participants/add (Live belegt).
type addParticipantWire struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Role        string         `json:"role"`
	Kind        *string        `json:"kind"`
	PhoneNumber string         `json:"phoneNumber"`
	ExternalID  *string        `json:"externalId"`
	Joined      bool           `json:"joined"`
	Invited     bool           `json:"invited"`
	Attributes  map[string]any `json:"attributes"`
}

type addParticipantsWire struct {
	Participants          []addParticipantWire `json:"participants"`
	ResendInvitationEmail bool                 `json:"resendInvitationEmail"`
}

// AddParticipant lädt einen externen Empfänger per E-Mail (type EXTERNAL) mit der gegebenen Rolle
// (ConversationRole*) in die Konversation ein.
func (s *conversationService) AddParticipant(ctx context.Context, conversationID, email, role string) error {
	if err := s.client.EnsureSession(ctx); err != nil {
		return err
	}
	body := addParticipantsWire{Participants: []addParticipantWire{{
		ID: email, Name: email, Type: "EXTERNAL", Role: role,
		PhoneNumber: "", Joined: true, Invited: false, Attributes: map[string]any{},
	}}}
	return s.postParticipants(ctx, conversationID, "add", body)
}

// removeParticipantWire / removeParticipantsWire: Body von POST …/participants/remove (Live belegt).
type removeParticipantWire struct {
	ID                      string         `json:"id"`
	Name                    string         `json:"name"`
	Type                    string         `json:"type"`
	PhoneNumber             string         `json:"phoneNumber"`
	Joined                  bool           `json:"joined"`
	Invited                 bool           `json:"invited"`
	ConversationPermissions []string       `json:"conversationPermissions"`
	Attributes              map[string]any `json:"attributes"`
}

type removeParticipantsWire struct {
	Participants  []removeParticipantWire `json:"participants"`
	KeepDocuments bool                    `json:"keepDocuments"`
	KeepHistory   bool                    `json:"keepHistory"`
}

// RemoveParticipant entfernt den Teilnehmer mit der gegebenen ID aus der Konversation (das volle
// Teilnehmer-Objekt wird dazu aus der Konversation geladen).
func (s *conversationService) RemoveParticipant(ctx context.Context, conversationID, participantID string) error {
	if err := s.client.EnsureSession(ctx); err != nil {
		return err
	}
	conv, err := s.Get(ctx, conversationID)
	if err != nil {
		return err
	}
	var found *Participant
	for i := range conv.Participants {
		if conv.Participants[i].ID == participantID {
			found = &conv.Participants[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("fileee: participant %q not in conversation: %w", participantID, ErrNotFound)
	}
	attrs := found.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}
	body := removeParticipantsWire{Participants: []removeParticipantWire{{
		ID: found.ID, Name: found.Name, Type: found.Type, PhoneNumber: found.PhoneNumber,
		Joined: found.Joined, Invited: found.Invited,
		ConversationPermissions: found.ConversationPermissions, Attributes: attrs,
	}}}
	return s.postParticipants(ctx, conversationID, "remove", body)
}

// PendingInvitations liefert die Konversationen, zu denen eine offene Einladung an das eigene Konto
// vorliegt (Konversationsfeld invitation=true).
func (s *conversationService) PendingInvitations(ctx context.Context) ([]Conversation, error) {
	res, err := s.Diff(ctx, NewCursor("Conversation"))
	if err != nil {
		return nil, err
	}
	var out []Conversation
	for _, conv := range res.Rows {
		if conv.Invitation {
			out = append(out, conv)
		}
	}
	return out, nil
}

// acceptInvitationWire ist der (live belegte) Body von …/invitations/:token/accept — der Token
// steht zusätzlich im Body, acceptToS bestätigt ggf. die Nutzungsbedingungen (invitationIsToS).
type acceptInvitationWire struct {
	Token     string `json:"token"`
	AcceptToS bool   `json:"acceptToS"`
}

// AcceptInvitation nimmt eine Einladung über ihren Invitation-Token an
// (POST /api/conversations/invitations/:token/accept). Der Token ist Conversation.Token einer
// offenen Einladung (PendingInvitations), NICHT die Conversation-ID. LIVE end-to-end verifiziert.
func (s *conversationService) AcceptInvitation(ctx context.Context, invitationToken string) error {
	if err := s.client.EnsureSession(ctx); err != nil {
		return err
	}
	body, err := json.Marshal(acceptInvitationWire{Token: invitationToken, AcceptToS: false})
	if err != nil {
		return fmt.Errorf("fileee: accept invitation encode: %w", err)
	}
	resp, err := s.client.postJSON(ctx, "/api/conversations/invitations/"+invitationToken+"/accept", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("fileee: accept invitation read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return parseAPIError(resp.StatusCode, respBody)
	}
	return nil
}

// postParticipants sendet einen participants/add- bzw. -remove-Request und prüft den Status.
func (s *conversationService) postParticipants(ctx context.Context, conversationID, action string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("fileee: participants %s encode: %w", action, err)
	}
	resp, err := s.client.postJSON(ctx, "/api/conversations/"+conversationID+"/participants/"+action, raw)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("fileee: participants %s read: %w", action, err)
	}
	if resp.StatusCode != http.StatusOK {
		return parseAPIError(resp.StatusCode, respBody)
	}
	return nil
}

// Conversations liefert alle Konversationen, in denen das Dokument geteilt ist (Filter über
// ConversationState.SharedDocumentIDs). Damit sieht der Aufrufer, mit wem ein Dokument geteilt ist
// und ob die Empfänger angenommen haben (Participant.Accepted). Die anonymen Link-Freigaben stehen
// separat in Document.ShareInformation.ShareIDs.
func (s *DocumentService) Conversations(ctx context.Context, documentID string) ([]Conversation, error) {
	res, err := s.client.Conversations.Diff(ctx, NewCursor("Conversation"))
	if err != nil {
		return nil, err
	}
	var out []Conversation
	for _, conv := range res.Rows {
		for _, id := range conv.State.SharedDocumentIDs {
			if id == documentID {
				out = append(out, conv)
				break
			}
		}
	}
	return out, nil
}

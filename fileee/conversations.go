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
	Version            int64             `json:"version"`
	Created            string            `json:"created,omitempty"`
	Modified           string            `json:"modified,omitempty"`
	// Messages bleiben roh: die Nachrichtenstruktur variiert (Text, geteilte Dokumente,
	// System-Ereignisse) und wird bei Bedarf vom Aufrufer dekodiert.
	Messages []json.RawMessage `json:"messages,omitempty"`
}

// Participant ist ein Teilnehmer einer Konversation. Invited = Zeitpunkt der Einladung, Joined =
// Zeitpunkt der Annahme. Ist Joined leer, hat der Teilnehmer die Freigabe noch nicht angenommen.
type Participant struct {
	ID                      string          `json:"id"`
	Name                    string          `json:"name"`
	Type                    string          `json:"type"`
	Invited                 string          `json:"invited"`
	Joined                  string          `json:"joined"`
	ConversationPermissions json.RawMessage `json:"conversationPermissions,omitempty"`
}

// Accepted meldet, ob der Teilnehmer die Freigabe angenommen hat (Joined gesetzt).
func (p Participant) Accepted() bool { return p.Joined != "" }

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
}

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

// lastMessageID liefert die id der letzten Nachricht einer Konversation (leer, wenn keine).
func lastMessageID(c *Conversation) string {
	if len(c.Messages) == 0 {
		return ""
	}
	var m struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(c.Messages[len(c.Messages)-1], &m)
	return m.ID
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

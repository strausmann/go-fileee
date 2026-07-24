package fileee

import (
	"context"
	"encoding/json"
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

// ConversationService liest Konversationen (Query/Diff/Get). Die schreibenden Operationen
// (Nachricht senden, Teilnehmer verwalten, Einladung annehmen) folgen, sobald die Request-Formate
// verifiziert sind.
type ConversationService interface {
	ReadService[Conversation]
}

type conversationService struct {
	restService[Conversation]
}

func newConversationService(c *Client) ConversationService {
	return &conversationService{restService: restService[Conversation]{client: c, resourcePath: "conversations"}}
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

package fileee

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// contactService implementiert WriteService[Contact]: Query/Diff/Get kommen von restService[Contact]
// (Embedding), Create/Update sind handschriftlich (Umbrella-Spec §10.3 E — Wire-Form nicht direkt
// beobachtet, folgt dem generischen REST-Muster, live vor erstem produktiven Write verifizieren).
type contactService struct {
	restService[Contact]
}

func newContactService(c *Client) WriteService[Contact] {
	return &contactService{restService: restService[Contact]{client: c, resourcePath: "contacts"}}
}

// contactCreateWire spiegelt ContactCreateRequest (openapi.json + API.md §4.6 "Body-Felder").
// firstName/lastName sind laut §10.3 E nicht im beobachteten POST-Body bestätigt, werden hier
// aber mitgesendet (best-effort). LIVE VERIFIZIERT (2026-07-23, Testkonto): der Server lehnt
// einen Create-Request ohne contactStatus/connectedToOtherUser/fromUserDb/documentCounter/
// deleted/version MIT 400 ab (leerer Fehler-Body) — diese Felder sind entgegen der ursprünglichen
// Annahme (nur die hier zuvor vorhandene Teilmenge) serverseitig PFLICHT, nicht optional.
type contactCreateWire struct {
	ID                   string        `json:"id"`
	CompanyID            string        `json:"companyId,omitempty"`
	CompanyName          string        `json:"companyName"`
	FirstName            string        `json:"firstName,omitempty"`
	LastName             string        `json:"lastName,omitempty"`
	Email                string        `json:"email,omitempty"`
	PhoneNumber          string        `json:"phoneNumber,omitempty"`
	URL                  string        `json:"url,omitempty"`
	Address              Address       `json:"address"`
	ContactType          ContactType   `json:"contactType,omitempty"`
	ContactStatus        ContactStatus `json:"contactStatus"`
	ConnectedToOtherUser bool          `json:"connectedToOtherUser"`
	FromUserDB           bool          `json:"fromUserDb"`
	DocumentCounter      int           `json:"documentCounter"`
	Deleted              bool          `json:"deleted"`
	Version              int64         `json:"version"`
}

// Create legt einen Kontakt an; fehlt entity.ID, wird eine ObjectId erzeugt.
func (s *contactService) Create(ctx context.Context, entity *Contact) (*Contact, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	id := entity.ID
	if id == "" {
		var err error
		id, err = newObjectID()
		if err != nil {
			return nil, err
		}
	}
	// contactStatus ist serverseitig Pflicht (siehe Kommentar contactCreateWire) — CUSTOM ist der
	// Default für einen manuell angelegten, nicht mit einem anderen Nutzer verknüpften Kontakt
	// (API.md §6.5), falls der Aufrufer keinen Status vorgibt.
	status := entity.ContactStatus
	if status == "" {
		status = ContactStatusCustom
	}
	wire := contactCreateWire{
		ID: id, CompanyID: entity.CompanyID, CompanyName: entity.CompanyName,
		FirstName: entity.FirstName, LastName: entity.LastName, Email: entity.Email,
		PhoneNumber: entity.PhoneNumber, URL: entity.URL, Address: entity.Address,
		ContactType: entity.ContactType, ContactStatus: status,
		ConnectedToOtherUser: entity.ConnectedToOtherUser, FromUserDB: entity.FromUserDB,
		DocumentCounter: entity.DocumentCounter, Deleted: entity.Deleted, Version: entity.Version,
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("fileee: contact create encode: %w", err)
	}
	resp, err := s.client.postJSON(ctx, "/api/contacts/rest", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: contact create read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}
	var created Contact
	if err := json.Unmarshal(respBody, &created); err != nil {
		return nil, fmt.Errorf("fileee: contact create decode: %w", err)
	}
	return &created, nil
}

// Update speichert Änderungen an einem bestehenden Kontakt.
func (s *contactService) Update(ctx context.Context, entity *Contact) (*Contact, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	body, err := json.Marshal(entity)
	if err != nil {
		return nil, fmt.Errorf("fileee: contact update encode: %w", err)
	}
	resp, err := s.client.putJSON(ctx, "/api/contacts/rest/"+entity.ID, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: contact update read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}
	var updated Contact
	if err := json.Unmarshal(respBody, &updated); err != nil {
		return nil, fmt.Errorf("fileee: contact update decode: %w", err)
	}
	return &updated, nil
}

// Delete löscht einen Kontakt unwiderruflich (Hard-DELETE, DELETE /api/contacts/rest/:id) — es
// gibt serverseitig keinen Papierkorb/Undo für diese Operation. Die Lib bietet Delete bewusst als
// geguardete Opt-in-Methode an (ADR-0007/ADR-0008); der fileee-server registriert die zugehörige
// HTTP-Route nur, wenn beim Start FILEEE_ALLOW_DESTRUCTIVE gesetzt ist. Ein fehlender Kontakt
// liefert ErrNotFound (per errors.Is prüfbar), jeder andere Fehlerstatus ein *APIError.
func (s *contactService) Delete(ctx context.Context, id string) error {
	return s.restService.delete(ctx, id)
}

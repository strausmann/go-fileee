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

// contactCreateWire spiegelt ContactCreateRequest (openapi.json) — firstName/lastName sind laut
// §10.3 E nicht im beobachteten POST-Body bestätigt, werden hier aber mitgesendet (best-effort,
// Server ignoriert unbekannte Felder erfahrungsgemäß, statt sie abzulehnen).
type contactCreateWire struct {
	ID          string      `json:"id"`
	CompanyID   string      `json:"companyId,omitempty"`
	CompanyName string      `json:"companyName,omitempty"`
	FirstName   string      `json:"firstName,omitempty"`
	LastName    string      `json:"lastName,omitempty"`
	Email       string      `json:"email,omitempty"`
	PhoneNumber string      `json:"phoneNumber,omitempty"`
	URL         string      `json:"url,omitempty"`
	Address     Address     `json:"address"`
	ContactType ContactType `json:"contactType,omitempty"`
}

func (s *contactService) Create(ctx context.Context, entity *Contact) (*Contact, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	id := entity.ID
	if id == "" {
		id = newObjectID()
	}
	wire := contactCreateWire{
		ID: id, CompanyID: entity.CompanyID, CompanyName: entity.CompanyName,
		FirstName: entity.FirstName, LastName: entity.LastName, Email: entity.Email,
		PhoneNumber: entity.PhoneNumber, URL: entity.URL, Address: entity.Address,
		ContactType: entity.ContactType,
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

package fileee

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Reminder ist eine an ein Dokument gebundene Frist/Erinnerung (`/api/reminders/rest`).
// StartDate ist ein reines Datum im Format YYYY-MM-DD. Created und Modified werden vom Server
// gesetzt und beim Anlegen nicht mitgesendet.
type Reminder struct {
	ID                  string `json:"id"`
	Description         string `json:"description"`
	DetailedDescription string `json:"detailedDescription"`
	DocumentID          string `json:"documentId"`
	StartDate           string `json:"startDate"`
	Done                bool   `json:"done"`
	Deleted             bool   `json:"deleted"`
	Version             int64  `json:"version"`
	Created             string `json:"created,omitempty"`
	Modified            string `json:"modified,omitempty"`
}

// ReminderService liest, legt an, aktualisiert und löscht Erinnerungen. Delete ist ein
// unwiderruflicher Hard-DELETE — bewusst geguardet angeboten (ADR-0007/ADR-0008), nicht
// automatisch server-seitig freigeschaltet.
type ReminderService interface {
	ReadService[Reminder]
	Create(ctx context.Context, r *Reminder) (*Reminder, error)
	// Update speichert Änderungen an einer bestehenden Erinnerung.
	Update(ctx context.Context, r *Reminder) (*Reminder, error)
	// Delete löscht eine Erinnerung unwiderruflich anhand ihrer ID (kein serverseitiger Papierkorb).
	Delete(ctx context.Context, id string) error
}

type reminderService struct {
	restService[Reminder]
}

func newReminderService(c *Client) ReminderService {
	return &reminderService{restService: restService[Reminder]{client: c, resourcePath: "reminders"}}
}

// reminderCreateWire ist der Anlege-Body. Er lässt Created/Modified bewusst weg: schickt man sie
// mit, antwortet der Server mit 500.
type reminderCreateWire struct {
	ID                  string `json:"id"`
	Description         string `json:"description"`
	DetailedDescription string `json:"detailedDescription"`
	DocumentID          string `json:"documentId"`
	StartDate           string `json:"startDate"`
	Done                bool   `json:"done"`
	Deleted             bool   `json:"deleted"`
	Version             int64  `json:"version"`
}

// Create legt eine Erinnerung an. Fehlt r.ID, wird eine client-seitige ObjectId erzeugt.
func (s *reminderService) Create(ctx context.Context, r *Reminder) (*Reminder, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	id := r.ID
	if id == "" {
		var err error
		if id, err = newObjectID(); err != nil {
			return nil, err
		}
	}
	body, err := json.Marshal(reminderCreateWire{
		ID:                  id,
		Description:         r.Description,
		DetailedDescription: r.DetailedDescription,
		DocumentID:          r.DocumentID,
		StartDate:           r.StartDate,
		Done:                r.Done,
		Deleted:             r.Deleted,
		Version:             r.Version,
	})
	if err != nil {
		return nil, fmt.Errorf("fileee: reminder create encode: %w", err)
	}
	resp, err := s.client.postJSON(ctx, "/api/reminders/rest", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: reminder create read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}
	var created Reminder
	if err := json.Unmarshal(respBody, &created); err != nil {
		return nil, fmt.Errorf("fileee: reminder create decode: %w", err)
	}
	return &created, nil
}

// reminderUpdateWire ist der Aktualisierungs-Body — analog zu reminderCreateWire lässt er
// Created/Modified bewusst weg. Grund: ein realistischer Update-Aufruf lädt zuerst per Get einen
// Reminder (Created/Modified dabei vom Server befüllt) und schickt ihn danach an Update zurück —
// ein naives json.Marshal(r) des öffentlichen Reminder-Structs (dessen Created/Modified nur
// `omitempty` sind, nicht generell ausgeschlossen) würde diese Felder dann unbeabsichtigt erneut
// mitsenden, exakt die Feld-Kombination, an der das Create-Pendant nachweislich mit 500
// antwortet (siehe reminderCreateWire-Kommentar). Bis das exakte Update-Wire-Format live gegen
// den echten Server verifiziert ist, mirrort dieser Typ defensiv dasselbe Muster wie Create
// (Whole-Codebase-Review Finding I3).
type reminderUpdateWire struct {
	ID                  string `json:"id"`
	Description         string `json:"description"`
	DetailedDescription string `json:"detailedDescription"`
	DocumentID          string `json:"documentId"`
	StartDate           string `json:"startDate"`
	Done                bool   `json:"done"`
	Deleted             bool   `json:"deleted"`
	Version             int64  `json:"version"`
}

// Update speichert Änderungen an einer bestehenden Erinnerung (PUT /api/reminders/rest/:id,
// analog zu Contacts.Update). Das exakte Update-Wire-Format ist wie bei Contacts.Update nicht
// separat code-belegt, folgt aber demselben generischen REST-Muster wie Documents.Update/
// Contacts.Update — MIT der Ausnahme, dass Created/Modified bewusst weggelassen werden
// (reminderUpdateWire, Finding I3).
func (s *reminderService) Update(ctx context.Context, r *Reminder) (*Reminder, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	body, err := json.Marshal(reminderUpdateWire{
		ID:                  r.ID,
		Description:         r.Description,
		DetailedDescription: r.DetailedDescription,
		DocumentID:          r.DocumentID,
		StartDate:           r.StartDate,
		Done:                r.Done,
		Deleted:             r.Deleted,
		Version:             r.Version,
	})
	if err != nil {
		return nil, fmt.Errorf("fileee: reminder update encode: %w", err)
	}
	resp, err := s.client.putJSON(ctx, "/api/reminders/rest/"+r.ID, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: reminder update read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, respBody)
	}
	var updated Reminder
	if err := json.Unmarshal(respBody, &updated); err != nil {
		return nil, fmt.Errorf("fileee: reminder update decode: %w", err)
	}
	return &updated, nil
}

// Delete löscht eine Erinnerung unwiderruflich (Hard-DELETE, DELETE /api/reminders/rest/:id) — es
// gibt serverseitig keinen Papierkorb/Undo für diese Operation. Die Lib bietet Delete bewusst als
// geguardete Opt-in-Methode an (ADR-0007/ADR-0008); der fileee-server registriert die zugehörige
// HTTP-Route nur, wenn beim Start FILEEE_ALLOW_DESTRUCTIVE gesetzt ist. Eine fehlende Erinnerung
// liefert ErrNotFound (per errors.Is prüfbar), jeder andere Fehlerstatus ein *APIError.
func (s *reminderService) Delete(ctx context.Context, id string) error {
	return s.restService.delete(ctx, id)
}

package fileee

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestReminders_Create_WireForm(t *testing.T) {
	var captured map[string]any
	srv := newMockJSONCaptureServer(t, ensureSessionRoutes(),
		"POST", "/api/reminders/rest",
		func(raw []byte) (int, []byte) {
			_ = json.Unmarshal(raw, &captured)
			return 200, []byte(`{"id":"r1","description":"Frist","documentId":"d1","startDate":"2026-08-24","done":false,"deleted":false,"version":0,"created":"2026-07-24T00:00:00.000Z","modified":"2026-07-24T00:00:00.000Z"}`)
		})
	c := newTestClientAgainstMockServer(t, srv)

	r, err := c.Reminders.Create(context.Background(), &Reminder{
		Description: "Frist", DocumentID: "d1", StartDate: "2026-08-24",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.ID != "r1" || r.Created == "" {
		t.Fatalf("Response nicht dekodiert: %+v", r)
	}
	if captured["id"] == "" || captured["id"] == nil {
		t.Error("id muss client-generiert mitgesendet werden")
	}
	if captured["startDate"] != "2026-08-24" {
		t.Errorf("startDate = %v, erwartet bare Date 2026-08-24", captured["startDate"])
	}
	if _, ok := captured["created"]; ok {
		t.Error("created darf NICHT gesendet werden (setzt der Server)")
	}
	if _, ok := captured["modified"]; ok {
		t.Error("modified darf NICHT gesendet werden (setzt der Server)")
	}
	for _, k := range []string{"description", "documentId", "done", "deleted", "version"} {
		if _, ok := captured[k]; !ok {
			t.Errorf("Pflichtfeld %q fehlt im Request", k)
		}
	}
}

func TestReminders_Create_ServerError(t *testing.T) {
	srv := newMockJSONCaptureServer(t, ensureSessionRoutes(),
		"POST", "/api/reminders/rest",
		func(raw []byte) (int, []byte) {
			return 500, []byte(`{"apiError":"boom"}`)
		})
	c := newTestClientAgainstMockServer(t, srv)
	_, err := c.Reminders.Create(context.Background(), &Reminder{Description: "x", DocumentID: "d1", StartDate: "2026-08-24"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("erwartet *APIError, bekam %T: %v", err, err)
	}
}

// TestReminders_Update_OmitsCreatedModified ist der Regressionstest für Whole-Codebase-Review
// Finding I3: Update() marshallte bisher das öffentliche Reminder-Struct direkt (json.Marshal(r)),
// wodurch nicht-leere Created/Modified-Felder unbeabsichtigt mitgesendet wurden — exakt die
// Feld-Kombination, an der das Create-Pendant nachweislich mit 500 scheitert (siehe
// reminderCreateWire-Kommentar in reminders.go). Der realistische Aufrufpfad ist Get (liefert
// Created/Modified befüllt) gefolgt von Update() mit demselben, unveränderten Reminder — genau
// das wird hier simuliert.
func TestReminders_Update_OmitsCreatedModified(t *testing.T) {
	var captured map[string]any
	srv := newMockJSONCaptureServer(t, ensureSessionRoutes(),
		"PUT", "/api/reminders/rest/r1",
		func(raw []byte) (int, []byte) {
			_ = json.Unmarshal(raw, &captured)
			return 200, []byte(`{"id":"r1","description":"Neu","documentId":"d1","startDate":"2026-09-01","version":1}`)
		})
	c := newTestClientAgainstMockServer(t, srv)

	// Get-geformter Reminder: Created/Modified sind vom Server befüllt (nicht leer).
	r := &Reminder{
		ID: "r1", Description: "Neu", DocumentID: "d1", StartDate: "2026-09-01", Version: 0,
		Created: "2026-07-01T00:00:00.000Z", Modified: "2026-07-20T00:00:00.000Z",
	}
	if _, err := c.Reminders.Update(context.Background(), r); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if captured == nil {
		t.Fatal("Mock-Handler wurde nicht aufgerufen — kein Request-Body eingefangen")
	}
	if _, ok := captured["created"]; ok {
		t.Errorf("created darf NICHT im Update-Request landen (siehe reminderCreateWire-500-Bug), Body: %+v", captured)
	}
	if _, ok := captured["modified"]; ok {
		t.Errorf("modified darf NICHT im Update-Request landen (siehe reminderCreateWire-500-Bug), Body: %+v", captured)
	}
	if captured["id"] != "r1" {
		t.Errorf("id = %v, erwartet r1", captured["id"])
	}
	if captured["description"] != "Neu" {
		t.Errorf("description = %v, erwartet Neu", captured["description"])
	}
}

// TestReminders_Create_NetworkError schließt die in Finding I4 vermisste Lücke: Happy-Path
// (TestReminders_Create_WireForm) und Error-Path (TestReminders_Create_ServerError) existieren
// bereits, aber kein Network-Error-Test — anders als bei Update/Delete in dieser Datei.
func TestReminders_Create_NetworkError(t *testing.T) {
	client, err := NewClient(
		Credentials{Username: "test@example.invalid", Password: "test-pw"},
		WithBaseURL("http://127.0.0.1:1"),
		WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = client.Reminders.Create(context.Background(), &Reminder{Description: "x", DocumentID: "d1", StartDate: "2026-08-24"})
	if err == nil {
		t.Fatalf("erwartet Network-Error, bekommen nil")
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("Network-Error darf kein *APIError sein, bekommen %v", apiErr)
	}
}

// TestReminderServiceUpdateHappyErrorNetwork deckt Update() als Mutation-Funktion vollständig ab
// (Test-Coverage-Pflicht): Happy-Path (200, dekodierte Reminder), Error-Path (422 -> gewrapptes
// *APIError) und Network-Error. Analog zu TestContactServiceUpdateHappyErrorNetwork.
func TestReminderServiceUpdateHappyErrorNetwork(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"PUT /api/reminders/rest/r1": {Status: 200, Body: []byte(`{"id":"r1","description":"Neue Frist","documentId":"d1","startDate":"2026-09-01","version":1}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		updated, err := client.Reminders.Update(context.Background(), &Reminder{ID: "r1", Description: "Neue Frist", DocumentID: "d1", StartDate: "2026-09-01", Version: 0})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.ID != "r1" || updated.Description != "Neue Frist" || updated.Version != 1 {
			t.Fatalf("Update-Ergebnis falsch: %+v", updated)
		}
	})

	t.Run("error path 422", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"PUT /api/reminders/rest/r1": {Status: 422, Body: []byte(`{"errorCode":"INVALID_UPDATE","errorMessage":"ungültige Änderung"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		_, err := client.Reminders.Update(context.Background(), &Reminder{ID: "r1"})
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("erwartet gewrapptes *APIError, bekommen %T: %v", err, err)
		}
		if apiErr.HTTPStatus != 422 {
			t.Fatalf("erwarteter HTTPStatus 422, bekommen %d", apiErr.HTTPStatus)
		}
	})

	t.Run("network error", func(t *testing.T) {
		client, err := NewClient(
			Credentials{Username: "test@example.invalid", Password: "test-pw"},
			WithBaseURL("http://127.0.0.1:1"),
			WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		)
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		_, err = client.Reminders.Update(context.Background(), &Reminder{ID: "r1"})
		if err == nil {
			t.Fatalf("erwartet Network-Error, bekommen nil")
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("Network-Error darf kein *APIError sein, bekommen %v", apiErr)
		}
	})
}

// TestReminderServiceDeleteHappyErrorNetwork deckt Delete() als Mutation-Funktion vollständig ab
// (Test-Coverage-Pflicht): Happy-Path (200 -> nil), Error-Path (404 -> ErrNotFound per errors.Is)
// und Network-Error. Delete ist ein Hard-DELETE — ADR-0007/ADR-0008 dokumentieren, warum die Lib
// diese Methode trotzdem bewusst (geguardet) anbietet.
func TestReminderServiceDeleteHappyErrorNetwork(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"DELETE /api/reminders/rest/r1": {Status: 200},
		})
		client := newTestClientAgainstMock(t, routes)
		if err := client.Reminders.Delete(context.Background(), "r1"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})

	t.Run("error path 404 -> ErrNotFound", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"DELETE /api/reminders/rest/unbekannt": {Status: 404, Body: []byte(`{"errorCode":"NOT_FOUND"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		err := client.Reminders.Delete(context.Background(), "unbekannt")
		if !errorsIsNotFound(err) {
			t.Fatalf("erwartet ErrNotFound, bekommen %v", err)
		}
	})

	t.Run("network error", func(t *testing.T) {
		client, err := NewClient(
			Credentials{Username: "test@example.invalid", Password: "test-pw"},
			WithBaseURL("http://127.0.0.1:1"),
			WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		)
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		err = client.Reminders.Delete(context.Background(), "r1")
		if err == nil {
			t.Fatalf("erwartet Network-Error, bekommen nil")
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("Network-Error darf kein *APIError sein, bekommen %v", apiErr)
		}
	})
}

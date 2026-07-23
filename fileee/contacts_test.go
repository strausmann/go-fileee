package fileee

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestContactServiceCreateHappyErrorNetwork(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"POST /api/contacts/rest": {Status: 200, Body: []byte(`{"id":"contact-1","firstName":"Max","lastName":"Testmann"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		created, err := client.Contacts.Create(context.Background(), &Contact{FirstName: "Max", LastName: "Testmann"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if created.ID != "contact-1" {
			t.Fatalf("Create-Ergebnis falsch: %+v", created)
		}
	})

	t.Run("error path 400", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"POST /api/contacts/rest": {Status: 400, Body: []byte(`{"errorCode":"INVALID_CONTACT"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		_, err := client.Contacts.Create(context.Background(), &Contact{})
		if err == nil {
			t.Fatalf("erwartet Fehler bei 400, bekommen nil")
		}
	})

	t.Run("network error", func(t *testing.T) {
		client, err := New(
			Credentials{Username: "test@example.invalid", Password: "test-pw"},
			WithBaseURL("http://127.0.0.1:1"),
			WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = client.Contacts.Create(context.Background(), &Contact{})
		if err == nil {
			t.Fatalf("erwartet Network-Error, bekommen nil")
		}
	})
}

// TestContactServiceUpdateHappyErrorNetwork deckt Update() analog zu Create() vollständig ab
// (Mutation-Funktion -> Happy+Error+Network ist laut Test-Coverage-Pflicht verbindlich):
// Happy-Path (200, dekodierter Contact), Error-Path (4xx -> gewrapptes *APIError) und
// Network-Error (unerreichbarer Host -> kein *APIError, sondern Transport-Fehler).
func TestContactServiceUpdateHappyErrorNetwork(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"PUT /api/contacts/rest/contact-1": {Status: 200, Body: []byte(`{"id":"contact-1","firstName":"Neu"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		updated, err := client.Contacts.Update(context.Background(), &Contact{ID: "contact-1", FirstName: "Neu"})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.ID != "contact-1" || updated.FirstName != "Neu" {
			t.Fatalf("Update-Ergebnis falsch: %+v", updated)
		}
	})

	t.Run("error path 422", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"PUT /api/contacts/rest/contact-1": {Status: 422, Body: []byte(`{"errorCode":"INVALID_UPDATE","errorMessage":"ungültige Änderung"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		_, err := client.Contacts.Update(context.Background(), &Contact{ID: "contact-1"})
		if err == nil {
			t.Fatalf("erwartet Fehler bei 422, bekommen nil")
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("erwartet gewrapptes *APIError, bekommen %T: %v", err, err)
		}
		if apiErr.HTTPStatus != 422 {
			t.Fatalf("erwarteter HTTPStatus 422, bekommen %d", apiErr.HTTPStatus)
		}
	})

	t.Run("network error", func(t *testing.T) {
		client, err := New(
			Credentials{Username: "test@example.invalid", Password: "test-pw"},
			WithBaseURL("http://127.0.0.1:1"),
			WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = client.Contacts.Update(context.Background(), &Contact{ID: "contact-1"})
		if err == nil {
			t.Fatalf("erwartet Network-Error, bekommen nil")
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("Network-Error darf kein *APIError sein, bekommen %v", apiErr)
		}
	})
}

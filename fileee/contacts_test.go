package fileee

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

// TestContactServiceDeleteHappyErrorNetwork deckt Delete() als Mutation-Funktion vollständig ab
// (Test-Coverage-Pflicht): Happy-Path (200 -> nil), Error-Path (404 -> ErrNotFound per errors.Is)
// und Network-Error. Delete ist ein Hard-DELETE — ADR-0007/ADR-0008 dokumentieren, warum die Lib
// diese Methode trotzdem bewusst (geguardet) anbietet.
func TestContactServiceDeleteHappyErrorNetwork(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"DELETE /api/contacts/rest/contact-1": {Status: 200},
		})
		client := newTestClientAgainstMock(t, routes)
		if err := client.Contacts.Delete(context.Background(), "contact-1"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})

	t.Run("error path 404 -> ErrNotFound", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"DELETE /api/contacts/rest/unbekannt": {Status: 404, Body: []byte(`{"errorCode":"NOT_FOUND"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		err := client.Contacts.Delete(context.Background(), "unbekannt")
		if !errorsIsNotFound(err) {
			t.Fatalf("erwartet ErrNotFound, bekommen %v", err)
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
		err = client.Contacts.Delete(context.Background(), "contact-1")
		if err == nil {
			t.Fatalf("erwartet Network-Error, bekommen nil")
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("Network-Error darf kein *APIError sein, bekommen %v", apiErr)
		}
	})
}

// TestContactServiceCreateSendetPflichtfelderImRequestBody belegt das Wire-Format, dessen Fehlen
// live gegen das Testkonto einen 400 ausgelöst hat (siehe Kommentar über contactCreateWire in
// contacts.go): Create() MUSS contactStatus/connectedToOtherUser/fromUserDb/documentCounter/
// deleted/version im TATSÄCHLICH GESENDETEN JSON-Body mitschicken. Bisher prüfte kein Test den
// gesendeten Request-Body, sondern nur die Antwort-Dekodierung (siehe
// TestContactServiceCreateHappyErrorNetwork) — ein Regressionsrisiko: eine künftige Änderung an
// contactCreateWire/Create() könnte diese Pflichtfelder wieder verlieren, ohne dass ein Test
// anschlägt. Der body-inspizierende Mock-Handler (newMockJSONCaptureServer, analog zu
// newMockUploadServer in documents_test.go) fängt den POST /api/contacts/rest Request ab und
// dekodiert ihn generisch in eine map[string]any, damit exakt die gesendeten Feldwerte geprüft
// werden können — inklusive des CUSTOM-Defaults für contactStatus, wenn der Aufrufer (wie hier)
// keinen Status vorgibt.
func TestContactServiceCreateSendetPflichtfelderImRequestBody(t *testing.T) {
	var gotBody map[string]any
	authRoutes := ensureSessionRoutes()
	srv := newMockJSONCaptureServer(t, authRoutes, http.MethodPost, "/api/contacts/rest", func(raw []byte) (int, []byte) {
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Fatalf("gesendeten Contact-Create-Body dekodieren: %v (Body: %s)", err, raw)
		}
		return http.StatusOK, []byte(`{"id":"contact-neu"}`)
	})
	client := newTestClientAgainstMockServer(t, srv)

	_, err := client.Contacts.Create(context.Background(), &Contact{FirstName: "Max", LastName: "Testmann"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if gotBody == nil {
		t.Fatalf("Mock-Handler wurde nicht aufgerufen — kein Request-Body eingefangen")
	}

	if _, present := gotBody["id"]; !present {
		t.Fatalf("Pflichtfeld id fehlt im gesendeten Body: %+v", gotBody)
	}

	requiredZeroValueFelder := map[string]any{
		"connectedToOtherUser": false,
		"fromUserDb":           false,
		"documentCounter":      float64(0),
		"deleted":              false,
		"version":              float64(0),
	}
	for feld, erwartet := range requiredZeroValueFelder {
		got, present := gotBody[feld]
		if !present {
			t.Fatalf("Pflichtfeld %q fehlt im gesendeten Body: %+v", feld, gotBody)
		}
		if got != erwartet {
			t.Fatalf("Feld %q = %v (%T), erwartet %v (%T)", feld, got, got, erwartet, erwartet)
		}
	}

	status, _ := gotBody["contactStatus"].(string)
	if status != string(ContactStatusCustom) {
		t.Fatalf("contactStatus = %q, erwartet CUSTOM-Default %q (kein Status vom Aufrufer vorgegeben)", status, ContactStatusCustom)
	}
}

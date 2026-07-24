package fileee

import (
	"context"
	"encoding/json"
	"errors"
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

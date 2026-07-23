package fileee

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
)

func TestNewValidiertCredentials(t *testing.T) {
	_, err := New(Credentials{})
	if err == nil {
		t.Fatalf("erwartet Fehler bei leeren Credentials, bekommen nil")
	}
}

func TestNewMitOptionsUndLogin(t *testing.T) {
	routes := map[string]mockRoute{
		"GET /api/f/start":     {Status: 204},
		"POST /api/f/existent": {Status: 200, Body: []byte(`{"existent":true,"twoFactorAuthEnabled":false}`)},
		"POST /api/f/login":    {Status: 200, Body: []byte(`{"loggedIn":true}`), Cookies: []*http.Cookie{{Name: "JSESSIONID", Value: "sess-client"}}},
	}
	srv := newMockServer(t, jsonHandler(t, routes))
	store := NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))

	client, err := New(
		Credentials{Username: "test@example.invalid", Password: "test-pw"},
		WithBaseURL(srv.URL),
		WithSessionStore(store),
		WithRateLimit(1000, 1000),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.Documents == nil || client.Tags == nil || client.Companies == nil || client.Contacts == nil || client.DocumentTypes == nil {
		t.Fatalf("nicht alle Services wurden verdrahtet: %+v", client)
	}
	if err := client.Login(context.Background()); err != nil {
		t.Fatalf("Login über Client: %v", err)
	}
	sess, err := store.Load(context.Background())
	if err != nil || sess == nil {
		t.Fatalf("WithSessionStore wurde nicht verwendet: %v / %+v", err, sess)
	}
}

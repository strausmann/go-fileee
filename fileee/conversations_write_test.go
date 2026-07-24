package fileee

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
)

func jwtWithSub(sub string) string {
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	return "jwt " + enc(map[string]string{"typ": "JWT"}) + "." + enc(map[string]string{"sub": sub}) + ".sig"
}

func TestConversations_SendMessage(t *testing.T) {
	var captured map[string]any
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/f/start":
			http.SetCookie(w, &http.Cookie{Name: "XSRF-TOKEN", Value: "x"})
			w.WriteHeader(204)
		case "/api/f/existent":
			w.Write([]byte(`{"existent":true,"twoFactorAuthEnabled":false}`))
		case "/api/f/token/login":
			w.WriteHeader(401)
		case "/api/f/login":
			http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: jwtWithSub("u-me")})
			w.Write([]byte(`{"loggedIn":true}`))
		case "/api/f/user-session":
			w.Write([]byte(`{"authorized":true}`))
		case "/api/conversations/rest/c1":
			w.Write([]byte(`{"id":"c1","messages":[{"id":"m-1"},{"id":"m-last"}]}`))
		case "/api/conversations/rest/c1/message":
			b := make([]byte, r.ContentLength)
			r.Body.Read(b)
			_ = json.Unmarshal(b, &captured)
			w.Write([]byte(`{"conversationId":"c1","messageId":"m-new","messageIndex":8,"currentReadToIndex":8}`))
		default:
			w.WriteHeader(404)
		}
	}))
	c, err := New(Credentials{Username: "u@example.invalid", Password: "p"},
		WithBaseURL(srv.URL), WithRateLimit(1000, 1000),
		WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "s.json"))))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Conversations.SendMessage(context.Background(), "c1", "Guten Tag")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if res.MessageID != "m-new" || res.MessageIndex != 8 {
		t.Fatalf("Antwort falsch: %+v", res)
	}
	msg, _ := captured["message"].(map[string]any)
	if msg["message"] != "Guten Tag" || msg["type"] != "CHAT" || msg["direction"] != "FROM_USER" {
		t.Errorf("message-Felder falsch: %+v", msg)
	}
	if msg["senderId"] != "u-me" {
		t.Errorf("senderId = %v, erwartet u-me (JWT sub)", msg["senderId"])
	}
	if msg["id"] == nil || msg["id"] == "" {
		t.Error("message.id (client ObjectId) fehlt")
	}
	ls, _ := captured["localState"].(map[string]any)
	if ls["lastMessageId"] != "m-last" {
		t.Errorf("localState.lastMessageId = %v, erwartet m-last", ls["lastMessageId"])
	}
}

func TestUserID_FromJWTSub(t *testing.T) {
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/f/start":
			http.SetCookie(w, &http.Cookie{Name: "XSRF-TOKEN", Value: "x"})
			w.WriteHeader(204)
		case "/api/f/existent":
			w.Write([]byte(`{"existent":true,"twoFactorAuthEnabled":false}`))
		case "/api/f/token/login":
			w.WriteHeader(401)
		case "/api/f/login":
			http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: jwtWithSub("u-42")})
			w.Write([]byte(`{"loggedIn":true}`))
		case "/api/f/user-session":
			w.Write([]byte(`{"authorized":true}`))
		default:
			w.WriteHeader(404)
		}
	}))
	c, err := New(Credentials{Username: "u@example.invalid", Password: "p"},
		WithBaseURL(srv.URL), WithRateLimit(1000, 1000),
		WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "s.json"))))
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.UserID(context.Background())
	if err != nil {
		t.Fatalf("UserID: %v", err)
	}
	if id != "u-42" {
		t.Fatalf("UserID = %q, erwartet u-42", id)
	}
}

func TestConversations_ShareDocument(t *testing.T) {
	var captured map[string]any
	srv := newMockServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/f/start":
			http.SetCookie(w, &http.Cookie{Name: "XSRF-TOKEN", Value: "x"})
			w.WriteHeader(204)
		case "/api/f/existent":
			w.Write([]byte(`{"existent":true,"twoFactorAuthEnabled":false}`))
		case "/api/f/token/login":
			w.WriteHeader(401)
		case "/api/f/login":
			http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: jwtWithSub("u-me")})
			w.Write([]byte(`{"loggedIn":true}`))
		case "/api/f/user-session":
			w.Write([]byte(`{"authorized":true}`))
		case "/api/conversations/rest/c1":
			w.Write([]byte(`{"id":"c1","messages":[{"id":"m-last"}]}`))
		case "/api/conversations/rest/c1/message":
			b := make([]byte, r.ContentLength)
			r.Body.Read(b)
			_ = json.Unmarshal(b, &captured)
			w.Write([]byte(`{"conversationId":"c1","messageId":"m-doc","messageIndex":2}`))
		default:
			w.WriteHeader(404)
		}
	}))
	c, err := New(Credentials{Username: "u@example.invalid", Password: "p"},
		WithBaseURL(srv.URL), WithRateLimit(1000, 1000),
		WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "s.json"))))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Conversations.ShareDocument(context.Background(), "c1", "doc-42"); err != nil {
		t.Fatalf("ShareDocument: %v", err)
	}
	msg, _ := captured["message"].(map[string]any)
	if msg["type"] != "DOCUMENT" || msg["documentId"] != "doc-42" || msg["remove"] != false {
		t.Errorf("DOCUMENT-Nachricht falsch: type=%v documentId=%v remove=%v", msg["type"], msg["documentId"], msg["remove"])
	}
}

package fileee

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// networkErrorTestClient baut einen Client gegen einen unerreichbaren Host (127.0.0.1:1) — der
// Standard-Fixture für Network-Error-Subtests (Test-Coverage-Pflicht: jede Mutation-Funktion
// braucht Happy + Error(4xx/5xx) + Network-Error).
func networkErrorTestClient(t *testing.T) *Client {
	t.Helper()
	client, err := NewClient(
		Credentials{Username: "test@example.invalid", Password: "test-pw"},
		WithBaseURL("http://127.0.0.1:1"),
		WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

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
	c, err := NewClient(Credentials{Username: "u@example.invalid", Password: "p"},
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
	c, err := NewClient(Credentials{Username: "u@example.invalid", Password: "p"},
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
	c, err := NewClient(Credentials{Username: "u@example.invalid", Password: "p"},
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

// TestConversations_SendMessageErrorNetwork deckt SendMessage() als Mutation-Funktion vollständig
// ab (Test-Coverage-Pflicht Finding I2): Happy-Path ist bereits durch TestConversations_
// SendMessage abgedeckt, hier folgen Error-Path (echter Server-5xx) und Network-Error.
func TestConversations_SendMessageErrorNetwork(t *testing.T) {
	t.Run("error path 500", func(t *testing.T) {
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
				w.Write([]byte(`{"id":"c1","messages":[]}`))
			case "/api/conversations/rest/c1/message":
				w.WriteHeader(500)
			default:
				w.WriteHeader(404)
			}
		}))
		c := convTestClient(t, srv)
		_, err := c.Conversations.SendMessage(context.Background(), "c1", "hallo")
		if err == nil {
			t.Fatal("erwartet Fehler bei 500, bekommen nil")
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("erwartet gewrapptes *APIError, bekommen %T: %v", err, err)
		}
	})

	t.Run("network error", func(t *testing.T) {
		client := networkErrorTestClient(t)
		_, err := client.Conversations.SendMessage(context.Background(), "c1", "hallo")
		if err == nil {
			t.Fatal("erwartet Network-Error, bekommen nil")
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("Network-Error darf kein *APIError sein, bekommen %v", apiErr)
		}
	})
}

// TestConversations_ShareDocumentErrorNetwork deckt ShareDocument() als Mutation-Funktion
// vollständig ab (Test-Coverage-Pflicht Finding I2): Happy-Path ist bereits durch
// TestConversations_ShareDocument abgedeckt, hier folgen Error-Path und Network-Error.
func TestConversations_ShareDocumentErrorNetwork(t *testing.T) {
	t.Run("error path 500", func(t *testing.T) {
		srv := newMockServer(t, convAuthMock(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/conversations/rest/c1":
				w.Write([]byte(`{"id":"c1","messages":[]}`))
			case "/api/conversations/rest/c1/message":
				w.WriteHeader(500)
			default:
				w.WriteHeader(404)
			}
		}))
		c := convTestClient(t, srv)
		_, err := c.Conversations.ShareDocument(context.Background(), "c1", "doc-42")
		if err == nil {
			t.Fatal("erwartet Fehler bei 500, bekommen nil")
		}
	})

	t.Run("network error", func(t *testing.T) {
		client := networkErrorTestClient(t)
		_, err := client.Conversations.ShareDocument(context.Background(), "c1", "doc-42")
		if err == nil {
			t.Fatal("erwartet Network-Error, bekommen nil")
		}
	})
}

// TestConversations_UnshareDocument ist der Happy-Path-Test für Whole-Codebase-Review Finding I1:
// UnshareDocument (der remove=true-Zweig von docMessage) hatte 0% Coverage — kein Test rief sie
// je auf. Analog zu TestConversations_ShareDocument, prüft aber zusätzlich remove=true statt
// remove=false in der gesendeten DOCUMENT-Nachricht.
func TestConversations_UnshareDocument(t *testing.T) {
	var captured map[string]any
	srv := newMockServer(t, convAuthMock(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/conversations/rest/c1":
			w.Write([]byte(`{"id":"c1","messages":[{"id":"m-last"}]}`))
		case "/api/conversations/rest/c1/message":
			b := make([]byte, r.ContentLength)
			r.Body.Read(b)
			_ = json.Unmarshal(b, &captured)
			w.Write([]byte(`{"conversationId":"c1","messageId":"m-unshare","messageIndex":3}`))
		default:
			w.WriteHeader(404)
		}
	}))
	c := convTestClient(t, srv)
	res, err := c.Conversations.UnshareDocument(context.Background(), "c1", "doc-42")
	if err != nil {
		t.Fatalf("UnshareDocument: %v", err)
	}
	if res.MessageID != "m-unshare" {
		t.Fatalf("Antwort falsch: %+v", res)
	}
	msg, _ := captured["message"].(map[string]any)
	if msg["type"] != "DOCUMENT" || msg["documentId"] != "doc-42" || msg["remove"] != true {
		t.Errorf("DOCUMENT-Nachricht falsch: type=%v documentId=%v remove=%v", msg["type"], msg["documentId"], msg["remove"])
	}
}

// TestConversations_UnshareDocumentErrorNetwork rundet Finding I1 ab: Error-Path (echter
// Server-5xx) und Network-Error für UnshareDocument, analog zu ShareDocument.
func TestConversations_UnshareDocumentErrorNetwork(t *testing.T) {
	t.Run("error path 500", func(t *testing.T) {
		srv := newMockServer(t, convAuthMock(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/conversations/rest/c1":
				w.Write([]byte(`{"id":"c1","messages":[]}`))
			case "/api/conversations/rest/c1/message":
				w.WriteHeader(500)
			default:
				w.WriteHeader(404)
			}
		}))
		c := convTestClient(t, srv)
		_, err := c.Conversations.UnshareDocument(context.Background(), "c1", "doc-42")
		if err == nil {
			t.Fatal("erwartet Fehler bei 500, bekommen nil")
		}
	})

	t.Run("network error", func(t *testing.T) {
		client := networkErrorTestClient(t)
		_, err := client.Conversations.UnshareDocument(context.Background(), "c1", "doc-42")
		if err == nil {
			t.Fatal("erwartet Network-Error, bekommen nil")
		}
	})
}

func convAuthMock(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		default:
			handler(w, r)
		}
	}
}

func convTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient(Credentials{Username: "u@example.invalid", Password: "p"},
		WithBaseURL(srv.URL), WithRateLimit(1000, 1000),
		WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "s.json"))))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConversations_AddParticipant(t *testing.T) {
	var captured map[string]any
	srv := newMockServer(t, convAuthMock(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversations/c1/participants/add" {
			b := make([]byte, r.ContentLength)
			r.Body.Read(b)
			_ = json.Unmarshal(b, &captured)
			w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(404)
	}))
	c := convTestClient(t, srv)
	if err := c.Conversations.AddParticipant(context.Background(), "c1", "julia@example.invalid", ConversationRoleViewer); err != nil {
		t.Fatalf("AddParticipant: %v", err)
	}
	ps, _ := captured["participants"].([]any)
	if len(ps) != 1 {
		t.Fatalf("participants falsch: %+v", captured)
	}
	p := ps[0].(map[string]any)
	if p["id"] != "julia@example.invalid" || p["type"] != "EXTERNAL" || p["role"] != "VIEWER" {
		t.Errorf("participant falsch: %+v", p)
	}
	if captured["resendInvitationEmail"] != false {
		t.Errorf("resendInvitationEmail = %v", captured["resendInvitationEmail"])
	}
}

// TestConversations_AddParticipantErrorNetwork deckt AddParticipant() als Mutation-Funktion
// vollständig ab (Test-Coverage-Pflicht Finding I2): Happy-Path ist bereits durch
// TestConversations_AddParticipant abgedeckt, hier folgen Error-Path und Network-Error.
func TestConversations_AddParticipantErrorNetwork(t *testing.T) {
	t.Run("error path 500", func(t *testing.T) {
		srv := newMockServer(t, convAuthMock(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/conversations/c1/participants/add" {
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(404)
		}))
		c := convTestClient(t, srv)
		err := c.Conversations.AddParticipant(context.Background(), "c1", "julia@example.invalid", ConversationRoleViewer)
		if err == nil {
			t.Fatal("erwartet Fehler bei 500, bekommen nil")
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("erwartet gewrapptes *APIError, bekommen %T: %v", err, err)
		}
	})

	t.Run("network error", func(t *testing.T) {
		client := networkErrorTestClient(t)
		err := client.Conversations.AddParticipant(context.Background(), "c1", "julia@example.invalid", ConversationRoleViewer)
		if err == nil {
			t.Fatal("erwartet Network-Error, bekommen nil")
		}
	})
}

func TestConversations_RemoveParticipant(t *testing.T) {
	var captured map[string]any
	srv := newMockServer(t, convAuthMock(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/conversations/rest/c1":
			w.Write([]byte(`{"id":"c1","participants":[{"id":"p-x","name":"Weg","type":"USER","invited":true,"joined":false,"conversationPermissions":["CHAT"]}]}`))
		case "/api/conversations/c1/participants/remove":
			b := make([]byte, r.ContentLength)
			r.Body.Read(b)
			_ = json.Unmarshal(b, &captured)
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(404)
		}
	}))
	c := convTestClient(t, srv)
	if err := c.Conversations.RemoveParticipant(context.Background(), "c1", "p-x"); err != nil {
		t.Fatalf("RemoveParticipant: %v", err)
	}
	ps, _ := captured["participants"].([]any)
	if len(ps) != 1 || ps[0].(map[string]any)["id"] != "p-x" {
		t.Fatalf("remove participant falsch: %+v", captured)
	}
	if captured["keepDocuments"] != false || captured["keepHistory"] != false {
		t.Errorf("keep-Flags falsch: %+v", captured)
	}
}

// TestConversations_RemoveParticipantErrorNetwork deckt den in Finding I2 explizit vermissten
// Fall ab: einen ECHTEN Server-Fehler (4xx/5xx) auf dem participants/remove-Endpunkt — nicht nur
// den bereits existierenden CLIENT-SEITIGEN not-found-Fall (Teilnehmer nicht in der lokal
// geladenen Liste, TestConversations_RemoveParticipant_NotFound), plus Network-Error.
func TestConversations_RemoveParticipantErrorNetwork(t *testing.T) {
	t.Run("error path 500 (echter API-Fehler, nicht der client-seitige not-found)", func(t *testing.T) {
		srv := newMockServer(t, convAuthMock(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/conversations/rest/c1":
				w.Write([]byte(`{"id":"c1","participants":[{"id":"p-x","name":"Weg","type":"USER","invited":true,"joined":false}]}`))
			case "/api/conversations/c1/participants/remove":
				w.WriteHeader(500)
			default:
				w.WriteHeader(404)
			}
		}))
		c := convTestClient(t, srv)
		err := c.Conversations.RemoveParticipant(context.Background(), "c1", "p-x")
		if err == nil {
			t.Fatal("erwartet Fehler bei 500, bekommen nil")
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("erwartet gewrapptes *APIError vom echten Server-Fehler, bekommen %T: %v", err, err)
		}
	})

	t.Run("network error", func(t *testing.T) {
		client := networkErrorTestClient(t)
		err := client.Conversations.RemoveParticipant(context.Background(), "c1", "p-x")
		if err == nil {
			t.Fatal("erwartet Network-Error, bekommen nil")
		}
	})
}

func TestConversations_RemoveParticipant_NotFound(t *testing.T) {
	srv := newMockServer(t, convAuthMock(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversations/rest/c1" {
			w.Write([]byte(`{"id":"c1","participants":[]}`))
			return
		}
		w.WriteHeader(404)
	}))
	c := convTestClient(t, srv)
	err := c.Conversations.RemoveParticipant(context.Background(), "c1", "ghost")
	if err == nil {
		t.Fatal("erwartet Fehler bei unbekanntem Teilnehmer")
	}
}

func TestConversations_PendingInvitations(t *testing.T) {
	diff := `{"rows":[
	  {"id":"c1","invitation":true,"title":"Einladung A"},
	  {"id":"c2","invitation":false,"title":"schon drin"},
	  {"id":"c3","invitation":true,"title":"Einladung B"}
	],"totalRows":3,"idsToDelete":[]}`
	srv := newMockServer(t, convAuthMock(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversations/rest/diff" {
			w.Write([]byte(diff))
			return
		}
		w.WriteHeader(404)
	}))
	c := convTestClient(t, srv)
	pend, err := c.Conversations.PendingInvitations(context.Background())
	if err != nil {
		t.Fatalf("PendingInvitations: %v", err)
	}
	if len(pend) != 2 {
		t.Fatalf("erwartet 2 offene Einladungen, bekommen %d", len(pend))
	}
	for _, p := range pend {
		if !p.Invitation {
			t.Errorf("Konversation %s ohne invitation-Flag in der Liste", p.ID)
		}
	}
}

func TestConversations_AcceptInvitation(t *testing.T) {
	var captured map[string]any
	srv := newMockServer(t, convAuthMock(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/conversations/invitations/tok-42/accept" && r.Method == http.MethodPost {
			b := make([]byte, r.ContentLength)
			r.Body.Read(b)
			_ = json.Unmarshal(b, &captured)
			w.Write([]byte(`{"id":"c1"}`))
			return
		}
		w.WriteHeader(404)
	}))
	c := convTestClient(t, srv)
	if err := c.Conversations.AcceptInvitation(context.Background(), "tok-42"); err != nil {
		t.Fatalf("AcceptInvitation: %v", err)
	}
	if captured["token"] != "tok-42" || captured["acceptToS"] != false {
		t.Errorf("Accept-Body falsch: %+v", captured)
	}
}

// TestConversations_AcceptInvitationErrorNetwork deckt AcceptInvitation() als Mutation-Funktion
// vollständig ab (Test-Coverage-Pflicht Finding I2): Happy-Path ist bereits durch
// TestConversations_AcceptInvitation abgedeckt, hier folgen Error-Path und Network-Error.
func TestConversations_AcceptInvitationErrorNetwork(t *testing.T) {
	t.Run("error path 500", func(t *testing.T) {
		srv := newMockServer(t, convAuthMock(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/conversations/invitations/tok-42/accept" && r.Method == http.MethodPost {
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(404)
		}))
		c := convTestClient(t, srv)
		err := c.Conversations.AcceptInvitation(context.Background(), "tok-42")
		if err == nil {
			t.Fatal("erwartet Fehler bei 500, bekommen nil")
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("erwartet gewrapptes *APIError, bekommen %T: %v", err, err)
		}
	})

	t.Run("network error", func(t *testing.T) {
		client := networkErrorTestClient(t)
		err := client.Conversations.AcceptInvitation(context.Background(), "tok-42")
		if err == nil {
			t.Fatal("erwartet Network-Error, bekommen nil")
		}
	})
}

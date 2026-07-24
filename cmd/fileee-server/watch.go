package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/strausmann/go-fileee/fileee"
)

// watchWebhookTimeout ist der Timeout für einen einzelnen Webhook-POST — kurz gehalten, damit ein
// hängender/nicht erreichbarer Empfänger nicht den Poll-Takt des Watchers blockiert (der nächste
// Tick soll pünktlich stattfinden können, siehe watchLoop).
const watchWebhookTimeout = 5 * time.Second

// watchWebhookMessage ist der "message"-Teil des Webhook-Payloads (Spec §4.1c,
// docs/superpowers/specs/2026-07-24-fileee-server-design.md im homelab-management-Repo):
// `{conversationId, message:{id,senderId,text,timestamp}, documentIds}`.
type watchWebhookMessage struct {
	ID        string `json:"id"`
	SenderID  string `json:"senderId"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
}

// watchWebhookPayload ist der vollständige JSON-Body eines Watch-Webhook-POSTs.
type watchWebhookPayload struct {
	ConversationID string              `json:"conversationId"`
	Message        watchWebhookMessage `json:"message"`
	DocumentIDs    []string            `json:"documentIds"`
}

// StartWatch startet einen Hintergrund-Poller, der Konversationen (Conversations.Diff)
// beobachtet und bei einer NEUEN eingehenden Chat-Antwort (Message.IsReply(), also Type CHAT +
// Direction TO_USER — "jemand anderes als das Server-Konto hat geantwortet") einen
// POST webhookURL auslöst (Spec §4.1c). Der Body ist
// `{conversationId, message:{id,senderId,text,timestamp}, documentIds}`; documentIds sind die in
// der Konversation geteilten Dokumente (Conversation.State.SharedDocumentIDs) — der fachliche
// Bezug, welches Dokument die Antwort betrifft.
//
// interval<=0 ODER eine leere webhookURL sind ein bewusstes No-op: es wird KEINE Goroutine
// gestartet, und die zurückgegebene stop-Funktion tut nichts (aber ist gefahrlos aufrufbar) —
// so kann der Aufrufer (main.go) StartWatch IMMER aufrufen und muss die Deaktivierungs-Fälle
// nicht selbst prüfen.
//
// Andernfalls startet StartWatch GENAU EINE Goroutine (watchLoop) und liefert ein stop(), das
// idempotent ist (mehrfacher Aufruf ist sicher, über sync.Once) und BLOCKIERT, bis die Goroutine
// tatsächlich beendet ist (sync.WaitGroup) — kein Goroutine-Leak. Der übergebene ctx wird über
// context.WithCancel abgeleitet: sowohl eine Cancellation von ctx selbst (z. B. Server-Shutdown)
// als auch ein Aufruf von stop() beenden den Poller.
//
// BASELINE (kein Feuern für den Bestand): beim ERSTEN erfolgreichen Poll wird für jede
// zurückgelieferte Konversation nur die zuletzt gesehene Message-ID gemerkt (kein Webhook-POST) —
// sonst würde jede bereits vorhandene Antwort beim Start sofort einen Webhook auslösen. Erst ab
// dem zweiten Poll gelten neu hinzugekommene Nachrichten als "neu".
//
// FEHLERTOLERANZ: ein Diff-Fehler oder ein fehlgeschlagener Webhook-POST wird ausschließlich über
// s.log geloggt — der Loop läuft beim nächsten Tick unverändert weiter. Ein einzelner
// Fehler-Tick darf den Watcher nicht dauerhaft beenden.
func (s *Server) StartWatch(ctx context.Context, webhookURL string, interval time.Duration) (stop func()) {
	if interval <= 0 || webhookURL == "" {
		return func() {}
	}

	watchCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.watchLoop(watchCtx, webhookURL, interval)
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			wg.Wait()
		})
	}
}

// watchLoop ist die eigentliche Ticker-Schleife von StartWatch. Sie hält ihren gesamten
// veränderlichen Zustand (cursor, seen, firstPoll) als lokale Variablen — NUR diese eine
// Goroutine liest/schreibt sie, es gibt also keinen gemeinsamen Zustand, der einen Mutex bräuchte
// (der `seen`-Map-Zugriff ist race-frei, weil er niemals von einer zweiten Goroutine berührt
// wird — siehe Task-13-Brief "run with -race").
func (s *Server) watchLoop(ctx context.Context, webhookURL string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	client := &http.Client{Timeout: watchWebhookTimeout}
	cursor := fileee.NewCursor("Conversation")
	seen := map[string]string{}
	firstPoll := true

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cursor, firstPoll = s.watchPollOnce(ctx, client, webhookURL, cursor, seen, firstPoll)
		}
	}
}

// watchPollOnce führt EINEN Diff-Poll aus und verarbeitet dessen Zeilen. Bei einem Diff-Fehler
// wird nur geloggt; cursor/firstPoll bleiben unverändert, sodass der nächste Tick es mit demselben
// Cursor erneut versucht (kein Datenverlust durch einen übersprungenen Zwischenstand).
func (s *Server) watchPollOnce(
	ctx context.Context,
	client *http.Client,
	webhookURL string,
	cursor fileee.Cursor,
	seen map[string]string,
	firstPoll bool,
) (fileee.Cursor, bool) {
	result, err := s.fc.Conversations.Diff(ctx, cursor)
	if err != nil {
		s.log.Error("watch: Conversations.Diff fehlgeschlagen", "error", err)
		return cursor, firstPoll
	}

	for _, conv := range result.Rows {
		s.watchProcessConversation(ctx, client, webhookURL, conv, seen, firstPoll)
	}

	return result.NextCursor, false
}

// watchProcessConversation wertet EINE Diff-Zeile aus: beim Baseline-Poll (firstPoll) wird nur der
// Seen-Marker gesetzt, sonst werden alle Nachrichten NACH dem zuletzt gesehenen Marker geprüft und
// bei IsReply()==true per Webhook gemeldet. Der Seen-Marker wird danach auf die jeweils letzte
// Nachricht vorgerückt — unabhängig davon, ob sie ein Reply war —, damit dieselbe Nachricht nie ein
// zweites Mal als "neu" gilt.
func (s *Server) watchProcessConversation(
	ctx context.Context,
	client *http.Client,
	webhookURL string,
	conv fileee.Conversation,
	seen map[string]string,
	firstPoll bool,
) {
	if firstPoll {
		seen[conv.ID] = lastMessageIDOf(conv)
		return
	}

	lastSeen := seen[conv.ID]
	for _, m := range messagesAfter(conv.Messages, lastSeen) {
		if m.IsReply() {
			s.fireWatchWebhook(ctx, client, webhookURL, conv, m)
		}
	}
	if newest := lastMessageIDOf(conv); newest != "" {
		seen[conv.ID] = newest
	}
}

// lastMessageIDOf liefert die ID der letzten (jüngsten) Nachricht einer Konversation, oder einen
// leeren String, wenn die Konversation (noch) keine Nachrichten trägt. Conversation.Messages ist
// aufsteigend sortiert (siehe fileee/conversations.go-Doku), die letzte ID ist also die jüngste.
func lastMessageIDOf(conv fileee.Conversation) string {
	if len(conv.Messages) == 0 {
		return ""
	}
	return conv.Messages[len(conv.Messages)-1].ID
}

// messagesAfter liefert die Nachrichten NACH lastID aus der aufsteigend sortierten Nachrichtenliste
// msgs. lastID=="" bedeutet "kein Baseline-Marker vorhanden" (entweder eine dem Watcher komplett
// neue Konversation, die erst nach dem Baseline-Poll aufgetaucht ist, oder eine Konversation, die
// beim Baseline-Poll noch keine Nachrichten hatte) — in diesem Fall gelten ALLE aktuellen
// Nachrichten als neu. Wird lastID in msgs NICHT gefunden (z. B. weil der Fileee-Server aus
// irgendeinem Grund nur einen Ausschnitt statt der vollen Historie liefert), werden aus
// Sicherheitsgründen ebenfalls ALLE aktuellen Nachrichten als neu behandelt — ein doppeltes
// Webhook-Feuern ist im Zweifel harmloser als eine stillschweigend verpasste Antwort.
func messagesAfter(msgs []fileee.Message, lastID string) []fileee.Message {
	if lastID == "" {
		return msgs
	}
	for i, m := range msgs {
		if m.ID == lastID {
			return msgs[i+1:]
		}
	}
	return msgs
}

// fireWatchWebhook baut den Webhook-Payload für Nachricht m in Konversation conv und sendet ihn
// per POST an webhookURL. Ist s.cfg.WebhookSecret gesetzt, wird zusätzlich eine
// HMAC-SHA256-Signatur (hex-kodiert) über den rohen Body im Header X-Fileee-Signature mitgesendet
// (Spec §4.1c "optional HMAC-Signatur") — Empfänger können den Payload damit verifizieren, ohne
// dass fileee-server ein zusätzliches Auth-Schema braucht. Jeder Fehler (Encode, Request-Aufbau,
// Transport, Non-2xx-Antwort) wird NUR geloggt; der Aufrufer (watchProcessConversation) behandelt
// das Feuern als abgeschlossen, unabhängig vom Ausgang — ein einzelner fehlgeschlagener Webhook
// darf den Poller nicht stoppen.
func (s *Server) fireWatchWebhook(
	ctx context.Context,
	client *http.Client,
	webhookURL string,
	conv fileee.Conversation,
	m fileee.Message,
) {
	payload := watchWebhookPayload{
		ConversationID: conv.ID,
		Message: watchWebhookMessage{
			ID:        m.ID,
			SenderID:  m.SenderID,
			Text:      m.Text,
			Timestamp: m.Timestamp,
		},
		DocumentIDs: conv.State.SharedDocumentIDs,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		s.log.Error("watch: Webhook-Payload encode fehlgeschlagen", "error", err, "conversation_id", conv.ID, "message_id", m.ID)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		s.log.Error("watch: Webhook-Request-Aufbau fehlgeschlagen", "error", err, "conversation_id", conv.ID, "message_id", m.ID)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.cfg.WebhookSecret != "" {
		mac := hmac.New(sha256.New, []byte(s.cfg.WebhookSecret))
		mac.Write(body)
		req.Header.Set("X-Fileee-Signature", hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := client.Do(req)
	if err != nil {
		s.log.Error("watch: Webhook-POST fehlgeschlagen", "error", err, "conversation_id", conv.ID, "message_id", m.ID)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		s.log.Error("watch: Webhook-POST non-2xx-Antwort", "status", resp.StatusCode, "conversation_id", conv.ID, "message_id", m.ID)
	}
}

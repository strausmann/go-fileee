package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/strausmann/go-fileee/fileee"
)

// watchDiffPoll1 ist die Antwort auf den ERSTEN Diff-Poll: Konversation c1 mit genau EINER
// bereits vorhandenen eingehenden Antwort (m1, CHAT/TO_USER) — dem "Backlog" aus Sicht des
// Watchers. Da der erste Poll ausschließlich der Baseline dient (Task-13-Brief Punkt 1), darf für
// m1 KEIN Webhook feuern, obwohl m1.IsReply() true wäre.
const watchDiffPoll1 = `{"rows":[
  {"id":"c1","version":1,"messages":[
    {"id":"m1","direction":"TO_USER","timestamp":"2026-07-24T10:00:00Z","message":"alte Antwort","type":"CHAT","senderId":"u1","senderName":"Alice"}
  ],"state":{"sharedDocumentIds":["d1"]}}
],"totalRows":1,"idsToDelete":[]}`

// watchDiffPoll2 ist die Antwort auf den ZWEITEN (und jeden weiteren) Diff-Poll: dieselbe
// Konversation c1, jetzt mit einer ZUSÄTZLICHEN neuen eingehenden Antwort (m2). Nur für m2 darf
// der Watcher einen Webhook feuern — m1 wurde bereits beim Baseline-Poll als gesehen markiert.
const watchDiffPoll2 = `{"rows":[
  {"id":"c1","version":2,"messages":[
    {"id":"m1","direction":"TO_USER","timestamp":"2026-07-24T10:00:00Z","message":"alte Antwort","type":"CHAT","senderId":"u1","senderName":"Alice"},
    {"id":"m2","direction":"TO_USER","timestamp":"2026-07-24T10:05:00Z","message":"neue Antwort","type":"CHAT","senderId":"u1","senderName":"Alice"}
  ],"state":{"sharedDocumentIds":["d1"]}}
],"totalRows":1,"idsToDelete":[]}`

// watchMixedBatchPoll1 ist der Baseline-Poll für TestStartWatch_MixedBatchFiresOnlyForGenuineReply:
// dieselbe Backlog-Situation wie watchDiffPoll1 (eine bereits vorhandene CHAT/TO_USER-Antwort m1,
// die als Baseline gilt und daher nicht feuern darf).
const watchMixedBatchPoll1 = `{"rows":[
  {"id":"c1","version":1,"messages":[
    {"id":"m1","direction":"TO_USER","timestamp":"2026-07-24T10:00:00Z","message":"alte Antwort","type":"CHAT","senderId":"u1","senderName":"Alice"}
  ],"state":{"sharedDocumentIds":["d1"]}}
],"totalRows":1,"idsToDelete":[]}`

// watchMixedBatchPoll2 liefert nach der Baseline (m1) einen gemischten Batch NEUER Nachrichten:
// m2 (CHAT, aber FROM_USER — die eigene ausgehende Nachricht, IsReply()==false wegen Direction),
// m3 (Type DOCUMENT, IsReply()==false wegen Type), m4 (Type PARTICIPANT_STATE, IsReply()==false
// wegen Type) und GENAU EINE echte neue eingehende Antwort m5 (CHAT + TO_USER). Nur m5 darf einen
// Webhook auslösen — m2..m4 sind zwar "neu" (nach dem Baseline-Marker m1), aber keine Replies.
const watchMixedBatchPoll2 = `{"rows":[
  {"id":"c1","version":2,"messages":[
    {"id":"m1","direction":"TO_USER","timestamp":"2026-07-24T10:00:00Z","message":"alte Antwort","type":"CHAT","senderId":"u1","senderName":"Alice"},
    {"id":"m2","direction":"FROM_USER","timestamp":"2026-07-24T10:01:00Z","message":"eigene Antwort","type":"CHAT","senderId":"me","senderName":"Ich"},
    {"id":"m3","direction":"TO_USER","timestamp":"2026-07-24T10:02:00Z","type":"DOCUMENT","senderId":"u1","senderName":"Alice","documentId":"d2"},
    {"id":"m4","direction":"TO_USER","timestamp":"2026-07-24T10:03:00Z","type":"PARTICIPANT_STATE","senderId":"u1","senderName":"Alice"},
    {"id":"m5","direction":"TO_USER","timestamp":"2026-07-24T10:04:00Z","message":"echte neue Antwort","type":"CHAT","senderId":"u1","senderName":"Alice"}
  ],"state":{"sharedDocumentIds":["d1"]}}
],"totalRows":1,"idsToDelete":[]}`

// newWatchTestClient baut — analog zu newTestFileeeClient (handlers_test.go) — einen eigenen
// httptest-Mock-Server samt *fileee.Client (Session vorbefüllt, /api/f/user-session-Kurzschluss),
// registriert aber für POST /api/conversations/rest/diff einen DYNAMISCHEN Handler statt einer
// festen mockRoute-Antwort: diffBody liefert je nach Aufrufzähler (1-basiert) einen anderen
// Response-Body. Das erlaubt es, das Verhalten über mehrere StartWatch-Ticks hinweg deterministisch
// zu variieren (z. B. "beim 2. Poll erscheint eine neue Nachricht"), ohne dass der Test auf reines
// Timing angewiesen wäre. newTestFileeeClient selbst unterstützt das nicht (mockRoute ist eine
// feste Fixture, keine Funktion) — deshalb hier ein eigenständiger, kleiner Aufbau mit denselben
// Bausteinen (testJWTWithSub, fileee.New/WithBaseURL/WithSessionStore).
func newWatchTestClient(t *testing.T, diffBody func(callN int) []byte) *fileee.Client {
	t.Helper()

	var calls int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/f/user-session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"authorized":true,"secondsBlocked":0}`))
	})
	mux.HandleFunc("POST /api/conversations/rest/diff", func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&calls, 1))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(diffBody(n))
	})

	mockSrv := httptest.NewServer(mux)
	t.Cleanup(mockSrv.Close)

	store := fileee.NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))
	seedSession := &fileee.Session{
		Cookies: []*http.Cookie{{Name: "JSESSIONID", Value: testJWTWithSub("test-user-1")}},
		SavedAt: time.Now(),
	}
	if err := store.Save(context.Background(), seedSession); err != nil {
		t.Fatalf("Session-Store vorbefüllen: %v", err)
	}

	creds := fileee.Credentials{Username: "test@example.invalid", Password: "test-pw"}
	// WithRateLimit(1000,1000) hebt den Default-Token-Bucket (1 rps) auf — sonst würde der
	// Watcher-Poll-Takt (10ms in den Tests dieser Datei) künstlich auf den Lib-eigenen
	// Default-Rate-Limiter gebremst, was den Test unnötig verlangsamt (kein Determinismus-Gewinn,
	// nur Wartezeit) — analog zum Muster in fileee/conversations_write_test.go u.a.
	fc, err := fileee.New(creds,
		fileee.WithBaseURL(mockSrv.URL),
		fileee.WithSessionStore(store),
		fileee.WithRateLimit(1000, 1000),
	)
	if err != nil {
		t.Fatalf("fileee.New: %v", err)
	}
	return fc
}

// newWatchWebhookServer baut einen httptest-Server, der jeden empfangenen POST-Body auf den
// zurückgegebenen gepufferten Channel schiebt (Puffer 4 — mehr als genug für die Tests dieser
// Datei) und mit 200 antwortet.
func newWatchWebhookServer(t *testing.T) (*httptest.Server, chan []byte) {
	t.Helper()
	received := make(chan []byte, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- body
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, received
}

// TestStartWatch_FiresOnlyOnNewReply deckt den Kern-Vertrag von Task 13 ab: der erste Poll
// baselined den Bestand (m1) OHNE zu feuern, ein späterer Poll mit einer neuen Antwort (m2) löst
// GENAU EINEN Webhook-POST mit m2s Daten aus, und stop() beendet den Poller sauber ohne weitere
// POSTs danach.
func TestStartWatch_FiresOnlyOnNewReply(t *testing.T) {
	fc := newWatchTestClient(t, func(n int) []byte {
		if n == 1 {
			return []byte(watchDiffPoll1)
		}
		return []byte(watchDiffPoll2)
	})
	webhookSrv, received := newWatchWebhookServer(t)

	s := &Server{fc: fc, log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	stop := s.StartWatch(context.Background(), webhookSrv.URL, 10*time.Millisecond)
	t.Cleanup(stop)

	var body []byte
	select {
	case body = <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: kein Webhook-POST innerhalb 2s empfangen")
	}

	var payload watchWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Webhook-Body dekodieren: %v (body=%s)", err, body)
	}
	if payload.ConversationID != "c1" {
		t.Errorf("ConversationID = %q, erwartet c1", payload.ConversationID)
	}
	if payload.Message.ID != "m2" {
		t.Errorf("Message.ID = %q, erwartet m2 (m1 war Backlog/Baseline, darf nicht feuern)", payload.Message.ID)
	}
	if payload.Message.Text != "neue Antwort" {
		t.Errorf("Message.Text = %q, erwartet 'neue Antwort'", payload.Message.Text)
	}
	if len(payload.DocumentIDs) != 1 || payload.DocumentIDs[0] != "d1" {
		t.Errorf("DocumentIDs = %+v, erwartet [d1]", payload.DocumentIDs)
	}

	// Kein zweiter Webhook: m2 ist jetzt als gesehen markiert, jeder Folge-Poll liefert dieselbe
	// (unveränderte) Nachrichtenliste — messagesAfter muss dafür leer bleiben.
	select {
	case unexpected := <-received:
		t.Fatalf("unerwarteter zweiter Webhook-POST: %s", unexpected)
	case <-time.After(150 * time.Millisecond):
	}

	stop()

	// Nach stop() darf der Poller nicht mehr weiterlaufen — kein weiterer POST, auch nicht nach
	// Ablauf mehrerer Ticker-Intervalle.
	select {
	case unexpected := <-received:
		t.Fatalf("Webhook-POST nach stop() empfangen: %s", unexpected)
	case <-time.After(150 * time.Millisecond):
	}
}

// TestStartWatch_MixedBatchFiresOnlyForGenuineReply deckt die im Task-13-Review identifizierte
// Lücke ab: bislang wurde das `if m.IsReply()`-Gate in watchProcessConversation NIE gegen einen
// Batch getestet, der NEUE Nicht-Reply-Nachrichten NEBEN einer echten neuen Reply enthält — ein
// Test, der das Gate entfernt oder invertiert (Mutation), wäre damit unbemerkt geblieben. Dieser
// Test liefert im zweiten Poll vier neue Nachrichten nach dem Baseline-Marker m1 (m2 CHAT/
// FROM_USER, m3 DOCUMENT, m4 PARTICIPANT_STATE, m5 CHAT/TO_USER) und prüft, dass GENAU EIN
// Webhook feuert — und zwar für m5, nicht für eine der drei Nicht-Replies.
func TestStartWatch_MixedBatchFiresOnlyForGenuineReply(t *testing.T) {
	fc := newWatchTestClient(t, func(n int) []byte {
		if n == 1 {
			return []byte(watchMixedBatchPoll1)
		}
		return []byte(watchMixedBatchPoll2)
	})
	webhookSrv, received := newWatchWebhookServer(t)

	s := &Server{fc: fc, log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	stop := s.StartWatch(context.Background(), webhookSrv.URL, 10*time.Millisecond)
	t.Cleanup(stop)

	var body []byte
	select {
	case body = <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: kein Webhook-POST innerhalb 2s empfangen")
	}

	var payload watchWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Webhook-Body dekodieren: %v (body=%s)", err, body)
	}
	if payload.ConversationID != "c1" {
		t.Errorf("ConversationID = %q, erwartet c1", payload.ConversationID)
	}
	if payload.Message.ID != "m5" {
		t.Errorf("Message.ID = %q, erwartet m5 (einzige echte CHAT+TO_USER-Antwort im Mixed-Batch; m2/m3/m4 sind neu, aber keine Replies)", payload.Message.ID)
	}

	// Sanity-Check (Mutation-Nachweis): Würde das `if m.IsReply()`-Gate in watchProcessConversation
	// entfernt oder invertiert, würde fireWatchWebhook für JEDE der vier neuen Nachrichten aufgerufen
	// (m2, m3, m4, m5) statt nur für m5 — der gepufferte received-Channel (Puffer 4) hätte dann
	// bereits einen zweiten Body bereitliegen, und der folgende select würde SOFORT einen
	// unerwarteten zweiten POST liefern statt in den 150ms-Timeout zu laufen. Dieser Test ist damit
	// nicht-vakuos: er würde bei entferntem/invertiertem Gate zuverlässig fehlschlagen.
	select {
	case unexpected := <-received:
		t.Fatalf("unerwarteter zweiter Webhook-POST — das IsReply()-Gate scheint auch für Nicht-Replies zu feuern: %s", unexpected)
	case <-time.After(150 * time.Millisecond):
	}

	stop()

	// Nach stop() darf kein weiterer POST mehr eintreffen (analog TestStartWatch_FiresOnlyOnNewReply).
	select {
	case unexpected := <-received:
		t.Fatalf("Webhook-POST nach stop() empfangen: %s", unexpected)
	case <-time.After(150 * time.Millisecond):
	}
}

// TestStartWatch_NoopWhenDisabled prüft die No-op-Vorgabe aus Task 13: interval<=0 ODER eine
// leere webhookURL starten KEINE Goroutine und liefern ein stop(), das gefahrlos (auch mehrfach)
// aufgerufen werden kann.
func TestStartWatch_NoopWhenDisabled(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	cases := []struct {
		name       string
		webhookURL string
		interval   time.Duration
	}{
		{"interval null", "https://example.invalid/webhook", 0},
		{"interval negativ", "https://example.invalid/webhook", -1 * time.Second},
		{"webhookURL leer", "", 10 * time.Millisecond},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{log: log}
			stop := s.StartWatch(context.Background(), tc.webhookURL, tc.interval)
			if stop == nil {
				t.Fatal("StartWatch lieferte nil stop-Funktion")
			}
			// Sicher mehrfach aufrufbar (idempotent), kein Panic.
			stop()
			stop()
		})
	}
}

// TestStartWatch_ContextCancelStopsLoop prüft, dass eine Cancellation des ÜBERGEBENEN Parent-
// Kontexts den Poller ebenfalls beendet — nicht nur der von stop() zurückgegebene Mechanismus
// (Brief Punkt 5).
func TestStartWatch_ContextCancelStopsLoop(t *testing.T) {
	fc := newWatchTestClient(t, func(n int) []byte { return []byte(watchDiffPoll1) })
	webhookSrv, received := newWatchWebhookServer(t)

	s := &Server{fc: fc, log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	ctx, cancel := context.WithCancel(context.Background())
	stop := s.StartWatch(ctx, webhookSrv.URL, 10*time.Millisecond)
	t.Cleanup(stop)

	// Baseline-Poll abwarten (kein Reply in watchDiffPoll1 nach Baseline, also kein Webhook
	// erwartet) — kurze Wartezeit reicht, danach Parent-Kontext canceln.
	time.Sleep(30 * time.Millisecond)
	cancel()

	// stop() muss trotz bereits abgebrochenem Parent-Kontext zurückkehren (WaitGroup wartet auf
	// die bereits durch ctx.Done() beendete Goroutine) — ohne Timeout/Deadlock.
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop() kehrte nach Parent-Context-Cancel nicht zurück (Goroutine-Leak?)")
	}

	select {
	case unexpected := <-received:
		t.Fatalf("unerwarteter Webhook-POST: %s", unexpected)
	default:
	}
}

// TestStartWatch_DiffErrorLoggedAndLoopRecovers deckt die Fehlertoleranz-Vorgabe aus dem
// Task-13-Brief ab (Punkt 3): ein fehlgeschlagener Diff-Poll (hier: kaputtes JSON im ERSTEN
// Response-Body, sodass Conversations.Diff einen Decode-Fehler liefert) darf den Watcher NICHT
// dauerhaft stoppen — der nächste Tick muss es erneut versuchen und danach normal weiterlaufen.
func TestStartWatch_DiffErrorLoggedAndLoopRecovers(t *testing.T) {
	fc := newWatchTestClient(t, func(n int) []byte {
		switch n {
		case 1:
			return []byte(`{not valid json`)
		case 2:
			return []byte(watchDiffPoll1)
		default:
			return []byte(watchDiffPoll2)
		}
	})
	webhookSrv, received := newWatchWebhookServer(t)

	var logBuf bytes.Buffer
	s := &Server{fc: fc, log: slog.New(slog.NewTextHandler(&logBuf, nil))}

	stop := s.StartWatch(context.Background(), webhookSrv.URL, 10*time.Millisecond)

	// Trotz des kaputten ersten Polls muss der Watcher irgendwann bei m2 ankommen (Poll 2 =
	// Baseline, Poll 3+ = neue Antwort) — das beweist, dass der Fehler-Tick den Loop nicht
	// beendet hat.
	var body []byte
	select {
	case body = <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: Watcher hat sich nach Diff-Fehler nicht erholt (kein Webhook-POST)")
	}
	stop() // vor dem Lesen von logBuf: sicherstellen, dass die Loop-Goroutine nicht mehr schreibt.

	var payload watchWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Webhook-Body dekodieren: %v", err)
	}
	if payload.Message.ID != "m2" {
		t.Errorf("Message.ID = %q, erwartet m2", payload.Message.ID)
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("Diff fehlgeschlagen")) {
		t.Errorf("Log enthält keinen Diff-Fehler-Eintrag: %s", logBuf.String())
	}
}

// TestStartWatch_WebhookNon2xxLoggedAndLoopContinues deckt den Fehlerpfad einer Non-2xx-Antwort
// des Webhook-Empfängers ab: fireWatchWebhook loggt nur, der Poller läuft unverändert weiter
// (kein Retry der einzelnen Nachricht, aber auch kein Abbruch des gesamten Loops).
func TestStartWatch_WebhookNon2xxLoggedAndLoopContinues(t *testing.T) {
	fc := newWatchTestClient(t, func(n int) []byte {
		if n == 1 {
			return []byte(watchDiffPoll1)
		}
		return []byte(watchDiffPoll2)
	})

	received := make(chan []byte, 4)
	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- body
		w.WriteHeader(http.StatusInternalServerError) // Empfänger meldet einen Fehler zurück.
	}))
	t.Cleanup(webhookSrv.Close)

	var logBuf bytes.Buffer
	s := &Server{fc: fc, log: slog.New(slog.NewTextHandler(&logBuf, nil))}

	stop := s.StartWatch(context.Background(), webhookSrv.URL, 10*time.Millisecond)

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: kein Webhook-POST empfangen")
	}
	// Kurze Gnadenfrist: der Server hat den Body bereits empfangen (Channel-Send), aber der
	// Response-Roundtrip (WriteHeader → client.Do kehrt zurück → fireWatchWebhook loggt) braucht
	// noch einen Moment. Ohne diese Frist würde stop() den watchCtx u.U. abbrechen, BEVOR
	// client.Do() die 500-Antwort überhaupt gelesen hat — dann läge in logBuf "context canceled"
	// statt des erwarteten "non-2xx"-Eintrags (kein Produktionsfehler, reines Test-Timing).
	time.Sleep(30 * time.Millisecond)
	stop()

	if !bytes.Contains(logBuf.Bytes(), []byte("non-2xx")) {
		t.Errorf("Log enthält keinen non-2xx-Eintrag: %s", logBuf.String())
	}
}

// TestStartWatch_WebhookTransportErrorLoggedAndLoopContinues deckt den Netzwerkfehler-Pfad ab:
// webhookURL zeigt auf einen bereits geschlossenen Server (Connection Refused). fireWatchWebhook
// muss den Transport-Fehler NUR loggen; der Diff-Poller muss unabhängig davon weiterlaufen
// (nachgewiesen über den wachsenden Diff-Aufrufzähler).
func TestStartWatch_WebhookTransportErrorLoggedAndLoopContinues(t *testing.T) {
	var diffCalls int32
	fc := newWatchTestClient(t, func(n int) []byte {
		atomic.StoreInt32(&diffCalls, int32(n))
		if n == 1 {
			return []byte(watchDiffPoll1)
		}
		return []byte(watchDiffPoll2)
	})

	deadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := deadSrv.URL
	deadSrv.Close() // sofort schließen → jeder Connect schlägt fehl (Connection Refused).

	var logBuf bytes.Buffer
	var mu sync.Mutex
	safeBuf := &syncBuffer{buf: &logBuf, mu: &mu}
	s := &Server{fc: fc, log: slog.New(slog.NewTextHandler(safeBuf, nil))}

	stop := s.StartWatch(context.Background(), deadURL, 10*time.Millisecond)

	// Genug Ticks abwarten, bis mindestens der Baseline- UND ein Fire-Versuch stattgefunden haben
	// sollten — der Diff-Aufrufzähler muss dabei weiter wachsen, obwohl der Webhook-POST jedes Mal
	// scheitert.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&diffCalls) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	stop()

	if got := atomic.LoadInt32(&diffCalls); got < 3 {
		t.Fatalf("Diff wurde nur %d Mal aufgerufen — Loop scheint nach Webhook-Transportfehler gestoppt zu haben", got)
	}
	mu.Lock()
	logged := logBuf.String()
	mu.Unlock()
	if !bytes.Contains([]byte(logged), []byte("Webhook-POST fehlgeschlagen")) {
		t.Errorf("Log enthält keinen Webhook-Transportfehler-Eintrag: %s", logged)
	}
}

// syncBuffer umschließt einen *bytes.Buffer mit einem Mutex, damit ein slog.Handler
// (Watcher-Goroutine) gleichzeitig hineinschreiben kann, während der Test per atomic-Zähler pollt
// — ohne diesen Wrapper wäre das ein Data Race unter -race (Test-Goroutine läse buf theoretisch
// gleichzeitig mit einem Schreibzugriff des Loggers).
type syncBuffer struct {
	buf *bytes.Buffer
	mu  *sync.Mutex
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// TestStartWatch_SignsPayloadWithHMACWhenWebhookSecretSet deckt den optionalen
// HMAC-Signatur-Zweig ab (Spec §4.1c "optional HMAC-Signatur"): ist cfg.WebhookSecret gesetzt,
// muss jeder Webhook-POST den Header X-Fileee-Signature mit der korrekten HMAC-SHA256-Hex-Signatur
// über den rohen Body tragen.
func TestStartWatch_SignsPayloadWithHMACWhenWebhookSecretSet(t *testing.T) {
	const secret = "test-webhook-secret"

	fc := newWatchTestClient(t, func(n int) []byte {
		if n == 1 {
			return []byte(watchDiffPoll1)
		}
		return []byte(watchDiffPoll2)
	})

	type captured struct {
		body []byte
		sig  string
	}
	received := make(chan captured, 4)
	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- captured{body: body, sig: r.Header.Get("X-Fileee-Signature")}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(webhookSrv.Close)

	s := &Server{
		fc:  fc,
		cfg: Config{WebhookSecret: secret},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	stop := s.StartWatch(context.Background(), webhookSrv.URL, 10*time.Millisecond)
	t.Cleanup(stop)

	var got captured
	select {
	case got = <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: kein Webhook-POST empfangen")
	}

	if got.sig == "" {
		t.Fatal("X-Fileee-Signature-Header fehlt, obwohl WebhookSecret gesetzt ist")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(got.body)
	want := hex.EncodeToString(mac.Sum(nil))
	if got.sig != want {
		t.Errorf("X-Fileee-Signature = %q, erwartet %q", got.sig, want)
	}
}

// TestLastMessageIDOf_EmptyConversation deckt den Guard-Zweig ab: eine Konversation ohne
// Nachrichten liefert eine leere ID statt eines Index-Out-of-Range-Panics.
func TestLastMessageIDOf_EmptyConversation(t *testing.T) {
	if got := lastMessageIDOf(fileee.Conversation{}); got != "" {
		t.Errorf("lastMessageIDOf(leer) = %q, erwartet leerer String", got)
	}
}

// TestMessagesAfter deckt die drei Zweige von messagesAfter ab: leerer lastID (alles ist neu),
// lastID gefunden (nur die Nachrichten danach) und lastID NICHT gefunden (Fail-safe: alles gilt
// als neu statt es stillschweigend zu verwerfen).
func TestMessagesAfter(t *testing.T) {
	msgs := []fileee.Message{{ID: "m1"}, {ID: "m2"}, {ID: "m3"}}

	if got := messagesAfter(msgs, ""); len(got) != 3 {
		t.Errorf("messagesAfter(leer lastID) = %d Nachrichten, erwartet 3 (alles neu)", len(got))
	}

	got := messagesAfter(msgs, "m2")
	if len(got) != 1 || got[0].ID != "m3" {
		t.Errorf("messagesAfter(lastID=m2) = %+v, erwartet [m3]", got)
	}

	if got := messagesAfter(msgs, "unbekannt"); len(got) != 3 {
		t.Errorf("messagesAfter(lastID nicht gefunden) = %d Nachrichten, erwartet 3 (Fail-safe: alles neu)", len(got))
	}
}

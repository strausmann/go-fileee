package fileee

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
)

// currentTOTP generiert den aktuellen 6-stelligen TOTP-Code aus einem RFC-6238-Base32-Seed
// (Umbrella-Spec §4.2). Ein leerer Seed liefert einen leeren Code (Konten ohne 2FA) statt
// eines Fehlers — der Aufrufer (login-Handshake, Task 7) lässt two-factor-token dann weg.
func currentTOTP(seed string) (string, error) {
	if seed == "" {
		return "", nil
	}
	code, err := totp.GenerateCode(seed, time.Now())
	if err != nil {
		return "", fmt.Errorf("fileee: totp generate: %w", err)
	}
	return code, nil
}

// contextKey vermeidet Kollisionen mit context-Keys anderer Pakete.
type contextKey int

const skipReauthKey contextKey = iota

// withSkipReauth markiert einen Request-Context so, dass der Transport (Task 10) bei einem 403
// KEINEN Re-Auth-Zyklus startet — verwendet für die Auth-Handshake-Endpunkte selbst, um
// rekursive Re-Auth-Versuche/Mutex-Deadlocks zu vermeiden (siehe Design-Entscheidung im Task-7-
// Brief: falscher Login-Versuch → 403 darf keinen weiteren Login auslösen, reauthMu ist nicht
// reentrant).
func withSkipReauth(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipReauthKey, true)
}

func isReauthSkipped(ctx context.Context) bool {
	v, _ := ctx.Value(skipReauthKey).(bool)
	return v
}

// Credentials sind die Login-Daten für das eigene Fileee-Konto (Umbrella-Spec §3.1). Der
// Aufrufer lädt sie aus einem Secret-Manager — die Lib kennt keinen Secret-Manager.
type Credentials struct {
	Username string
	Password string
	TOTPSeed string
}

// authClient bündelt den Login-Handshake unabhängig vom fertigen Client (Task 11 delegiert an
// ihn). Nicht exportiert — Aufrufer sehen nur Client.Login/.Logout/.EnsureSession/.AccountStatus.
type authClient struct {
	hc      *http.Client
	baseURL string
	creds   Credentials
	store   SessionStore
}

type existentRequestWire struct {
	Username string `json:"username"`
}

type existentResponseWire struct {
	Existent             bool `json:"existent"`
	TwoFactorAuthEnabled bool `json:"twoFactorAuthEnabled"`
}

// authCookieScopeURL liefert die URL, gegen die der Cookie-Jar nach Handshake-Cookies befragt
// wird. Bewusst NICHT die reine baseURL (Pfad "/"): Set-Cookie-Antworten des Handshakes
// (/api/f/start, /api/f/existent, /api/f/login) tragen kein explizites Path-Attribut, daher
// leitet http.CookieJar den Default-Path nach RFC 6265 §5.1.4 vom Request-Pfad ab — das
// Verzeichnis von z.B. "/api/f/login" ist "/api/f", NICHT "/". Eine Abfrage mit der reinen
// baseURL (Pfad "/") matcht diesen Cookie-Path dann NICHT (path-match schlägt fehl) und liefert
// still eine leere Cookie-Liste — live per Test belegt (TestPersistSessionUndCookieValue schlug
// mit der ursprünglich im Brief vorgesehenen `url.Parse(a.baseURL)`-Variante fehl). "/api/f/" ist
// sowohl Präfix eines expliziten Server-seitigen "Path=/" als auch des abgeleiteten "/api/f"
// Default-Path und deckt damit beide Fälle ab.
func (a *authClient) authCookieScopeURL() (*url.URL, error) {
	return url.Parse(a.baseURL + "/api/f/")
}

func (a *authClient) cookieValue(name string) string {
	if a.hc.Jar == nil {
		return ""
	}
	u, err := a.authCookieScopeURL()
	if err != nil {
		return ""
	}
	for _, c := range a.hc.Jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func (a *authClient) persistSession(ctx context.Context) error {
	u, err := a.authCookieScopeURL()
	if err != nil {
		return fmt.Errorf("fileee: base url parse: %w", err)
	}
	cookies := a.hc.Jar.Cookies(u)
	return a.store.Save(ctx, &Session{Cookies: cookies, SavedAt: time.Now()})
}

// login führt den vollen Handshake durch (API.md §2.1-§2.5):
// GET /api/f/start -> POST /api/f/existent -> POST /api/f/login (+TOTP falls aktiv).
func (a *authClient) login(ctx context.Context) error {
	ctx = withSkipReauth(ctx)

	startReq, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/api/f/start", nil)
	if err != nil {
		return fmt.Errorf("fileee: /f/start request: %w", err)
	}
	startResp, err := a.hc.Do(startReq)
	if err != nil {
		return fmt.Errorf("fileee: /f/start: %w", err)
	}
	startResp.Body.Close()

	existentBody, err := json.Marshal(existentRequestWire{Username: a.creds.Username})
	if err != nil {
		return fmt.Errorf("fileee: existent request encode: %w", err)
	}
	existentReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/api/f/existent", bytes.NewReader(existentBody))
	if err != nil {
		return fmt.Errorf("fileee: /f/existent request: %w", err)
	}
	existentReq.Header.Set("Content-Type", "application/json")
	existentResp, err := a.hc.Do(existentReq)
	if err != nil {
		return fmt.Errorf("fileee: /f/existent: %w", err)
	}
	defer existentResp.Body.Close()
	existentRespBody, err := io.ReadAll(existentResp.Body)
	if err != nil {
		return fmt.Errorf("fileee: /f/existent read: %w", err)
	}
	if existentResp.StatusCode != http.StatusOK {
		return parseAPIError(existentResp.StatusCode, existentRespBody)
	}
	var existent existentResponseWire
	if err := json.Unmarshal(existentRespBody, &existent); err != nil {
		return fmt.Errorf("fileee: /f/existent decode: %w", err)
	}
	if !existent.Existent {
		return ErrInvalidCredentials
	}

	form := url.Values{
		"username":                     {a.creds.Username},
		"password":                     {a.creds.Password},
		"conversationAddWithoutInvite": {"false"},
	}
	if existent.TwoFactorAuthEnabled {
		code, err := currentTOTP(a.creds.TOTPSeed)
		if err != nil {
			return err
		}
		form.Set("two-factor-token", code)
	}

	loginReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/api/f/login", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("fileee: /f/login request: %w", err)
	}
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResp, err := a.hc.Do(loginReq)
	if err != nil {
		return fmt.Errorf("fileee: /f/login: %w", err)
	}
	defer loginResp.Body.Close()
	loginBody, err := io.ReadAll(loginResp.Body)
	if err != nil {
		return fmt.Errorf("fileee: /f/login read: %w", err)
	}

	switch loginResp.StatusCode {
	case http.StatusOK:
		return a.persistSession(ctx)
	case http.StatusUnauthorized, http.StatusForbidden:
		if existent.TwoFactorAuthEnabled {
			return ErrTwoFactorInvalid
		}
		return ErrInvalidCredentials
	default:
		return parseAPIError(loginResp.StatusCode, loginBody)
	}
}

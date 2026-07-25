package fileee

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
	// Freshness-Fenster (Umbrella-Spec §4.5-Erweiterung): nach einem erfolgreichen Verify/Login
	// gilt die Session bis verifiedUntil als bestätigt; EnsureSession überspringt den user-session-
	// Round-Trip, solange das Fenster gilt. freshness<=0 deaktiviert das (Verhalten wie bisher:
	// jeder Aufruf verifiziert). now ist injizierbar für Tests (Default time.Now).
	freshMu       sync.Mutex
	verifiedUntil time.Time
	freshness     time.Duration
	now           func() time.Time
}

// nowFn liefert die (ggf. injizierte) Uhr.
func (a *authClient) nowFn() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// isFresh meldet, ob die Session aktuell als bestätigt gilt (innerhalb des Freshness-Fensters).
func (a *authClient) isFresh() bool {
	if a.freshness <= 0 {
		return false
	}
	a.freshMu.Lock()
	defer a.freshMu.Unlock()
	return a.nowFn().Before(a.verifiedUntil)
}

// markFresh verlängert das Freshness-Fenster nach einem erfolgreichen Verify/Login.
func (a *authClient) markFresh() {
	if a.freshness <= 0 {
		return
	}
	a.freshMu.Lock()
	a.verifiedUntil = a.nowFn().Add(a.freshness)
	a.freshMu.Unlock()
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
// Default-Path und deckt damit beide Fälle ab. Delegiert an apiCookieScopeURL (transport.go), das
// injectXSRF für exakt denselben Zweck nutzt — ein Pfad-Literal, zwei Aufrufer (DRY).
func (a *authClient) authCookieScopeURL() (*url.URL, error) {
	return apiCookieScopeURL(a.baseURL)
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

	// Fail-fast VOR dem POST /api/f/login: meldet der Server 2FA-Pflicht, aber es liegt kein
	// TOTP-Seed vor, würde der bisherige Code stillschweigend ein leeres "two-factor-token"
	// senden — der Login schlägt dann erst am Server mit 401/403 fehl und der Aufrufer sieht nur
	// das generische ErrTwoFactorInvalid, ohne zu erfahren, dass ihm schlicht der Seed fehlt
	// (Copilot-Review PR#7). errors.Is(err, ErrTwoFactorInvalid) bleibt weiterhin true (%w).
	if existent.TwoFactorAuthEnabled && a.creds.TOTPSeed == "" {
		return fmt.Errorf("fileee: Konto erfordert 2FA, aber kein TOTP-Seed konfiguriert: %w", ErrTwoFactorInvalid)
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

// userSessionWire ist die Wire-Form von GET /api/f/user-session (API.md §2.8, ADR-0005): eine
// leichte Authorized-Probe für eine bereits gespeicherte Session, inklusive Blocked-Zähler.
type userSessionWire struct {
	Authorized     bool    `json:"authorized"`
	SecondsBlocked float64 `json:"secondsBlocked"`
}

func (a *authClient) userSession(ctx context.Context) (*userSessionWire, error) {
	req, err := http.NewRequestWithContext(withSkipReauth(ctx), http.MethodGet, a.baseURL+"/api/f/user-session", nil)
	if err != nil {
		return nil, fmt.Errorf("fileee: user-session request: %w", err)
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fileee: user-session: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: user-session read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}
	var w userSessionWire
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("fileee: user-session decode: %w", err)
	}
	return &w, nil
}

// EnsureSession stellt sicher, dass eine gültige Session existiert (Umbrella-Spec §3.2):
// 1. gespeicherte Session laden, 2. per user-session prüfen, 3. bei ungültig/fehlend
// reauthentifizieren.
func (a *authClient) EnsureSession(ctx context.Context) error {
	return a.ensureSession(ctx, false)
}

// ensureSession implementiert EnsureSession mit optionalem Freshness-Bypass. force=true (Keepalive/
// RefreshSession) ignoriert das Freshness-Fenster und verifiziert immer.
func (a *authClient) ensureSession(ctx context.Context, force bool) error {
	if !force && a.isFresh() {
		return nil
	}
	sess, err := a.store.Load(ctx)
	if err != nil {
		return fmt.Errorf("fileee: session store load: %w", err)
	}
	if sess != nil && len(sess.Cookies) > 0 {
		loadCookiesIntoJar(a.hc.Jar, a.baseURL, sess.Cookies)
		if us, err := a.userSession(ctx); err == nil && us.Authorized {
			if us.SecondsBlocked > 0 {
				return &BlockedError{SecondsBlocked: int(us.SecondsBlocked)}
			}
			a.markFresh()
			return nil
		}
	}
	if err := a.reauthenticate(ctx); err != nil {
		return err
	}
	a.markFresh()
	return nil
}

// UserID liefert die eigene Fileee-User-ID der aktiven Session — bevorzugt aus dem `userId`-Cookie,
// sonst aus dem `sub`-Claim des JSESSIONID-JWT (ohne Signaturprüfung; die Session gilt bereits als
// vertrauenswürdig). Wird u. a. für den senderId beim Senden von Chat-Nachrichten gebraucht.
func (c *Client) UserID(ctx context.Context) (string, error) {
	if err := c.EnsureSession(ctx); err != nil {
		return "", err
	}
	if id := c.auth.cookieValue("userId"); id != "" {
		return id, nil
	}
	if sub := jwtSub(c.auth.cookieValue("JSESSIONID")); sub != "" {
		return sub, nil
	}
	return "", errors.New("fileee: user id not available in session")
}

// jwtSub extrahiert den sub-Claim aus einem (evtl. "jwt "-präfixierten) JWT, ohne die Signatur zu
// prüfen. Leerer Rückgabewert, wenn das Token nicht dekodierbar ist.
func jwtSub(raw string) string {
	raw = strings.TrimPrefix(raw, "jwt ")
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if payload, err = base64.URLEncoding.DecodeString(parts[1]); err != nil {
			return ""
		}
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.Sub
}

// tokenLogin ist der bevorzugte headless-Re-Auth-Pfad über das rememberMe-JWT-Cookie
// (API.md §2.6, LIVE bestätigt part4).
func (a *authClient) tokenLogin(ctx context.Context) error {
	ctx = withSkipReauth(ctx)
	token := a.cookieValue("rememberMe")
	if token == "" {
		return ErrSessionExpired
	}
	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/api/f/token/login", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("fileee: token/login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.hc.Do(req)
	if err != nil {
		return fmt.Errorf("fileee: token/login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return parseAPIError(resp.StatusCode, body)
	}
	return a.persistSession(ctx)
}

// reauthenticate implementiert §4.5 der Umbrella-Spec: zuerst token/login (rememberMe), bei
// Fehlschlag voller Passwort+TOTP-Login. Schlägt auch das fehl -> Fehler, der SOWOHL über
// errors.Is(err, ErrSessionExpired) auffindbar ist (bestehender Vertrag aller Aufrufer) ALS AUCH
// den zugrundeliegenden Fehler von login() bewahrt (errors.Is/errors.As) — vorher wurde dieser
// Fehler mit dem bloßen Rückgabewert ErrSessionExpired verworfen, ein Aufrufer konnte "Netzwerk
// kurz down" nicht von "Credentials sind ungültig/veraltet" unterscheiden (Whole-Branch-Review-
// Finding). EnsureSession funnelt auch gültige-Session-Netzwerkfehler hierher, daher ist die
// Unterscheidbarkeit wichtig.
func (a *authClient) reauthenticate(ctx context.Context) error {
	if err := a.tokenLogin(ctx); err == nil {
		return nil
	}
	if err := a.login(ctx); err != nil {
		return errors.Join(ErrSessionExpired, fmt.Errorf("fileee: reauth fehlgeschlagen: %w", err))
	}
	return nil
}

// logout meldet die aktuelle Session serverseitig ab und leert den lokalen SessionStore
// (API.md §2.7) — ein anschließendes EnsureSession führt damit zwingend einen vollen
// Reauth-Zyklus durch.
func (a *authClient) logout(ctx context.Context) error {
	req, err := http.NewRequestWithContext(withSkipReauth(ctx), http.MethodPost, a.baseURL+"/api/f/logout", nil)
	if err != nil {
		return fmt.Errorf("fileee: logout request: %w", err)
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return fmt.Errorf("fileee: logout: %w", err)
	}
	resp.Body.Close()
	// Der Server widerruft das Token; den lokalen Jar mitleeren, damit kein totes rememberMe-Cookie
	// zurückbleibt.
	a.clearJar()
	return a.store.Save(ctx, &Session{})
}

// clearJar entfernt alle Cookies der Basis-URL aus dem In-Memory-Cookie-Jar.
func (a *authClient) clearJar() {
	if a.hc.Jar == nil {
		return
	}
	u, err := url.Parse(a.baseURL)
	if err != nil {
		return
	}
	current := a.hc.Jar.Cookies(u)
	if len(current) == 0 {
		return
	}
	expired := make([]*http.Cookie, 0, len(current))
	for _, c := range current {
		expired = append(expired, &http.Cookie{Name: c.Name, Value: "", Path: "/", MaxAge: -1})
	}
	a.hc.Jar.SetCookies(u, expired)
}

// accountStatusWire ist die Wire-Form von GET /api/f/account-status (API.md §2.9). toDomain()
// übersetzt in die öffentliche AccountStatus-Form (types.go).
type accountStatusWire struct {
	AccountTypeID       string `json:"accountTypeId"`
	CurrentSubscription struct {
		Name      string  `json:"name"`
		Frequency string  `json:"frequency"`
		Amount    float64 `json:"amount"`
	} `json:"currentSubscription"`
	PayedUntil        string `json:"payedUntil"`
	NextLicenseRefill string `json:"nextLicenseRefill"`
	Problem           string `json:"problem"`
}

func (w accountStatusWire) toDomain() *AccountStatus {
	return &AccountStatus{
		AccountTypeID:      w.AccountTypeID,
		SubscriptionName:   w.CurrentSubscription.Name,
		SubscriptionFreq:   w.CurrentSubscription.Frequency,
		SubscriptionAmount: w.CurrentSubscription.Amount,
		PayedUntil:         decodeTimeValue(json.RawMessage(`"` + w.PayedUntil + `"`)),
		NextLicenseRefill:  decodeTimeValue(json.RawMessage(`"` + w.NextLicenseRefill + `"`)),
		Problem:            w.Problem,
	}
}

// accountStatus liefert Konto-/Abo-Informationen (API.md §2.9). Ruft zuerst EnsureSession auf,
// damit der Aufrufer nicht selbst um eine gültige Session kümmern muss.
func (a *authClient) accountStatus(ctx context.Context) (*AccountStatus, error) {
	if err := a.EnsureSession(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/api/f/account-status", nil)
	if err != nil {
		return nil, fmt.Errorf("fileee: account-status request: %w", err)
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fileee: account-status: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: account-status read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}
	var w accountStatusWire
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("fileee: account-status decode: %w", err)
	}
	return w.toDomain(), nil
}

package main

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/strausmann/go-fileee/fileee"
)

// statusError ist der eigene Huma-Fehlertyp von fileee-server. Er erfüllt huma.StatusError
// (GetStatus/Error), liefert im JSON-Response-Body aber AUSSCHLIESSLICH die zwei Felder "error"
// (menschenlesbare Meldung) und "code" (stabiler Kurzcode) — bewusst NICHT Humas
// Standard-Fehlerform huma.ErrorModel (type/title/status/detail/instance/errors, RFC 9457) —
// Spec §12 verlangt das schlanke {error, code}-Schema, damit N8N-Workflows und
// CI-Automatisierung ein stabiles, minimales Fehlerformat auswerten können.
type statusError struct {
	// status ist der HTTP-Status, den GetStatus() liefert. Bewusst unexportiert (kein
	// JSON-Feld) — der Status steht bereits im HTTP-Response-Status-Code, nicht nochmal im Body.
	status int
	// ErrorMsg ist Feld "error" im JSON-Body: eine menschenlesbare Meldung. Enthält NIEMALS
	// Token- oder sonstige Secret-Werte — nur Fileee-eigene öffentliche Fehlermeldungen oder
	// von uns fest formulierte Texte.
	ErrorMsg string `json:"error"`
	// ErrorCode ist Feld "code" im JSON-Body: ein stabiler, maschinenlesbarer Kurzcode, den
	// Aufrufer programmatisch auswerten können (z. B. "duplicate", "rate_limited").
	ErrorCode string `json:"code"`
}

// Error liefert die menschenlesbare Fehlermeldung und erfüllt damit das error-Interface.
func (e *statusError) Error() string { return e.ErrorMsg }

// GetStatus liefert den HTTP-Status und erfüllt damit huma.StatusError — Huma setzt diesen
// Status auf die Response, sobald ein Operation-Handler diesen Fehler zurückgibt (huma@v2
// huma.go: errors.As(err, &se) prüft StatusError NACH HeadersError, siehe withRetryAfter).
func (e *statusError) GetStatus() int { return e.status }

// newStatusError baut einen statusError mit den drei Pflichtangaben (Status, Kurzcode, Meldung).
func newStatusError(status int, code, msg string) *statusError {
	return &statusError{status: status, ErrorMsg: msg, ErrorCode: code}
}

// withRetryAfter verpackt err mit einem Retry-After-Header (Sekunden) über huma.ErrorWithHeaders.
//
// GEFUNDENER MECHANISMUS (Task 5, für Task 6/7/8 relevant): huma@v2 kennt zwei getrennte,
// optionale Interfaces auf einem von einem Operation-Handler zurückgegebenen error:
//   - huma.HeadersError  { GetHeaders() http.Header; Error() string }
//   - huma.StatusError   { GetStatus() int;          Error() string }
//
// huma.go registriert je Operation einen generierten Handler, der den vom Nutzer-Handler
// zurückgegebenen error zuerst per errors.As auf huma.HeadersError prüft (setzt bei Treffer alle
// Header auf die Response) und ERST DANACH per errors.As auf huma.StatusError prüft (bestimmt
// Status + Body). huma.ErrorWithHeaders(err, headers) liefert einen Wrapper (*errWithHeaders),
// der NUR huma.HeadersError erfüllt (kein GetStatus()) und den ursprünglichen err per Unwrap()
// weiterreicht — deshalb muss mapError als Rückgabetyp das eingebaute error-Interface haben,
// NICHT huma.StatusError (der Wrapper würde huma.StatusError sonst nicht erfüllen und der Code
// nicht kompilieren). errors.As findet den inneren *statusError trotzdem über die Unwrap-Kette.
//
// Es gibt (Stand huma v2.35.0) KEIN globales Retry-After-Override und keinen dritten Weg über
// huma.NewError — die Kombination HeadersError+StatusError ist der einzige/idiomatische Weg,
// einem Fehler zusätzliche Response-Header mitzugeben.
func withRetryAfter(err *statusError, seconds int) error {
	return huma.ErrorWithHeaders(err, http.Header{"Retry-After": []string{strconv.Itoa(seconds)}})
}

// mapError bildet einen Fehler der Core-Lib (github.com/strausmann/go-fileee/fileee) auf einen
// Huma-tauglichen HTTP-Fehler ab (Spec §12,
// docs/superpowers/specs/2026-07-24-fileee-server-design.md im homelab-management-Repo). Die
// Zuordnung läuft ausschließlich über errors.Is/errors.As gegen die von der Lib exportierten
// Sentinel-Fehler bzw. Typen (fileee/errors.go) — NIE anhand von Fehlertexten oder eigenen
// Statuscode-Vermutungen. nil liefert nil (kein Fehler).
//
// Mapping:
//   - fileee.ErrDuplicateDocument           → 409 "duplicate"
//   - fileee.ErrUnsupportedFileType         → 415 "unsupported_file_type"
//   - fileee.ErrRateLimited                 → 429 "rate_limited" (kein Retry-After, siehe unten)
//   - *fileee.BlockedError                  → 503 "blocked" + Retry-After: <SecondsBlocked>
//   - fileee.ErrNotFound                    → 404 "not_found"
//   - fileee.ErrSessionExpired              → 502 "upstream_auth" (Reauth nach Session-Ablauf
//     endgültig fehlgeschlagen, ADR-0005/fileee/auth.go)
//   - sonstiger *fileee.APIError            → dessen eigener HTTPStatus (Pass-Through), Code/
//     Message der Lib (mit Fallback, falls die Lib defensiv leer geparst hat — siehe
//     fileee/errors.go parseAPIError)
//   - alles andere                          → 500 "internal_error"
//
// KEIN Retry-After bei ErrRateLimited (429): *fileee.APIError trägt nur HTTPStatus/Code/Message/
// Localized (fileee/errors.go) — der ursprüngliche Retry-After-Header der 429-Antwort wird von
// der Lib nicht mitgeführt, und ErrRateLimited wird laut fileee/errors.go-Kommentar erst
// zurückgegeben, NACHDEM die Lib ihre eigenen Backoff-Retries bereits ausgeschöpft hat. Ohne
// einen von der Lib belegten Sekundenwert wird hier bewusst KEIN Retry-After erfunden — anders
// als bei BlockedError, das SecondsBlocked konkret kennt. Folge-Kandidat: die Lib könnte den
// Retry-After-Header künftig in APIError aufnehmen (dann hier nachziehen).
func mapError(err error) error {
	if err == nil {
		return nil
	}

	var blocked *fileee.BlockedError
	var apiErr *fileee.APIError

	switch {
	case errors.Is(err, fileee.ErrDuplicateDocument):
		return newStatusError(http.StatusConflict, "duplicate", "document already exists")
	case errors.Is(err, fileee.ErrUnsupportedFileType):
		return newStatusError(http.StatusUnsupportedMediaType, "unsupported_file_type", "unsupported file type")
	case errors.Is(err, fileee.ErrRateLimited):
		return newStatusError(http.StatusTooManyRequests, "rate_limited", "rate limited by fileee")
	case errors.As(err, &blocked):
		msg := fmt.Sprintf("account blocked for %ds", blocked.SecondsBlocked)
		return withRetryAfter(newStatusError(http.StatusServiceUnavailable, "blocked", msg), blocked.SecondsBlocked)
	case errors.Is(err, fileee.ErrNotFound):
		return newStatusError(http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, fileee.ErrSessionExpired):
		return newStatusError(http.StatusBadGateway, "upstream_auth", "fileee session expired and re-authentication failed")
	case errors.As(err, &apiErr):
		code := apiErr.Code
		if code == "" {
			code = "api_error"
		}
		msg := apiErr.Message
		if msg == "" {
			msg = "fileee api error"
		}
		return newStatusError(apiErr.HTTPStatus, code, msg)
	default:
		return newStatusError(http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

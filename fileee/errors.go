package fileee

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Sentinel-Fehler für die häufigsten Fileee-Fehlerfälle (API.md §2.6/§4.1). Aufrufer prüfen mit
// errors.Is, ob eine Antwort einem dieser Fälle entspricht.
var (
	ErrInvalidCredentials = errors.New("fileee: invalid credentials")
	ErrTwoFactorInvalid   = errors.New("fileee: invalid two-factor token")
	ErrSessionExpired     = errors.New("fileee: session expired")
	ErrNotFound           = errors.New("fileee: resource not found")
)

// BlockedError wird zurückgegeben, wenn user-session.secondsBlocked > 0 meldet (API.md §2.8,
// ADR-0005) — der Aufrufer MUSS warten, kein blindes Retry.
type BlockedError struct {
	SecondsBlocked int
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("fileee: account blocked for %ds", e.SecondsBlocked)
}

// APIError kapselt eine strukturierte Fehlerantwort. Fileee liefert je nach Fall
// {apiError, errorCode, errorMessage} (z.B. 404) bzw. {apiError, errorMessage, localizedMessage}
// (z.B. 403 auf /f/exists) — bei 403 auf /diff-Endpunkten teils LEEREN Body (API.md §2.6, LIVE
// bestätigt part4 2026-07-23).
type APIError struct {
	HTTPStatus int
	Code       string
	Message    string
	Localized  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("fileee: api error %s (http %d): %s", e.Code, e.HTTPStatus, e.Message)
}

// apiErrorBody deckt beide belegten Fehler-Body-Varianten strukturell ab (Felder sind ein
// Superset — nicht jede Variante befüllt jedes Feld). Zeiger statt Werttypen, damit "Feld fehlt"
// (nil) von "Feld ist Leerstring" unterscheidbar bleibt.
type apiErrorBody struct {
	APIError         *string `json:"apiError"`
	ErrorCode        *string `json:"errorCode"`
	ErrorMessage     *string `json:"errorMessage"`
	LocalizedMessage *string `json:"localizedMessage"`
}

// parseAPIError liest den Response-Body (falls vorhanden) und baut daraus einen *APIError.
// Bei leerem oder nicht-JSON Body bleiben Code/Message/Localized leer, HTTPStatus ist trotzdem
// gesetzt — kein Panic, kein Fehler-Return (defensiv, ADR-0003: reverse-engineertes API liefert
// nicht immer den erwarteten Body).
func parseAPIError(status int, body []byte) *APIError {
	e := &APIError{HTTPStatus: status}
	if len(body) == 0 {
		return e
	}
	var b apiErrorBody
	if err := json.Unmarshal(body, &b); err != nil {
		return e
	}
	// errorCode ist das spezifischere Feld (z.B. 404-Fälle liefern beide) — vorrangig
	// verwenden, sonst auf apiError zurückfallen (z.B. 403-Fälle ohne errorCode).
	switch {
	case b.ErrorCode != nil:
		e.Code = *b.ErrorCode
	case b.APIError != nil:
		e.Code = *b.APIError
	}
	if b.ErrorMessage != nil {
		e.Message = *b.ErrorMessage
	}
	if b.LocalizedMessage != nil {
		e.Localized = *b.LocalizedMessage
	}
	return e
}

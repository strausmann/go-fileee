package fileee

import (
	"errors"
	"testing"
)

func TestParseAPIError(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      []byte
		wantCode  string
		wantMsg   string
		wantLocal string
	}{
		{
			name:      "404 mit apiError/errorCode/errorMessage",
			status:    404,
			body:      []byte(`{"apiError":"not_found","errorCode":"DOC_404","errorMessage":"Dokument nicht gefunden"}`),
			wantCode:  "DOC_404",
			wantMsg:   "Dokument nicht gefunden",
			wantLocal: "",
		},
		{
			name:      "403 mit localizedMessage (z.B. /f/exists)",
			status:    403,
			body:      []byte(`{"apiError":"forbidden","errorMessage":"csrf mismatch","localizedMessage":"Sitzung abgelaufen"}`),
			wantCode:  "forbidden",
			wantMsg:   "csrf mismatch",
			wantLocal: "Sitzung abgelaufen",
		},
		{
			name:      "403 mit leerem Body (diff-Endpunkte, API.md §2.6)",
			status:    403,
			body:      nil,
			wantCode:  "",
			wantMsg:   "",
			wantLocal: "",
		},
		{
			// LIVE VERIFIZIERT 2026-07-23 gegen Testkonto (Contacts.Create, "Invalid Id format"):
			// errorCode kommt hier als JSON-ZAHL statt als String — ohne die RawMessage-basierte
			// Dekodierung (decodeErrorCode) würde json.Unmarshal an dieser Stelle abbrechen und der
			// GESAMTE Body (inkl. errorMessage) verworfen (siehe apiErrorBody-Kommentar).
			name:      "400 mit numerischem errorCode (LIVE verifiziert, Contacts.Create)",
			status:    400,
			body:      []byte(`{"errorCode":10,"apiError":"IllegalConditions","errorMessage":"Your API Call did not match required conditions. Invalid Id format"}`),
			wantCode:  "10",
			wantMsg:   "Your API Call did not match required conditions. Invalid Id format",
			wantLocal: "",
		},
		{
			name:      "kaputtes JSON -> nur Status übernehmen, kein Panic",
			status:    500,
			body:      []byte(`nicht-json`),
			wantCode:  "",
			wantMsg:   "",
			wantLocal: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := parseAPIError(tc.status, tc.body)
			if err.HTTPStatus != tc.status {
				t.Errorf("HTTPStatus = %d, erwartet %d", err.HTTPStatus, tc.status)
			}
			if err.Code != tc.wantCode {
				t.Errorf("Code = %q, erwartet %q", err.Code, tc.wantCode)
			}
			if err.Message != tc.wantMsg {
				t.Errorf("Message = %q, erwartet %q", err.Message, tc.wantMsg)
			}
			if err.Localized != tc.wantLocal {
				t.Errorf("Localized = %q, erwartet %q", err.Localized, tc.wantLocal)
			}
		})
	}
}

func TestErrorsIsUndAsVerhalten(t *testing.T) {
	wrapped := fmtErrorfWrap(ErrSessionExpired)
	if !errors.Is(wrapped, ErrSessionExpired) {
		t.Errorf("errors.Is erkennt gewrapptes ErrSessionExpired nicht")
	}

	var blocked *BlockedError
	var errAny error = &BlockedError{SecondsBlocked: 30}
	if !errors.As(errAny, &blocked) {
		t.Fatalf("errors.As erkennt *BlockedError nicht")
	}
	if blocked.SecondsBlocked != 30 {
		t.Errorf("SecondsBlocked = %d, erwartet 30", blocked.SecondsBlocked)
	}
	if blocked.Error() == "" {
		t.Errorf("BlockedError.Error() liefert leeren String")
	}

	var apiErr *APIError
	var errAny2 error = parseAPIError(404, []byte(`{"errorCode":"X"}`))
	if !errors.As(errAny2, &apiErr) {
		t.Fatalf("errors.As erkennt *APIError nicht")
	}
	if apiErr.Error() == "" {
		t.Errorf("APIError.Error() liefert leeren String")
	}
}

// fmtErrorfWrap ist ein winziger Test-Helper, um %w-Wrapping zu simulieren, ohne fmt hier zu
// importieren.
func fmtErrorfWrap(err error) error {
	return &wrappedErr{err}
}

type wrappedErr struct{ inner error }

func (w *wrappedErr) Error() string { return "wrapped: " + w.inner.Error() }
func (w *wrappedErr) Unwrap() error { return w.inner }

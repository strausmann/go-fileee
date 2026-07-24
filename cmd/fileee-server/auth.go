package main

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// unauthorizedBody ist der feststehende JSON-Body für jede 401-Antwort der Auth-Middleware.
// Er enthält absichtlich NIEMALS den konfigurierten oder den präsentierten Token-Wert — weder
// bei fehlendem noch bei falschem Token.
const unauthorizedBody = `{"error":"unauthorized"}`

// APITokenAuth liefert Middleware, die next hinter einem einzigen statischen API-Token
// absichert. Der Token wird aus dem Header X-API-Key oder, falls dieser fehlt, aus
// Authorization: Bearer <token> gelesen und per crypto/subtle.ConstantTimeCompare
// zeitkonstant mit token verglichen — so verrät die Antwortzeit nichts über die Länge oder den
// Inhalt des korrekten Tokens. Ist exempt(r.URL.Path) true, wird next OHNE jede Token-Prüfung
// aufgerufen (für unauthentifizierte Routen wie /healthz, /openapi.json oder /docs). Fehlt der
// Token oder stimmt er nicht, antwortet die Middleware mit HTTP 401, Content-Type
// application/json und dem festen Body {"error":"unauthorized"} — bewusst 401 statt 403, damit
// CrowdSecs http-bruteforce-Szenario die fehlgeschlagenen Versuche zählt. Der präsentierte
// Token-Wert wird in keinem Fall geloggt oder in die Antwort übernommen.
func APITokenAuth(token string, exempt func(path string) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if exempt != nil && exempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		if !tokenMatches(token, presentedToken(r)) {
			writeUnauthorized(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// presentedToken extrahiert den vom Client präsentierten Token aus dem Request: zuerst
// X-API-Key, sonst Authorization: Bearer <token> (Präfix "Bearer " wird entfernt). Sind beide
// Header leer oder abwesend, liefert presentedToken einen leeren String.
func presentedToken(r *http.Request) string {
	if v := r.Header.Get("X-API-Key"); v != "" {
		return v
	}
	const prefix = "Bearer "
	if v := r.Header.Get("Authorization"); strings.HasPrefix(v, prefix) {
		return strings.TrimPrefix(v, prefix)
	}
	return ""
}

// tokenMatches vergleicht got mit want zeitkonstant über subtle.ConstantTimeCompare. Bei
// unterschiedlicher Länge liefert ConstantTimeCompare bereits intern 0 (kein Panic, kein
// zusätzlicher eigener Early-Return nötig) — ein separater manueller Längenvergleich davor
// würde nur denselben ohnehin in ConstantTimeCompare enthaltenen Kurzschluss duplizieren, ohne
// zusätzliche Sicherheit zu bringen.
//
// Ausnahme davon ist ein leerer konfigurierter Token (want == ""): ConstantTimeCompare liefert
// für zwei leere Byte-Slices 1 (Match), was ohne diesen Guard bei einer Fehlkonfiguration
// (Middleware mit token == "" aufgesetzt) UND fehlendem/leerem Client-Header zu einem
// stillschweigenden Fail-Open (kompletter Auth-Bypass) führen würde. Die Middleware ist die
// Sicherheitsgrenze und muss unabhängig von Aufrufer-Fehlkonfiguration fail-closed bleiben —
// deshalb wird ein leerer want-Wert vor dem eigentlichen Vergleich explizit abgelehnt.
func tokenMatches(want, got string) bool {
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

// writeUnauthorized schreibt die feste 401-Antwort (Content-Type application/json, Body
// {"error":"unauthorized"}) — niemals den präsentierten oder konfigurierten Token-Wert.
func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(unauthorizedBody))
}

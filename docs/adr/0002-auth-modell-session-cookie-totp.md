# ADR-0002: Auth-Modell — Session-Cookie + headless TOTP

**Status:** accepted
**Datum:** 2026-07-23
**Ersetzt:** —
**Ersetzt durch:** —
**Verwandt:** ADR-0001, ADR-0003

## Kontext

Die HAR-Analyse des `my.fileee.com`-Web-App-Traffics (siehe [`docs/API.md`](../API.md) Abschnitt 2) zeigt: Es gibt **kein** Bearer- oder Refresh-Token-API. Die Login-Response (`POST /api/f/login`) enthält keinen Token im Body — die Authentifizierung lebt ausschließlich in einem httpOnly-Session-Cookie plus einem CSRF-Header `x-xsrf-token` (klassisches Double-Submit-Cookie-Pattern, Wert kommt aus dem `XSRF-TOKEN`-Cookie).

Zusätzlich ist 2FA per TOTP aktiv. Für eine automatisierte, headless nutzbare Library muss der Login-Flow ohne manuelle Eingabe eines 2FA-Codes funktionieren.

## Entscheidung

Die Lib implementiert den Login-Flow exakt wie von der Web-App beobachtet:

```
GET  /api/f/start                                     → Session/CSRF-Cookie initialisieren
POST /api/f/existent   {username}                      → Konto- + 2FA-Status prüfen
POST /api/f/login      {username, password,
                         two-factor-token, ...}         → Session-Cookie setzen
```

Der TOTP-Code wird **im selben Request** wie Username/Passwort als `two-factor-token` mitgeschickt (kein separater 2FA-Schritt). Die Lib generiert den Code selbst aus einem gespeicherten **TOTP-Seed** (RFC 6238, Bibliothek `pquerna/otp`) — der Seed wird beim Login-Aufruf vom Aufrufer übergeben (aus Vaultwarden/Infisical geladen, siehe ADR-0004/§6 API.md).

Die Cookie-Jar (Session-Cookie + `XSRF-TOKEN`) wird persistiert (Datei, Rechte `0600`), damit nicht bei jedem Programmstart neu eingeloggt werden muss. Bei `401` oder `authorized:false` (aus `GET /api/f/user-session`) führt die Lib automatisch einen Re-Login durch.

Der `x-xsrf-token`-Header wird von der Lib **automatisch** auf allen mutierenden Requests (`POST`/`PUT`) aus dem aktuellen Cookie-Jar-Wert gesetzt — Aufrufer müssen sich darum nicht kümmern.

## Konsequenzen

**Positiv:**
- Vollständig headless-fähig — keine manuelle 2FA-Eingabe nötig, solange der TOTP-Seed sicher hinterlegt ist.
- Persistierte Cookie-Jar reduziert unnötige Logins (schont Rate-Limits, siehe `secondsBlocked` in `user-session`).
- Auth-Logik ist an einer Stelle gekapselt (`fileee/auth.go`) — CSRF-Handling ist für Aufrufer transparent.

**Negativ / akzeptiertes Risiko:**
- TOTP-Seed ist ein hochsensibles Secret — Kompromittierung erlaubt vollständigen Kontozugriff inkl. 2FA-Bypass. Muss zwingend in einem Secret-Manager liegen, nie im Repo oder in Logs.
- Die persistierte Session-Cookie-Jar ist selbst ein Secret (kürzere Lebensdauer als der TOTP-Seed, aber bei Diebstahl reicht sie für Session-Hijacking bis zum nächsten Re-Login).
- Der genaue Zweck von `GET /api/f/start` und die Session-Lebensdauer sind noch nicht live verifiziert (siehe API.md §7, Punkte 5–6) — das Re-Login-Verhalten kann sich nach Verifikation noch anpassen.

## Referenzen

- [`docs/API.md`](../API.md) Abschnitt 2 (Authentifizierung), Abschnitt 6 (Sicherheitshinweise)

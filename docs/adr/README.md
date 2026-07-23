# Architecture Decision Records — go-fileee

Diese Registry führt alle Architecture Decision Records (ADRs) für `go-fileee`. ADRs dokumentieren wichtige, langfristig wirkende Entscheidungen — inklusive Kontext und Konsequenzen — damit spätere Sessions und Mitwirkende nachvollziehen können, **warum** etwas so gebaut wurde und nicht anders.

Neues ADR anlegen: Kopiere [`template.md`](template.md) nach `docs/adr/NNNN-slug.md` (nächste freie Nummer, vierstellig) und trage es unten in die Tabelle ein.

## Registry

| Nr. | Titel | Status | Datum |
|-----|-------|--------|-------|
| [0001](0001-library-first-architektur.md) | Library-first-Architektur | accepted | 2026-07-23 |
| [0002](0002-auth-modell-session-cookie-totp.md) | Auth-Modell: Session-Cookie + headless TOTP | accepted | 2026-07-23 |
| [0003](0003-reverse-engineered-api-risiko.md) | Reverse-engineered internes API (akzeptiertes Risiko) | accepted | 2026-07-23 |
| [0004](0004-test-strategie.md) | Test-Strategie | accepted | 2026-07-23 |

## Status-Werte

| Status | Bedeutung |
|--------|-----------|
| `proposed` | Entwurf, noch nicht final entschieden |
| `accepted` | Gültig, wird umgesetzt/befolgt |
| `superseded` | Durch ein neueres ADR vollständig abgelöst (siehe `Ersetzt durch` im Header) |
| `deprecated` | Nicht mehr gültig, ohne direkten Nachfolger |

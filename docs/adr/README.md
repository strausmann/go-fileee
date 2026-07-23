# Architecture Decision Records — go-fileee

Diese Registry führt alle Architecture Decision Records (ADRs) für `go-fileee`. ADRs dokumentieren wichtige, langfristig wirkende Entscheidungen — inklusive Kontext und Konsequenzen — damit spätere Sessions und Mitwirkende nachvollziehen können, **warum** etwas so gebaut wurde und nicht anders.

Neues ADR anlegen: Kopiere [`template.md`](template.md) nach `docs/adr/NNNN-slug.md` (nächste freie Nummer, vierstellig) und trage es unten in die Tabelle ein.

## ADR-Regelwerk

- **Was/Wann:** Ein ADR dokumentiert **jede bedeutsame Architektur-, Technologie- oder Betriebs-Entscheidung** samt Kontext und Konsequenzen — damit spätere Sessions und Mitwirkende nachvollziehen können, warum etwas so entschieden wurde. **Nicht** für Trivialitäten, reine Formatierung oder offensichtliche Umsetzungsdetails.
- **Nummerierung:** fortlaufend `NNNN` (4-stellig, z. B. `0006`), Dateiname `NNNN-kebab-slug.md`. Nummern werden **nie wiederverwendet** — auch nicht für abgelöste/verworfene ADRs.
- **Template:** Für jedes neue ADR **immer** [`template.md`](template.md) als Ausgangspunkt nutzen (einheitliche Felder Status/Datum/Lineage/Kontext/Entscheidung/Konsequenzen/Referenzen).
- **Status-Lifecycle:** `proposed` → `accepted` → (`superseded` | `deprecated`). Ein ADR startet als `proposed`, wird nach Freigabe `accepted`, und geht bei Ablösung/Ungültigkeit in einen der Endzustände über.
- **Lineage (beidseitig pflegen):** Die Header-Felder `Ersetzt` / `Ersetzt durch` (vollständige Ablösung → Vorgänger auf `superseded` setzen) und `Verwandt` (Querbezug ohne Ablösung) werden **auf beiden beteiligten ADRs** eingetragen. **Beim Ablösen nur den Header** des alten ADR anfassen (Status + `Ersetzt durch`) — Kontext und Entscheidung des alten ADR werden **nie umgeschrieben** (sie sind ein historisches Protokoll).
- **Registry-Pflicht:** Jedes neue oder im Status geänderte ADR wird **sofort** in die Registry-Tabelle unten eingetragen bzw. aktualisiert (Nr, Titel, Status, Datum). Ein ADR, das nicht in der Registry steht, gilt als „übersehen" und damit als nicht existent.
- **Sprache:** ADRs werden auf **Deutsch** verfasst (echte Umlaute ä ö ü ß), Code/CLI/Bezeichner auf Englisch.

## Registry

| Nr. | Titel | Status | Datum |
|-----|-------|--------|-------|
| [0001](0001-library-first-architektur.md) | Library-first-Architektur | accepted | 2026-07-23 |
| [0002](0002-auth-modell-session-cookie-totp.md) | Auth-Modell: Session-Cookie + headless TOTP | accepted | 2026-07-23 |
| [0003](0003-reverse-engineered-api-risiko.md) | Reverse-engineered internes API (akzeptiertes Risiko) | accepted | 2026-07-23 |
| [0004](0004-test-strategie.md) | Test-Strategie | accepted | 2026-07-23 |
| [0005](0005-schonender-betrieb-rate-limiting.md) | Schonender Betrieb / Rate-Limiting | accepted | 2026-07-23 |

## Status-Werte

| Status | Bedeutung |
|--------|-----------|
| `proposed` | Entwurf, noch nicht final entschieden |
| `accepted` | Gültig, wird umgesetzt/befolgt |
| `superseded` | Durch ein neueres ADR vollständig abgelöst (siehe `Ersetzt durch` im Header) |
| `deprecated` | Nicht mehr gültig, ohne direkten Nachfolger |

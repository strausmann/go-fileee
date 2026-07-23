# ADR-0001: Library-first-Architektur

**Status:** accepted
**Datum:** 2026-07-23
**Ersetzt:** —
**Ersetzt durch:** —
**Verwandt:** ADR-0002, ADR-0004, ADR-0006

## Kontext

`go-fileee` hat mehrere Konsumenten, die dieselbe Fileee-Logik brauchen, aber unterschiedliche Aufgaben haben:

- eine **CLI** (`cmd/fileee`), die Dokumente exportiert, in ein dauerhaftes Disk-Archiv überführt und nach Paperless-ngx migriert (inklusive inkrementellem Sync),
- ein **MCP-Server** (`cmd/fileee-mcp`), der Fileee-Inhalte für AI-Tools (Claude Code, ChatGPT, …) zugänglich macht,
- ein **externes Scanner-Projekt**, das gescannte Dokumente direkt nach Fileee hochlädt.

Ohne klare Trennung würde Fileee-spezifischer HTTP-/Auth-/Entity-Code in mehreren Repos dupliziert oder — schlimmer — Paperless-spezifisches Wissen (Correspondents, Custom-Fields, Document-Types-Mapping) würde sich in eine Schicht mischen, die eigentlich nur „Fileee sprechen" soll. Das würde die Wiederverwendung im Scanner-Projekt erschweren und die Testbarkeit verschlechtern.

## Entscheidung

`go-fileee` wird als **eigenständige, zustandslose Core-Lib** (`fileee/`) gebaut, die ausschließlich das Fileee-Protokoll kapselt: Auth, Entities (Dokumente, Tags, Companies, Contacts, Document-Types), Download, Upload. Die Lib hält **keinen** Sync-Cursor selbst — das bleibt Sache des Aufrufers (Cursor-Objekt wird übergeben/zurückgegeben).

Darauf aufbauend gibt es zwei dünne Adapter im selben Repo:

- `cmd/fileee` — die CLI, die Export/Migration/Sync orchestriert.
- `cmd/fileee-mcp` — der MCP-Server für AI-Zugriff.

**Paperless-ngx-spezifisches Wissen (Mapping-Regeln, API-Calls gegen Paperless) lebt ausschließlich in `internal/paperless`, das nur von der CLI importiert wird — niemals in der Core-Lib.** Die Core-Lib kennt Paperless nicht.

Das externe Scanner-Projekt importiert `fileee/` direkt als Go-Modul-Abhängigkeit (`github.com/strausmann/go-fileee/fileee`).

## Konsequenzen

**Positiv:**
- Die Core-Lib ist im Scanner-Projekt ohne Umweg wiederverwendbar (kein Vendoring, kein Copy-Paste).
- Klare Verantwortungsgrenzen: Wer die Core-Lib ändert, weiß, dass er das Fileee-Protokoll ändert — nicht Paperless-Mapping oder MCP-Verhalten.
- Tests der Core-Lib brauchen kein Paperless-Wissen und keinen MCP-Kontext (schlankere Test-Fixtures).

**Negativ / akzeptiertes Risiko:**
- Etwas mehr initiale Struktur (drei Pakete statt eines monolithischen Tools) — für ein Ein-Personen-Projekt ein bewusster Mehraufwand, der sich durch Wiederverwendung im Scanner-Projekt auszahlt.
- Das zustandslose Design verlagert Cursor-Persistenz-Verantwortung auf jeden Aufrufer einzeln (CLI, MCP, Scanner müssen das jeweils selbst lösen).

## Referenzen

- [`docs/API.md`](../API.md)

# ADR-0006: Domänen-Neutralität

**Status:** accepted
**Datum:** 2026-07-23
**Ersetzt:** —
**Ersetzt durch:** —
**Verwandt:** ADR-0001

## Kontext

`go-fileee` war ursprünglich stark auf einen konkreten Zweck ausgerichtet — die Migration von Fileee zu Paperless-ngx. ADR-0001 hielt Paperless-Wissen zwar aus der Core-Lib heraus, ließ es aber im selben Repo (in der CLI bzw. `internal/paperless`) leben.

Diese Kopplung ist unnötig einschränkend: Die Library soll ein **neutraler, allgemeiner** Fileee-Client sein, der für viele Zwecke wiederverwendbar ist — Export, Backup/Archivierung, Automatisierung, AI-/MCP-Zugriff, Scanner-Upload, und Migration zu einem **beliebigen** DMS. Das gilt auch für Szenarien, in denen Fileee **weiter genutzt** wird (kein Wegzug), sondern nur programmatisch angesprochen werden soll. Sobald ein bestimmtes Zielsystem (Paperless o. ä.) im Repo verankert ist, entsteht der Eindruck einer Spezial-Tools und die Wiederverwendung leidet.

## Entscheidung

`go-fileee` enthält **keinen** domänen- oder zielsystem-spezifischen Code:

- Die **Core-Lib (`fileee/`)** kapselt ausschließlich das Fileee-Protokoll (Auth, Entities, Download, Upload) — zustandslos, ohne Wissen über irgendein Fremd-/Zielsystem.
- Die **CLI (`cmd/fileee`)** ist **generisch**: `export` / `sync` / `download` / `upload` in ein dauerhaftes Disk-Archiv. Sie enthält **keinen** DMS-/zielsystem-spezifischen Code (kein Paperless-Mapping, keine Paperless-API-Calls).
- Es gibt **kein** `internal/paperless` oder vergleichbares zielsystem-gebundenes Paket in diesem Repo.
- **Konsumierende Integrationen** — etwa eine Paperless-Migration oder ein Scanner-Upload-Tool — leben in **separaten Repositories**, die `go-fileee` als Go-Modul-Library importieren.

Dieses ADR **verfeinert ADR-0001**: Der dort formulierte Grundsatz „Paperless-Wissen nur in der CLI, nie in der Lib" wird verschärft zu „**gar kein** Ziel-/Fremdsystem-Wissen im Repo — auch nicht in der CLI".

## Konsequenzen

**Positiv:**
- **Maximale Wiederverwendbarkeit:** go-fileee ist ein sauberer, allgemeiner Fileee-Client für beliebige Konsumenten und Zwecke.
- Klare Grenze: Wer das Repo liest, sieht „Fileee sprechen", nicht „nach Paperless migrieren".
- Externe Consumer können unabhängig versioniert, getestet und veröffentlicht werden, ohne go-fileee zu berühren.

**Negativ / bewusst in Kauf genommen:**
- Die Zielsystem-Kopplung (z. B. Paperless-Mapping) liegt **außerhalb** dieses Repos — es braucht ein separates Consumer-Projekt, das go-fileee importiert. Das ist etwas mehr Projekt-Overhead als ein Monorepo, aber der Preis für saubere Wiederverwendbarkeit.
- Das Paperless-Mapping in [`docs/API.md`](../API.md) bleibt als **Beispiel/Referenz** für externe Consumer erhalten, ist aber ausdrücklich **nicht** Teil der Lib-Implementierung.

## Referenzen

- [`docs/API.md`](../API.md)
- [ADR-0001](0001-library-first-architektur.md) (Library-first-Architektur — wird hiermit verfeinert)

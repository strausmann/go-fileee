# go-fileee

[![CI](https://github.com/strausmann/go-fileee/actions/workflows/test.yml/badge.svg)](https://github.com/strausmann/go-fileee/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/strausmann/go-fileee/fileee.svg)](https://pkg.go.dev/github.com/strausmann/go-fileee/fileee)
[![Go Report Card](https://goreportcard.com/badge/github.com/strausmann/go-fileee)](https://goreportcard.com/report/github.com/strausmann/go-fileee)
[![Go Version](https://img.shields.io/github/go-mod/go-version/strausmann/go-fileee)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Eine **inoffizielle** Go-Client-Library für das **interne** Web-App-API von [Fileee](https://www.fileee.com) (`my.fileee.com`). Fileee bietet kein öffentliches API — diese Library kapselt das Protokoll, das die eigene Web-App verwendet, rekonstruiert aus mitgeschnittenem Netzwerk-Traffic eines eingeloggten eigenen Kontos.

> **Status:** In Entwicklung — privat. Noch keine stabile Version, kein `v0`-Tag, API kann sich jederzeit ändern.

## Über Fileee

[Fileee](https://www.fileee.com) ist ein Dokumentenmanagement-Dienst der fileee GmbH (Deutschland),
der Papierkram digitalisiert und automatisch ordnet. Belege werden per App gescannt oder importiert;
eine Texterkennung (OCR) erkennt Dokumenttyp, Absender, Datum und Fristen automatisch, sodass sich
alles per Volltext durchsuchen lässt. Dazu kommen Fristen-Erinnerungen, Teilen über „fileee Spaces"
(inkl. Bearbeitungsschutz) sowie Export- und Direkt-Integrationen (u. a. DATEV, lexoffice, SevDesk).

Datenschutz ist ein Kernversprechen: **Hosting in Deutschland, DSGVO-konform**, Dokumente werden
**individuell verschlüsselt** gespeichert, Zwei-Faktor-Authentifizierung ist verfügbar.

Ergänzt wird der Dienst durch die **fileeeBox** — eine physische Ablagebox mit individuellem
Barcode: Dokumente werden gescannt und einfach oben in die Box gelegt; die App merkt sich Box und
Position. Über die Box eingescannte Seiten (bis zu 600) belasten das monatliche Upload-Kontingent
nicht. Als günstige Selbstbau-Variante gibt es **fileeeDIY** (Druckvorlagen für Schuhkarton/Ordner).

*Diese Library ist ein inoffizielles, unabhängiges Community-Projekt und steht in keiner Verbindung
zur fileee GmbH.*

## Installation

```bash
go get github.com/strausmann/go-fileee/fileee
```

Voraussetzung: Go 1.23 oder neuer.

## Quickstart

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/strausmann/go-fileee/fileee"
)

func main() {
	// Credentials aus einer Secret-Quelle laden, nie hartkodieren.
	client, err := fileee.New(fileee.Credentials{
		Username: os.Getenv("FILEEE_USERNAME"),
		Password: os.Getenv("FILEEE_PASSWORD"),
		TOTPSeed: os.Getenv("FILEEE_TOTP_SEED"), // Base32-Seed, falls Zwei-Faktor aktiv ist
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	if err := client.EnsureSession(ctx); err != nil {
		log.Fatal(err)
	}

	// Volltextsuche -> Dokument-IDs, dann Details laden.
	res, err := client.Documents.Search(ctx, "Rechnung", fileee.SearchOptions{Limit: 20})
	if err != nil {
		log.Fatal(err)
	}
	for _, id := range res.IDs {
		doc, err := client.Documents.Get(ctx, id)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(doc.ID)
	}
}
```

Credentials gehören nicht in den Quellcode — aus einer Secret-Quelle (Umgebungsvariable, Vault,
Keyring) laden. Weitere Konfiguration über die `With…`-Optionen von `New` (Rate-Limit, eigener
`*http.Client`, Session-Store, User-Agent). Vollständige Referenz aller Typen und Methoden:
[pkg.go.dev](https://pkg.go.dev/github.com/strausmann/go-fileee/fileee) bzw. `go doc ./fileee`.

## Funktionsumfang

Was die Library aktuell abdeckt und was (noch) nicht. Das Fileee-API ist reverse-engineered; nicht
abgedeckte Punkte sind teils bewusst ausgelassen (Risiko), teils schlicht noch nicht implementiert.

**Abgedeckt**

| Bereich | Methoden |
|---|---|
| Auth/Session | `Login`, `Logout` (serverseitiger Widerruf + Jar-Clear), `EnsureSession`, automatische Re-Auth, TOTP-2FA, `AccountStatus` |
| Dokumente | `Documents.Query` / `Diff` / `Get` / `Update` / `Upload`, `Search` (Volltext), `DownloadPDF`, `DownloadPageImage` |
| Export | `Documents.ExportZIP` / `ExportAll` (passwortgeschütztes ZIP als Prozess), `Processes.Get` (Fortschritt) |
| Teilen | `Documents.Share` (Freigabe-Link) / `Unshare` |
| Share-Link nutzen (anonym) | `NewShareClient` → `Resolve(token)` / `DownloadPageImage` (ohne Login, z. B. für N8N-Webhook-Flows); `ShareTokenFromLink` |
| Dokumenttyp-Felder | `DocumentTypeSchemes` (`Query`/`Diff`/`Get`, `Fields()` je Typ) |
| Fehler-Erkennung | `errors.Is(err, ErrRateLimited \| ErrUnsupportedFileType \| ErrNotFound \| ErrDuplicateDocument)`, `BlockedError` (secondsBlocked) |
| FileeeBoxen | `Boxes.List` / `Get` / `AddDocument` / `RemoveDocument` |
| Erinnerungen | `Reminders.Query` / `Diff` / `Get` / `Create` |
| Kontakte | `Contacts.Query` / `Diff` / `Get` / `Create` / `Update` |
| Stammdaten (read) | `Tags`, `Companies`, `DocumentTypes` (`Query` / `Diff` / `Get`) |
| Betrieb | Rate-Limiting, Backoff, Session-Persistenz, konfigurierbarer User-Agent |

**Nicht abgedeckt (bewusst ausgelassen)**

| Funktion | Grund |
|---|---|
| `revision-lock` (Aufbewahrungssperre) | Kann ein Dokument serverseitig unbrauchbar machen (bei Tests beobachtet) — zu riskant für einen Client, bis das Verhalten geklärt ist |
| Hartes Löschen von Dokumenten/Kontakten | Destruktiv; nicht Teil des aktuellen Scopes |

**Noch nicht implementiert (geplant/denkbar)**

| Funktion | API |
|---|---|
| Teilen an einen Kontakt (Konversation) | Konversationen (`documents/rest/share` = Link-Freigabe ist abgedeckt) |
| Anonymer Voll-PDF-Download eines Share-Links | Endpunkt noch nicht verifiziert (Seitenbild via `ShareClient.DownloadPageImage` ist abgedeckt) |
| „Alle Dateien" ZIP-Datei abholen | Export-Start + Prozess-Polling abgedeckt; die finale ZIP-Download-URL des fertigen Prozesses noch offen |
| Erneut analysieren | `POST /api/documents/rest/:id/reanalyze` |
| Papierkorb-Flow | `deleted-documents/list`, `delete-permanently` |
| Seiten-Operationen | Merge/Split/Rotate/Extract/Reorder |
| Tags anlegen/zuweisen, OCR-Text, Live-Push (SSE) | diverse |

> Details zu den Endpunkten: [`docs/API.md`](docs/API.md). Die vollständige Methoden-Referenz steht auf
> [pkg.go.dev](https://pkg.go.dev/github.com/strausmann/go-fileee/fileee).

## Warum

`go-fileee` ist ein **neutraler, allgemeiner** Go-Client für das Fileee-API: er kapselt Login (inkl. TOTP-2FA), Dokument-Sync, Download und Upload programmatisch — ohne Bindung an ein bestimmtes Ziel- oder Fremdsystem. Damit lässt sich Fileee für viele Zwecke automatisieren:

- **Export** von Dokumenten und Stammdaten (Tags, Firmen, Kontakte, Dokumenttypen),
- **Backup/Archivierung** in ein dauerhaftes Disk-Archiv,
- allgemeine **Automatisierung** rund um Fileee-Inhalte,
- **MCP-/AI-Zugriff** auf die eigenen Dokumente,
- **Scanner-Upload** neuer Dokumente nach Fileee,
- **Migration zu einem beliebigen DMS** (z. B. Paperless-ngx) — als **externes** Consumer-Projekt.

> Domänen-/zielsystem-spezifische Integrationen (z. B. eine Paperless-Migration) leben in separaten Consumer-Projekten, die go-fileee importieren. Dieses Repo bleibt bewusst **domänen-neutral** (siehe [ADR-0006](docs/adr/0006-domaenen-neutralitaet.md)).

## Komponenten

```mermaid
graph TD
    A["fileee/ (Core-Lib)"] --> B["cmd/fileee (generische CLI)"]
    A --> C["cmd/fileee-mcp (MCP-Server)"]
    A --> D["externe Consumer-Projekte"]
    B --> E["export / sync / download / upload → Disk-Archiv"]
    C --> F["AI-Zugriff (Claude Code / ChatGPT)"]
    D --> G["z. B. Paperless-Migration, Scanner-Upload"]
```

| Komponente | Zweck |
|-------|-------|
| **Core-Lib** (`fileee/`) | Auth (Session-Cookie + TOTP), Entities, Download, Upload. Zustandslos — der Aufrufer hält den Sync-Cursor. Kein Ziel-/Fremdsystem-Wissen. |
| **CLI** (`cmd/fileee`) | **Generische** CLI: `export` / `sync` / `download` / `upload` in ein dauerhaftes Disk-Archiv. Kein DMS-/zielsystem-spezifischer Code. |
| **MCP-Server** (`cmd/fileee-mcp`) | Stellt Fileee-Inhalte für AI-Tools (Claude Code, ChatGPT, …) bereit. Da es sich um private Finanzdokumente handelt: **read-only als Default**, Transport/Auth bewusst gewählt. |
| **Externe Consumer** | Importieren die Core-Lib als Go-Modul — z. B. eine Paperless-Migration oder ein Scanner-Projekt. Nicht Teil dieses Repos. |

## Geplante Modulstruktur

```
go-fileee/
├── fileee/              # Core-Lib: Auth, Entities, Client (domänen-neutral)
│   ├── auth.go          # Session-Cookie-Login + TOTP
│   ├── documents.go      # Dokumente: query/diff/get/put/upload
│   ├── companies.go      # Firmen
│   ├── contacts.go        # Kontakte (CRUD)
│   ├── tags.go            # Tags
│   ├── documenttypes.go   # Dokumenttypen
│   └── client.go          # HTTP-Client, Cookie-Jar, CSRF-Handling
├── cmd/
│   ├── fileee/            # generische CLI: export / sync / download / upload
│   └── fileee-mcp/        # MCP-Server für AI-Zugriff
├── docs/
│   ├── API.md              # Fileee-API-Referenz (secret-safe, aus HAR rekonstruiert)
│   └── adr/                # Architecture Decision Records
└── fixtures/               # HAR-abgeleitete Test-Fixtures (keine echten Secrets)
```

> Kein `internal/paperless` oder anderes zielsystem-spezifisches Paket — solche Integrationen sind externe Consumer-Projekte (siehe [ADR-0006](docs/adr/0006-domaenen-neutralitaet.md)).

## Auth-Modell (Kurzfassung)

Fileee hat **kein** Bearer-/Refresh-Token-API. Die Session lebt in einem httpOnly-Cookie plus CSRF-Header (`x-xsrf-token`, Double-Submit-Cookie). 2FA läuft über TOTP und wird **im selben Login-Request** mitgeschickt — dadurch ist ein vollständig headless-Betrieb möglich, solange der TOTP-Seed bekannt ist.

```
GET  /api/f/start                        → Session/CSRF-Cookie initialisieren
POST /api/f/existent   {username}        → Konto- + 2FA-Check
POST /api/f/login      {user,pw,totp}    → setzt Session-Cookie
GET  /api/f/user-session                 → authorized:true zur Kontrolle
```

Details: [`docs/API.md`](docs/API.md) Abschnitt 2, sowie [ADR-0002](docs/adr/0002-auth-modell-session-cookie-totp.md).

## Sicherheit

- **Credentials** (Username, Passwort, TOTP-Seed) gehören **ausschließlich** in einen Secret-Manager (Vaultwarden/Infisical) — niemals in Code, Fixtures oder Commits.
- Die **Session-Cookie-Jar** ist ein Secret (Dateirechte `0600`), wird nie geloggt oder committed.
- Dokument-Inhalte und -Metadaten sind **PII** — Test-Fixtures enthalten ausschließlich synthetische oder bewusst anonymisierte Daten.
- Die Library **schont Fileees Infrastruktur bewusst**: ein eingebauter Rate-Limiter (konservativer Default) plus Exponential-Backoff mit Jitter im HTTP-Transport begrenzen die Request-Frequenz für alle Konsumenten, Delta-Sync (`/diff`) statt Voll-Reloads reduziert die Last, und Konto-Sperren (`secondsBlocked`) werden respektiert. Details: [ADR-0005](docs/adr/0005-schonender-betrieb-rate-limiting.md).
- Siehe [`docs/API.md`](docs/API.md) Abschnitt 6 für die vollständigen Secret-Hinweise.

## Disclaimer

Dies ist **kein offizielles Fileee-API** — es handelt sich um eine Rekonstruktion des internen Protokolls der Web-App `my.fileee.com`, gewonnen aus dem Netzwerkverkehr eines eigenen, eingeloggten Kontos. Konsequenzen:

- Fileee kann das interne API **jederzeit ohne Ankündigung ändern** — diese Library kann dadurch brechen.
- Die Nutzung ist **ausschließlich für das eigene Fileee-Konto** vorgesehen, nicht für fremde Konten oder Massenzugriffe.
- Es gibt **keine Gewähr** für Vollständigkeit, Korrektheit oder Dauerhaftigkeit der Funktionalität.
- Nutzer sind selbst dafür verantwortlich, die Nutzungsbedingungen von Fileee einzuhalten.

## Dokumentation

- [`docs/API.md`](docs/API.md) — vollständige API-Referenz (Endpunkte, Auth-Ablauf, Datenmodell; das enthaltene Paperless-Mapping ist ein Beispiel für externe Consumer, nicht Teil der Lib)
- [`docs/adr/`](docs/adr/) — Architecture Decision Records (Grundsatzentscheidungen zu Architektur, Auth, Risiko, Tests)

## Lizenz

[MIT](LICENSE) — Copyright © 2026 Björn Strausmann

# go-fileee

Eine **inoffizielle** Go-Client-Library für das **interne** Web-App-API von [Fileee](https://www.fileee.com) (`my.fileee.com`). Fileee bietet kein öffentliches API — diese Library kapselt das Protokoll, das die eigene Web-App verwendet, rekonstruiert aus mitgeschnittenem Netzwerk-Traffic eines eingeloggten eigenen Kontos.

> **Status:** In Entwicklung — privat. Noch keine stabile Version, kein `v0`-Tag, API kann sich jederzeit ändern.

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

# CLAUDE.md — Instruktionen für Claude Code (go-fileee)

Diese Datei richtet sich an Claude Code (und andere KI-Agents), die in diesem Repository arbeiten. Die hier festgelegten Regeln sind **verbindlich**.

## Projektüberblick

**go-fileee** ist eine Go-Library, die das **interne** Web-App-API von [Fileee](https://www.fileee.com) (`my.fileee.com`) kapselt. Es gibt **kein offizielles Fileee-API** — dieses Repo rekonstruiert das Protokoll der Web-App aus mitgeschnittenem Traffic eines eigenen Kontos.

go-fileee ist **domänen-neutral** — es enthält keinen Ziel-/Fremdsystem-Code. Komponenten:

| Modul | Zweck |
|-------|-------|
| **Core-Lib** (`fileee/`) | Auth (Session-Cookie + TOTP), Entities, Download, Upload. Zustandslos. |
| **CLI** (`cmd/fileee`) | **Generische** CLI: `export` / `sync` / `download` / `upload` in ein dauerhaftes Disk-Archiv. |
| **MCP-Server** (`cmd/fileee-mcp`) | Fileee-Inhalte für AI-Tools (Claude Code, ChatGPT). |
| Externe Consumer | Importieren die Core-Lib — z. B. eine Paperless-Migration oder ein Scanner-Projekt. **Nicht Teil dieses Repos.** |

Referenzen für jede Arbeit an diesem Repo:
- **API-Referenz:** [`docs/API.md`](docs/API.md) — Endpunkte, Auth-Ablauf, Datenmodell, Paperless-Mapping.
- **Grundsatzentscheidungen:** [`docs/adr/`](docs/adr/) — vor jeder Architektur-/Tech-Entscheidung lesen.

## Sprache

- **Deutsch** für Dokumentation, Code-Kommentare, Commit-Messages und Issues.
- **Englisch** für Code, CLI-Ausgaben, Bezeichner (Package-/Funktions-/Variablennamen), YAML-Keys.
- In deutscher Prosa **echte Umlaute** verwenden: `ä ö ü ß` — niemals `ae/oe/ue/ss` als Ersatz.

## Architektur / Modulschnitt (VERBINDLICH)

Siehe [ADR-0001](docs/adr/0001-library-first-architektur.md) und [ADR-0006](docs/adr/0006-domaenen-neutralitaet.md).

- Die **Core-Lib (`fileee/`) ist zustandslos und domänen-neutral** — der **Sync-Cursor liegt beim Aufrufer** (wird übergeben/zurückgegeben, nicht in der Lib gehalten).
- **Kein Domänen-/Zielsystem-Code im Repo** — die CLI ist **generisch** (`export`/`sync`/`download`/`upload`), kein DMS-/zielsystem-spezifischer Code. Solche Integrationen (z. B. eine Paperless-Migration) sind **externe Konsumenten**, die go-fileee importieren.
- **CLI und MCP-Server sind dünne Adapter** über der Core-Lib — keine Fileee-Protokoll-Logik duplizieren, keine Geschäftslogik in die Adapter verlagern, die in die Lib gehört.

## Auth (kurz)

Siehe [ADR-0002](docs/adr/0002-auth-modell-session-cookie-totp.md) und [`docs/API.md`](docs/API.md) Abschnitt 2.

- Login: Session-Cookie via `POST /api/f/login` (`username`, `password`, `two-factor-token`) — **kein** Bearer-/Refresh-Token.
- 2FA = **headless TOTP** (RFC 6238), Code aus gespeichertem Seed generiert und im Login-Request mitgeschickt.
- CSRF: Header `x-xsrf-token` (Wert aus `XSRF-TOKEN`-Cookie) wird auf mutierenden Requests automatisch gesetzt.
- Session-Persistenz über ein `SessionStore`-Interface (Cookie-Jar, Datei `0600`); Re-Login bei `401` / `authorized:false`.

## Secrets (VERBINDLICH)

- **Credentials** (Username, Passwort, **TOTP-Seed**) kommen aus **Vaultwarden/Infisical** und werden der Lib als Struct übergeben — **NIE** im Repo, im Code oder in Test-Fixtures hartkodiert.
- **Session-Cookie** und **Dokumentinhalte/-Metadaten (PII)** werden **niemals** geloggt oder committed.
- **HAR-Fixtures vor dem Commit sanitisieren:** Cookies, Tokens, `x-xsrf-token`, echte Namen/Adressen/Beträge und sonstige PII entfernen bzw. durch synthetische Werte ersetzen.
- Siehe [`docs/API.md`](docs/API.md) Abschnitt 6.

## Fileee-Infrastruktur schonen (VERBINDLICH)

Siehe [ADR-0005](docs/adr/0005-schonender-betrieb-rate-limiting.md).

- **Eingebauter Rate-Limiter** (Token-Bucket, konservativer Default) + **Exponential-Backoff mit Jitter** bei `429`/`5xx` im HTTP-Transport.
- **`secondsBlocked` respektieren** (`ErrBlocked` + Warten, kein blindes Retry).
- **`/diff` (Delta-Sync) vor Voll-Reloads**; Downloads/Seiten **seriell mit Pausen**, nicht massiv parallel.
- **Keine** Last-, Stress- oder Fuzz-Tests gegen die echte Fileee-Infrastruktur.

## Tests (VERBINDLICH)

Siehe [ADR-0004](docs/adr/0004-test-strategie.md).

- **TDD strict:** immer zuerst einen failing Test schreiben, dann die Implementierung.
- **Offline-Unit-Tests** aus **HAR-Fixtures** — laufen in der öffentlichen CI, **ohne** Secrets. Hauptabdeckung (Parser/Mapping/Auth-Handshake/Fehlerbehandlung).
- **Integration-Tests** niedrigfrequent gegen ein **Wegwerf-Test-Konto**, aus einem Docker-Container, Creds aus **Infisical** — **nie** in öffentlicher CI.
- **Mutations-Pfade** (Upload/Update): Happy-Path + Error-Path (4xx/5xx) + Network-Error/Timeout abdecken.

## ADR-Prozess

- **Vor jeder Architektur-, Technologie- oder Betriebs-Entscheidung** die bestehenden ADRs unter [`docs/adr/`](docs/adr/) lesen.
- Neue ADRs mit [`docs/adr/template.md`](docs/adr/template.md) anlegen (`NNNN-kebab-slug.md`, fortlaufende 4-stellige Nummer, nie wiederverwenden).
- Die **Registry** [`docs/adr/README.md`](docs/adr/README.md) bei jedem neuen/geänderten ADR sofort pflegen.
- **Lineage-Felder beidseitig** eintragen (`Ersetzt` / `Ersetzt durch` / `Verwandt`); beim Ablösen nur den Header des alten ADR ändern, Kontext/Entscheidung nie umschreiben.
- Vollständiges Regelwerk: Abschnitt „ADR-Regelwerk" in [`docs/adr/README.md`](docs/adr/README.md).

## Git

- **Conventional Commits auf Deutsch:** `feat:` (neues Feature), `fix:` (Bugfix), `docs:` (Doku), `refactor:` (Umbau ohne Funktionsänderung), `chore:` (Maintenance/Dependencies).
- **Issue-Referenz** in Commits/PRs: `Refs #N` oder `Closes #N`.
- **Feature-Branches** nutzen; bei größeren Änderungen **kein Direkt-Push auf `main`** — über Branch + PR.

# Beitragen zu go-fileee

Danke für dein Interesse an diesem Projekt. go-fileee ist ein **inoffizielles**
Community-Projekt für das interne Web-App-API von [Fileee](https://www.fileee.com) — es gibt
keine offizielle Kooperation mit der fileee GmbH. Diese Datei beschreibt, wie ein Beitrag
(Issue, Pull Request) reibungslos durch die Qualitäts-Gates dieses Repos kommt.

## Bevor du anfängst

- **ADRs lesen:** Architektur-Entscheidungen stehen unter [`docs/adr/`](docs/adr/) — insbesondere
  [ADR-0001](docs/adr/0001-library-first-architektur.md) (Library-first),
  [ADR-0004](docs/adr/0004-test-strategie.md) (Test-Strategie) und
  [ADR-0006](docs/adr/0006-domaenen-neutralitaet.md) (Domänen-Neutralität — **kein**
  Ziel-/Fremdsystem-Code in diesem Repo, z. B. keine Paperless-/DMS-spezifische Logik).
- **API-Referenz:** [`docs/API.md`](docs/API.md) beschreibt Endpunkte, Auth-Ablauf und
  Datenmodell des reverse-engineerten Fileee-API.
- Für größere Änderungen erst ein Issue eröffnen und die Richtung abstimmen, bevor viel Code
  geschrieben wird.

## Entwicklungs-Workflow

1. Fork oder Branch anlegen (kein Direkt-Push auf `main`).
2. Änderungen **strikt TDD** umsetzen: zuerst einen fehlschlagenden Test schreiben, dann die
   Implementierung, bis der Test grün ist (siehe
   [ADR-0004](docs/adr/0004-test-strategie.md)). Offline-Unit-Tests laufen aus HAR-Fixtures ohne
   Secrets; Integration-Tests gegen ein echtes Konto laufen **nicht** in der öffentlichen CI.
3. Mutations-Pfade (Upload/Update-Operationen) decken mindestens ab: Happy-Path,
   Error-Path (4xx/5xx) und Network-Error/Timeout.
4. Lokal vor dem Commit prüfen:
   ```bash
   go build ./...
   go vet ./...
   go test ./... -race -coverprofile=cover.out
   ./scripts/coverage-gate-strict.sh cover.out fileee/<geänderte-datei>.go:<schwelle>
   ./scripts/doc-coverage.sh
   ```
   Die Coverage-Schwellen pro Datei stehen in
   [`.github/workflows/test.yml`](.github/workflows/test.yml) — sie sind ein **Floor** (an den
   gemessenen Ist-Stand angelehnt), keine aspirationalen Zielwerte. Wird eine bestehende Datei
   geändert, darf ihre Coverage nicht unter die dort hinterlegte Schwelle fallen.
5. **Doku-Pflicht:** Ändert der Beitrag exportierte Symbole, Verhalten (Auth, Rate-Limit,
   Timeouts, Re-Auth) oder Optionen der Core-Lib (`fileee/`) — dann müssen im **selben PR**
   `README.md` (Feature-Tabellen, Quickstart) **und** `fileee/doc.go` (Package-Godoc,
   rendert auf [pkg.go.dev](https://pkg.go.dev/github.com/strausmann/go-fileee/fileee))
   nachgezogen werden. Jedes exportierte Symbol braucht einen Godoc-Kommentar —
   `scripts/doc-coverage.sh` muss 0 undokumentierte Exports melden.
6. Falls die Änderung eine Architektur- oder Technologie-Entscheidung enthält: ADR unter
   `docs/adr/` anlegen (`docs/adr/template.md` als Vorlage) und in
   [`docs/adr/README.md`](docs/adr/README.md) registrieren.

## Commit-Konvention

Commit-Messages folgen [Conventional Commits](https://www.conventionalcommits.org/) und werden
per Husky-Hook + [commitlint](https://commitlint.js.org/) geprüft
(`.commitlintrc.json`, `@commitlint/config-conventional`):

- **Typ:** `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`, `ci:` — auf Deutsch
  formuliert (z. B. `feat(documents): diff-sync für gelöschte einträge ergänzen`).
- **Subject in Kleinbuchstaben** (`subject-case`-Regel aus `config-conventional`) — kein
  großgeschriebener Satzanfang.
- **Scope** aus der festen Liste in `.commitlintrc.json` (`auth`, `transport`, `session`,
  `client`, `documents`, `contacts`, `reminders`, `conversations`, `boxes`, `ocr`, `share`,
  `links`, `adr`, `ci`, `deps`, `docs`, `release`) — kein neuer Scope ohne Anpassung der Datei.
- **Issue-Referenz:** `Refs #N` oder `Closes #N` in Commit oder PR-Beschreibung.

Der `commit-msg`-Hook läuft automatisch nach `npm install` (installiert Husky via
`"prepare": "husky"` in `package.json`). Committen ohne `npm install` vorher übersprungen den
lokalen Hook — der `commitlint`-PR-Check in CI greift trotzdem als Gate.

**Nie `git commit --no-verify` verwenden**, um den Hook zu umgehen.

## Pull Requests

- Ziel-Branch ist `main`. `main` ist geschützt — kein Direkt-Push, nur PRs mit grüner CI.
- CI-Gates, die grün sein müssen:
  - `test.yml` — `go build`, `go vet`, `go test ./... -race`, Coverage-Gate
    (`scripts/coverage-gate-strict.sh`)
  - `commitlint.yml` — jeder Commit im PR muss der Konvention entsprechen
- PR-Beschreibung nutzt die [Pull-Request-Vorlage](.github/PULL_REQUEST_TEMPLATE.md) — Issue-Bezug,
  Testnachweis und Doku-Sync-Checkbox nicht auslassen.
- Kleine, fokussierte PRs bevorzugt gegenüber großen Sammel-PRs.

## Fragen oder Bugs melden

- **Bug:** [Bug-Report-Vorlage](.github/ISSUE_TEMPLATE/bug_report.md) nutzen — HAR-Fixture
  (sanitisiert, keine echten Credentials/PII!) hilft beim Reproduzieren.
- **Feature-Wunsch:** [Feature-Request-Vorlage](.github/ISSUE_TEMPLATE/feature_request.md)
  nutzen — bei Wunsch nach domänen-spezifischer Logik (z. B. DMS-Integration) bitte
  [ADR-0006](docs/adr/0006-domaenen-neutralitaet.md) beachten: solche Integrationen sind externe
  Konsumenten dieser Lib, nicht Teil dieses Repos.

## Sicherheit

Fileee-Credentials (Username, Passwort, **TOTP-Seed**) gehören niemals in Code, Tests oder
Issues. HAR-Fixtures vor dem Commit sanitisieren (Cookies, `x-xsrf-token`, echte Namen/Adressen
entfernen). Sicherheitsrelevante Funde bitte nicht als öffentliches Issue melden, sondern den
Maintainer direkt kontaktieren.

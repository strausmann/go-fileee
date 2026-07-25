## Beschreibung

Was ändert dieser PR und warum?

## Bezug

Refs #… <!-- oder: Closes #… -->

## Checkliste

- [ ] **TDD strict eingehalten** — Test zuerst geschrieben, dann Implementierung (siehe
      [ADR-0004](../docs/adr/0004-test-strategie.md)).
- [ ] Mutations-Pfade (Upload/Update) decken Happy-Path, Error-Path (4xx/5xx) und
      Network-Error/Timeout ab, falls zutreffend.
- [ ] `go build ./...`, `go vet ./...` und `go test ./... -race` laufen lokal grün.
- [ ] `./scripts/coverage-gate-strict.sh` besteht für geänderte Dateien (Schwellen aus
      `.github/workflows/test.yml`).
- [ ] **Doku-Sync (falls exportierte API/Verhalten geändert):** `README.md` und
      `fileee/doc.go` im selben PR nachgezogen — `./scripts/doc-coverage.sh` meldet 0
      undokumentierte Exports.
- [ ] Neues ADR angelegt und in `docs/adr/README.md` registriert, falls eine Architektur-/
      Technologie-Entscheidung enthalten ist.
- [ ] Commit-Messages sind Conventional-Commits-konform (Kleinbuchstaben-Subject, gültiger
      Scope aus `.commitlintrc.json`).
- [ ] Keine Secrets/Credentials/PII in Code, Tests oder HAR-Fixtures.

## Testnachweis

Wie wurde die Änderung verifiziert (Testlauf-Output, manuelle Prüfung)?

# ADR-0004: Test-Strategie

**Status:** accepted
**Datum:** 2026-07-23
**Ersetzt:** —
**Ersetzt durch:** —
**Verwandt:** ADR-0001, ADR-0003, ADR-0005

## Kontext

`go-fileee` unterliegt der Test-Coverage-Pflicht (jede neue Funktion braucht einen Test, Mutation-Pfade brauchen Happy-Path + Error-Path + Network-Error-Abdeckung). Gleichzeitig ist das Projekt in hohem Maße secret-sensibel: Tests dürfen niemals echte Fileee-Credentials (Username, Passwort, TOTP-Seed) oder echte Dokumentinhalte (PII) in einer öffentlich sichtbaren CI preisgeben.

## Entscheidung

Die Test-Strategie trennt zwei Ebenen strikt:

**1. Offline-Unit-Tests (in CI, ohne Secrets)**
Aus den vorliegenden HAR-Mitschnitten werden anonymisierte/synthetische **Fixtures** abgeleitet (JSON-Response-Bodies für Login-Handshake, Dokument-/Tag-/Company-/Contact-Listen, Fehlerantworten). Diese Fixtures enthalten keine echten Credentials und keine echten personenbezogenen Dokumentinhalte. Getestet werden damit:
- Parser/Mapping-Logik (Response → Go-Struct),
- der Auth-Handshake-Ablauf (Request-Sequenz, CSRF-Header-Handling, Re-Login-Trigger) gegen einen lokalen Mock-HTTP-Server,
- Fehlerbehandlung (`apiError`/`errorCode`, HTTP-Fehlercodes, Netzwerk-Timeouts simuliert).

Diese Tests laufen in der **öffentlichen/normalen CI-Pipeline** und brauchen keinerlei Secret-Zugriff.

**2. Integration-Tests (gegen ein dediziertes Test-Konto, niemals in öffentlicher CI)**
Zusätzlich existiert eine Test-Suite, die gegen ein **eigens angelegtes, dediziertes Fileee-Test-Konto** läuft (keine Produktivdaten). Diese Tests laufen aus einem Docker-Container heraus, die Credentials (Username, Passwort, TOTP-Seed des Test-Kontos) werden zur Laufzeit aus Infisical injiziert (`infisical run --env=<env>`, siehe `.claude/rules/secret-environment-awareness.md` im homelab-management-Repo für das Environment-Modell). Diese Integration-Tests laufen **niemals** in einer öffentlichen/für Dritte einsehbaren CI-Pipeline, sondern ausschließlich in einer kontrollierten, privaten Umgebung.

**Mutation-Pfade** (Upload, Update von Dokumenten/Kontakten) werden — analog zur Test-Coverage-Pflicht — mit drei Fällen abgedeckt: Happy-Path (Erfolg), Error-Path (4xx/5xx vom Fileee-Backend) und Network-Error/Timeout.

## Konsequenzen

**Positiv:**
- Die öffentliche CI bleibt vollständig secret-frei und kann ohne Risiko auch bei einer späteren Veröffentlichung des Repos laufen.
- Integration-Tests sind reproduzierbar gegen Wegwerf-/Test-Daten, ohne das eigene Produktivkonto zu gefährden.
- Erfüllt die Mutation-Pfad-Anforderung der Test-Coverage-Pflicht (Happy + Error + Network für jede schreibende Operation).

**Negativ / akzeptiertes Risiko:**
- Es braucht ein separates, dediziertes Fileee-Test-Konto (zusätzlicher Setup-Aufwand, ggf. zusätzliche Kosten je nach Fileee-Tarif).
- Integration-Tests laufen nicht automatisch bei jedem Public-CI-Run — sie müssen bewusst in einer privaten/kontrollierten Pipeline oder lokal ausgeführt werden, was die Kontinuität der Absicherung von Prozess-Disziplin abhängig macht.
- Fixtures können veralten, wenn sich das reale API ändert (siehe ADR-0003) — Offline-Tests allein erkennen das nicht; die Integration-Tests sind die eigentliche Frühwarnung.

## Referenzen

- [`docs/API.md`](../API.md)
- `.claude/rules/test-coverage-pflicht.md` (homelab-management-Repo)
- `.claude/rules/secret-environment-awareness.md` (homelab-management-Repo)
- [ADR-0005](0005-schonender-betrieb-rate-limiting.md) (Schonender Betrieb / Rate-Limiting)

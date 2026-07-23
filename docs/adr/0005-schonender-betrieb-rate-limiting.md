# ADR-0005: Schonender Betrieb / Rate-Limiting

**Status:** accepted
**Datum:** 2026-07-23
**Ersetzt:** —
**Ersetzt durch:** —
**Verwandt:** ADR-0003, ADR-0004

## Kontext

Fileee bietet **kein offizielles API** — `go-fileee` nutzt die interne Web-App-API von `my.fileee.com` (siehe ADR-0003 und [`docs/API.md`](../API.md)). Diese Infrastruktur ist nicht für automatisierte Massenzugriffe ausgelegt, sondern für die interaktive Nutzung durch die Web-App.

Aggressive oder hochfrequente Requests (etwa parallele Voll-Exports tausender Dokumente oder blindes Retry bei Fehlern) könnten Fileees Infrastruktur unnötig belasten und in der Folge unser Konto sperren — das API signalisiert eine solche Sperre über `secondsBlocked` in `GET /api/f/user-session` (>0 = blockiert). Es besteht daher eine **ethische wie praktische Pflicht** zum schonenden Betrieb: fremde Infrastruktur wird respektvoll behandelt, und die eigene Konto-Verfügbarkeit bleibt erhalten.

## Entscheidung

`go-fileee` ist konsequent auf schonenden Betrieb ausgelegt. Konkret:

1. **Eingebauter Rate-Limiter:** Die Core-Lib enthält im HTTP-Transport einen Rate-Limiter (Token-Bucket) mit einem **konservativen Default von wenigen Requests pro Sekunde**. Er ist über `Options` konfigurierbar, aber der Default ist bewusst niedrig. Da der Limiter im Transport sitzt, gilt er **automatisch für alle Konsumenten** (CLI, MCP-Server, Scanner) — kein Aufrufer kann ihn versehentlich umgehen.

2. **Exponential-Backoff mit Jitter:** Bei `429` (Too Many Requests) und `5xx`-Fehlern wird mit exponentiell wachsender Wartezeit und Jitter erneut versucht — kein enges Retry-Loop, das die Last weiter erhöht.

3. **`secondsBlocked` wird respektiert:** Meldet `user-session.secondsBlocked > 0`, gibt die Lib einen definierten Fehler (`ErrBlocked`) zurück und wartet die angegebene Sperrzeit ab, statt blind weiter zu requesten.

4. **Delta-Sync statt Voll-Reload:** Die Synchronisation bevorzugt die `/diff`-Endpunkte (inkrementeller Delta-Sync, siehe API.md §3) gegenüber wiederholten Voll-Exports über `query`. Downloads von PDFs/Seiten laufen **seriell mit Pausen**, nicht massiv parallel.

5. **Tests belasten die echte Infrastruktur nicht:** Die Hauptabdeckung stammt aus **Offline-Fixtures** (0 echte Requests, siehe ADR-0004). Integration-Tests laufen **niedrigfrequent** gegen ein dediziertes Wegwerf-Test-Konto. Es gibt **keine** Last-, Stress- oder Fuzz-Tests gegen die echte Fileee-Infrastruktur.

## Konsequenzen

**Positiv:**
- Fileees Infrastruktur wird bewusst und respektvoll behandelt — kein aggressives Zugriffsmuster.
- Konto-Sperren (`secondsBlocked`) werden vermieden bzw. sauber gehandhabt, die eigene Verfügbarkeit bleibt erhalten.
- Rate-Limit- und Backoff-Parameter sind zentral (`Options`) konfigurierbar — Verhalten lässt sich an geänderte Gegebenheiten anpassen, ohne Aufrufercode zu ändern.
- Da der Limiter im Transport sitzt, ist der schonende Betrieb für alle Konsumenten garantiert, nicht nur pro-Adapter dokumentiert.

**Negativ / bewusst in Kauf genommen:**
- Bulk-Operationen (z. B. der initiale Voll-Export für die Paperless-Migration) sind **langsamer** — das ist eine bewusste Abwägung zugunsten des schonenden Betriebs.
- Ein sehr niedriger Default kann bei großen Archiven spürbar sein; Nutzer können den Wert erhöhen, tragen dann aber die Verantwortung für einen weiterhin fairen Zugriff.

## Referenzen

- [`docs/API.md`](../API.md) (Rate-Limiting / `secondsBlocked` in `user-session`, §2.6/§7)
- [ADR-0003](0003-reverse-engineered-api-risiko.md) (reverse-engineered internes API — akzeptiertes Risiko)
- [ADR-0004](0004-test-strategie.md) (Test-Strategie)

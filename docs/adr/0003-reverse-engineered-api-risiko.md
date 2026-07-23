# ADR-0003: Reverse-engineered internes API (akzeptiertes Risiko)

**Status:** accepted
**Datum:** 2026-07-23
**Ersetzt:** —
**Ersetzt durch:** —
**Verwandt:** ADR-0001, ADR-0002

## Kontext

Fileee bietet **kein offizielles, öffentlich dokumentiertes API**. Alles, was `go-fileee` über das Protokoll weiß, stammt aus der Analyse mitgeschnittenen Netzwerkverkehrs (HAR-Dateien) einer eingeloggten eigenen Session der Web-App `my.fileee.com` (siehe [`docs/API.md`](../API.md)). Um die Fileee→Paperless-Migration und weitere Automatisierung zu ermöglichen, muss dieses interne API dennoch genutzt werden — eine Alternative existiert nicht.

## Entscheidung

`go-fileee` baut das interne API der Web-App bewusst nach, mit folgenden Leitplanken:

- Die Library wird **ausschließlich für das eigene Fileee-Konto** entworfen und genutzt — keine Unterstützung für fremde Konten oder Massenzugriffe auf Konten Dritter.
- **Read-only ist der Default** in allen Konsumenten, die es nicht anders brauchen (insbesondere der MCP-Server, siehe ADR-0001) — schreibende Operationen (Upload, Update) sind explizit anzufordern.
- Die Nutzungsbedingungen von Fileee sind zu beachten; die Library ersetzt keine Rechtsprüfung im Einzelfall.

Die Risiken, dass Fileee das interne API jederzeit ohne Ankündigung ändern kann, werden **bewusst akzeptiert und aktiv mitigiert**:

1. **Defensive Parser:** Unbekannte/neue Felder in Responses werden toleriert (nicht strikt validiert), damit kleinere Änderungen die Library nicht sofort brechen.
2. **Fixtures + Integration-Tests:** HAR-abgeleitete Fixtures für Offline-Tests plus Integration-Tests gegen ein dediziertes Test-Konto (siehe ADR-0004) decken reale Abweichungen frühzeitig auf.
3. **Versionserkennung:** `/version/version.json` (bzw. äquivalenter Endpunkt der Web-App) wird beobachtet, um Deploy-bedingte Änderungen zu erkennen, bevor sie als kryptische Fehler auffallen.
4. **Enge Fehlerbehandlung:** API-Fehler (`apiError`/`errorCode`/`errorMessage`, siehe API.md §4.1) werden explizit behandelt und nicht stillschweigend verschluckt, damit Breaking Changes schnell sichtbar werden statt sich in falschen Daten zu verstecken.

## Konsequenzen

**Positiv:**
- Die Migration nach Paperless-ngx und weitere Automatisierung werden überhaupt erst möglich.
- Die Mitigationen (defensive Parser, Versionserkennung, enge Fehlerbehandlung) reduzieren das Risiko stiller Breakage.

**Negativ / akzeptiertes Risiko:**
- `go-fileee` **kann jederzeit brechen**, wenn Fileee sein internes API ändert — es gibt keine Update-Garantie oder Deprecation-Ankündigung von Fileee-Seite, da es kein offizielles API ist.
- Es besteht ein rechtliches Restrisiko bezüglich der Nutzungsbedingungen von Fileee; dieses wird durch die Beschränkung auf das eigene Konto und Read-only-Defaults minimiert, aber nicht vollständig eliminiert.
- Mehrere Aufrufsemantiken sind noch nicht live verifiziert (siehe [`docs/API.md`](../API.md) Abschnitt 7) — Implementierung muss diese Punkte vor Produktivbetrieb kontrolliert gegen das eigene Konto klären.

## Referenzen

- [`docs/API.md`](../API.md) Abschnitt 6 (Sicherheits-/Secret-Hinweise), Abschnitt 7 (Offene Verifikationspunkte)

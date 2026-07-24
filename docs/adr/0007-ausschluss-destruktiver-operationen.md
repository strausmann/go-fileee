# ADR-0007: Ausschluss destruktiver und riskanter Operationen

**Status:** accepted
**Datum:** 2026-07-24
**Ersetzt:** —
**Ersetzt durch:** —
**Verwandt:** ADR-0003 (Reverse-engineered internes API — akzeptiertes Risiko)

## Kontext

Das Fileee-API bietet Operationen, die Daten unwiederbringlich verändern oder — beim reverse-engineerten,
nicht garantierten API — schwer vorhersehbare Nebenwirkungen haben:

- **`POST /api/documents/rest/:id/revision-lock`** setzte in einer Live-Verifikation (2026-07-24) ein
  Dokument in einen Zustand, in dem der Server es nicht mehr serialisieren konnte: Einzel-`GET` sowie
  `diff`/`query` lieferten danach `500`. Das eine defekte Dokument riss die gesamte Dokumentenliste in
  einen Fehler. Die Nutzdaten (PDF) blieben zwar abrufbar, aber der reguläre Sync-Pfad war blockiert.
- **Hartes Löschen** von Dokumenten (`DELETE /api/documents/rest/:id`) und Kontakten ist irreversibel.

Als Client-Library, die Konsumenten (CLI, MCP-Server, Scanner-Anbindung) unterstützt, muss abgewogen
werden, welche dieser Operationen exponiert werden.

## Entscheidung

Destruktive und nachweislich riskante Operationen werden vorerst **nicht** als Methoden der Library
angeboten:

- **`revision-lock`** wird nicht implementiert, bis das serverseitige Verhalten geklärt und
  reproduzierbar ungefährlich ist.
- **Hartes Löschen** (Dokumente, Kontakte) ist nicht Teil des aktuellen Scopes.

Der reguläre, sichere Funktionsumfang (Lesen, Suchen, Anlegen von Erinnerungen/Kontakten, Boxen-Zuordnung,
Teilen, ZIP-Export) bleibt vollständig verfügbar. Der Ausschluss wird im README-Funktionsumfang und in
`docs/API.md` (mit Warnhinweis am jeweiligen Endpunkt) sichtbar dokumentiert.

## Konsequenzen

- **Positiv:** Ein Konsument kann mit der Library keine Dokumente unbrauchbar machen oder unabsichtlich
  unwiederbringlich löschen. Das senkt das Risiko gerade im automatisierten/MCP-Einsatz erheblich.
- **Positiv:** Die Warnhinweise (README, API.md, OpenAPI-`description`) bewahren das Wissen um die Gefahr,
  auch wenn die Operation nicht angeboten wird.
- **Negativ:** Anwendungsfälle, die eine dieser Operationen zwingend brauchen, sind (noch) nicht abgedeckt
  und müssen bei Bedarf bewusst und mit eigener Absicherung nachgezogen werden.
- Die Entscheidung ist revidierbar: Sobald `revision-lock` reproduzierbar sicher ist oder ein
  Lösch-Flow mit ausreichenden Schutzmaßnahmen (z. B. Papierkorb statt Hard-Delete, explizite
  Bestätigung) entworfen ist, kann ein Folge-ADR den Scope erweitern.

## Referenzen

- [`docs/API.md`](../API.md) §0 (Nachträge 2026-07-24), §4.1 (revision-lock-Warnung)
- README-Abschnitt „Funktionsumfang" (nicht abgedeckt)
- ADR-0003 (Reverse-engineered internes API — akzeptiertes Risiko)

# ADR-0008: fileee-server — REST-API-Service (Single-Tenant, geguardetes Löschen)

**Status:** proposed
**Datum:** 2026-07-24
**Ersetzt:** —
**Ersetzt durch:** —
**Verwandt:** ADR-0007, ADR-0005, ADR-0001

## Kontext

Automatisierungen (primär N8N-Workflows), CI-Pipelines und andere Maschinen-Clients sollen
Fileee-Operationen auslösen können, ohne die Core-Lib in Go einzubinden. Dafür entsteht im selben
Repo ein neuer, dünner HTTP-Adapter (`cmd/fileee-server`), der die Core-Lib hinter einer
authentifizierten REST-API bereitstellt und als Docker-Image ausgeliefert wird.

Der Server bedient dabei **bewusst nur ein einziges Fileee-Konto** — ein Token→Credential-Mapping
für mehrere Konten ist nicht vorgesehen (ADR-0003: eigenes Konto, akzeptiertes Risiko der
reverse-engineerten API gilt unverändert). Damit stellt sich zusätzlich die Frage, ob und wie der
Server destruktive Operationen (Hard-Delete) anbieten darf, die ADR-0007 für die Library bislang
kategorisch ausschließt — siehe die dortige Header-Lineage-Ergänzung.

Vollständige Herleitung, Alternativen und Detail-Entscheidungen: Konzept
`docs/superpowers/specs/2026-07-24-fileee-server-design.md` (homelab-management-Repo). Dieses ADR
fasst die für spätere Sessions/Mitwirkende relevanten, langfristig wirkenden Entscheidungen
zusammen.

## Entscheidung

1. **Single-Tenant, ein API-Token.** Der Server hält genau eine `fileee.Client`-Instanz für genau
   ein Fileee-Konto (Login inklusive TOTP beim Boot). Clients authentifizieren sich mit **einem**
   statischen Bearer-/`X-API-Key`-Token, konstant-zeitlich verglichen (`crypto/subtle`). Fehlender
   oder falscher Token → `401` (bewusst nicht `403`, siehe Punkt 4).

2. **Destruktiv-Gate statt Ausschluss.** Anders als der bisherige generelle Ausschluss in ADR-0007
   registriert der Server die `DELETE`-Routen (`/v1/documents/:id`, `/v1/contacts/:id`,
   `/v1/reminders/:id`) **nur**, wenn beim Start `FILEEE_ALLOW_DESTRUCTIVE=true` gesetzt ist —
   Default ist **aus**, die Routen existieren dann serverseitig gar nicht (Aufruf liefert `404`,
   nicht `403` — sieht für CrowdSec wie Scanning/Probing aus, kein Informationsleck über das
   Feature). Die Lib-seitige Voraussetzung dafür sind die neuen **geguardeten** `Delete`-Methoden
   (`Documents.Delete`, `Contacts.Delete`, `Reminders.Delete`), die der Aufrufer aktiv nutzen muss
   — kein impliziter oder automatischer Zugriff. `revision-lock` bleibt **vollständig
   ausgeschlossen**, auch mit gesetztem Flag (unverändertes Risiko: macht ein Dokument serverseitig
   unserialisierbar, ADR-0007).

3. **Huma für OpenAPI 3.1 + `/docs`.** Das HTTP-Layer nutzt das Framework
   [Huma](https://huma.rocks/) (net/http-kompatibel): OpenAPI 3.1 wird automatisch aus den
   getippten Request-/Response-Strukturen erzeugt (`GET /openapi.json`), dazu eine
   self-contained Docs-UI unter `GET /docs` (kein CDN, passt zu CSP-freiem Betrieb). Diese
   Framework-Abhängigkeit lebt **ausschließlich im Server-Binary** (`cmd/fileee-server`) — die
   Core-Lib (`fileee/`) bleibt dependency-arm (ADR-0001 „library-first" gilt weiterhin für die
   Lib, nicht für Adapter-Binaries).

4. **Distroless, rootless, Dual-Mode im Go-Binary.** Laufzeit-Basis-Image ist
   `gcr.io/distroless/static-debian12:nonroot` (keine Shell, kein Paketmanager, läuft als
   `uid 65532`); das Server-Binary ist statisch (`CGO_ENABLED=0`), eine statische `infisical`-CLI
   wird einkopiert. Da distroless keine Shell für ein `entrypoint.sh` hat, entscheidet **das
   Go-Binary selbst** beim Start, ob es in den Infisical-Dual-Mode wechselt
   (`SECRET_BACKEND=infisical` oder gesetzte Universal-Auth-Client-ID, sofern nicht
   `SECRET_BACKEND=env`): Token minten → Secrets per `infisical export --format=dotenv` holen →
   in die eigene Umgebung mergen → **`fileee-server` per `syscall.Exec` direkt exec'en** (der
   Server wird dabei PID 1). Bewusst **nicht** `infisical run -- fileee-server` — das würde
   `infisical` zu PID 1 machen und Signal-Forwarding (Graceful-Shutdown bei `SIGTERM`) von der
   CLI abhängig machen. Ein Re-Exec-Sentinel (`FILEEE_INFISICAL_REEXEC`) verhindert eine
   Endlosschleife nach dem Re-Exec.

5. **NGINX-Access-Log-Format für CrowdSec.** Der Server schreibt auf stdout ein Access-Log im
   NGINX-`combined`-Format, damit CrowdSec den bereits vorhandenen `crowdsecurity/nginx`-Parser
   und dessen HTTP-Szenarien (`http-probing`, `http-bruteforce`, `http-crawl-non-statics`) **ohne
   eigenen Custom-Parser** nutzen kann. Wiederholte `401` (falscher/fehlender Token) und `404`
   (unbekannter Endpunkt bzw. deaktivierte Destruktiv-Route) fallen damit automatisch unter die
   bestehenden Erkennungsmuster. Die Client-IP wird nur aus Reverse-Proxy-Headern übernommen, wenn
   die TCP-Quelle in `FILEEE_TRUSTED_PROXIES` liegt — sonst gilt immer die TCP-Quelle (kein
   Header wird blind geglaubt). App-/Audit-Log (strukturiert, secret-safe) läuft getrennt über
   stderr.

6. **Conversation-Watch → Webhook (Polling).** Der Server kann Konversationen beobachten und bei
   einer neuen eingehenden Chat-Antwort (`Message.IsReply()`) einen konfigurierbaren Webhook
   aufrufen (N8N-Trigger „jemand hat geantwortet"). Realisiert wird das primär über **Polling**
   (`FILEEE_WATCH_INTERVAL` gegen `Conversations.Diff`) — dieselbe Methode, mit der auch die
   Fileee-Web-App den Chat aktualisiert (kein SSE für den Chat belegt). Ein SSE-Pfad
   (`/push/sse/:id`) existiert laut API-Doku, wird aber nicht als Vorbedingung dieses ADR
   umgesetzt.

## Konsequenzen

**Positiv:**
- Automatisierungen ohne Go-Kenntnisse (N8N, curl, CI) können Fileee ansprechen, ohne
  Fileee-Credentials selbst zu halten — nur ein Server-Token.
- Das Destruktiv-Gate hält den sicheren Default (ADR-0007-Grundhaltung) aufrecht: ohne explizites
  Flag existieren die gefährlichen Routen serverseitig überhaupt nicht — kein „vergessen zu
  sperren", weil es nichts zu sperren gibt.
- Huma hält Spec und Implementierung synchron (kein manuell gepflegtes OpenAPI-Dokument für die
  eigene Server-API) und deckt einen Teil der Eingabevalidierung bereits ab — bei laut ADR-0001
  unverändert dependency-armer Core-Lib, da die Abhängigkeit im Adapter bleibt (siehe auch
  ADR-0006, Domänen-Neutralität: der Server bringt kein Zielsystem-Wissen mit, nur den
  Fileee-Protokoll-Zugriff der Lib).
- distroless + rootless minimiert die Angriffsfläche des Laufzeit-Images (keine Shell, kein
  Paketmanager, kein Root); der Rate-Limiter/Backoff der Lib (ADR-0005, schonender Betrieb) schützt
  Fileee unverändert vor Überlast, auch wenn viele Server-Handler denselben `fileee.Client` teilen.
- Der Dual-Mode-Re-Exec macht `fileee-server` zu PID 1 — `SIGTERM`/Graceful-Shutdown funktionieren
  garantiert, unabhängig vom Secret-Backend.
- CrowdSec-Integration ohne Custom-Parser senkt den Betriebsaufwand und nutzt bewährte
  HTTP-Erkennungsmuster.

**Negativ / bewusst in Kauf genommen:**
- Das Destruktiv-Gate ist eine **Deployment-Entscheidung** (ENV-Flag) und kein Schutz gegen einen
  Betreiber, der es absichtlich aktiviert — die Verantwortung für den sicheren Einsatz von
  `FILEEE_ALLOW_DESTRUCTIVE=true` liegt beim Betreiber, nicht bei der Lib/dem Server.
  `revision-lock` bleibt aus genau diesem Grund auch mit Flag außen vor (unverändertes,
  unabhängig vom Gate bestehendes Server-Risiko).
- Single-Tenant/Single-Token ist eine bewusste Scope-Grenze: Mehrere Fileee-Konten hinter einem
  Server-Deployment sind (noch) nicht vorgesehen; wer das braucht, betreibt mehrere
  Server-Instanzen.
- Huma bringt eine zusätzliche Laufzeit-Abhängigkeit ins Server-Binary — für die Core-Lib bleibt
  das ohne Wirkung (ADR-0001), erhöht aber die Angriffs-/Update-Fläche des Server-Adapters
  gegenüber einer reinen `net/http`-Lösung.
- Polling-basiertes Conversation-Watch erzeugt zusätzliche, wenn auch rate-limitierte, Requests
  gegen Fileee; ein echter Push-Mechanismus (SSE) ist nicht verifiziert und daher nicht Teil dieser
  Entscheidung.

## Referenzen

- Konzept: `docs/superpowers/specs/2026-07-24-fileee-server-design.md` (homelab-management-Repo,
  vollständige API-Fläche, Config-Referenz, Fehler-Mapping, Compose-Samples)
- [ADR-0001](0001-library-first-architektur.md) (Library-first-Architektur — Framework-Abhängigkeit
  bleibt auf das Server-Binary beschränkt)
- [ADR-0005](0005-schonender-betrieb-rate-limiting.md) (Schonender Betrieb / Rate-Limiting — der
  Server teilt sich einen `fileee.Client` und dessen Rate-Limiter über alle Handler)
- [ADR-0006](0006-domaenen-neutralitaet.md) (Domänen-Neutralität — der Server bringt kein
  Zielsystem-Wissen mit)
- [ADR-0007](0007-ausschluss-destruktiver-operationen.md) (Ausschluss destruktiver und riskanter
  Operationen — durch dieses ADR verfeinert, nicht abgelöst)
- [`docs/API.md`](../API.md)

# go-fileee — Fileee interne API Referenz (aus HAR rekonstruiert)

**Datum:** 2026-07-23
**Quelle:** 3 HAR-Mitschnitte einer eingeloggten `my.fileee.com`-Session (561 + 335 + 404 Requests), secret-safe strukturell ausgewertet.
**Status:** Rekonstruktion — Struktur (Endpunkte, Feldnamen, Response-Form) ist aus echtem Traffic belegt; Aufruf-**Semantik** teils abgeleitet und mit „⚠️ verifizieren" markiert.
**Zweck:** Grundlage für die Go-Library `go-fileee` (Sub-Projekt A) und deren Konsumenten (CLI-Migration, MCP-Server, Scanner-Upload).

> **Kein offizielles API.** Fileee bietet keine öffentliche API. Dies ist die interne API der Web-App (`my.fileee.com`). Sie kann sich jederzeit ändern. Nutzung auf eigenes Konto beschränkt.

---

## 1. Grundlagen

| Eigenschaft | Wert |
|-------------|------|
| Basis-URL | `https://my.fileee.com` |
| Datenformat | JSON (Requests teils `application/x-www-form-urlencoded` bzw. `multipart/form-data`) |
| Auth | **Session-Cookie** (httpOnly) + CSRF-Header `x-xsrf-token` |
| Kein Bearer-/Refresh-Token | Die Login-Response enthält **keinen** API-Token → Auth lebt im Cookie |
| Push | Server-Sent-Events unter `/push/sse/:id` |
| Statische Renders | Seiten-JPEGs + PDFs unter `/api/v1/...` |

**REST-Konventionen der App:**

- **Einzelobjekt lesen:** `GET /api/<resource>/rest/:id`
- **Objekt anlegen:** `POST /api/<resource>/rest`
- **Objekt ändern:** `PUT /api/<resource>/rest/:id`
- **Liste paginiert (offset):** `POST /api/<resource>/rest/query`
- **Delta-Sync (inkrementell):** `POST /api/<resource>/rest/diff`

`query` und `diff` teilen sich denselben Request-Body (siehe §3). `diff` liefert zusätzlich `idsToDelete` für die Synchronisation.

---

## 2. Authentifizierung

### 2.1 Ablauf (headless, mit TOTP)

```
1. GET  /api/f/start                         → 204   (Session/CSRF-Cookie initialisieren)
2. POST /api/f/existent   {username}         → prüft Konto + 2FA-Status
3. POST /api/f/login      (form-urlencoded)  → setzt Session-Cookie (+ XSRF-TOKEN)
4. (ab jetzt) Cookies mitsenden;
   bei POST/PUT zusätzlich Header  x-xsrf-token: <Wert aus XSRF-TOKEN-Cookie>
5. GET  /api/f/user-session                  → authorized:true zur Kontrolle
```

### 2.2 `POST /api/f/existent`

Prüft, ob ein Konto existiert und ob 2FA aktiv ist — **vor** dem eigentlichen Login.

```http
POST /api/f/existent
Content-Type: application/json

{ "username": "<email>" }
```

**Response:**

| Feld | Typ | Bedeutung |
|------|-----|-----------|
| `existent` | bool | Konto existiert |
| `twoFactorAuthEnabled` | bool | 2FA aktiv → `two-factor-token` beim Login nötig |
| `enabledLoginOptions` | list | verfügbare Login-Wege (Passwort, Google/OIDC) |
| `loggedIn` | bool | bereits eingeloggt (bestehende Session) |
| `actingUserType` | str | Nutzertyp |

### 2.3 `POST /api/f/login` — Passwort-Login (+ TOTP)

Das **OTP wird im selben Request** als `two-factor-token` mitgeschickt (kein separater 2FA-Schritt).

```http
POST /api/f/login
Content-Type: application/x-www-form-urlencoded

username=<email>&password=<pw>&two-factor-token=<6-stelliger TOTP>&conversationAddWithoutInvite=false
```

| Request-Feld | Pflicht | Bedeutung |
|--------------|---------|-----------|
| `username` | ja | E-Mail |
| `password` | ja | Passwort |
| `two-factor-token` | bei aktivem 2FA | aktueller TOTP-Code (RFC 6238) — vom Tool aus dem gespeicherten Seed generiert |
| `conversationAddWithoutInvite` | — | in Traffic `false` (Zweck ⚠️ verifizieren, vermutlich UI-Flag) |

**Erfolg:** `Set-Cookie` mit Session + `XSRF-TOKEN`; Body:

| Feld | Typ |
|------|-----|
| `loggedIn` | bool |
| `userId` / `username` | str |
| `user.participantId` / `user.participantName` / `user.type` | str |
| `groups` / `additionalRights` | list |

> **Wichtig:** Kein Token im Body. Die Session steckt ausschließlich im gesetzten Cookie. Für headless-Betrieb: Cookie-Jar persistieren und wiederverwenden, bei `401`/`authorized:false` neu einloggen.

### 2.4 `POST /api/f/token/login` — Token-Login (Alternative)

```http
POST /api/f/token/login
Content-Type: application/x-www-form-urlencoded

token=<token>
```

Nimmt einen einzelnen `token` an. **Zweck ⚠️ verifizieren** — vermutlich „Remember-Me"/One-Time-Login-Token oder OIDC-Austausch. Kandidat für eine langlebigere Session, falls vorhanden. Für die erste Lib-Version nicht nötig (Passwort+TOTP reicht).

### 2.5 `GET /api/f/start`

Antwortet `204`. Initialisiert vermutlich Session/CSRF-Cookie. **Vor** dem Login aufrufen. ⚠️ genaue Wirkung verifizieren.

### 2.6 `GET /api/f/user-session`

Aktuelle Session + vollständiges Nutzerprofil.

| Feld (Auswahl) | Typ | Bedeutung |
|----------------|-----|-----------|
| `authorized` | bool | Session gültig |
| `secondsBlocked` | num | Rate-Limit/Sperre (>0 = blockiert) |
| `user.hasFileeePassword` | bool | Passwort-Login möglich (vs. nur Google) |
| `user.fileeeEmail` / `user.username` | str | |
| `user.id` / `user.userCompanyId` | str | eigene IDs (referenziert in Dokumenten als `receiverId`) |
| `user.language` | str | |
| `user.addresses` | list | Adressbuch des Nutzers |

### 2.7 `GET /api/f/account-status`

Abo-/Lizenzstatus.

| Feld (Auswahl) | Typ |
|----------------|-----|
| `accountTypeId` | str |
| `currentSubscription.name` / `.frequency` / `.amount` | str/num |
| `payedUntil` / `nextLicenseRefill` | str (Datum) |
| `problem` | str |

### 2.8 CSRF (`x-xsrf-token`)

Bei **schreibenden** Requests (`POST`/`PUT`) muss der Header `x-xsrf-token` gesetzt sein. Wert = Inhalt des `XSRF-TOKEN`-Cookies (klassisches Double-Submit-Cookie). In der Go-Lib: aus dem Cookie-Jar lesen und auf mutierenden Requests automatisch anhängen.

---

## 3. Paginierung & Filter (`query` / `diff`)

Gemeinsamer Request-Body:

```http
POST /api/<resource>/rest/query
Content-Type: application/json

{
  "criteria": { },        // Filter — Struktur ⚠️ verifizieren (im Traffic meist leer/{})
  "sortOrder": [ ],       // Sortierung — Struktur ⚠️ verifizieren
  "limit": 100,           // Seitengröße
  "start": 0,             // Offset (Paginierung)
  "onlyIds": false        // query: nur IDs zurückgeben
}
```

`diff` nutzt statt `onlyIds` das Feld **`localResults`** (die dem Client bereits bekannten Objekte/Versionen) und liefert:

```json
{ "rows": [ ... ], "idsToDelete": [ ... ], "totalRows": 1234 }
```

- **`rows`** — geänderte/neue Objekte (jeweils mit `id`, `version`, `modified`)
- **`idsToDelete`** — serverseitig gelöschte IDs (für Sync-Konsistenz)
- **`totalRows`** — Gesamtzahl (für Fortschritt/Paginierung)

**Sync-Strategie der Lib:** pro Ressource `version`/`modified`-Cursor lokal halten; `diff` mit `localResults` pollen; `rows` upserten, `idsToDelete` entfernen. Voll-Export = `query` mit `start`/`limit` durchblättern.

**Generische Variante:** `POST /api/:id/rest/query` und `POST /api/:id/rest/diff` existieren als generischer Einstieg (`:id` = Ressourcen-Key). ⚠️ verifizieren, welche Ressourcen so adressierbar sind.

---

## 4. Ressourcen-Endpunkte

### 4.1 Dokumente

Die zentrale Ressource. Metadaten liegen in `attributes.data`, die Datei in `pages[]` (als Seiten) bzw. abrufbar als PDF.

#### `POST /api/documents/rest/diff` — Liste/Sync
Siehe §3. `rows[]` je Dokument:

| Feld | Typ | Bedeutung |
|------|-----|-----------|
| `id` | str | Dokument-ID |
| `version` / `modified` / `created` | int/str | Versionierung |
| `status` | str | Verarbeitungsstatus |
| `type` | str | interner Objekttyp |
| `deleted` | bool | Soft-Delete |
| `pages[]` | list | Seiten: `{id, imageVersion, contentVersion}` → Bild-Download |
| `attributes.data` | obj | **Metadaten** (siehe §5) |
| `uploadAttribute` | obj | Herkunft: `originalFileName`, `originalFileType`, `sourceName`, `uploadDate`, `uploadMetaData` (E-Mail-Header, Cloud-Storage-Pfad) |
| `sharedSpaceIds` | list | geteilte Bereiche |
| `forbiddenActions` | list | gesperrte Aktionen |

#### `GET /api/documents/rest/:id` — Einzeldokument
Volles Objekt inkl. erweitertem `attributes.data` (siehe §5). `404` + `apiError`/`errorCode`/`errorMessage` bei unbekannter ID.

#### `PUT /api/documents/rest/:id` — Dokument ändern
Body = vollständiges Dokumentobjekt (`attributes`, `pages`, `status`, `uploadAttribute`, `version`, …). Für Metadaten-Korrekturen (Titel, Tags, Typ). ⚠️ Optimistic-Locking über `version` verifizieren.

#### `POST /api/documents/rest` — Dokument anlegen (**Upload**)
```http
POST /api/documents/rest
Content-Type: multipart/form-data

document=<JSON-Metadaten>   # form field
file=<Binärdatei>           # form field (PDF/Bild)
id=<vorab erzeugte ID?>     # ⚠️ verifizieren ob Client- oder Server-generiert
```
→ Das ist der Endpunkt für den **Scanner-Upload**. Response = angelegtes Dokument mit `pages[]` und `uploadAttribute.newUpload=true`. ⚠️ Genaue Struktur des `document`-Feldes (Minimal-Metadaten) live verifizieren.

#### `GET /api/v1/documents/:id/pdf?mode=<...>` — **Original-PDF**
Liefert `application/pdf`. `mode` steuert die Variante (⚠️ Werte verifizieren — vermutlich Original vs. zusammengeführt/annotiert). **Primärer Weg für die Paperless-Migration.**

#### `GET /api/v1/pages/:id/image?size=<...>&version=<...>` — Seiten-Bild
Liefert `image/jpeg` je Seite. `size` = Auflösungsstufe (Thumbnail/Full, ⚠️ Werte verifizieren), `version` = `imageVersion` aus `pages[]`. Fallback, falls kein PDF verfügbar.

#### `GET /api/pages/:id` — Seiten-Metadaten (JSON)
Metadaten zu einer Seite (OCR-Text/Layout?). ⚠️ Response-Schema verifizieren.

### 4.2 Tags → Paperless-**Tags**

#### `POST /api/tags/rest/diff` / `GET /api/tags/rest/:id`

| Feld | Typ |
|------|-----|
| `id` | str |
| `name` | str |
| `colorCode` | str (Farbe) |
| `documentCounter` | int (Anzahl Dokumente) |
| `lastAdded` / `created` / `modified` | str |
| `version` / `deleted` | int/bool |

### 4.3 Companies → Paperless-**Correspondents**

#### `POST /api/companies/rest/diff` / `GET /api/companies/rest/:id`
Absender/Empfänger-Firmen (Amazon, Vodafone, Hetzner …).

| Feld (Auswahl) | Typ | Bedeutung |
|----------------|-----|-----------|
| `id` | str | referenziert in Dokument `senderId`/`receiverId` |
| `companyName` | str | → Correspondent-Name |
| `contactType` / `contactStatus` | str | |
| `documentCounter` | int | |
| `connected` / `fromUserDb` | bool | System- vs. eigene Firma |
| `attributes.data` | obj | reiche Firmendaten: `ibans`, `vatIds`, `emails`, `phoneNumbers`, `websites`, `germanTaxIds` … |
| `brandingColors` / `hasLogo` | obj/bool | Logo via `GET /api/v1/companies/:id/logo/HD?version` |

### 4.4 Contacts → Correspondents / Custom-Fields

Persönliche Kontakte (mit Adresse). **Vollständige Pflege via CRUD:**

#### `GET /api/contacts/rest/:id`

| Feld (Auswahl) | Typ |
|----------------|-----|
| `id` / `companyId` | str |
| `firstName` / `lastName` / `companyName` | str |
| `email` / `phoneNumber` / `faxNumber` | str |
| `url` / `supportURL` / `userPortalURL` | str |
| `address.{street, secondLine, zipCode, city, state, country, countryLocale}` | str |
| `contactType` / `contactStatus` | str |
| `documentCounter` / `version` / `deleted` | int/bool |

#### `POST /api/contacts/rest` — Kontakt anlegen
Body-Felder: `id`, `companyId`, `companyName`, `email`, `phoneNumber`, `url`, `address`, `contactType`, `contactStatus`, `connectedToOtherUser`, `fromUserDb`, `documentCounter`, `deleted`, `version`.
⚠️ `firstName`/`lastName` erscheinen im GET, nicht im beobachteten POST-Body — beim Anlegen ggf. in `address`/separat. Live verifizieren.

#### Update
Über `PUT /api/contacts/rest/:id` (analog Dokumente). ⚠️ im HAR nicht direkt beobachtet, Muster gilt.

### 4.5 Document-Types → Paperless-**Document-Types**

#### `POST /api/document-types/rest/query`

| Feld | Typ | Bedeutung |
|------|-----|-----------|
| `id` | str | referenziert in Dokument `attributes.data.documentTypeId` |
| `i18NName` | str | Anzeigename |
| `i18nDictionary` | obj | Felddefinitionen des Typs (z. B. `invoiceId`, `invoiceDate`, `amount{value,currency}`, `bankAccount1{iban,bic,bank,account_holder}`, `customerId`, `payed`) |
| `documentTypeScheme` | str | Schema-Referenz |
| `schemaDefinition` | obj | Feld-Constraints, `displayHints`, `readOnly`, `hidden` … |
| `documentCounter` | int | |

→ Diese `i18nDictionary`-Felder definieren, **welche strukturierten Werte** ein Dokument je Typ trägt → direkte Vorlage für **Paperless Custom-Fields**.

### 4.6 Weitere Ressourcen (Sync-fähig, gleiches `diff`-Muster)

| Endpunkt | Inhalt | Kernfelder |
|----------|--------|-----------|
| `POST /api/reminders/rest/diff` | Erinnerungen/Fristen | `description`, `documentId`, `startDate`, `done` |
| `POST /api/conversations/rest/diff` | Nachrichten/Konversationen (Fileee-Postfach) | `messages[]`, `participants`, `conversationType` |
| `POST /api/processes/diff` | Prozesse/Workflows | (im Traffic leer) |
| `POST /api/settings/rest/query` | Nutzereinstellungen | `type`, `value`, `valueType`, `priority` |
| `GET /api/actions/:id` | verfügbare Aktionen | `rows` |

### 4.7 Push (SSE)

#### `GET /push/sse/:id?subscription=<...>&keepAlive=<...>`
`text/event-stream` — Echtzeit-Benachrichtigung über Änderungen. Für einen Live-Sync-Modus statt Polling. ⚠️ Event-Format verifizieren.

---

## 5. Dokument-Metadaten (`attributes.data`) → Paperless-Mapping

Jeder Wert in `attributes.data` ist ein Objekt (mit Wert + Quelle/Confidence, ⚠️ genaue Sub-Struktur verifizieren). Beobachtete Schlüssel:

| `attributes.data`-Schlüssel | Bedeutung | → Paperless |
|-----------------------------|-----------|-------------|
| `title` | Dokumenttitel | **Titel** |
| `documentTypeId` | Verweis auf Document-Type | **Document-Type** |
| `tagIds` | Liste Tag-IDs | **Tags** |
| `senderId` | Absender (Company/Contact-ID) | **Correspondent** |
| `receiverId` | Empfänger (meist eigene User-ID) | (i. d. R. man selbst) |
| `invoiceDate` / `issueDate` | Rechnungs-/Ausstellungsdatum | **Dokumentdatum** |
| `invoiceDueDate` | Fälligkeit | Custom-Field |
| `invoiceId` | Rechnungsnummer | Custom-Field |
| `amount` / `grossIncome` / `netIncome` | Beträge `{value,currency}` | Custom-Field |
| `customerId` | Kundennummer | Custom-Field |
| `bankAccount1` | `{iban,bic,bank,account_holder}` | Custom-Field(s) |
| `paymentReference` | Verwendungszweck | Custom-Field |
| `payed` | bezahlt-Flag | Custom-Field/Tag |
| `contentLanguage` | Sprache | (OCR-Sprache) |
| `totalPageCount` / `maxPageNr` | Seitenzahl | — |
| `read` / `reviewed` / `secured` | Status-Flags | ggf. Tags |

---

## 6. Sicherheits-/Secret-Hinweise (verbindlich)

- **Credentials:** Username, Passwort, **TOTP-Seed** gehören in einen Secret-Manager (Vaultwarden/Infisical, Item „Fileee API"), nie in Code/Repo. Die Lib liest sie via bestehende Vault-Integration des Aufrufers.
- **Session-Cookie** ist ein Secret — persistierte Cookie-Jar mit Dateirechten `600`, nie loggen.
- **`x-xsrf-token`**, Cookie-Werte, PDF-Inhalte, Dokument-Metadaten = **PII** → nie in Logs/Ausgaben/Commits.
- Dieses Dokument enthält bewusst **keine** echten Werte — nur Struktur.

---

## 7. Offene Verifikationspunkte (vor Implementierung live prüfen)

1. `criteria`- und `sortOrder`-Struktur (Filter/Sortierung) bei `query`/`diff`.
2. `mode`-Werte bei `/api/v1/documents/:id/pdf`; `size`-Werte bei `/pages/:id/image`.
3. Upload: Minimal-Struktur des `document`-Felds + ob `id` client- oder serverseitig.
4. `POST /api/f/token/login`: Herkunft/Lebensdauer des `token` (langlebige Session möglich?).
5. `/api/f/start`-Wirkung (CSRF-Bootstrap?) und exakte Cookie-Namen (Session vs. `XSRF-TOKEN`).
6. Session-Lebensdauer (wie oft Re-Login nötig).
7. Rate-Limiting (`secondsBlocked` in `user-session`).
8. SSE-Event-Format unter `/push/sse/:id`.

Verifikation erfolgt kontrolliert gegen das **eigene** Konto (Environment-Bewusstsein: nur Lesen, keine destruktiven Tests).

---

## Anhang: Vollständige Endpunkt-Liste (33)

```
# Auth / Session
GET  /api/f/start                        Session/CSRF init (204)
POST /api/f/existent                     Konto-/2FA-Check
POST /api/f/login                        Passwort+TOTP-Login → Cookie
POST /api/f/token/login                  Token-Login (Alternative)
GET  /api/f/exists                       (Konto-Existenz, GET-Variante)
GET  /api/f/user-session                 Session + Profil
GET  /api/f/account-status               Abo/Lizenz

# Dokumente
POST /api/documents/rest                 Upload (multipart)
GET  /api/documents/rest/:id             Einzeldokument
PUT  /api/documents/rest/:id             Ändern
POST /api/documents/rest/diff            Liste/Sync
GET  /api/v1/documents/:id/pdf           Original-PDF
GET  /api/pages/:id                      Seiten-Metadaten
GET  /api/v1/pages/:id/image             Seiten-JPEG

# Stammdaten
POST /api/tags/rest/diff                 Tags-Sync
GET  /api/tags/rest/:id                  Tag
POST /api/companies/rest/diff            Firmen-Sync
GET  /api/companies/rest/:id             Firma
GET  /api/v1/companies/:id/logo/HD       Firmen-Logo
POST /api/contacts/rest                  Kontakt anlegen
GET  /api/contacts/rest/:id              Kontakt
POST /api/document-types/rest/query      Dokumenttypen

# Weitere
POST /api/reminders/rest/diff            Erinnerungen
POST /api/conversations/rest/diff        Konversationen
POST /api/processes/diff                 Prozesse
POST /api/settings/rest/query            Einstellungen
GET  /api/actions/:id                    Aktionen
GET  /push/sse/:id                       Live-Push (SSE)

# Generisch
POST /api/:id/rest/query                 generischer Query
POST /api/:id/rest/diff                  generischer Diff

# OIDC-Callbacks (Browser-Flow)
GET  /api/callback/openid-connect/google
GET  /api/callback/openid/legacy
```

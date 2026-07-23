# Fileee Interne API — konsolidierte Vollreferenz

**Datum:** 2026-07-23
**Quelle:** 3 HAR-Mitschnitte einer eingeloggten `my.fileee.com`-Session (561 + 335 + 404 Requests,
secret-safe strukturell ausgewertet) **plus** eine anschließende Offline-Code-Analyse von
**416 eindeutigen JS-Bundle-Dateien** (8,5 MB, aus denselben HARs extrahiert, SHA-256-dedupliziert),
darunter ein 4,2 MB großes Kotlin/JS-kompiliertes „Core-SDK" (`io.fileee.shared.*`). Kein einziger
Live-Request wurde für die Code-Analyse gestellt.
**Status:** **Kanonische API-Doku dieser Library.** Alles, was zur Implementierung von `go-fileee`
gebraucht wird, ist hier enthalten. Rekonstruiert aus eigenem, eingeloggtem HAR-Traffic **und**
Offline-Analyse des ausgelieferten Anwendungscodes — kein Reverse-Engineering von Server-Binaries,
keine öffentliche API-Zusage. Siehe auch ADR-0002 (Auth-Modell) und `docs/auth-flow.svg`.

> **Kein offizielles API.** Fileee bietet keine öffentliche, dokumentierte API. Dies ist die
> **interne** API der Web-App (`my.fileee.com`), rekonstruiert aus eigenem, eingeloggtem Traffic
> und dem an den Browser ausgelieferten Anwendungscode. Sie kann sich jederzeit ändern.
> Nutzung ausschließlich gegen das **eigene** Konto, mit konservativem Rate-Limiting (siehe §8).

**Status-Legende:** Struktur (Endpunkt-Pfad, HTTP-Methode, Feldnamen, Response-Form, Enum-Werte)
ist entweder aus echtem Live-Traffic **oder** aus dem tatsächlich ausgeführten Anwendungscode
belegt — beides gilt hier als „belegt", nicht als Vermutung. Nur die **wenigen** verbleibenden
Punkte, die strukturell weder aus Traffic noch aus Code ableitbar sind (siehe §9), tragen noch
den Marker „⚠️ nur Live-Verifikation kann das klären".

---

## 1. Grundlagen

| Eigenschaft | Wert |
|---|---|
| Basis-URL | `https://my.fileee.com` |
| Datenformat | JSON; Login als `application/x-www-form-urlencoded`; Upload als `multipart/form-data` |
| Auth | Session-Cookie (httpOnly) + CSRF-Header `x-xsrf-token` |
| Kein Bearer-/Refresh-Token | Login-Response enthält keinen API-Token — Session lebt ausschließlich im Cookie |
| Push | Server-Sent-Events unter `/push/sse/:id` |
| Statische Renders | Seiten-JPEGs + PDFs unter `/api/v1/...` |

**REST-Konventionen der App:**

| Muster | Bedeutung |
|---|---|
| `GET /api/<resource>/rest/:id` | Einzelobjekt lesen |
| `POST /api/<resource>/rest` | Objekt anlegen |
| `PUT /api/<resource>/rest/:id` | Objekt ändern (Optimistic-Locking über `version`) |
| `DELETE /api/<resource>/rest/:id` | Objekt hart löschen (generischer REST-Client, code-belegt — siehe §4.1) |
| `POST /api/<resource>/rest/query` | Liste paginiert (offset-basiert) |
| `POST /api/<resource>/rest/diff` | Delta-Sync (inkrementell) |

`query` und `diff` teilen sich denselben Request-Body (siehe §3). `diff` liefert zusätzlich
`idsToDelete` für die Synchronisation. Der zugrunde liegende HTTP-Client-Layer kennt intern alle
7 Standard-Methoden (`GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS`, siehe §6) — im beobachteten
Web-App-Traffic kamen aber nur `GET`/`POST`/`PUT` vor; `DELETE` ist nur code-belegt (§4.1).

**Client/Server-Architektur (aus Code-Analyse, wichtig fürs Verständnis der Semantik):** Die
Web-App nutzt einen generischen REST-Basisklassen-Unterbau (Kotlin/JS-Klassenhierarchie
`aVn`/`vVn`, pro Ressource als konkrete Subklasse wie `hjn` für Dokumente instanziiert), der
`persist()` → `POST`, `update()` → `PUT`, `deleteById()` → `DELETE` bereitstellt. Eine Cache-Schicht
darüber (`put(entity)`) entscheidet **client-seitig**, ob create oder update nötig ist:

```js
put(entity) {
  return this.entityCache.getEntity(entity.id)
    ? doCacheAwareUpdate({entityType: this.entityType, entity})   // PUT, wenn ID bereits lokal bekannt
    : doCacheAwareCreate({entityType: this.entityType, entity});  // POST, wenn neu
}
```

`doCacheAwareUpdate` markiert die Entity **sofort lokal** als aktualisiert (Optimistic-UI) und
sieht einen **automatischen Retry-Versuch** bei fehlgeschlagenem Update vor (`retryOnceUpdate`) —
ein starkes Indiz dafür, dass serverseitige `version`-Konflikte ein erwarteter Normalfall sind,
auch wenn der exakte HTTP-Fehlercode dafür code-seitig nicht sichtbar ist (§9).

---

## 2. Authentifizierung

### 2.1 Ablauf (headless, mit TOTP)

```
1. GET  /api/f/start                         → 204   (Startup-Bootstrap-Ping)
2. POST /api/f/existent   {username}         → prüft Konto + 2FA-Status
3. POST /api/f/login      (form-urlencoded)  → setzt Session-Cookie (+ XSRF-TOKEN)
   Felder: username, password, two-factor-token, conversationAddWithoutInvite
4. (ab jetzt) Cookies mitsenden; bei POST/PUT/DELETE zusätzlich Header x-xsrf-token
5. GET  /api/f/user-session                  → authorized:true zur Kontrolle
   (am Ende der Session: POST /api/f/logout)
```

### 2.2 `GET /api/f/start`

Antwortet `204`. **Zweck jetzt code-belegt:** reiner **Startup-Bootstrap-Ping**, in der Web-App
als React-Query unter dem Query-Key `startup.fStart` eingebunden (Pendant: `startup.fExists`):

```js
function Jw(){
  return fc.get("/f/start")
    .then(() => "success")
    .catch(() => { logger.error("Failed to make fStart query"); return "failed"; });
}
```

Der Response-**Body** wird komplett ignoriert — nur Erfolg/Misserfolg zählt. Das stützt (beweist
aber nicht abschließend) die Annahme, dass hier per `Set-Cookie`-Seiteneffekt Session-/CSRF-Cookies
initialisiert werden. **Vor** dem Login aufrufen.

### 2.3 `POST /api/f/existent`

Prüft, ob ein Konto existiert und ob 2FA aktiv ist — **vor** dem eigentlichen Login.

```http
POST /api/f/existent
Content-Type: application/json

{ "username": "<email>" }
```

| Response-Feld | Typ | Bedeutung |
|---|---|---|
| `existent` | bool | Konto existiert |
| `twoFactorAuthEnabled` | bool | 2FA aktiv → `two-factor-token` beim Login nötig |
| `enabledLoginOptions` | list | verfügbare Login-Wege (Passwort, Google/OIDC) |
| `loggedIn` | bool | bereits eingeloggt (bestehende Session) |
| `actingUserType` | str | Nutzertyp |

### 2.4 `GET /api/f/exists`

GET-Variante des Konto-Existenz-Checks (im Traffic beobachtet). Response ⚠️ nicht im Detail
verifiziert — analog `POST /api/f/existent` annehmen.

### 2.5 `POST /api/f/login` — Passwort-Login (+ TOTP)

Das OTP wird **im selben Request** als `two-factor-token` mitgeschickt — kein separater
2FA-Schritt.

```http
POST /api/f/login
Content-Type: application/x-www-form-urlencoded

username=<email>&password=<pw>&two-factor-token=<6-stelliger TOTP>&conversationAddWithoutInvite=false
```

| Request-Feld | Pflicht | Bedeutung |
|---|---|---|
| `username` | ja | E-Mail |
| `password` | ja | Passwort |
| `two-factor-token` | bei aktivem 2FA | aktueller TOTP-Code (RFC 6238) |
| `conversationAddWithoutInvite` | — | im Traffic `false` (Zweck ⚠️ verifizieren, vermutlich UI-Flag) |

**Erfolg:** `Set-Cookie` mit Session + `XSRF-TOKEN`; Body:

| Response-Feld | Typ |
|---|---|
| `loggedIn` | bool |
| `userId` / `username` | str |
| `user.participantId` / `user.participantName` / `user.type` / `user.username` | str |
| `groups` / `additionalRights` | list |

Kein Token im Body — Session steckt ausschließlich im gesetzten Cookie. Für headless-Betrieb:
Cookie-Jar persistieren, bei `401`/`authorized:false` neu einloggen.

### 2.6 `POST /api/f/token/login` — Token-Login (Alternative, zwei Code-Pfade)

```http
POST /api/f/token/login
Content-Type: application/x-www-form-urlencoded

token=<token>
```

**Zwei unabhängige Aufrufer im Code (neu geklärt durch JS-Analyse):**

1. **Web-App (leichtgewichtig):** `fc.post("/f/token/login", stringify({token: e}))` — nur ein
   `token`-Feld.
2. **Kotlin-Core-SDK:** ruft denselben Pfad, aber mit einem **reichhaltigeren** Body-Objekt
   (mehrere Positionsparameter, im Detail nicht vollständig aufgelöst) — deutet auf einen
   **Device-/Mobile-Login-Flow mit Zusatzfeldern** hin, der über die reine Web-App hinausgeht.

**Token-Quelle GEKLÄRT (Browser-Session 2026-07-23, §2.11):** Der `token` ist das langlebige
**`rememberMe`-JWT-Cookie**. Damit ist `token/login` der bevorzugte **headless-Re-Auth-Pfad** —
einmal per Passwort+TOTP einloggen, `rememberMe` aus `Set-Cookie` übernehmen, danach ohne
erneutes Passwort+TOTP re-authentifizieren.

### 2.7 `POST /api/f/logout` (NEU, code-belegt)

Kein Body. Gegenstück zu `/f/login` — beendet die Session serverseitig. Bisher in keinem HAR
beobachtet, aber im Haupt-Bundle (`index-DSrKMhJw.js`) fest verdrahtet.

### 2.8 `GET /api/f/user-session`

Aktuelle Session + vollständiges Nutzerprofil.

| Response-Feld | Typ | Bedeutung |
|---|---|---|
| `authorized` | bool | Session gültig |
| `secondsBlocked` | num | Rate-Limit/Sperre (>0 = blockiert; generisches Muster, siehe §6.7/§9) |
| `user.hasFileeePassword` | bool | Passwort-Login möglich (vs. nur Google) |
| `user.fileeeEmail` / `user.username` | str | |
| `user.id` / `user.userCompanyId` | str | eigene IDs (referenziert in Dokumenten als `receiverId`) |
| `user.language` | str | |
| `user.addresses` | list | Adressbuch des Nutzers |
| `user.address` | obj | Einzeladresse: `street`, `zipCode`, `city`, `state`, `country`, `countryLocale`, `firstName`, `lastName` — zusätzlich zum `addresses`-Array |
| `user.actingUserType` | str | Nutzertyp |
| `user.activationRequired` | bool | Aktivierung ausstehend |
| `user.groups` | list | Gruppen/Rechte |
| `user.registerDate` | str | Registrierungsdatum |
| `user.created` / `user.modified` / `user.version` / `user.deleted` | int/str/bool | Standard-Objektfelder |

### 2.9 `GET /api/f/account-status`

Abo-/Lizenzstatus.

| Response-Feld | Typ |
|---|---|
| `accountTypeId` | str |
| `currentSubscription.name` / `.frequency` / `.amount` | str/num |
| `payedUntil` / `nextLicenseRefill` | str (Datum) |
| `problem` | str |

Ergänzt durch `POST /api/feature-licenses/rest/query` (§4.8) für granulares Lizenz-/Kontingent-Tracking.

### 2.10 CSRF (`x-xsrf-token`)

Bei **schreibenden** Requests (`POST`/`PUT`/`DELETE`) muss der Header `x-xsrf-token` gesetzt sein.
Wert = Inhalt des `XSRF-TOKEN`-Cookies (klassisches Double-Submit-Cookie-Pattern). In der Go-Lib:
aus dem Cookie-Jar lesen und auf mutierenden Requests automatisch anhängen.

### 2.11 Cookie-Übersicht (aus Browser-Session belegt, 2026-07-23)

Direkt aus dem eingeloggten Browser-Tab (DevTools → Application → Cookies) verifiziert — schließt
die zuvor offenen §9-Punkte (a) und (c). **Nur Namen/Flags, keine Werte.**

| Cookie | httpOnly | Secure | Path | Rolle |
|---|---|---|---|---|
| `JSESSIONID` | ✓ | ✓ | `/` | Session-Cookie (Wert ist ein JWT), langlebig (~1 Jahr) |
| `rememberMe` | ✓ | ✓ | `/` | **Remember-Me-JWT — Quelle des `token` für `POST /api/f/token/login`**, langlebig |
| `webappjetty` | ✓ | ✓ | `/api` | Jetty Load-Balancer-Affinity (geht nur an `/api`-Requests) |
| `XSRF-TOKEN` | ✗ | ✓ | `/` | CSRF Double-Submit — **nicht** httpOnly (JS/Lib liest ihn → `x-xsrf-token`-Header) |
| `userId` | ✗ | ✗ | `/` | eigene User-ID, client-lesbar |

Nicht-Auth-Cookies (`fileee-domain`, `fileee_branding`, `disable_tracking`, `gdpr-consent`) und
Tracking-Cookies auf `.fileee.com` (`_ga*`, `_fbp`, `_gcl_*`, `_hjSession*`, `_uetsid`/`_uetvid` =
Google/Facebook/Hotjar/Microsoft-UET) sind für die API irrelevant.

**Go-Lib-Konsequenz:** Der Cookie-Jar übernimmt `JSESSIONID`/`rememberMe`/`webappjetty` automatisch
aus `Set-Cookie`; die Lib liest nur `XSRF-TOKEN` aus dem Jar und setzt daraus den Header.
`webappjetty` (Path `/api`) geht per Path-Matching automatisch nur an API-Requests. Cookie-Horizont
~1 Jahr → Re-Login selten; persistenter Re-Auth über `rememberMe` (§2.6).

### 2.12 OIDC-Callbacks (Browser-Flow, nicht headless nutzbar)

```
GET /api/callback/openid-connect/google
GET /api/callback/openid/legacy
```

Nur relevant für den interaktiven Browser-Login (Google-SSO). Für einen headless Go-Client ohne
Bedeutung.

---

## 3. Paginierung & Filter (`query` / `diff`)

### 3.1 Gemeinsamer Request-Body

```http
POST /api/<resource>/rest/query
Content-Type: application/json

{
  "criteria": [ ],         // Filter — Array typisierter Bedingungen (Grammatik §3.2)
  "sortOrder": [ ],        // Sortierung — Array (Grammatik §3.2)
  "limit": 100,            // Seitengröße
  "start": 0,              // Offset (Paginierung)
  "onlyIds": false         // query: nur IDs zurückgeben
}
```

`diff` nutzt statt `onlyIds` das Feld **`localResults`** (die dem Client bereits bekannten
Objekte/Versionen) und liefert:

```json
{ "rows": [ ], "idsToDelete": [ ], "totalRows": 1234 }
```

| Feld | Bedeutung |
|---|---|
| `rows` | geänderte/neue Objekte (jeweils mit `id`, `version`, `modified`) |
| `idsToDelete` | serverseitig gelöschte IDs (für Sync-Konsistenz) |
| `totalRows` | Gesamtzahl (für Fortschritt/Paginierung) |

**Sync-Strategie:** pro Ressource `version`/`modified`-Cursor lokal halten; `diff` mit
`localResults` pollen; `rows` upserten, `idsToDelete` entfernen. Voll-Export = `query` mit
`start`/`limit` durchblättern.

**Generische Variante:** `POST /api/:id/rest/query` und `POST /api/:id/rest/diff` existieren als
generischer Einstieg (`:id` = Ressourcen-Key). ⚠️ verifizieren, welche Ressourcen so adressierbar
sind (vermutlich alle Sync-fähigen Ressourcen aus §4).

### 3.2 `criteria` / `sortOrder`-Grammatik (vollständig belegt: HAR + Code)

`criteria` ist ein **Array typisierter Bedingungen** (kein freies Objekt). Jede Bedingung
referenziert ein Schema-Feld über eine interne Query-DSL-Konstante im Format
`<Entity>Field:<KONSTANTE>`:

```
QueryRequest {
  start:      int             // Offset
  limit:      int             // Seitengröße
  criteria:   Criterion[]
  sortOrder:  SortOrder[]
  onlyIds:    bool            // nur bei /query, nicht bei /diff
}

Criterion {
  field:    { value: <EntityField-Konstante>, serializeInformation: { type: Boolean|Enum|List|String } }
  operator: <Operator>        // eine der 21 Werte, siehe §6.2
  optional: bool
  value:    { value: <any>, serializeInformation: { type: <TypName> } }
}

SortOrder {
  baseAttribute: { value: <EntityField-Konstante>, serializeInformation: { type: <TypName> } }
  descending:    bool
  nullsFirst:    bool
}
```

**`serializeInformation.type`-Werte (belegt):** `Boolean`, `Enum`, `List`, `String`.

**Operatoren:** vollständige 21-Werte-Enum — siehe §6.2. Aus Live-Traffic tatsächlich beobachtet:
`EQ`, `IN`, `NEQ`, `OR` (Teilmenge der vollständigen Liste).

### 3.3 Feld-Konstanten je Ressource (`EntityField`-Familie, Auszug — belegt)

| Feld-Enum | Beobachtete Konstanten |
|---|---|
| `ContactinyObjectField` (sic — Tippfehler im Original-API, vermutlich „ContactObject") | `CONTACT_TYPE`, `COMPANY_NAME` |
| `ConversationField` | `PRESENTATION`, `READ` |
| `DocumentField` | `DOCUMENT_INFORMATION__ISREAD`, `DOCUMENT_INFORMATION__STATUS`, `DOCUMENT_INFORMATION__CREATED` |
| `EntityObjectField` (übergreifend, alle Ressourcen) | `ID`, `CREATED`, `MODIFIED` |
| `ForeignAccountField` | `ACCOUNT_TYPE` |
| `HasDocumentCounterField` (übergreifend) | `DOCUMENT_COUNTER` |
| `QueryOperationField` | `COMBINATION` (Verknüpfung mehrerer Bedingungen) |
| `ReminderField` | `DONE`, `START_DATE` |
| `ServerProcessField` | `Status`, `Dismissed` |

→ Für `go-fileee`: pro Ressource ein typisiertes Feld-Enum → typsichere Query-Builder.
Vollständigkeit dieser Liste ⚠️ nicht garantiert — nur die tatsächlich im Traffic beobachteten
Konstanten sind aufgeführt.

---

## 4. Ressourcen

### 4.1 Dokumente

Die zentrale Ressource. Metadaten liegen in `attributes.data` (§5), die Datei in `pages[]` (als
Seiten) bzw. abrufbar als PDF.

#### `POST /api/documents/rest/diff` — Liste/Sync

Siehe §3. `rows[]` je Dokument:

| Feld | Typ | Bedeutung |
|---|---|---|
| `id` | str | Dokument-ID |
| `version` / `modified` / `created` | int/str | Versionierung |
| `status` | str | Verarbeitungsstatus — vollständige Enum `PublicDocumentStatus`, siehe §6.1 |
| `type` | str | interner Objekttyp |
| `deleted` | bool | Soft-Delete-Flag |
| `pages[]` | list | Seiten: `{id, imageVersion, contentVersion}` → Bild-Download |
| `attributes.data` | obj | Metadaten (siehe §5) |
| `uploadAttribute` | obj | Herkunft: `originalFileName`, `originalFileType`, `sourceName`, `uploadDate`, `uploadMetaData` (E-Mail-Header, Cloud-Storage-Pfad), `newUpload` (bool, nach frischem Upload) |
| `sharedSpaceIds` | list | geteilte Bereiche |
| `forbiddenActions` | list | gesperrte Aktionen (Werte aus `DocumentAction`, §6.3) |

#### `GET /api/documents/rest/:id` — Einzeldokument

Volles Objekt inkl. erweitertem `attributes.data`. `404` + `apiError`/`errorCode`/`errorMessage`
bei unbekannter ID.

#### `PUT /api/documents/rest/:id` — Dokument ändern

Body = vollständiges Dokumentobjekt (`attributes`, `pages`, `status`, `uploadAttribute`,
`version`, …). Für Metadaten-Korrekturen (Titel, Tags, Typ). **Optimistic-Locking über `version`**
ist Design-Intention (client-seitiger Auto-Retry bei fehlgeschlagenem Update, siehe §1) — der
exakte Server-Fehlercode bei Versionskonflikt ist ⚠️ nicht code-sichtbar (§9).

#### `DELETE /api/documents/rest/:id` (NEU, code-belegt)

Hartes Löschen. Im Live-Traffic **nie beobachtet** (die Web-UI nutzt für Dokumente einen
Soft-Delete/Trash-Flow über §4.2), aber im Core-SDK real verdrahtet: die Dokument-API-Klasse erbt
`deleteById()` von der generischen REST-Basisklasse, die `baseUrl = "documents/rest"` und
HTTP-Methode `DELETE` setzt. Vorsicht: unklar, ob dieser Pfad in der Praxis vom Server überhaupt
für bereits nicht-gelöschte Dokumente akzeptiert wird oder nur intern für Aufräumzwecke existiert
— vor produktivem Einsatz gegen ein Test-Konto verifizieren.

#### `POST /api/documents/rest` — Dokument anlegen (**Upload**, vollständig geklärt)

```http
POST /api/documents/rest
Content-Type: multipart/form-data

file=<Binärdatei>                          # Formfeld, MEHRFACH möglich (mehrere Dateien → 1 Dokument)
id=<client-generierte ID>                  # IMMER gesendet
document=<JSON-Metadaten>                  # optional
attributes.data.title.value=<Titel/Dateiname>  # Fallback, falls kein "document"-Objekt vorbereitet ist
meta=<JSON.stringify(...)>                 # optional, bisher undokumentiertes Feld
```

**Client-Logik (aus `document-uploader-*.js`, unminifiziert):**

```js
function(axiosInstance, apiPath, documentToUpload, cancelToken, onProgress){
  let formData = new FormData();
  documentToUpload.files.forEach(f => formData.append("file", f));
  formData.append("id", documentToUpload.id);
  documentToUpload.document
    ? formData.append("document", documentToUpload.document)
    : formData.append("attributes.data.title.value", documentToUpload.title || documentToUpload.files[0].name);
  documentToUpload.meta && formData.append("meta", JSON.stringify(documentToUpload.meta));
  return axiosInstance.post(apiPath, formData, {onUploadProgress: onProgress, cancelToken: cancelToken.token});
}
```

| Feld | Pflicht | Bedeutung |
|---|---|---|
| `file` | ja | Binärdatei — mehrfach anhängbar |
| `id` | ja | **Client-generiert** via `InstanceHelper.newObjectId()` (siehe unten), immer gesendet |
| `document` | optional | volles JSON-Metadaten-Objekt |
| `attributes.data.title.value` | Fallback | typischer Upload-Pfad — flaches Formfeld statt vollem `document`-Objekt |
| `meta` | optional | `JSON.stringify()`-Extra-Metadaten |

**`id` ist client-generiert (nicht mehr offen):**

```js
function x(uploadMetaData){
  let uploadAttr = new UploadAttribute();
  uploadMetaData && (uploadAttr.uploadMetaData = uploadMetaData);
  const newId = InstanceHelper.newObjectId();   // Client generiert die ID selbst
  const doc = new DocumentApiDTO([], uploadAttr, new BaseComposedAttribute, new Set, null, null, new Set, null, PublicDocumentStatus.NEW);
  doc.id = newId;
  return doc;
}
```

Das lokale Dokument wird mit `status = PublicDocumentStatus.NEW` (Optimistic-UI) konstruiert,
bevor eine Server-Antwort vorliegt.

**Serverseitige Duplikaterkennung (neuer Verhaltens-Fakt):** Die zurückgegebene `response.data.id`
wird mit der client-generierten `id` verglichen — weichen sie ab, hat der Server ein bereits
existierendes Dokument erkannt und dessen ID zurückgegeben statt ein neues anzulegen:

```js
let uiStatus = response.data.id !== uploadItem.uploadId ? "Duplicate" : "Success";
```

→ Für `go-fileee`: nach jedem Upload die zurückgegebene `id` mit der gesendeten vergleichen, um
Duplikate zu erkennen, statt sich blind auf die eigene `id` zu verlassen.

#### `POST /api/documents/rest/:id/revision-lock` (NEU) — gesetzliche Aufbewahrungssperre

Body-Schema (`RevisionInformation`):

```
{
  lockDurationInYears: int,       // Pflichtfeld
  lockedSince:  <timestamp>?,     // optional, Default = jetzt
  lockedUntil:  <timestamp>?      // optional, Default = lockedSince + lockDurationInYears
}
```

Setzt eine Revisionssicherheits-Sperre (z. B. § 147 AO / GoBD-Aufbewahrungsfrist) auf ein
Dokument — entspricht dem `DocumentAction`-Wert `REVISION_LOCK` (§6.3).

#### `POST /api/documents/rest/multi-edit` (NEU) — Bulk-Edit über Query-Filter

Body-Schema (`MultiEditRequest`):

```
{
  documentQuery: <QueryRequest — dieselbe criteria/sortOrder-Struktur wie /query, §3>,
  changes:       <partielles Attribut-Änderungs-Objekt>
}
```

Ändert alle Dokumente, die auf `documentQuery` matchen, in einem Request statt per Einzel-`PUT`.

#### `POST /api/documents/rest/zip` (NEU) — asynchroner ZIP-Export-Job

Body-Schema (`CreateDocumentZipRequest`):

```
{
  documentIds: string[],
  zipPassword: string?   // optional, ZIP-Verschlüsselung
}
```

Ablauf: `POST .../zip` (Job starten) → **`GET /api/documents/rest/zip/:jobId`** (Status pollen,
NEU) → **`DELETE /api/documents/rest/zip/:jobId`** (abbrechen, NEU). Passt zum
`ServerProcess`-Konzept (asynchrone Hintergrund-Jobs, §4.8).

#### `GET /api/v1/documents/:id/pdf?mode=<download|print>` — Original-PDF (Werte jetzt vollständig belegt)

Liefert `application/pdf`. Aus dem URL-Builder-Modul (`imageHelper-*.js`) sind **beide** Werte
als Literal-Argumente an drei unabhängigen Call-Sites belegt:

```js
function u(e){return A()+"/v1/documents/"+e+"/pdf?mode=download"}
function r(e){return A()+"/v1/documents/"+e+"/pdf?mode=print"}
```

- **`mode=download`** — Standardvariante, „Herunterladen"-Aktion.
- **`mode=print`** — Druckansicht/-vorschau (öffnet in neuem Tab).

Kein dritter Wert im gesamten Code gefunden — die Enum gilt damit als abschließend binär, bis ein
gegenteiliger Live-Beleg auftaucht. **Primärer Weg für Dokument-Export.**

#### `GET /api/v1/documents/:id/original` (NEU) — Original-Datei ohne PDF-Wrapping

Lädt die ursprünglich hochgeladene Datei direkt herunter (z. B. das Original-Bild/-Scan ohne
PDF-Konvertierung).

#### `GET /api/v1/documents/download?documents=<id1,id2,...>` (NEU) — Bulk-Download

Mehrere Dokumente gebündelt herunterladen, IDs als Kommaliste im Query-Parameter `documents`.

#### `GET /api/pages/:id` — Seiten-OCR (JSON, Response-Schema vollständig geklärt)

Response ist eine **Liste von OCR-Bounding-Boxes** (ein Eintrag je erkanntem Wort/Textblock):

```json
[ { "text": "<OCR-Fragment>", "left": 0, "top": 0, "right": 0, "bottom": 0, "width": 0, "height": 0, "webappId": "<id>" } ]
```

`text` ist erkannter Dokumentinhalt → **PII**, nie loggen/ausgeben. Nutzbar für
Volltext-Suche/Positionierung.

#### `GET /api/v1/pages/:id/image?size=<smedium|medium>&version=<...>` — Seiten-Bild

Liefert `image/jpeg` je Seite. `version` = `imageVersion` aus `pages[]` — immer frisch aus dem
zuletzt geladenen `pages[]`-Array nehmen, nicht cachen. **`size`-Werte vollständig belegt** (im
Code als benannte Konstanten definiert):

```js
const p={value:"smedium"}, N={value:"medium"};
```

`smedium` = Default für kleine Vorschau-Thumbnails, `medium` = größere Leseansicht. Andere Wörter
wie `small`/`large`/`full`/`original` kommen im Code vor, gehören aber zu **anderen** Kontexten
(UI-Layout-Klassen, Icon-Größen) — nicht zu diesem Parameter. Nur diese zwei Werte sind belegt;
eine größere Stufenleiter ist nicht auszuschließen, aber im Code nicht zu finden.

#### `GET /api/v1/documents/:id/pages/:pageId/image?size=<...>` (NEU) — alternative Seiten-Bild-Route

Adressiert das Seiten-Bild über Dokument-ID **und** Seiten-ID statt nur über die flache
Seiten-ID (`/v1/pages/:id/image`). Beide Routen existieren parallel im Code.

### 4.2 Papierkorb / Soft-Delete (NEU, komplette Ressourcengruppe)

Die Web-UI nutzt für Dokumente standardmäßig einen Soft-Delete/Trash-Flow statt des harten
`DELETE /api/documents/rest/:id` (§4.1):

| Endpunkt | Zweck |
|---|---|
| `POST /api/deleted-documents/list` | Liste der gelöschten, aber nicht endgültig entfernten Dokumente (Papierkorb) |
| `DELETE /api/deleted-documents/:id/delete-permanently` | Einzelnes Dokument endgültig löschen |
| `DELETE /api/deleted-documents/delete-permanently-all` | Papierkorb komplett leeren |

Vermutlich setzt ein „normales" Löschen aus der UI zunächst nur das `deleted`-Flag (§4.1,
`rows[].deleted`) bzw. verschiebt in diese separate Ressource — der genaue Übergangsmechanismus
(PUT mit `deleted=true` vs. eigener Move-Endpunkt) ist ⚠️ nicht abschließend im Code verfolgt.

### 4.3 Fileee-Boxes (NEU, bisher unbekannte Ressource)

„Fileee-Box" ist ein bisher nicht dokumentiertes Organisationskonzept (vermutlich ein Ordner/eine
Sammel-Ablage für Dokumente, ähnlich eines Shared-Space):

| Endpunkt | Zweck |
|---|---|
| `POST /api/fileeeboxes/:boxId/:documentId` | Dokument einer Fileee-Box hinzufügen |
| `DELETE /api/fileeeboxes/:boxId/:documentId` | Dokument aus einer Fileee-Box entfernen |
| `POST /api/fileeeboxes/delete` | Fileee-Box selbst löschen |

Feldstruktur der Box selbst (Name, Owner, geteilte Mitglieder) ⚠️ nicht code-seitig vertieft —
für `go-fileee` v1 nicht kritisch, nur als bekannte Lücke vermerkt.

### 4.4 Tags → Paperless-**Tags**

#### `POST /api/tags/rest/diff` / `GET /api/tags/rest/:id`

| Feld | Typ |
|---|---|
| `id` | str |
| `name` | str |
| `colorCode` | str (Farbe) |
| `documentCounter` | int (Anzahl Dokumente) |
| `lastAdded` / `created` / `modified` | str |
| `version` / `deleted` | int/bool |

### 4.5 Companies → Paperless-**Correspondents**

#### `POST /api/companies/rest/diff` / `GET /api/companies/rest/:id`

Absender-/Empfänger-Firmen (Amazon, Vodafone, Hetzner …).

| Feld | Typ | Bedeutung |
|---|---|---|
| `id` | str | referenziert in Dokument `senderId`/`receiverId` |
| `companyName` | str | → Correspondent-Name |
| `contactType` / `contactStatus` | str | |
| `documentCounter` | int | |
| `connected` / `fromUserDb` | bool | System- vs. eigene Firma |
| `attributes.data` | obj | reiche Firmendaten: `ibans`, `vatIds`, `emails`, `phoneNumbers`, `websites`, `germanTaxIds`, `deutschePostCodes`, `iataAirlineCodes`, `uicTicketVendorCodes` … (in `diff`-`rows[]` zusätzlich `bonusPoints`, `connectedCustomer`, `enterpriseCustomer`, `mainConversationId`, `connectedConversation`, `connectedSponsoredCompany`) |
| `brandingColors` / `hasLogo` | obj/bool | Logo via `GET /api/v1/companies/:id/logo/HD?version`. `brandingColors`-Felder: `primaryColorCode`, `primaryTextColorCode`, `secondaryColorCode`, `secondaryTextColorCode`, `interactionColorCode`, `warningColorCode`, `logoBackgroundColorCode`, `logoTextColorCode` |
| `mainContactId` / `userPreferredContactId` | str | Haupt-/bevorzugter Kontakt |
| `connectedToOtherUser` / `connectedToType` | bool/str | Verknüpfungsstatus |
| `supportedCommunications` | list | unterstützte Kommunikationswege |
| `socialMediaInformation` | obj | `facebookAccount`, `twitterAccount` |
| `userAttributes` | obj | benutzerspezifische Overrides (gleiche Wrapper-Struktur wie `attributes`, siehe §5) |
| `type` / `version` / `created` / `modified` / `deleted` | str/int/bool | Standard-Objektfelder |

#### `GET /api/v1/companies/:id/logo/HD?version=<...>`

Firmen-Logo (High-Definition-Variante).

#### `POST /api/companies/rest/:id/main-contact` (NEU) — Haupt-Ansprechpartner setzen

Setzt `mainContactId` einer Firma.

#### `DELETE /api/companies/:id/logo` bzw. `DELETE /api/:id/logo` (NEU) — Logo löschen

Zwei Code-Pfade beobachtet: ein öffentliches Firmen-Logo (`deletePublicLogo`) und ein
Profil-Logo des eigenen Nutzers (`deleteProfileLogo`) — beide über denselben `/logo`-Unterpfad.

### 4.6 Contacts → Correspondents / Custom-Fields

Persönliche Kontakte (mit Adresse). **Vollständige Pflege via CRUD:**

#### `GET /api/contacts/rest/:id`

| Feld | Typ |
|---|---|
| `id` / `companyId` | str |
| `firstName` / `lastName` / `companyName` | str |
| `email` / `phoneNumber` / `faxNumber` | str |
| `url` / `supportURL` / `userPortalURL` | str |
| `address.{street, secondLine, zipCode, city, state, country, countryLocale}` | str |
| `contactType` | str — Enum `ContactType`, siehe §6.4 |
| `contactStatus` | str — Enum `ContactStatus`, siehe §6.5 |
| `documentCounter` / `version` / `deleted` | int/bool |

#### `POST /api/contacts/rest` — Kontakt anlegen

Body-Felder: `id`, `companyId`, `companyName`, `email`, `phoneNumber`, `url`, `address`,
`contactType`, `contactStatus`, `connectedToOtherUser`, `fromUserDb`, `documentCounter`,
`deleted`, `version`.

⚠️ `firstName`/`lastName` erscheinen im GET, nicht im beobachteten POST-Body — beim Anlegen ggf.
in `address`/separat. Live verifizieren (§9).

#### `PUT /api/contacts/rest/:id` — Kontakt ändern

⚠️ im HAR nicht direkt beobachtet, Muster gilt analog zu Dokumenten (generische
`PUT .../rest/:id`-Konvention, §1).

### 4.7 Document-Types & Document-Type-Schemes → Paperless-**Document-Types** + **Custom-Fields**

**Zwei getrennte, eigenständige Sync-Ressourcen**, verknüpft über den Fremdschlüssel
`documentTypeScheme`. Das **Schema** (Feldtypen, Pflicht/optional, UI-Hinweise) kommt aus
`document-type-schemes`; die **konkreten Werte je Dokumenttyp** aus `document-types`.

#### `POST /api/document-types/rest/query` — Dokumenttyp-Werte

| Feld | Typ | Bedeutung |
|---|---|---|
| `id` | str | referenziert in Dokument `attributes.data.documentTypeId` |
| `i18NName` | str | Anzeigename |
| `i18nDictionary` | obj | konkrete Felder des Typs (z. B. `invoiceId`, `invoiceDate`, `amount`, `bankAccount1`, `customerId`, `payed`) |
| `documentTypeScheme` | str | **FK** → `document-type-schemes` |
| `documentCounter` | int | |
| `type` / `created` / `modified` / `version` / `deleted` | str/int/bool | Standard-Objektfelder |

#### `POST /api/document-type-schemes/rest/query` — Feld-Schema

| Feld | Typ | Bedeutung |
|---|---|---|
| `id` | str | Ziel des `documentTypeScheme`-FK |
| `i18nDictionary` | obj | Feld-**Beschreibungen** (z. B. `amount.{currency,value}`, `bankAccount1.{account_holder,bank,bic,iban}`, `customerId`, `invoiceDate`, `invoiceId`, `payed`) |
| `schemaDefinition` | obj | vollständige Feld-Constraint-Struktur je Feld: `key`, `id`, `concreteType`, `composingTypes`, `constraints`, `dispensable`, `displayHints`, `fieldDescription`, `helpText`, `hintText`, `hidden`, `readOnly`, `serverOnly`, `valueTemplate` |
| `type` / `created` / `modified` / `deleted` | str/int/bool | Standard-Objektfelder |

→ `schemaDefinition` + `i18nDictionary` sind die direkte Vorlage für **Paperless Custom-Fields**.
Das vollständige `i18nDictionary`-Typ-Universum jenseits der belegten Beispielfelder bleibt offen
(§9) — Schema-Definitionen sind serverseitig dynamisch, nicht im Frontend-Code hartkodiert.

### 4.8 Weitere Sync-Ressourcen (gleiches `diff`-/`query`-Muster)

| Endpunkt | Inhalt | Kernfelder |
|---|---|---|
| `POST /api/conversations/rest/diff` | Nachrichten/Konversationen (Fileee-Postfach) | `messages[]`, `participants`, `conversationType` |
| `POST /api/feature-licenses/rest/query` | Lizenz-/Kontingent-Tracking, ergänzt `account-status` | `license.{allowedMaximum, used, refreshCycle, className, key.keyId, id, version, deleted}` |
| `POST /api/foreign-accounts/rest/query` | externe verbundene Konten | in bisher untersuchten Konten leer → Feldstruktur ⚠️ offen (§9) |
| `POST /api/processes/diff` | Prozesse/Workflows | (im Traffic leer) |
| `POST /api/reminders/rest/diff` | Erinnerungen/Fristen | `description`, `documentId`, `startDate`, `done` |
| `POST /api/settings/rest/query` | Nutzereinstellungen | `type`, `value`, `valueType`, `priority` |
| `GET /api/actions/<action-key>` | benannte Aktionsgruppen (semantischer Key, **keine** opake ID — belegt: `companies-with-actions`) | `rows` |

### 4.9 Sharing-Endpunkte (öffentliche Freigaben, kein Login nötig)

| Endpunkt | Zweck |
|---|---|
| `GET /api/shares/get/:token/:id/pdf?mode=<download\|print>` | PDF eines öffentlich geteilten Dokuments (gleiche `mode`-Werte wie §4.1) |
| `GET /api/v1/sharing/:token/:pageId?size=<smedium\|medium>` | Seiten-Bild einer öffentlichen Freigabe (gleiche `size`-Werte wie §4.1) |

`:token` ist der Freigabe-Token aus einem Share-Link — kein Session-Cookie nötig, da öffentlich
zugänglich (analog Pangolin-Freigabelinks im HomeLab-Kontext, nur fileee-intern).

---

## 5. Dokument-Metadaten (`attributes.data`) → Paperless-Mapping

Jeder Wert in `attributes.data` ist ein **typisierter Wrapper**, nicht der nackte Wert:

- **Einfaches Attribut** (z. B. `title`, `invoiceId`, `senderId`, `receiverId`, `read`,
  `reviewed`, `contentLanguage`, `documentTypeId`, `invoiceDate`, `issueDate`,
  `preventAutoTitle`, `acceptedAttributes`-Elemente): `{ value, modified, source, type }` —
  `source` = Herkunft/Confidence (OCR vs. manuell), `modified` = Zeitstempel, `type` = Typname.
- **Listen-Attribut** (z. B. `tagIds`, `reminderIds`, `acceptedAttributes`, bei Firmen: `ibans`,
  `vatIds`, `emails`, `phoneNumbers`, `websites`, `germanTaxIds`, `deutschePostCodes`,
  `iataAirlineCodes`, `uicTicketVendorCodes`): zusätzlich `containedType` (Element-Typname).
- **Enum-Attribut** (z. B. `autoProcessingStatus`): `{ value, enumClassName, modified, type }`
  (statt `source`).
- **Gruppen-Attribut** (z. B. `amount`, `bankAccount1`): `{ attributeGroup, data: {...}, modified,
  source, type }` — `data` trägt **rekursiv** dieselbe Wrapper-Struktur (`amount.data` =
  `{currency, value}`, `bankAccount1.data` = `{iban, bic, bank, account_holder}`).

Für `go-fileee`: ein generischer `Attribute<T>`-Wrapper mit `Value/Source/Modified/Type` (+
`ContainedType`/`EnumClassName`/`AttributeGroup` je Variante).

Beobachtete Schlüssel:

| `attributes.data`-Schlüssel | Bedeutung | → Paperless |
|---|---|---|
| `acceptedAttributes` | Liste akzeptierter Auto-Attribute | — |
| `amount` / `grossIncome` / `netIncome` | Beträge `{value,currency}` | Custom-Field |
| `autoProcessingStatus` | Enum-Status der Auto-Verarbeitung | — |
| `bankAccount1` | `{iban,bic,bank,account_holder}` | Custom-Field(s) |
| `contentLanguage` | Sprache (OCR-Sprache) | — |
| `customerId` | Kundennummer | Custom-Field |
| `documentTypeId` | Verweis auf Document-Type | **Document-Type** |
| `invoiceDate` / `issueDate` | Rechnungs-/Ausstellungsdatum | **Dokumentdatum** |
| `invoiceDueDate` | Fälligkeit | Custom-Field |
| `invoiceId` | Rechnungsnummer | Custom-Field |
| `maxPageNr` / `totalPageCount` | Seitenzahl | — |
| `payed` | bezahlt-Flag | Custom-Field/Tag |
| `paymentReference` | Verwendungszweck | Custom-Field |
| `read` / `reviewed` / `secured` | Status-Flags | ggf. Tags |
| `receiverId` | Empfänger (meist eigene User-ID) | (i. d. R. man selbst) |
| `senderId` | Absender (Company/Contact-ID) | **Correspondent** |
| `tagIds` | Liste Tag-IDs | **Tags** |
| `title` | Dokumenttitel | **Titel** |

---

## 6. Enums & Konstanten

Alle Werte (außer PDF-`mode`/Bild-`size`, die aus Call-Sites stammen) sind aus dem
Kotlin/JS-kompilierten Core-SDK belegt — echte Enum-`values()`-Listen aus dem Bytecode, kein
Rateergebnis.

### 6.1 `PublicDocumentStatus` (`io.fileee.shared.enums.PublicDocumentStatus`) — der reale `status`-Wert von Dokumenten

```
UPLOADING, IP, OCR, ANALYSING, CLASSIFIED, DONE,
DELETED, DELETED_PERMANENTLY, ERROR, LOCAL, NEW
```

11 Werte. Verwendungs-Logik: Seitenbild wird bereits ab **`ANALYSING`** angezeigt (nicht erst
nach `DONE`); `UPLOADING`/`IP`/`NEW`/`ERROR` zeigen einen Platzhalter. `NEW` ist der lokale
Optimistic-UI-Zustand direkt nach Upload-Start (§4.1), bevor die Server-Antwort da ist.

**Abgrenzung zu `IdentStatus`:** `IdentStatus` (`NEW, IN_PROCESS, COMPLETED, FAILED`) ist ein
**Identitätsprüfungs-Status** (KYC/Ident-Flow, vermutlich PostIdent-artige Verifizierung) — eine
**andere, unabhängige** Enum. Beide beginnen mit `NEW`, sind aber nicht zu verwechseln:
`PublicDocumentStatus` beschreibt den Dokument-Verarbeitungsstatus, `IdentStatus` einen separaten
Ident-Vorgang.

### 6.2 `Operator` (`io.fileee.shared.storage.query.Operator`) — vollständige Query-DSL

```
AFTER, BEFORE, BIGGER_EQUAL, BIGGER, EQ, NEQ, LIKE, FUZZY,
SMALLER_EQUAL, SMALLER, HAS, HAS_ANY, HAS_NONE, NOT_IN, IN,
HAS_ALL, EXISTS, DOES_NOT_EXIST, OR, AND, HAS_ELEMENTS
```

21 Werte. Aus Live-Traffic tatsächlich beobachtet (Teilmenge): `EQ`, `IN`, `NEQ`, `OR`.

### 6.3 `DocumentAction` (`io.fileee.shared.enums.DocumentAction`) — UI-Aktionen auf Dokumenten

```
MERGE, DELETE, SPLIT, DOWNLOAD, SHARE, EXPORT, EXTRACT_PAGES, EDIT,
EDIT_REMINDER, EDIT_TAGS, ROTATE_PAGES, DELETE_PAGES, REORDER_PAGES, VIEW, REVISION_LOCK
```

15 Werte. Bekannte REST-Pfade für einen Teil davon: `DELETE` → §4.1 (`DELETE .../rest/:id` oder
Soft-Delete §4.2), `REVISION_LOCK` → §4.1 (`.../revision-lock`), `EXPORT`/`DOWNLOAD` → §4.1
(`/pdf`, `/original`, Bulk-`/download`), `SHARE` → §4.9. Die REST-Pfade für `MERGE`, `SPLIT`,
`EXTRACT_PAGES`, `ROTATE_PAGES`, `DELETE_PAGES`, `REORDER_PAGES`, `EDIT_REMINDER`, `EDIT_TAGS`
sind **⚠️ nicht einzeln nachverfolgt** (§9) — vermutlich weitere `documents/rest/...`-Unterpfade
analog zu `zip`/`multi-edit`/`revision-lock`.

### 6.4 `ContactType` (`io.fileee.shared.domain.dtos.ContactType`)

```
ME, COMPANY, PERSON
```

### 6.5 `ContactStatus` (`io.fileee.shared.domain.dtos.ContactStatus`)

```
MANAGED, LINKED, CUSTOM
```

### 6.6 `CrudOperation` — generisches CRUD-Enum (u. a. für SSE-Push-Events, §7)

```
CREATE, READ, UPDATE, DELETE
```

### 6.7 HTTP-Methoden-Enum (Core-SDK-HTTP-Client)

```
GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS
```

7 Werte — bestätigt, dass der zugrunde liegende HTTP-Client-Layer alle 7 Standard-Methoden kennt,
auch wenn der beobachtete Web-App-Traffic nur `GET`/`POST`/`PUT` zeigte.

### 6.8 PDF `mode` (Call-Sites, kein Enum-Klassenname gefunden, aber vollständig belegt)

```
download, print
```

### 6.9 Bild `size` (benannte Konstanten in `imageHelper.js`)

```
smedium (Default Thumbnail), medium (Default große Ansicht)
```

### 6.10 `serializeInformation.type` (Query-DSL-Typdiskriminator, §3.2)

```
Boolean, Enum, List, String
```

---

## 7. Push (Server-Sent-Events)

### `GET /push/sse/:id?subscription=<...>&keepAlive=<...>`

`text/event-stream` — Echtzeit-Benachrichtigung über Änderungen, Alternative zu Polling.

**Wichtige Korrektur gegenüber der ursprünglichen Annahme:** Das `:id` im Pfad ist **keine**
server-vergebene Subscription-ID, sondern eine **client-generierte Zufalls-UUID**
(`getRandomUUID()`) — reine Verbindungs-Kennung, keine Server-Ressource.

**Vollständige Client-Implementierung (Core-SDK, `startEventSourceListener`):**

```js
let eventSource = null, clientId = getRandomUUID();
function startEventSourceListener() {
  const params = new URLSearchParams();
  params.append("keepAlive", "10");
  let subscriptionBuilder = PushSubscriptionBuilder.builder()
    .subscribeType(EntityType.SERVER_PROCESS, SerializationFormat.KMM);
  getEntityTypesMigratedToKotlin().forEach(t => subscriptionBuilder.subscribeType(t, SerializationFormat.KMM));
  params.append("subscription", subscriptionBuilder.build().queryString);

  const url = `/push/sse/${clientId}?${params}`;
  eventSource = new EventSource(url, {withCredentials: true});
  eventSource.addEventListener("open", ...);
  eventSource.addEventListener("keepAlive", ...);
  eventSource.onmessage = (msg) => {
    if (msg.data === ":" || msg.data.startsWith(":")) return;  // Kommentar/Heartbeat-Zeilen ignorieren
    getCacheClient().getRequestClient().handlePushEvent(msg.data);
  };
}
```

**`subscription`-Parameter:** ein serialisierter Deskriptor (`PushSubscriptionBuilder`), der pro
`EntityType` (u. a. `SERVER_PROCESS` sowie alle „nach Kotlin migrierten" Entitätstypen) im Format
`SerializationFormat.KMM` anmeldet, welche Änderungen der Client empfangen möchte. Die
**vollständige Wortliste der `EntityType`-Enum** ist ⚠️ nicht vertieft (§9) — im untersuchten Code
nur `SERVER_PROCESS` als Literal belegt.

**Event-Typen:**

| SSE-Event-Name | Bedeutung |
|---|---|
| `open` | Verbindung aufgebaut (reines Bookkeeping, kein Datenfeld) |
| `keepAlive` | periodischer Ping (Intervall = `keepAlive`-Query-Param) |
| *(default) `message`* | eigentliche Push-Nutzlast, siehe Payload-Schema unten |

**Payload-Schema (aus `handlePushEvent`, JSON-String im `message`-Event):**

```json
{
  "operation": "CREATE | READ | UPDATE | DELETE",
  "entityType": "<EntityType, z. B. Document, Company, ServerProcess>",
  "dtoPayload": { "...": "volles Entity-Objekt, gleiche Form wie GET-Response" },
  "json": "<Roh-JSON-String, Fallback wenn dtoPayload nicht deserialisierbar ist>"
}
```

`operation` nutzt das generische `CrudOperation`-Enum (§6.6). Bei `DELETE`-Events kann
`dtoPayload` fehlen — der Client fällt dann auf einen Spezialpfad zurück
(`handlePushDeleteEntityWithSerializationIssue`), der die Entity nur anhand `entityType` + der im
`json`-Rohstring enthaltenen ID aus dem lokalen Cache entfernt. Kommentar-/Heartbeat-Zeilen im
SSE-Stream beginnen mit `:` und werden ignoriert (Standard-SSE-Konvention).

---

## 8. Sicherheits-/Secret-Hinweise (verbindlich)

- **Credentials:** Username, Passwort, **TOTP-Seed** gehören in Vaultwarden (Item „Fileee API"),
  nie in Code/Repo. Die Lib liest sie via bestehende Vault-Integration.
- **Session-Cookie** ist ein Secret — persistierte Cookie-Jar mit Dateirechten `600`, nie loggen.
- **`x-xsrf-token`**, Cookie-Werte, PDF-/OCR-Inhalte (`GET /api/pages/:id`), Dokument-Metadaten =
  **PII** → nie in Logs/Ausgaben/Commits.
- **Rate-Limit-Disziplin:** Token-Bucket mit konservativem Default, exponentielles Backoff bei
  `429`/`5xx` **und** bei `secondsBlocked > 0` im JSON-Error-Body (das Feld ist ein generisches
  Muster, nicht nur bei `user-session` — siehe §9). `/diff` statt Voll-Reload für Sync nutzen.
  Keine Last-/Fuzz-Tests gegen die echte Infra.
- Dieses Dokument enthält bewusst **keine** echten Werte — nur Struktur (Pfade, Feldnamen,
  Enum-Werte, Code-Logik).

---

## 9. Verifikations-Status — was noch offen ist

Der überwiegende Teil der API ist jetzt entweder durch Live-Traffic **oder** durch den
tatsächlich ausgeführten Anwendungscode belegt. Die zuvor offenen Punkte **(a) Cookies/Session**
und **(c) Token-Herkunft** wurden am 2026-07-23 per **Browser-Cookie-Inspektion** geschlossen
(§2.11) — sie stehen unten nur noch als **erledigt** markiert. **Die verbleibenden Punkte
(b, d–h)** lassen sich strukturell weder aus HAR noch aus JS-Code klären — sie erfordern einen
kontrollierten, lesenden Live-Check gegen das **eigene** Test-Konto (siehe
`secret-environment-awareness.md`, keine destruktiven Tests):

| # | Punkt | Status / Warum |
|---|---|---|
| a | Exakte Cookie-Namen + Rollen + Session-Lebensdauer | ✅ **GELÖST (Browser-Session 2026-07-23, §2.11):** Session=`JSESSIONID` (JWT, httpOnly), CSRF=`XSRF-TOKEN` (nicht httpOnly), + `webappjetty` (LB, Path `/api`) / `userId`; Cookies langlebig (~1 Jahr). Exakte `Max-Age`/`SameSite` für die Lib irrelevant (Jar spiegelt `Set-Cookie`). |
| b | Server-Fehlercode bei `version`-Konflikt (`PUT` mit veralteter `version`) | 🔴 offen — Client-Code sieht einen automatischen Retry vor, spezifiziert aber den erwarteten HTTP-Status/`apiError`-Wert nicht sichtbar im untersuchten Bundle |
| c | Herkunft/Lebensdauer des `token` bei `POST /api/f/token/login` | ✅ **GELÖST (Browser-Session 2026-07-23):** Es ist das langlebige **`rememberMe`-JWT-Cookie** → persistenter headless-Re-Auth ohne erneutes Passwort+TOTP (§2.6). |
| d | Exakter HTTP-Status bei aktivem Rate-Limit (429 vs. 200 mit Error-Body) | Code liest `secondsBlocked` generisch aus `error.response.data` (deckt beide Fälle ab); kein tatsächlich ausgelöster Sperr-Fall in den 3 HARs beobachtet |
| e | Vollständige Wortliste der `EntityType`-Enum (SSE-`subscription`, Push-Event `entityType`) | im untersuchten Code-Ausschnitt nur `SERVER_PROCESS` als Literal belegt; vollständige Liste würde eine gezielte Suche nach der `EntityType`-Klasse im selben Bundle erfordern |
| f | REST-Pfade der übrigen `DocumentAction`-Werte (`MERGE`, `SPLIT`, `EXTRACT_PAGES`, `ROTATE_PAGES`, `DELETE_PAGES`, `REORDER_PAGES`, `EDIT_REMINDER`, `EDIT_TAGS`) | Enum-Werte selbst sind belegt, dazugehörige Endpunkte wurden im Rahmen der bisherigen Analyse nicht einzeln nachverfolgt |
| g | Vollständiges `i18nDictionary`-Typ-Universum jenseits der belegten Beispielfelder | Schema-Definitionen sind serverseitig dynamisch, nicht im Frontend-Code hartkodiert |
| h | `foreign-accounts`-Feldstruktur | Ressource war in allen bisher untersuchten Konten leer — keine Felder beobachtbar |

**Das ist die vollständige Restliste.** Alles andere in diesem Dokument gilt als belegt (Traffic
oder Code) und muss vor der Implementierung **nicht** erneut recherchiert werden. Verifikation der
obigen Punkte erfolgt kontrolliert gegen das eigene (Test-)Konto — read-only, keine destruktiven
Tests.

---

## Anhang: Vollständige Endpunkt-Liste (56)

```
# Auth / Session (10)
GET    /api/f/start                              Startup-Bootstrap-Ping (204)
POST   /api/f/existent                           Konto-/2FA-Check
GET    /api/f/exists                             Konto-Existenz, GET-Variante
POST   /api/f/login                              Passwort+TOTP-Login -> Cookie
POST   /api/f/token/login                        Token-Login (Web-App leicht / Core-SDK Device-Flow)
POST   /api/f/logout                             Session beenden
GET    /api/f/user-session                       Session + Profil
GET    /api/f/account-status                     Abo/Lizenz
GET    /api/callback/openid-connect/google       OIDC-Callback (Browser-Flow)
GET    /api/callback/openid/legacy               OIDC-Callback (Browser-Flow, legacy)

# Dokumente (16)
POST   /api/documents/rest                       Upload (multipart)
GET    /api/documents/rest/:id                   Einzeldokument
PUT    /api/documents/rest/:id                   Aendern (Optimistic Locking ueber version)
DELETE /api/documents/rest/:id                   Hart loeschen (code-belegt, UI nutzt Soft-Delete)
POST   /api/documents/rest/diff                  Liste/Sync
POST   /api/documents/rest/:id/revision-lock     Aufbewahrungssperre setzen
POST   /api/documents/rest/multi-edit            Bulk-Edit ueber Query-Filter
POST   /api/documents/rest/zip                   ZIP-Export-Job starten
GET    /api/documents/rest/zip/:jobId            ZIP-Job-Status abfragen
DELETE /api/documents/rest/zip/:jobId            ZIP-Job abbrechen
GET    /api/v1/documents/:id/pdf                 Original-PDF (mode=download|print)
GET    /api/v1/documents/:id/original            Original-Datei ohne PDF-Wrapping
GET    /api/v1/documents/download                Bulk-Download (?documents=id1,id2,...)
GET    /api/pages/:id                            Seiten-OCR (Bounding-Boxes)
GET    /api/v1/pages/:id/image                   Seiten-JPEG (size=smedium|medium)
GET    /api/v1/documents/:id/pages/:pageId/image Seiten-JPEG, alternative Route

# Papierkorb / Soft-Delete (3)
POST   /api/deleted-documents/list                       Papierkorb-Liste
DELETE /api/deleted-documents/:id/delete-permanently     Einzeln endgueltig loeschen
DELETE /api/deleted-documents/delete-permanently-all      Papierkorb leeren

# Fileee-Boxes (3)
POST   /api/fileeeboxes/:boxId/:documentId       Dokument zu Box hinzufuegen
DELETE /api/fileeeboxes/:boxId/:documentId       Dokument aus Box entfernen
POST   /api/fileeeboxes/delete                   Box loeschen

# Sharing / oeffentliche Freigaben (2)
GET    /api/shares/get/:token/:id/pdf            PDF einer oeffentlichen Freigabe
GET    /api/v1/sharing/:token/:pageId            Seiten-Bild einer oeffentlichen Freigabe

# Tags (2)
POST   /api/tags/rest/diff                       Tags-Sync
GET    /api/tags/rest/:id                        Tag

# Companies (5)
POST   /api/companies/rest/diff                  Firmen-Sync
GET    /api/companies/rest/:id                   Firma
GET    /api/v1/companies/:id/logo/HD             Firmen-Logo
POST   /api/companies/rest/:id/main-contact      Haupt-Ansprechpartner setzen
DELETE /api/companies/:id/logo                   Logo loeschen (auch /api/:id/logo Profil-Variante)

# Contacts (3)
POST   /api/contacts/rest                        Kontakt anlegen
GET    /api/contacts/rest/:id                    Kontakt
PUT    /api/contacts/rest/:id                    Kontakt aendern (Muster analog Dokumente)

# Document-Types & Schemes (2)
POST   /api/document-types/rest/query            Dokumenttyp-Werte
POST   /api/document-type-schemes/rest/query     Dokumenttyp-Schemata (Feld-Constraints)

# Weitere Sync-Ressourcen (7)
POST   /api/conversations/rest/diff              Konversationen
POST   /api/feature-licenses/rest/query          Lizenz-/Kontingent-Tracking
POST   /api/foreign-accounts/rest/query          externe verbundene Konten
POST   /api/processes/diff                       Prozesse
POST   /api/reminders/rest/diff                  Erinnerungen
POST   /api/settings/rest/query                  Einstellungen
GET    /api/actions/<action-key>                 benannte Aktionsgruppen (z. B. companies-with-actions)

# Push (1)
GET    /push/sse/:id                             Live-Push (SSE, :id = client-generierte UUID)

# Generisch (2)
POST   /api/:id/rest/query                       generischer Query
POST   /api/:id/rest/diff                        generischer Diff
```

**Zählung:** 10 (Auth) + 16 (Dokumente) + 3 (Papierkorb) + 3 (Fileee-Boxes) + 2 (Sharing) +
2 (Tags) + 5 (Companies) + 3 (Contacts) + 2 (Document-Types) + 7 (Weitere) + 1 (Push) +
2 (Generisch) = **56 Endpunkte**.

---

## Referenzen

| Dokument | Pfad |
|---|---|
| JS-Bundle-Analyse (Code-Evidenz für alle „NEU"/„GELÖST"-Markierungen) | `docs/research/2026-07-23-fileee-api-js-analysis.md` |
| Skill | `.claude/skills/fileee/SKILL.md` |
| Endpunkt-Referenz (Skill, komprimiert) | `.claude/skills/fileee/references/api-endpoints.md` |
| Troubleshooting (Skill) | `.claude/skills/fileee/references/troubleshooting.md` |
| Secret-Safety-Regel | `.claude/rules/secret-safe-config-inspection.md` |
| Environment-Bewusstsein (Live-Verifikation) | `.claude/rules/secret-environment-awareness.md` |

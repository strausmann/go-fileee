//go:build integration

package fileee

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestIntegrationLiveFileee ist die kontrollierte Live-Verifikation gegen das echte
// my.fileee.com-API (Umbrella-Spec §10.3, Skill "Fileee-Infra schonen"). Läuft NICHT in der
// normalen CI (Build-Tag "integration", siehe .github/workflows/test.yml, das diesen Tag nicht
// setzt) — nur manuell gegen ein dediziertes Test-Konto.
//
// Voraussetzungen (Env-Vars, NIE im Klartext committen/loggen):
//
//	FILEEE_USERNAME   E-Mail des Test-Kontos
//	FILEEE_PASSWORD   Passwort des Test-Kontos
//	FILEEE_TOTP_SEED  Base32-TOTP-Seed, falls 2FA aktiv ist — leer, wenn das Konto kein 2FA hat
//	                  (currentTOTP liefert dann bewusst einen leeren Code, siehe auth.go)
//
// Schonender Betrieb (ADR-0005): EIN Testlauf, der eingebaute Rate-Limiter der Lib bleibt aktiv
// (kein WithRateLimit-Override), read-first vor dem einzigen Test-Write (§10.3 E).
func TestIntegrationLiveFileee(t *testing.T) {
	username := os.Getenv("FILEEE_USERNAME")
	password := os.Getenv("FILEEE_PASSWORD")
	totpSeed := os.Getenv("FILEEE_TOTP_SEED")
	if username == "" || password == "" {
		t.Skip("FILEEE_USERNAME/FILEEE_PASSWORD nicht gesetzt — Live-Integrationstest übersprungen")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Eigener, isolierter Session-Store im Test-Tempdir — berührt keinen produktiven Session-Cache.
	store := NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))

	client, err := New(Credentials{Username: username, Password: password, TOTPSeed: totpSeed}, WithSessionStore(store))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// --- Schritt 1: Login/Session (belegt zugleich den kompletten Auth-Handshake der Lib live) ---
	if err := client.EnsureSession(ctx); err != nil {
		t.Fatalf("EnsureSession (Login-Handshake): %v", err)
	}
	us, err := client.auth.userSession(ctx)
	if err != nil {
		t.Fatalf("user-session Probe nach Login: %v", err)
	}
	if !us.Authorized {
		t.Fatalf("user-session meldet authorized=false nach erfolgreichem EnsureSession — unerwartet")
	}
	t.Logf("Schritt 1 (Login/Session): authorized=%v secondsBlocked=%.0f", us.Authorized, us.SecondsBlocked)
	if us.SecondsBlocked > 0 {
		t.Fatalf("Konto ist aktuell rate-limited (secondsBlocked=%.0f) — Testlauf abgebrochen, kein Retry ohne Backoff (Skill 'Fileee-Infra schonen')", us.SecondsBlocked)
	}

	// --- §10.3 C: localResults-Delta (Documents.Diff, leerer vs. befüllter Cursor) ---
	t.Run("PunktC_localResults", func(t *testing.T) {
		cursorEmpty := NewCursor("Document")
		resultEmpty, err := client.Documents.Diff(ctx, cursorEmpty)
		if err != nil {
			t.Fatalf("Documents.Diff (leerer Cursor): %v", err)
		}
		t.Logf("Befund C: Diff mit leerem Cursor -> rows=%d totalRows=%d", len(resultEmpty.Rows), resultEmpty.TotalRows)

		if len(resultEmpty.Rows) == 0 {
			t.Log("Befund C: Testkonto hat keine Dokumente — localResults-Delta nicht verifizierbar, übersprungen")
			return
		}

		resultFilled, err := client.Documents.Diff(ctx, resultEmpty.NextCursor)
		if err != nil {
			t.Fatalf("Documents.Diff (befüllter Cursor): %v", err)
		}
		t.Logf("Befund C: Diff mit befülltem Cursor -> rows=%d totalRows=%d", len(resultFilled.Rows), resultFilled.TotalRows)

		if len(resultFilled.Rows) < len(resultEmpty.Rows) {
			t.Log("Befund C: Server honoriert localResults — befüllter Cursor liefert weniger rows (Delta-Sync funktioniert wie angenommen)")
		} else {
			t.Log("Befund C: befüllter Cursor liefert NICHT weniger rows als der leere — angenommene localResults-Wirkung nicht bestätigt, weiter untersuchen")
		}
	})

	// --- §10.3 D: DocumentType.Diff — direkter Endpunkt-Status vs. Query-Fallback ---
	t.Run("PunktD_DocumentTypeDiff", func(t *testing.T) {
		rawBody, err := json.Marshal(diffRequestWire{
			Criteria:     []criterionWire{},
			SortOrder:    []sortOrderWire{},
			LocalResults: buildLocalResults(NewCursor("DocumentType")),
			Limit:        defaultPageLimit,
		})
		if err != nil {
			t.Fatalf("diff request encode: %v", err)
		}
		resp, err := client.postJSON(ctx, "/api/document-types/rest/diff", rawBody)
		if err != nil {
			t.Fatalf("roher POST /api/document-types/rest/diff: %v", err)
		}
		_, _ = io.ReadAll(resp.Body) // Body-Inhalt fuer diesen Befund irrelevant, nur Status zaehlt
		resp.Body.Close()
		t.Logf("Befund D: POST /api/document-types/rest/diff roher Status = %d", resp.StatusCode)
		switch resp.StatusCode {
		case http.StatusOK:
			t.Log("Befund D: direkter diff-Endpunkt existiert (200) — kein Query-Fallback nötig")
		case http.StatusNotFound, http.StatusMethodNotAllowed:
			t.Log("Befund D: diff-Endpunkt existiert nicht (404/405) — Query-Fallback in documentTypeService.Diff greift wie in der Umbrella-Spec angenommen")
		default:
			t.Logf("Befund D: unerwarteter Status %d — weder 200 noch 404/405, Annahme nicht bestätigt, weiter untersuchen", resp.StatusCode)
		}

		// Der öffentliche Diff()-Wrapper muss unabhängig vom Pfad (direkt oder Fallback) erfolgreich sein.
		result, err := client.DocumentTypes.Diff(ctx, NewCursor("DocumentType"))
		if err != nil {
			t.Fatalf("DocumentTypes.Diff (öffentlicher Wrapper): %v", err)
		}
		t.Logf("Befund D: DocumentTypes.Diff (öffentlicher Wrapper) erfolgreich -> rows=%d totalRows=%d", len(result.Rows), result.TotalRows)
	})

	// --- §10.3 E (primär): Contact Query/Diff (read) + genau EIN Test-Kontakt Create/Update ---
	t.Run("PunktE_ContactCRUD", func(t *testing.T) {
		// a) Read: existieren die Endpunkte ueberhaupt?
		queryResult, errQuery := client.Contacts.Query(ctx, QueryOptions{Limit: 10})
		if errQuery != nil {
			t.Logf("Befund E (Read/Query): Contacts.Query fehlgeschlagen: %v", errQuery)
		} else {
			t.Logf("Befund E (Read/Query): Contacts.Query erfolgreich -> rows=%d totalRows=%d", len(queryResult.Rows), queryResult.TotalRows)
		}

		diffResult, errDiff := client.Contacts.Diff(ctx, NewCursor("Contact"))
		if errDiff != nil {
			t.Logf("Befund E (Read/Diff): Contacts.Diff fehlgeschlagen: %v", errDiff)
		} else {
			t.Logf("Befund E (Read/Diff): Contacts.Diff erfolgreich -> rows=%d totalRows=%d", len(diffResult.Rows), diffResult.TotalRows)
		}

		// b) Write: genau EIN, klar als Test markierter Kontakt (kein echter Personenbezug).
		testContact := &Contact{
			CompanyName: "ZZZ-gofileee-livecheck",
			FirstName:   "ZZZ-gofileee",
			LastName:    "livecheck",
			ContactType: ContactTypePerson,
		}

		created, errCreate := client.Contacts.Create(ctx, testContact)
		if errCreate != nil {
			t.Fatalf("Befund E (Write/Create): Contacts.Create fehlgeschlagen: %v — kein Test-Kontakt angelegt, kein Cleanup nötig", errCreate)
		}
		t.Logf("Befund E (Write/Create): Contact angelegt, id gesetzt=%v", created.ID != "")
		t.Logf("Befund E (Write/Create, x-unverified-Punkt): firstName im Response gesetzt=%v lastName im Response gesetzt=%v", created.FirstName != "", created.LastName != "")

		cleanedUp := false
		defer func() {
			if cleanedUp || created.ID == "" {
				return
			}
			req, errReq := http.NewRequestWithContext(ctx, http.MethodDelete, client.baseURL+"/api/contacts/rest/"+created.ID, nil)
			if errReq != nil {
				t.Logf("Cleanup: DELETE-Request konnte nicht gebaut werden: %v — Test-Kontakt bleibt bestehen, manuelles Aufräumen nötig", errReq)
				return
			}
			resp, errDo := client.httpClient.Do(req)
			if errDo != nil {
				t.Logf("Cleanup: DELETE fehlgeschlagen (Netzwerk): %v — Test-Kontakt ZZZ-gofileee-livecheck bleibt bestehen, manuelles Aufräumen nötig", errDo)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
				t.Log("Cleanup: Befund E — hartes DELETE auf Contact erfolgreich, Test-Kontakt entfernt")
				cleanedUp = true
				return
			}
			t.Logf("Cleanup: Befund E — DELETE lieferte Status %d, Löschen nicht möglich/nicht unterstützt — Test-Kontakt ZZZ-gofileee-livecheck bleibt bestehen, manuelles Aufräumen im Testkonto nötig", resp.StatusCode)
		}()

		// c) Update: ein Feld ändern, PUT-Pfad verifizieren.
		created.Email = "zzz-gofileee-livecheck@example.invalid"
		updated, errUpdate := client.Contacts.Update(ctx, created)
		if errUpdate != nil {
			t.Logf("Befund E (Write/Update): Contacts.Update fehlgeschlagen: %v", errUpdate)
			return
		}
		t.Logf("Befund E (Write/Update): Contacts.Update erfolgreich, Email im Response aktualisiert=%v", updated.Email == created.Email)
	})
}

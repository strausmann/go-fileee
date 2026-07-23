//go:build integration

package fileee

import (
	"bytes"
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

	// 6 Minuten Gesamtbudget: Login + Punkt C/D/E (schnell) + Punkt F (Upload, Analyse-Polling bis
	// zu 3 Min, Downloads, Query, Cleanup) teilen sich diesen einen Kontext.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
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

	// --- Punkt F: kompletter Dokument-Lebenszyklus (Upload -> Analyse-Polling -> Metadaten ->
	// Download PDF/Seiten-Bild -> Query -> hartes Löschen). Nutzt eine erfundene Test-Rechnung
	// (testdata/rechnung-livecheck.pdf, Quelle testdata/rechnung-livecheck.html) — Kernfrage: welche
	// attributes.data-Felder extrahiert Fileees Auto-Analyse aus einer Rechnung (API.md §5)?
	t.Run("PunktF_DokumentLebenszyklus", func(t *testing.T) {
		pdfBytes, err := os.ReadFile(filepath.Join("testdata", "rechnung-livecheck.pdf"))
		if err != nil {
			t.Fatalf("Befund F: Test-PDF lesen fehlgeschlagen: %v", err)
		}

		// a) Upload
		uploadResult, errUpload := client.Documents.Upload(ctx, bytes.NewReader(pdfBytes), UploadMetadata{
			Title: "ZZZ-gofileee-livecheck-Rechnung",
		})
		if errUpload != nil {
			t.Fatalf("Befund F (Upload): Documents.Upload fehlgeschlagen: %v — kein Dokument angelegt, kein Cleanup nötig", errUpload)
		}
		docID := uploadResult.Document.ID
		t.Logf("Befund F (Upload): id gesetzt=%v pages=%d initialer status=%s isDuplicate=%v",
			docID != "", len(uploadResult.Document.Pages), uploadResult.Document.Status, uploadResult.IsDuplicate)
		if uploadResult.IsDuplicate {
			t.Log("Befund F (Upload): Server meldet Duplicate — erwartet war ein frisches Dokument, weiter untersuchen")
		}

		cleanedUp := false
		defer func() {
			if cleanedUp || docID == "" {
				return
			}
			req, errReq := http.NewRequestWithContext(ctx, http.MethodDelete, client.baseURL+"/api/documents/rest/"+docID, nil)
			if errReq != nil {
				t.Logf("Cleanup F: DELETE-Request konnte nicht gebaut werden: %v — Test-Dokument ZZZ-gofileee-livecheck-Rechnung bleibt bestehen, manuelles Aufräumen nötig", errReq)
				return
			}
			resp, errDo := client.httpClient.Do(req)
			if errDo != nil {
				t.Logf("Cleanup F: DELETE fehlgeschlagen (Netzwerk): %v — Test-Dokument bleibt bestehen, manuelles Aufräumen nötig", errDo)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
				t.Log("Cleanup F: hartes DELETE auf Document erfolgreich, Test-Dokument entfernt")
				cleanedUp = true
				return
			}
			t.Logf("Cleanup F: DELETE lieferte Status %d — hartes Löschen nicht möglich/nicht unterstützt (API.md §4.1 „vor produktivem Einsatz verifizieren“ bestätigt sich hier ggf.), Test-Dokument ZZZ-gofileee-livecheck-Rechnung bleibt bestehen, manuelles Aufräumen im Testkonto nötig", resp.StatusCode)
		}()

		// b) Auf Fileees Analyse warten — schonendes Polling (Skill "Fileee-Infra schonen"):
		// ~12s-Intervall, Timeout ~3 Min, kein Hämmern.
		const pollInterval = 12 * time.Second
		const pollTimeout = 3 * time.Minute
		deadline := time.Now().Add(pollTimeout)
		statusHistory := []PublicDocumentStatus{uploadResult.Document.Status}
		lastStatus := uploadResult.Document.Status
		var final *Document
	pollLoop:
		for {
			doc, errGet := client.Documents.Get(ctx, docID)
			if errGet != nil {
				t.Logf("Befund F (Polling): Documents.Get fehlgeschlagen: %v", errGet)
				break pollLoop
			}
			final = doc
			if doc.Status != lastStatus {
				statusHistory = append(statusHistory, doc.Status)
				lastStatus = doc.Status
			}
			switch doc.Status {
			case StatusDone, StatusClassified, StatusError:
				break pollLoop
			}
			if time.Now().After(deadline) {
				t.Logf("Befund F (Polling): Timeout (%s) erreicht, letzter Status=%s — Analyse nicht abgeschlossen, arbeite mit Zwischenstand weiter", pollTimeout, doc.Status)
				break pollLoop
			}
			select {
			case <-ctx.Done():
				t.Logf("Befund F (Polling): Kontext beendet: %v", ctx.Err())
				break pollLoop
			case <-time.After(pollInterval):
			}
		}
		t.Logf("Befund F (Status-Verlauf): %v", statusHistory)
		if final == nil {
			t.Fatal("Befund F: kein finales Dokument-Objekt geladen — Documents.Get war nie erfolgreich")
		}

		// c) Extrahierte attributes.data-Metadaten — Kernfrage dieses Laufs (API.md §5).
		attrs := final.Attributes
		t.Logf("Befund F (Metadaten) title: gesetzt=%v wert=%q", attrs.Title != "", attrs.Title)
		t.Logf("Befund F (Metadaten) documentTypeId: gesetzt=%v wert=%q", attrs.DocumentTypeID != "", attrs.DocumentTypeID)
		t.Logf("Befund F (Metadaten) invoiceId: gesetzt=%v wert=%q", attrs.InvoiceID != "", attrs.InvoiceID)
		if attrs.InvoiceDate != nil {
			t.Logf("Befund F (Metadaten) invoiceDate: gesetzt=true wert=%s", attrs.InvoiceDate.Format("2006-01-02"))
		} else {
			t.Log("Befund F (Metadaten) invoiceDate: gesetzt=false")
		}
		if attrs.InvoiceDueDate != nil {
			t.Logf("Befund F (Metadaten) invoiceDueDate: gesetzt=true wert=%s", attrs.InvoiceDueDate.Format("2006-01-02"))
		} else {
			t.Log("Befund F (Metadaten) invoiceDueDate: gesetzt=false")
		}
		if attrs.Amount != nil {
			t.Logf("Befund F (Metadaten) amount: gesetzt=true wert=%.2f %s", attrs.Amount.Value, attrs.Amount.Currency)
		} else {
			t.Log("Befund F (Metadaten) amount: gesetzt=false")
		}
		t.Logf("Befund F (Metadaten) senderId: gesetzt=%v wert=%q", attrs.SenderID != "", attrs.SenderID)
		t.Logf("Befund F (Metadaten) customerId: gesetzt=%v wert=%q", attrs.CustomerID != "", attrs.CustomerID)
		if attrs.BankAccount1 != nil {
			t.Logf("Befund F (Metadaten) bankAccount1: gesetzt=true iban=%q bic=%q bank=%q accountHolder=%q",
				attrs.BankAccount1.IBAN, attrs.BankAccount1.BIC, attrs.BankAccount1.Bank, attrs.BankAccount1.AccountHolder)
		} else {
			t.Log("Befund F (Metadaten) bankAccount1: gesetzt=false")
		}
		t.Logf("Befund F (Metadaten) paymentReference: gesetzt=%v wert=%q", attrs.PaymentReference != "", attrs.PaymentReference)
		if len(attrs.RawExtra) > 0 {
			keys := make([]string, 0, len(attrs.RawExtra))
			for k := range attrs.RawExtra {
				keys = append(keys, k)
			}
			t.Logf("Befund F (Metadaten) weitere, bisher unbekannte attributes.data-Schlüssel (RawExtra): %v", keys)
		}

		// d) Download PDF (primärer Export-Weg, API.md §4.1)
		if pdfReader, errPDF := client.Documents.DownloadPDF(ctx, docID, PDFModeDownload); errPDF != nil {
			t.Logf("Befund F (DownloadPDF): fehlgeschlagen: %v", errPDF)
		} else {
			downloaded, errRead := io.ReadAll(pdfReader)
			pdfReader.Close()
			if errRead != nil {
				t.Logf("Befund F (DownloadPDF): Lesen fehlgeschlagen: %v", errRead)
			} else {
				isPDF := len(downloaded) > 4 && string(downloaded[:4]) == "%PDF"
				t.Logf("Befund F (DownloadPDF): bytes=%d isPDF(MagicBytes)=%v", len(downloaded), isPDF)
			}
		}

		// e) Download Seiten-Bild (Fallback-Weg, API.md §4.1) — nur wenn pages[] vorhanden
		if len(final.Pages) > 0 {
			page := final.Pages[0]
			if imgReader, errImg := client.Documents.DownloadPageImage(ctx, page.ID, ImageSizeMedium, int64(page.ImageVersion)); errImg != nil {
				t.Logf("Befund F (DownloadPageImage): fehlgeschlagen: %v", errImg)
			} else {
				imgBytes, errRead := io.ReadAll(imgReader)
				imgReader.Close()
				if errRead != nil {
					t.Logf("Befund F (DownloadPageImage): Lesen fehlgeschlagen: %v", errRead)
				} else {
					isJPEG := len(imgBytes) > 2 && imgBytes[0] == 0xFF && imgBytes[1] == 0xD8
					t.Logf("Befund F (DownloadPageImage): bytes=%d isJPEG(MagicBytes)=%v", len(imgBytes), isJPEG)
				}
			}
		} else {
			t.Log("Befund F (DownloadPageImage): Dokument hat kein pages[] — übersprungen")
		}

		// f) Query (Gegenstück zu Diff, API.md §3)
		if queryResult, errQuery := client.Documents.Query(ctx, QueryOptions{Limit: 10}); errQuery != nil {
			t.Logf("Befund F (Query): Documents.Query fehlgeschlagen: %v", errQuery)
		} else {
			t.Logf("Befund F (Query): Documents.Query erfolgreich -> rows=%d totalRows=%d", len(queryResult.Rows), queryResult.TotalRows)
		}

		// g) Hartes Löschen läuft über das oben registrierte defer (Cleanup F).
	})
}

//go:build integration

package fileee

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sort"
	"testing"
	"time"
)

// TestIntegrationFullSweep übt die GESAMTE öffentliche Lib-API einmal gegen ein echtes Fileee-Konto.
// Läuft NICHT in CI (Build-Tag "integration"), nur manuell:
//
//	set -a; . deine-creds.env; set +a
//	go test -tags integration -run TestIntegrationFullSweep -v -timeout 15m ./fileee/
//
// Env-Vars: FILEEE_USERNAME, FILEEE_PASSWORD, FILEEE_TOTP_SEED (leer ohne 2FA).
//
// Regeln (verbindlich): Es werden KEINE bestehenden Daten verändert oder gelöscht — nur selbst
// angelegte Testobjekte (Präfix "ZZZ-gofileee-") und die werden am Ende abgeräumt. Der Upload nutzt
// eindeutigen Inhalt, damit die Server-Duplikaterkennung nie auf ein bestehendes Dokument zeigt.
// Es werden AUSSCHLIESSLICH strukturelle Werte geloggt (Zähler, HTTP-Status, generische Schema-IDs/
// Feld-Keys) — NIE Titel, Namen, Beträge oder andere personenbezogene Inhalte.
// revision-lock wird bewusst nicht getestet (ADR-0007). Schonend: konservatives Rate-Limit + Pausen.
func TestIntegrationFullSweep(t *testing.T) {
	user := os.Getenv("FILEEE_USERNAME")
	if user == "" || os.Getenv("FILEEE_PASSWORD") == "" {
		t.Skip("FILEEE_USERNAME/FILEEE_PASSWORD nicht gesetzt")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	c, err := New(Credentials{Username: user, Password: os.Getenv("FILEEE_PASSWORD"), TOTPSeed: os.Getenv("FILEEE_TOTP_SEED")},
		WithRateLimit(0.5, 1))
	if err != nil {
		t.Fatal(err)
	}
	pause := func() { time.Sleep(1500 * time.Millisecond) }

	var testDocID, testContactID, testReminderID, boxID string
	defer func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer ccancel()
		del := func(label, path string) {
			resp, err := c.deleteReq(cctx, path)
			if err != nil {
				t.Logf("cleanup %s: FEHLER %v", label, err)
				return
			}
			resp.Body.Close()
			t.Logf("cleanup %s -> %d", label, resp.StatusCode)
		}
		if testReminderID != "" {
			del("reminder", "/api/reminders/rest/"+testReminderID)
		}
		if testContactID != "" {
			del("contact", "/api/contacts/rest/"+testContactID)
		}
		if testDocID != "" {
			del("document", "/api/documents/rest/"+testDocID)
		}
	}()

	// 1. Auth/Session (inkl. TOTP)
	if err := c.EnsureSession(ctx); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if as, err := c.AccountStatus(ctx); err != nil {
		t.Errorf("AccountStatus: %v", err)
	} else {
		t.Logf("1. AccountStatus: problem=%s", as.Problem)
	}
	pause()

	// 2. Read-only Sweep (nur Zähler)
	if r, err := c.Documents.Search(ctx, "a", SearchOptions{Limit: 5}); err != nil {
		t.Errorf("Search: %v", err)
	} else {
		t.Logf("2. Search -> %d Treffer (total %d)", len(r.IDs), r.TotalRows)
	}
	pause()
	for _, rs := range []struct {
		name string
		fn   func() (int, error)
	}{
		{"documents", func() (int, error) { r, e := c.Documents.Diff(ctx, NewCursor("Document")); return count(r, e) }},
		{"tags", func() (int, error) { r, e := c.Tags.Diff(ctx, NewCursor("Tag")); return count(r, e) }},
		{"companies", func() (int, error) { r, e := c.Companies.Diff(ctx, NewCursor("Company")); return count(r, e) }},
		{"contacts", func() (int, error) { r, e := c.Contacts.Diff(ctx, NewCursor("Contact")); return count(r, e) }},
		{"reminders", func() (int, error) { r, e := c.Reminders.Diff(ctx, NewCursor("Reminder")); return count(r, e) }},
	} {
		if n, err := rs.fn(); err != nil {
			t.Errorf("%s: %v", rs.name, err)
		} else {
			t.Logf("   %s total=%d", rs.name, n)
		}
		pause()
	}

	// 2b. Alle Dokumenttypen inkl. Felder — nur generische Schema-IDs/Feld-Keys, keine Anzeigenamen
	dt, errT := c.DocumentTypes.Query(ctx, QueryOptions{Limit: 50})
	pause()
	schemes, errS := c.DocumentTypeSchemes.Query(ctx, QueryOptions{Limit: 50})
	if errT != nil || errS != nil {
		t.Errorf("DocumentTypes/Schemes: %v / %v", errT, errS)
	} else {
		byID := map[string][]string{}
		for _, s := range schemes.Rows {
			keys := make([]string, 0, len(s.Fields()))
			for _, f := range s.Fields() {
				keys = append(keys, f.Key)
			}
			sort.Strings(keys)
			byID[s.ID] = keys
		}
		t.Logf("2b. Dokumenttypen=%d Schemata=%d", len(dt.Rows), len(schemes.Rows))
		for _, d := range dt.Rows {
			t.Logf("    %-14s Felder=%v", d.ID, byID[d.DocumentTypeScheme])
		}
	}
	pause()

	// 3. Boxen (nur Nummer + Dokumentzahl, KEINE Namen)
	boxes, err := c.Boxes.List(ctx)
	if err != nil {
		t.Errorf("Boxes.List: %v", err)
	} else {
		t.Logf("3. Boxen=%d", len(boxes))
		if len(boxes) > 0 {
			boxID = boxes[0].ID
			pause()
			if b, err := c.Boxes.Get(ctx, boxID); err != nil {
				t.Errorf("Boxes.Get: %v", err)
			} else {
				t.Logf("   Box boxNr=%d docs=%d", b.BoxNr, len(b.Documents))
			}
		}
	}
	pause()

	// 4. Upload eines eindeutigen Testdokuments
	pdf, err := os.ReadFile("testdata/rechnung-livecheck.pdf")
	if err != nil {
		t.Fatalf("Test-PDF: %v", err)
	}
	unique := append(append([]byte{}, pdf...), []byte("\n%zzz-gofileee-"+time.Now().Format("20060102T150405.000000000")+"\n")...)
	up, err := c.Documents.Upload(ctx, bytes.NewReader(unique), UploadMetadata{Title: "ZZZ-gofileee-integration.pdf"})
	if errors.Is(err, ErrDuplicateDocument) {
		t.Fatalf("Upload unerwartet Duplikat (id=%s) — Abbruch, kein bestehendes Dokument berühren", up.Document.ID)
	}
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	testDocID = up.Document.ID
	t.Log("4. Upload -> neues Dokument")
	pause()

	// 5. Doc-Operationen auf dem Testdokument
	doc, err := c.Documents.Get(ctx, testDocID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	t.Logf("5. Get status=%s pages=%d", doc.Status, len(doc.Pages))
	pause()

	doc.Attributes.Title = "ZZZ-gofileee-integration-edited"
	if _, err := c.Documents.Update(ctx, doc); err != nil {
		t.Errorf("Update: %v", err)
	} else {
		t.Log("   Update -> ok")
	}
	pause()

	if rc, err := c.Documents.DownloadPDF(ctx, testDocID, PDFModeDownload); err != nil {
		t.Errorf("DownloadPDF: %v", err)
	} else {
		n, _ := rc.Read(make([]byte, 64))
		rc.Close()
		t.Logf("   DownloadPDF -> %d Byte", n)
	}
	pause()

	var pageID string
	var imgVer int64
	for i := 0; i < 6; i++ {
		d, err := c.Documents.Get(ctx, testDocID)
		if err == nil && len(d.Pages) > 0 {
			pageID, imgVer = d.Pages[0].ID, int64(d.Pages[0].ImageVersion)
			break
		}
		time.Sleep(5 * time.Second)
	}
	if pageID != "" {
		if rc, err := c.Documents.DownloadPageImage(ctx, pageID, ImageSizeMedium, imgVer); err != nil {
			t.Errorf("DownloadPageImage: %v", err)
		} else {
			n, _ := rc.Read(make([]byte, 64))
			rc.Close()
			t.Logf("   DownloadPageImage -> %d Byte", n)
		}
	} else {
		t.Log("   DownloadPageImage übersprungen (keine Seite)")
	}
	pause()

	if sh, err := c.Documents.Share(ctx, []string{testDocID}); err != nil {
		t.Errorf("Share: %v", err)
	} else {
		t.Logf("   Share -> shareId=%v", sh.ShareID != "")
		pause()
		if err := c.Documents.Unshare(ctx, testDocID); err != nil {
			t.Errorf("Unshare: %v", err)
		} else {
			t.Log("   Unshare -> ok")
		}
	}
	pause()

	// 6. Box: Testdoc einheften + entfernen
	if boxID != "" {
		if err := c.Boxes.AddDocument(ctx, boxID, testDocID); err != nil {
			t.Errorf("AddDocument: %v", err)
		} else {
			t.Log("6. Boxes.AddDocument -> ok")
		}
		pause()
		if err := c.Boxes.RemoveDocument(ctx, boxID, testDocID); err != nil {
			t.Errorf("RemoveDocument: %v", err)
		} else {
			t.Log("   Boxes.RemoveDocument -> ok")
		}
		pause()
	}

	// 7. Reminder
	if rem, err := c.Reminders.Create(ctx, &Reminder{
		Description: "ZZZ-gofileee", DocumentID: testDocID,
		StartDate: time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	}); err != nil {
		t.Errorf("Reminders.Create: %v", err)
	} else {
		testReminderID = rem.ID
		t.Log("7. Reminders.Create -> ok")
	}
	pause()

	// 8. Kontakt anlegen/lesen/ändern
	if cont, err := c.Contacts.Create(ctx, &Contact{
		FirstName: "ZZZ", LastName: "gofileee-integration", CompanyName: "",
		ContactType: ContactTypePerson, Email: "zzz@example.invalid",
	}); err != nil {
		t.Errorf("Contacts.Create: %v", err)
	} else {
		testContactID = cont.ID
		t.Logf("8. Contacts.Create -> type=%s", cont.ContactType)
		pause()
		if g, err := c.Contacts.Get(ctx, testContactID); err != nil {
			t.Errorf("Contacts.Get: %v", err)
		} else {
			g.URL = "https://example.invalid/x"
			pause()
			if _, err := c.Contacts.Update(ctx, g); err != nil {
				t.Errorf("Contacts.Update: %v", err)
			} else {
				t.Log("   Contacts.Get+Update -> ok")
			}
		}
	}
	pause()

	// 9. ZIP-Export (nur Testdoc) + Prozess-Polling
	if proc, err := c.Documents.ExportZIP(ctx, []string{testDocID}, "ZZZ-pw"); err != nil {
		t.Errorf("ExportZIP: %v", err)
	} else {
		t.Logf("9. ExportZIP -> status=%s", proc.Status)
		for i := 0; i < 3 && proc.ID != ""; i++ {
			time.Sleep(3 * time.Second)
			p, err := c.Processes.Get(ctx, proc.ID)
			if err != nil {
				t.Errorf("Processes.Get: %v", err)
				break
			}
			t.Logf("   Processes.Get poll %d status=%s", i+1, p.Status)
			if p.Status != ProcessStatusWaiting && p.Status != ProcessStatusRunning {
				break
			}
		}
	}

	t.Log("── FullSweep fertig; Cleanup via defer ──")
}

// count reduziert ein DiffResult auf TotalRows (Helfer, hält die Testtabelle knapp).
func count[T any](r *DiffResult[T], err error) (int, error) {
	if err != nil {
		return 0, err
	}
	return r.TotalRows, nil
}

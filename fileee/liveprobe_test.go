//go:build liveprobe

package fileee

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveProbe_FullSweep testet die GESAMTE öffentliche Lib-API einmal gegen das PRODUKTIVE Konto.
// Regeln: keine BESTEHENDEN Daten löschen; selbst angelegte Testdaten werden am Ende abgeräumt.
// Schonend: konservatives Rate-Limit + Pausen. revision-lock wird bewusst NICHT getestet (ADR-0007).
func TestLiveProbe_FullSweep(t *testing.T) {
	u := os.Getenv("FILEEE_USERNAME")
	if u == "" {
		t.Skip("keine creds")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// Gentle: ~1 Request/2s, Burst 1.
	c, err := New(Credentials{Username: u, Password: os.Getenv("FILEEE_PASSWORD"), TOTPSeed: os.Getenv("FILEEE_TOTP_SEED")},
		WithRateLimit(0.5, 1))
	if err != nil {
		t.Fatal(err)
	}
	pause := func() { time.Sleep(1500 * time.Millisecond) }

	// Cleanup-Sammler: IDs von SELBST angelegten Objekten.
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

	// ── 1. Auth/Session ──
	if err := c.EnsureSession(ctx); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if as, err := c.AccountStatus(ctx); err != nil {
		t.Errorf("AccountStatus: %v", err)
	} else {
		t.Logf("1. AccountStatus: type=%s problem=%s", as.AccountTypeID, as.Problem)
	}
	pause()

	// ── 2. Read-only Sweep ──
	if r, err := c.Documents.Search(ctx, "Rechnung", SearchOptions{Limit: 5}); err != nil {
		t.Errorf("Search: %v", err)
	} else {
		t.Logf("2. Search 'Rechnung' -> %d Treffer (total %d)", len(r.IDs), r.TotalRows)
	}
	pause()
	if d, err := c.Documents.Diff(ctx, NewCursor("Document")); err != nil {
		t.Errorf("Documents.Diff: %v", err)
	} else {
		t.Logf("   Documents.Diff -> %d rows, total %d", len(d.Rows), d.TotalRows)
	}
	pause()
	for _, rs := range []struct {
		name string
		fn   func() (int, error)
	}{
		{"Tags", func() (int, error) {
			r, e := c.Tags.Diff(ctx, NewCursor("Tag"))
			if e != nil {
				return 0, e
			}
			return r.TotalRows, nil
		}},
		{"Companies", func() (int, error) {
			r, e := c.Companies.Diff(ctx, NewCursor("Company"))
			if e != nil {
				return 0, e
			}
			return r.TotalRows, nil
		}},
		{"Contacts", func() (int, error) {
			r, e := c.Contacts.Diff(ctx, NewCursor("Contact"))
			if e != nil {
				return 0, e
			}
			return r.TotalRows, nil
		}},
		{"Reminders", func() (int, error) {
			r, e := c.Reminders.Diff(ctx, NewCursor("Reminder"))
			if e != nil {
				return 0, e
			}
			return r.TotalRows, nil
		}},
		{"DocumentTypes", func() (int, error) {
			r, e := c.DocumentTypes.Query(ctx, QueryOptions{Limit: 50})
			if e != nil {
				return 0, e
			}
			return r.TotalRows, nil
		}},
	} {
		if n, err := rs.fn(); err != nil {
			t.Errorf("%s: %v", rs.name, err)
		} else {
			t.Logf("   %s -> total %d", rs.name, n)
		}
		pause()
	}

	// ── 2b. Alle Dokumenttypen enumerieren ──
	if dt, err := c.DocumentTypes.Query(ctx, QueryOptions{Limit: 50}); err != nil {
		t.Errorf("DocumentTypes enum: %v", err)
	} else {
		names := make([]string, 0, len(dt.Rows))
		for _, d := range dt.Rows {
			names = append(names, d.ID+"="+d.I18NName)
		}
		t.Logf("2b. Dokumenttypen (%d): %v", len(dt.Rows), names)
	}
	pause()

	// ── 3. Boxen (read) ──
	boxes, err := c.Boxes.List(ctx)
	if err != nil {
		t.Errorf("Boxes.List: %v", err)
	} else {
		t.Logf("3. Boxes.List -> %d Boxen", len(boxes))
		if len(boxes) > 0 {
			boxID = boxes[0].ID
			pause()
			if b, err := c.Boxes.Get(ctx, boxID); err != nil {
				t.Errorf("Boxes.Get: %v", err)
			} else {
				t.Logf("   Boxes.Get boxNr=%d docs=%d", b.BoxNr, len(b.Documents))
			}
		}
	}
	pause()

	// ── 4. Upload eines Testdokuments ──
	pdf, err := os.ReadFile("testdata/rechnung-livecheck.pdf")
	if err != nil {
		t.Fatalf("Test-PDF lesen: %v", err)
	}
	up, err := c.Documents.Upload(ctx, bytes.NewReader(pdf), UploadMetadata{Title: "ZZZ-gofileee-livesweep.pdf"})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	testDocID = up.Document.ID
	t.Logf("4. Upload -> id=%s isDuplicate=%v", testDocID, up.IsDuplicate)
	pause()

	// ── 5. Doc-Operationen auf dem Testdokument ──
	doc, err := c.Documents.Get(ctx, testDocID)
	if err != nil {
		t.Fatalf("Get testDoc: %v", err)
	}
	t.Logf("5. Get testDoc status=%s pages=%d", doc.Status, len(doc.Pages))
	pause()

	// Update: Titel des MEINEN Testdokuments ändern (vom Modell unterstützt)
	doc.Attributes.Title = "ZZZ-gofileee-livesweep-edited"
	if _, err := c.Documents.Update(ctx, doc); err != nil {
		t.Errorf("Update: %v", err)
	} else {
		t.Log("   Update (title) -> ok")
	}
	pause()

	// DownloadPDF
	if rc, err := c.Documents.DownloadPDF(ctx, testDocID, PDFModeDownload); err != nil {
		t.Errorf("DownloadPDF: %v", err)
	} else {
		n, _ := rc.Read(make([]byte, 256))
		rc.Close()
		t.Logf("   DownloadPDF -> %d Byte gelesen", n)
	}
	pause()

	// Auf Analyse warten (pages), dann DownloadPageImage — max ~6 gentle Polls
	var pageID string
	var imgVer int64
	for i := 0; i < 6; i++ {
		d, err := c.Documents.Get(ctx, testDocID)
		if err == nil && len(d.Pages) > 0 {
			pageID = d.Pages[0].ID
			imgVer = int64(d.Pages[0].ImageVersion)
			t.Logf("   Analyse fertig nach %d Polls (status=%s)", i+1, d.Status)
			break
		}
		time.Sleep(5 * time.Second)
	}
	if pageID != "" {
		if rc, err := c.Documents.DownloadPageImage(ctx, pageID, ImageSizeMedium, imgVer); err != nil {
			t.Errorf("DownloadPageImage: %v", err)
		} else {
			n, _ := rc.Read(make([]byte, 256))
			rc.Close()
			t.Logf("   DownloadPageImage -> %d Byte gelesen", n)
		}
	} else {
		t.Log("   DownloadPageImage übersprungen (noch keine Seite verfügbar)")
	}
	pause()

	// Share / Unshare
	if sh, err := c.Documents.Share(ctx, []string{testDocID}); err != nil {
		t.Errorf("Share: %v", err)
	} else {
		t.Logf("   Share -> shareId gesetzt=%v", sh.ShareID != "")
		pause()
		if err := c.Documents.Unshare(ctx, testDocID); err != nil {
			t.Errorf("Unshare: %v", err)
		} else {
			t.Log("   Unshare -> ok")
		}
	}
	pause()

	// ── 6. Box: Testdoc einheften + wieder entfernen ──
	if boxID != "" {
		if err := c.Boxes.AddDocument(ctx, boxID, testDocID); err != nil {
			t.Errorf("Boxes.AddDocument: %v", err)
		} else {
			t.Log("6. Boxes.AddDocument -> ok")
		}
		pause()
		if err := c.Boxes.RemoveDocument(ctx, boxID, testDocID); err != nil {
			t.Errorf("Boxes.RemoveDocument: %v", err)
		} else {
			t.Log("   Boxes.RemoveDocument -> ok")
		}
		pause()
	}

	// ── 7. Reminder auf dem Testdokument ──
	if rem, err := c.Reminders.Create(ctx, &Reminder{
		Description: "ZZZ-gofileee live-sweep", DocumentID: testDocID,
		StartDate: time.Now().AddDate(0, 1, 0).Format("2006-01-02"),
	}); err != nil {
		t.Errorf("Reminders.Create: %v", err)
	} else {
		testReminderID = rem.ID
		t.Logf("7. Reminders.Create -> id gesetzt=%v", rem.ID != "")
	}
	pause()

	// ── 8. Kontakt anlegen + lesen + ändern ──
	cont, err := c.Contacts.Create(ctx, &Contact{
		FirstName: "ZZZ", LastName: "gofileee-sweep", CompanyName: "",
		ContactType: ContactTypePerson, Email: "zzz-sweep@example.invalid",
	})
	if err != nil {
		t.Errorf("Contacts.Create: %v", err)
	} else {
		testContactID = cont.ID
		t.Logf("8. Contacts.Create -> type=%s", cont.ContactType)
		pause()
		if g, err := c.Contacts.Get(ctx, testContactID); err != nil {
			t.Errorf("Contacts.Get: %v", err)
		} else {
			g.URL = "https://example.invalid/sweep"
			pause()
			if _, err := c.Contacts.Update(ctx, g); err != nil {
				t.Errorf("Contacts.Update: %v", err)
			} else {
				t.Log("   Contacts.Get+Update -> ok")
			}
		}
	}
	pause()

	// ── 9. ZIP-Export (nur Testdoc) + Prozess-Polling ──
	if proc, err := c.Documents.ExportZIP(ctx, []string{testDocID}, "ZZZ-sweep-pw"); err != nil {
		t.Errorf("ExportZIP: %v", err)
	} else {
		t.Logf("9. ExportZIP -> process id gesetzt=%v status=%s", proc.ID != "", proc.Status)
		for i := 0; i < 3 && proc.ID != ""; i++ {
			time.Sleep(3 * time.Second)
			p, err := c.Processes.Get(ctx, proc.ID)
			if err != nil {
				t.Errorf("Processes.Get: %v", err)
				break
			}
			t.Logf("   Processes.Get poll %d -> status=%s", i+1, p.Status)
			if p.Status != ProcessStatusWaiting && p.Status != ProcessStatusRunning {
				break
			}
		}
	}

	t.Log("── FullSweep fertig; Cleanup läuft über defer ──")
}

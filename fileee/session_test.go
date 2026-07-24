package fileee

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileSessionStoreLoadOhneDateiLiefertNilOhneFehler(t *testing.T) {
	store := NewFileSessionStore(filepath.Join(t.TempDir(), "nicht-vorhanden", "session.json"))
	sess, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if sess != nil {
		t.Fatalf("erwartet nil-Session bei fehlender Datei, bekommen %+v", sess)
	}
}

func TestFileSessionStoreSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fileee", "session.json")
	store := NewFileSessionStore(path)
	in := &Session{
		Cookies: []*http.Cookie{
			{Name: "JSESSIONID", Value: "test-session-value"},
			{Name: "rememberMe", Value: "test-remember-value"},
		},
		SavedAt: time.Now().Truncate(time.Second),
	}
	if err := store.Save(context.Background(), in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("Dateirechte = %v, erwartet 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp-Datei %s.tmp hätte nach erfolgreichem Save nicht mehr existieren dürfen", path)
	}

	out, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out == nil || len(out.Cookies) != 2 {
		t.Fatalf("Roundtrip verlor Cookies: %+v", out)
	}
	if out.Cookies[0].Value != "test-session-value" && out.Cookies[1].Value != "test-session-value" {
		t.Fatalf("JSESSIONID-Wert nach Roundtrip nicht wiedergefunden: %+v", out.Cookies)
	}
}

func TestFileSessionStoreLoadKaputtesJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte("nicht-json"), 0o600); err != nil {
		t.Fatalf("Setup WriteFile: %v", err)
	}
	store := NewFileSessionStore(path)
	_, err := store.Load(context.Background())
	if err == nil {
		t.Fatalf("erwartet Fehler bei kaputtem JSON, bekommen nil")
	}
}

// TestFileSessionStoreSaveVerzeichniskomponenteIstDatei erzwingt einen Save-Fehler
// privilegien-unabhängig: statt eines 0500-Verzeichnisses (root umgeht DAC-Checks und würde
// diesen Test in einem root-basierten CI-Container grün durchlaufen lassen, obwohl Save dort
// tatsächlich fehlschlagen sollte) liegt hier eine reguläre Datei an der Stelle, an der Save
// ein Verzeichnis erwartet. os.MkdirAll scheitert daran für jeden User — auch root — weil eine
// Nicht-Verzeichnis-Komponente im Zielpfad liegt.
func TestFileSessionStoreSaveVerzeichniskomponenteIstDatei(t *testing.T) {
	base := t.TempDir()
	afile := filepath.Join(base, "afile")
	if err := os.WriteFile(afile, []byte("keine Verzeichnis-Datei"), 0o600); err != nil {
		t.Fatalf("Setup WriteFile: %v", err)
	}

	path := filepath.Join(afile, "session.json")
	store := NewFileSessionStore(path)
	err := store.Save(context.Background(), &Session{SavedAt: time.Now()})
	if err == nil {
		t.Fatalf("erwartet Fehler, weil %q eine Datei statt eines Verzeichnisses ist, bekommen nil", afile)
	}
}

func TestLoadCookiesIntoJar(t *testing.T) {
	jar, err := (&FileSessionStore{}).newTestJar()
	if err != nil {
		t.Fatalf("newTestJar: %v", err)
	}
	cookies := []*http.Cookie{{Name: "XSRF-TOKEN", Value: "test-xsrf"}}
	loadCookiesIntoJar(jar, "https://my.fileee.com", cookies)

	u, _ := parseTestURL("https://my.fileee.com")
	got := jar.Cookies(u)
	if len(got) != 1 || got[0].Value != "test-xsrf" {
		t.Fatalf("Cookie wurde nicht in den Jar übernommen: %+v", got)
	}
}

func (f *FileSessionStore) newTestJar() (http.CookieJar, error) {
	return cookiejar.New(nil)
}

func parseTestURL(raw string) (*url.URL, error) {
	return url.Parse(raw)
}

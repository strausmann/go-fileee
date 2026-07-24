package fileee

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// SessionStore persistiert die Fileee-Session (Cookie-Jar) über Prozessgrenzen hinweg
// (Umbrella-Spec §4.3). Die Lib ist bis auf diese Referenz zustandslos (ADR-0001).
type SessionStore interface {
	Load(ctx context.Context) (*Session, error)
	Save(ctx context.Context, s *Session) error
}

// Session hält die für den nächsten Prozessstart relevanten Cookies (JSESSIONID, rememberMe,
// webappjetty, XSRF-TOKEN — API.md §2.11).
type Session struct {
	Cookies []*http.Cookie `json:"cookies"`
	SavedAt time.Time      `json:"savedAt"`
}

// FileSessionStore ist die mitgelieferte Default-Implementierung: JSON-Datei mit Dateirechten
// 0600, atomarer Write (temp-Datei + rename) — analog zum bewährten Muster aus
// feedback_atomare_cache_writes (HomeLab-Learning, hier für Secret-Dateien übernommen).
type FileSessionStore struct {
	Path string
}

// NewFileSessionStore erzeugt einen SessionStore, der die Session als Datei unter path ablegt.
func NewFileSessionStore(path string) *FileSessionStore {
	return &FileSessionStore{Path: path}
}

// defaultSessionPath approximiert einen XDG-State-Dir mit os.UserCacheDir (Go-stdlib hat kein
// dediziertes XDG_STATE_HOME) — bewusste, dokumentierte Annahme.
func defaultSessionPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "fileee", "session.json")
}

// Load liest die gespeicherte Session; existiert keine Datei, liefert es (nil, nil).
func (f *FileSessionStore) Load(ctx context.Context) (*Session, error) {
	data, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fileee: session file read: %w", err)
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("fileee: session file decode: %w", err)
	}
	return &s, nil
}

// Save schreibt die Session atomar auf die Platte.
func (f *FileSessionStore) Save(ctx context.Context, s *Session) error {
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return fmt.Errorf("fileee: session dir create: %w", err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("fileee: session encode: %w", err)
	}
	// Eindeutige Temp-Datei im Zielverzeichnis, damit parallele Save-Läufe sich nicht überschreiben;
	// atomarer Rename auf den Zielpfad.
	tmpf, err := os.CreateTemp(filepath.Dir(f.Path), ".session-*.tmp")
	if err != nil {
		return fmt.Errorf("fileee: session tmp create: %w", err)
	}
	tmp := tmpf.Name()
	defer os.Remove(tmp) // no-op nach erfolgreichem Rename
	if err := tmpf.Chmod(0o600); err != nil {
		tmpf.Close()
		return fmt.Errorf("fileee: session tmp chmod: %w", err)
	}
	if _, err := tmpf.Write(data); err != nil {
		tmpf.Close()
		return fmt.Errorf("fileee: session tmp write: %w", err)
	}
	if err := tmpf.Close(); err != nil {
		return fmt.Errorf("fileee: session tmp close: %w", err)
	}
	if err := os.Rename(tmp, f.Path); err != nil {
		return fmt.Errorf("fileee: session rename: %w", err)
	}
	return nil
}

// loadCookiesIntoJar übernimmt gespeicherte Cookies in einen frischen Cookie-Jar, damit
// EnsureSession (Task 8) eine geladene Session sofort prüfen kann.
func loadCookiesIntoJar(jar http.CookieJar, baseURL string, cookies []*http.Cookie) {
	if jar == nil || len(cookies) == 0 {
		return
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return
	}
	jar.SetCookies(u, cookies)
}

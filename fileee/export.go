package fileee

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Prozess-Status eines serverseitigen Vorgangs (z. B. ZIP-Export). Waiting/Running sind die beiden
// LIVE belegten Nicht-Terminal-Zustände; der Terminal-Status-String (z. B. beim fertigen ZIP-Export)
// ist noch nicht verifiziert, weshalb IsTerminal bewusst über die Abwesenheit dieser beiden prüft,
// statt einen konkreten "Finished"-Wert zu raten.
const (
	ProcessStatusWaiting = "Waiting"
	ProcessStatusRunning = "Running"
)

// IsTerminal meldet, ob der Vorgang nicht mehr läuft (Status ist weder Waiting noch Running). Ein
// nachgelagerter Terminal-Status-String wird bewusst nicht angenommen — jeder andere Status gilt als
// abgeschlossen (erfolgreich ODER fehlerhaft; DocumentErrors prüfen).
func (p *Process) IsTerminal() bool {
	return p.Status != ProcessStatusWaiting && p.Status != ProcessStatusRunning
}

// WaitOptions steuert das Polling von WaitForProcess.
type WaitOptions struct {
	// Interval ist der Abstand zwischen zwei Statusabfragen. <= 0 verwendet defaultPollInterval.
	Interval time.Duration
}

const defaultPollInterval = 2 * time.Second

// Process ist ein asynchroner serverseitiger Vorgang. Der ZIP-Export ("Alle Dokumente
// herunterladen") läuft als Process vom Typ io.fileee.shared.process.DownloadAllProcess; sein
// Fortschritt wird über ProcessService.Get abgefragt.
type Process struct {
	ID             string            `json:"id"`
	Status         string            `json:"status"`
	Type           string            `json:"type,omitempty"`
	Documents      json.RawMessage   `json:"documents,omitempty"`
	DocumentErrors []json.RawMessage `json:"documentErrors,omitempty"`
	Retryable      bool              `json:"retryable,omitempty"`
	Dismissed      bool              `json:"dismissed,omitempty"`
	Created        string            `json:"created,omitempty"`
	Modified       string            `json:"modified,omitempty"`
	Deleted        bool              `json:"deleted,omitempty"`
}

// ProcessService fragt serverseitige Vorgänge ab.
type ProcessService interface {
	// Get lädt den aktuellen Stand eines Vorgangs; wiederholtes Aufrufen pollt den Fortschritt.
	Get(ctx context.Context, id string) (*Process, error)
}

type processService struct {
	client *Client
}

func newProcessService(c *Client) ProcessService {
	return &processService{client: c}
}

// Get lädt den aktuellen Stand eines Vorgangs.
func (s *processService) Get(ctx context.Context, id string) (*Process, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	resp, err := s.client.get(ctx, "/api/processes/"+id)
	if err != nil {
		return nil, err
	}
	return decodeProcess(resp)
}

// WaitForProcess pollt GET /api/processes/:id, bis der Vorgang terminal ist (IsTerminal) oder der
// Context abläuft, und liefert den finalen Prozess-Stand. Nützlich nach ExportZIP/ExportAll, um auf
// das fertige Ergebnis zu warten. Hinweis: Der konkrete Feldname der ZIP-Download-URL im fertigen
// DownloadAllProcess ist noch nicht verifiziert (der finale Snapshot wurde bisher nicht erfasst) —
// der Aufrufer inspiziert dazu Process.Documents (json.RawMessage) selbst.
func (c *Client) WaitForProcess(ctx context.Context, id string, opts WaitOptions) (*Process, error) {
	interval := opts.Interval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	for {
		proc, err := c.Processes.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if proc.IsTerminal() {
			return proc, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

type zipExportRequest struct {
	DocumentIDs []string `json:"documentIds"`
	ZipPassword string   `json:"zipPassword"`
}

// ExportZIP startet einen passwortgeschützten ZIP-Export der angegebenen Dokumente und liefert den
// zugehörigen Vorgang; sein Fortschritt wird über Client.Processes abgefragt. Eine leere ID-Liste
// exportiert alle Dokumente (siehe ExportAll).
func (s *DocumentService) ExportZIP(ctx context.Context, documentIDs []string, password string) (*Process, error) {
	if err := s.client.EnsureSession(ctx); err != nil {
		return nil, err
	}
	if documentIDs == nil {
		documentIDs = []string{}
	}
	body, err := json.Marshal(zipExportRequest{DocumentIDs: documentIDs, ZipPassword: password})
	if err != nil {
		return nil, fmt.Errorf("fileee: zip export encode: %w", err)
	}
	resp, err := s.client.postJSON(ctx, "/api/documents/rest/zip", body)
	if err != nil {
		return nil, err
	}
	return decodeProcess(resp)
}

// ExportAll startet einen passwortgeschützten ZIP-Export ALLER Dokumente des Kontos.
func (s *DocumentService) ExportAll(ctx context.Context, password string) (*Process, error) {
	return s.ExportZIP(ctx, []string{}, password)
}

func decodeProcess(resp *http.Response) (*Process, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fileee: process read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseAPIError(resp.StatusCode, body)
	}
	var p Process
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("fileee: process decode: %w", err)
	}
	return &p, nil
}

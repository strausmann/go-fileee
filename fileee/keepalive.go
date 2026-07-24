package fileee

import (
	"context"
	"time"
)

// RefreshSession verifiziert die Session sofort (bypass des Freshness-Fensters) und reauthentifiziert
// bei Bedarf; bei Erfolg wird das Freshness-Fenster neu gesetzt. Wird vom Keepalive genutzt, kann
// aber auch direkt aufgerufen werden, um eine warme Session zu erzwingen.
func (c *Client) RefreshSession(ctx context.Context) error {
	return c.auth.ensureSession(ctx, true)
}

// StartKeepAlive startet eine Hintergrund-Goroutine, die alle interval die Session auffrischt
// (RefreshSession) und so warm hält — so zahlt ein eingehender Request nicht die Reauth-Latenz
// (inkl. TOTP) und der user-session-Verify passiert proaktiv statt pro Aufruf. Die zurückgegebene
// stop-Funktion beendet die Goroutine (idempotent). interval<=0 startet keinen Keepalive und liefert
// eine No-op-stop-Funktion.
func (c *Client) StartKeepAlive(ctx context.Context, interval time.Duration) (stop func()) {
	if interval <= 0 {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Fehler werden bewusst verschluckt (nur geloggt via Transport): ein einzelner
				// fehlgeschlagener Ping darf den Keepalive nicht beenden; der nächste Tick versucht
				// es erneut, und ein eingehender Request würde ohnehin über EnsureSession reauthen.
				_ = c.RefreshSession(ctx)
			}
		}
	}()
	return cancel
}

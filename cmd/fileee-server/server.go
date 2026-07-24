package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/strausmann/go-fileee/fileee"
)

// version ist die Server-Versionsangabe, die Huma in der OpenAPI-Info (`info.version`) und
// in der Docs-UI ausweist. Config (config.go) trägt bewusst KEIN eigenes Versionsfeld — die
// Binary-Version ist ein Build-Constant, keine Laufzeit-Einstellung.
const version = "0.1.0"

// Server bündelt die Laufzeitabhängigkeiten von fileee-server: die geladene Konfiguration, den
// bereits gegen Fileee verdrahteten Core-Lib-Client und den strukturierten Logger. Server hält
// darüber hinaus KEINEN eigenen Zustand — alle drei Felder werden einmalig in NewServer gesetzt
// und danach nur gelesen, sodass ein *Server gefahrlos für die gesamte Prozesslaufzeit von
// mehreren Requests gleichzeitig verwendet werden kann.
type Server struct {
	cfg Config
	fc  *fileee.Client
	log *slog.Logger
}

// NewServer baut einen Server aus der geladenen Konfiguration (LoadConfig, Task 1), einem
// bereits gegen Fileee eingerichteten Core-Lib-Client (fileee.New, siehe main.go) und einem
// strukturierten Logger. NewServer selbst führt keinen I/O aus (kein Login, kein
// Datei-/Netzwerkzugriff) — das erledigt main.go beim Boot bzw. der Aufrufer der Fileee-Lib.
func NewServer(cfg Config, fc *fileee.Client, log *slog.Logger) *Server {
	return &Server{cfg: cfg, fc: fc, log: log}
}

// Handler baut den vollständigen HTTP-Handler von fileee-server: einen http.ServeMux mit
// registrierter Huma-API (OpenAPI 3.1 unter /openapi.json und /openapi.yaml, Docs-UI unter
// /docs — siehe newAPI in api.go) samt Domänen-Operationen (Task 7: Dokumente/Seiten/OCR und
// Stammdaten, siehe registerDocumentRoutes/registerEntityRoutes) sowie einer eigenen
// /healthz-Liveness-Route, umschlossen von der Middleware-Kette AccessLog → APITokenAuth. Diese
// Reihenfolge ist bewusst: AccessLog liegt AUSSERHALB von APITokenAuth, damit auch von der
// Auth-Middleware abgelehnte Requests (401) im NGINX-Access-Log landen — CrowdSecs
// http-bruteforce-Szenario wertet genau diese Zeilen aus (siehe accesslog.go-Doku). Das
// Access-Log schreibt nach os.Stdout, getrennt vom App-/Audit-Log (slog, s.log) auf stderr —
// Spec §"Doku-Stand" (Review-Auflösung,
// docs/superpowers/specs/2026-07-24-fileee-server-design.md im homelab-management-Repo).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	// newAPI registriert die Huma-Standardrouten (OpenAPI/Docs) auf mux und liefert die huma.API,
	// über die Task 7 (und Folge-Tasks) ihre Domänen-Operationen registrieren — daher wird der
	// Rückgabewert hier (anders als noch in Task 6) tatsächlich gebraucht und weitergereicht.
	api := newAPI(mux)
	s.registerDocumentRoutes(api)
	s.registerEntityRoutes(api)

	inner := APITokenAuth(s.cfg.APIToken, isAuthExempt(s.cfg), http.Handler(mux))
	return AccessLog(os.Stdout, s.cfg.TrustedProxies, s.cfg.ClientIPHeaders, inner)
}

// handleHealthz beantwortet GET /healthz mit HTTP 200 und einem festen JSON-Body. Der Check
// ist ein reiner Prozess-Lebendigkeits-Check (Liveness) — er löst AUSDRÜCKLICH KEINEN
// Roundtrip gegen die Fileee-API aus. Ein vorübergehend nicht erreichbares Fileee (Wartung,
// Netzwerkproblem, Rate-Limit) darf den fileee-server-Prozess nicht als "unhealthy" erscheinen
// lassen, solange der Go-Prozess selbst läuft und Requests entgegennimmt — analog zur
// Kubernetes-/Docker-Unterscheidung zwischen Liveness- und Readiness-Probe. Ein separater
// Readiness-Check (mit echtem Fileee-Roundtrip) ist bewusst NICHT Teil dieser Route.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// isAuthExempt liefert die exempt-Funktion für APITokenAuth (auth.go, Task 4): /healthz,
// /openapi.json und /openapi.yaml sind IMMER ohne Token erreichbar — Health-Checks/Monitoring
// brauchen eine Liveness-Probe ohne Credential, und die maschinenlesbare API-Beschreibung soll
// sich ohne Secret abrufen lassen (z. B. für Codegenerierung in N8N/CI). /docs ist NUR
// exempt, wenn cfg.DocsPublic gesetzt ist — bei öffentlichem Hosting kann die Doku-UI so
// hinter FILEEE_DOCS_PUBLIC=false gestellt werden (Spec §12/Review-Auflösung „Pangolin ohne
// SSO", docs/superpowers/specs/2026-07-24-fileee-server-design.md im homelab-management-Repo).
func isAuthExempt(cfg Config) func(path string) bool {
	return func(path string) bool {
		switch path {
		case "/healthz", "/openapi.json", "/openapi.yaml":
			return true
		case "/docs":
			return cfg.DocsPublic
		default:
			return false
		}
	}
}

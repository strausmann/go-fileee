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
// bereits gegen Fileee verdrahteten Core-Lib-Client, den credential-losen ShareClient für den
// anonymen Share-Proxy (Task 9) und den strukturierten Logger. Server hält darüber hinaus KEINEN
// eigenen Zustand — alle vier Felder werden einmalig in NewServer gesetzt und danach nur gelesen,
// sodass ein *Server gefahrlos für die gesamte Prozesslaufzeit von mehreren Requests gleichzeitig
// verwendet werden kann.
type Server struct {
	cfg Config
	fc  *fileee.Client
	sc  *fileee.ShareClient
	log *slog.Logger
}

// NewServer baut einen Server aus der geladenen Konfiguration (LoadConfig, Task 1), einem bereits
// gegen Fileee eingerichteten Core-Lib-Client (fileee.New, siehe main.go), einem ebenfalls bereits
// eingerichteten, credential-losen ShareClient (fileee.NewShareClient, siehe main.go) und einem
// strukturierten Logger. sc wird bewusst — analog zu fc — von AUSSEN injiziert statt intern in
// NewServer gebaut: das hält NewServer frei von I/O (kein Login, kein Datei-/Netzwerkzugriff) und
// erlaubt Tests, sc gegen einen eigenen httptest-Mock zu verdrahten (siehe newTestFileeeClient in
// handlers_test.go).
func NewServer(cfg Config, fc *fileee.Client, sc *fileee.ShareClient, log *slog.Logger) *Server {
	return &Server{cfg: cfg, fc: fc, sc: sc, log: log}
}

// Handler baut den vollständigen HTTP-Handler von fileee-server: einen http.ServeMux mit
// registrierter Huma-API (OpenAPI 3.1 unter /openapi.json und /openapi.yaml, Docs-UI unter
// /docs — siehe newAPI in api.go) samt Domänen-Operationen (Task 7: Dokumente/Seiten/OCR und
// Stammdaten, siehe registerDocumentRoutes/registerEntityRoutes; Task 9: anonymer Share-Proxy
// über s.sc, siehe registerShareProxyRoutes; Task 10: Unified Resolver POST /v1/resolve, siehe
// registerResolveRoute; Task 11: Konversationen/Chat/Einladungen, siehe
// registerConversationRoutes; Task 12: bedingte Hard-DELETE-Routen hinter dem
// Destruktiv-Gate, siehe registerDestructiveRoutes) sowie einer eigenen /healthz-Liveness-Route,
// umschlossen von der Middleware-Kette AccessLog → APITokenAuth. Diese
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
	s.registerShareRoutes(api)
	s.registerShareProxyRoutes(api)
	s.registerResolveRoute(api)
	s.registerConversationRoutes(api)

	// Task 12 (Destruktiv-Gate, ADR-0007/ADR-0008): die drei echten Hard-DELETE-Routen
	// (DELETE /v1/documents|contacts|reminders/{id}, handlers_destructive.go) werden NUR
	// registriert, wenn der Operator sie beim Start explizit freigeschaltet hat
	// (FILEEE_ALLOW_DESTRUCTIVE=true, Config.AllowDestructive). Bleibt das Flag false, wird
	// registerDestructiveRoutes gar nicht erst aufgerufen — die Pfade sind dem mux dann für das
	// DELETE-Verb komplett unbekannt (kein register-then-403-Zwischenzustand, siehe
	// registerDestructiveRoutes-Doku).
	if s.cfg.AllowDestructive {
		s.registerDestructiveRoutes(api)
	}

	// uploadSizeLimit deckelt POST /v1/documents auf cfg.MaxUploadBytes (handlers_documents.go) —
	// Huma wendet op.MaxBodyBytes NUR auf den regulären (Nicht-Multipart) Body-Lesepfad an
	// (huma@v2.35.0 huma.go readBody), NICHT auf den Multipart-Formular-Pfad (adapters/humago
	// GetMultipartForm ruft r.ParseMultipartForm ohne Größenlimit auf). Ohne dieses Limit könnte
	// ein Client beliebig viele Bytes senden, bevor ParseMultipartForm überhaupt zurückkehrt.
	limited := uploadSizeLimit(s.cfg.MaxUploadBytes, http.Handler(mux))

	inner := APITokenAuth(s.cfg.APIToken, isAuthExempt(s.cfg), limited)
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

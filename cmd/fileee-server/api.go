package main

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// newAPI konfiguriert eine Huma-API auf mux und liefert sie zurück. huma.DefaultConfig setzt
// die Standardpfade OpenAPIPath "/openapi" (→ Routen /openapi.json und /openapi.yaml) und
// DocsPath "/docs" — humago.New registriert diese Routen selbst auf mux, ein manuelles
// mux.HandleFunc dafür ist weder nötig noch vorgesehen.
//
// BEWUSST KEIN globaler huma.NewError-Override: Der ursprüngliche Task-Brief-Wortlaut
// ("Huma-CreateHooks/Error-Transformer auf mapError verweisen") ist durch die spätere,
// autoritative Review-Auflösung der Design-Spec überholt — §17 "Review-Auflösung" dort gilt
// laut Spec-Präambel ausdrücklich VOR früheren Abschnitten bei Widerspruch: "kein globaler
// huma.NewError-Override, mapError direkt aus Handlern" (docs/superpowers/specs/
// 2026-07-24-fileee-server-design.md im homelab-management-Repo, Abschnitt 17 "API/Code
// (§4)"). Task 5 (errors.go) bestätigt dieselbe Entscheidung explizit für Task 6. Huma behält
// deshalb für SEINE EIGENEN Validierungs-/Decode-Fehler (z. B. Request-Body-Schema-Verstöße)
// das eingebaute ErrorModel (RFC 9457 Problem Details); Domänen-Handler (Task 7ff.) geben
// stattdessen direkt mapError(err) zurück, das den fileee-server-eigenen schlanken
// {error, code}-Body erzeugt (errors.go). Beide Fehlerformen existieren bewusst nebeneinander.
func newAPI(mux *http.ServeMux) huma.API {
	config := huma.DefaultConfig("fileee-server", version)
	return humago.New(mux, config)
}

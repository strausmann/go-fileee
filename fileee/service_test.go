package fileee

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
)

func newTestClientAgainstMock(t *testing.T, routes map[string]mockRoute) *Client {
	t.Helper()
	srv := newMockServer(t, jsonHandler(t, routes))
	client, err := New(
		Credentials{Username: "test@example.invalid", Password: "test-pw"},
		WithBaseURL(srv.URL),
		WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		WithRateLimit(1000, 1000),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

// ensureSessionRoutes liefert die Standard-Routen, die JEDE Service-Methode über EnsureSession
// auslöst (kein rememberMe -> voller Login), damit Testfälle sich auf den eigentlichen Endpunkt
// konzentrieren können.
func ensureSessionRoutes() map[string]mockRoute {
	return map[string]mockRoute{
		"GET /api/f/start":     {Status: 204},
		"POST /api/f/existent": {Status: 200, Body: []byte(`{"existent":true,"twoFactorAuthEnabled":false}`)},
		"POST /api/f/login":    {Status: 200, Body: []byte(`{"loggedIn":true}`), Cookies: []*http.Cookie{{Name: "JSESSIONID", Value: "sess-svc"}}},
	}
}

func mergeRoutes(base map[string]mockRoute, extra map[string]mockRoute) map[string]mockRoute {
	out := map[string]mockRoute{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func TestTagServiceQueryHappyErrorNetwork(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"POST /api/tags/rest/query": {Status: 200, Body: []byte(`{"rows":[{"id":"tag-1","name":"Rechnung"}],"totalRows":1}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		result, err := client.Tags.Query(context.Background(), QueryOptions{})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(result.Rows) != 1 || result.Rows[0].Name != "Rechnung" {
			t.Fatalf("Rows falsch: %+v", result.Rows)
		}
	})

	t.Run("error path 500", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"POST /api/tags/rest/query": {Status: 500, Body: []byte(`{"apiError":"internal"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		_, err := client.Tags.Query(context.Background(), QueryOptions{})
		if err == nil {
			t.Fatalf("erwartet Fehler bei 500, bekommen nil")
		}
	})

	t.Run("network error", func(t *testing.T) {
		client, err := New(
			Credentials{Username: "test@example.invalid", Password: "test-pw"},
			WithBaseURL("http://127.0.0.1:1"),
			WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = client.Tags.Query(context.Background(), QueryOptions{})
		if err == nil {
			t.Fatalf("erwartet Network-Error, bekommen nil")
		}
	})
}

// TestTagServiceGetHappyErrorNetwork deckt restService[T].Get() vollständig ab — bisher war nur
// der 404-Zweig (TestTagServiceGet404LiefertErrNotFound) getestet, der Erfolgspfad (200 ->
// dekodierte Entity) lief nie durch einen Test. Ergänzt Happy-Path, Error-Path (5xx) und
// Network-Error, analog zum Query-Pendant TestTagServiceQueryHappyErrorNetwork.
func TestTagServiceGetHappyErrorNetwork(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"GET /api/tags/rest/tag-1": {Status: 200, Body: []byte(`{"id":"tag-1","name":"Rechnung","colorCode":"#ff0000"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		tag, err := client.Tags.Get(context.Background(), "tag-1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if tag.ID != "tag-1" || tag.Name != "Rechnung" || tag.ColorCode != "#ff0000" {
			t.Fatalf("Get-Ergebnis falsch dekodiert: %+v", tag)
		}
	})

	t.Run("error path 500", func(t *testing.T) {
		routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
			"GET /api/tags/rest/tag-1": {Status: 500, Body: []byte(`{"apiError":"internal"}`)},
		})
		client := newTestClientAgainstMock(t, routes)
		_, err := client.Tags.Get(context.Background(), "tag-1")
		if err == nil {
			t.Fatalf("erwartet Fehler bei 500, bekommen nil")
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("erwartet gewrapptes *APIError, bekommen %T: %v", err, err)
		}
		if apiErr.HTTPStatus != 500 {
			t.Fatalf("erwarteter HTTPStatus 500, bekommen %d", apiErr.HTTPStatus)
		}
	})

	t.Run("network error", func(t *testing.T) {
		client, err := New(
			Credentials{Username: "test@example.invalid", Password: "test-pw"},
			WithBaseURL("http://127.0.0.1:1"),
			WithSessionStore(NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))),
		)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = client.Tags.Get(context.Background(), "tag-1")
		if err == nil {
			t.Fatalf("erwartet Network-Error, bekommen nil")
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("Network-Error darf kein *APIError sein, bekommen %v", apiErr)
		}
	})
}

func TestTagServiceGet404LiefertErrNotFound(t *testing.T) {
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"GET /api/tags/rest/unbekannt": {Status: 404, Body: []byte(`{"errorCode":"NOT_FOUND"}`)},
	})
	client := newTestClientAgainstMock(t, routes)
	_, err := client.Tags.Get(context.Background(), "unbekannt")
	if !errorsIsNotFound(err) {
		t.Fatalf("erwartet ErrNotFound, bekommen %v", err)
	}
}

func TestCompanyServiceDiff(t *testing.T) {
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"POST /api/companies/rest/diff": {Status: 200, Body: []byte(`{"rows":[{"id":"company-1","companyName":"Testfirma"}],"idsToDelete":[],"totalRows":1}`)},
	})
	client := newTestClientAgainstMock(t, routes)
	result, err := client.Companies.Diff(context.Background(), NewCursor("Company"))
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(result.Rows) != 1 || result.NextCursor.Known["company-1"] != 0 {
		t.Fatalf("Diff-Ergebnis falsch: %+v", result)
	}
}

func TestDocumentTypeServiceDiffFaelltAufQueryZurueck(t *testing.T) {
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"POST /api/document-types/rest/diff":  {Status: 404, Body: []byte(`{"errorCode":"NOT_FOUND"}`)},
		"POST /api/document-types/rest/query": {Status: 200, Body: []byte(`{"rows":[{"id":"dt-1","i18NName":"Rechnung","version":1}],"totalRows":1}`)},
	})
	client := newTestClientAgainstMock(t, routes)
	result, err := client.DocumentTypes.Diff(context.Background(), NewCursor("DocumentType"))
	if err != nil {
		t.Fatalf("Diff mit Fallback: %v", err)
	}
	if len(result.Rows) != 1 || result.Rows[0].I18NName != "Rechnung" {
		t.Fatalf("Fallback-Ergebnis falsch: %+v", result)
	}
	if result.NextCursor.Known["dt-1"] != 1 {
		t.Fatalf("Fallback-Cursor falsch aufgebaut: %+v", result.NextCursor.Known)
	}
}

// errorsIsNotFound kapselt errors.Is(err, ErrNotFound) für bessere Lesbarkeit in Testfällen.
func errorsIsNotFound(err error) bool {
	return errorsIs(err, ErrNotFound)
}

// errorsIs ist ein winziger Alias für errors.Is, um den Import-Kopf des Beispiels knapp zu halten.
func errorsIs(err, target error) bool {
	return errors.Is(err, target)
}

package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// infisicalRequiredEnv liefert die Pflicht-Variablen für einen erfolgreichen Infisical-Modus
// (Universal Auth + Env/Path/Domain/ProjectID gesetzt) als Basis für Tests.
func infisicalRequiredEnv() map[string]string {
	return map[string]string{
		"INFISICAL_UNIVERSAL_AUTH_CLIENT_ID":     "cid",
		"INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET": "csecret",
		"INFISICAL_DOMAIN":                       "https://secretsmanager.strausmann.cloud/api",
		"INFISICAL_PROJECT_ID":                   "proj-123",
		"INFISICAL_ENV":                          "prod",
	}
}

// TestWantInfisicalAndDotenv ist der Brief-vorgegebene Ausgangstest: wantInfisical aktiv bei
// gesetzter Universal-Auth-Client-ID, inaktiv bei SECRET_BACKEND=env; parseDotenv zerlegt
// KEY=VALUE-Zeilen, ignoriert Kommentare/Leerzeilen und entfernt umschließende Quotes.
func TestWantInfisicalAndDotenv(t *testing.T) {
	on := map[string]string{"INFISICAL_UNIVERSAL_AUTH_CLIENT_ID": "x"}
	if !wantInfisical(func(k string) string { return on[k] }) {
		t.Fatal("sollte aktiv sein")
	}
	if wantInfisical(func(k string) string { return map[string]string{"SECRET_BACKEND": "env"}[k] }) {
		t.Fatal("SECRET_BACKEND=env darf nicht Infisical wählen")
	}
	got := parseDotenv([]byte("A=1\n# comment\n\nB=\"x y\"\n"))
	if len(got) != 2 || got[0] != "A=1" || got[1] != "B=x y" {
		t.Fatalf("parseDotenv=%v", got)
	}
}

// TestWantInfisical_ExplicitBackend prüft, dass SECRET_BACKEND=infisical auch ohne gesetzte
// Universal-Auth-Client-ID aktiviert (expliziter Override) und dass der Re-Exec-Sentinel
// FILEEE_INFISICAL_REEXEC den Infisical-Modus in jedem Fall unterdrückt.
func TestWantInfisical_ExplicitBackend(t *testing.T) {
	explicit := map[string]string{"SECRET_BACKEND": "infisical"}
	if !wantInfisical(func(k string) string { return explicit[k] }) {
		t.Fatal("SECRET_BACKEND=infisical sollte auch ohne Client-ID aktivieren")
	}

	sentinel := map[string]string{
		"INFISICAL_UNIVERSAL_AUTH_CLIENT_ID": "x",
		"FILEEE_INFISICAL_REEXEC":            "1",
	}
	if wantInfisical(func(k string) string { return sentinel[k] }) {
		t.Fatal("gesetzter Re-Exec-Sentinel muss den Infisical-Modus unterdrücken")
	}

	sentinelExplicit := map[string]string{
		"SECRET_BACKEND":          "infisical",
		"FILEEE_INFISICAL_REEXEC": "1",
	}
	if wantInfisical(func(k string) string { return sentinelExplicit[k] }) {
		t.Fatal("Sentinel muss auch bei explizitem SECRET_BACKEND=infisical unterdrücken")
	}
}

// TestWantInfisical_NeitherSetIsFalse stellt sicher, dass ohne jede Konfiguration
// (weder SECRET_BACKEND noch Universal-Auth-Client-ID gesetzt) der Env-Modus (Default) gilt.
func TestWantInfisical_NeitherSetIsFalse(t *testing.T) {
	if wantInfisical(func(string) string { return "" }) {
		t.Fatal("ohne jede Konfiguration darf der Infisical-Modus nicht aktiv sein")
	}
}

// TestParseDotenv_QuoteAndWhitespaceHandling deckt zusätzliche dotenv-Formen ab: einfache
// Quotes, Werte ohne Quotes mit umgebenden Leerzeichen, sowie eingerückte Kommentarzeilen.
func TestParseDotenv_QuoteAndWhitespaceHandling(t *testing.T) {
	in := []byte("FOO='bar baz'\n  # eingerückter Kommentar\nBAR = 42 \n\nBAZ=\n")
	got := parseDotenv(in)
	want := []string{"FOO=bar baz", "BAR=42", "BAZ="}
	if len(got) != len(want) {
		t.Fatalf("parseDotenv=%v, erwartet %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Zeile %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestParseDotenv_LineWithoutEqualsIsIgnored prüft, dass eine Zeile ohne "=" (weder Kommentar
// noch leer — z. B. ein defektes dotenv-Fragment) übersprungen wird, statt einen leeren oder
// falschen Eintrag zu erzeugen.
func TestParseDotenv_LineWithoutEqualsIsIgnored(t *testing.T) {
	got := parseDotenv([]byte("A=1\nMALFORMED_LINE_NO_EQUALS\nB=2\n"))
	want := []string{"A=1", "B=2"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("parseDotenv=%v, erwartet %v", got, want)
	}
}

// TestMaybeInjectInfisical_NoOpWhenNotWanted prüft, dass im Env-Modus (SECRET_BACKEND=env)
// weder run noch execServer aufgerufen werden — MaybeInjectInfisical ist dann ein reines No-Op.
func TestMaybeInjectInfisical_NoOpWhenNotWanted(t *testing.T) {
	env := map[string]string{"SECRET_BACKEND": "env"}
	getenv := func(k string) string { return env[k] }

	runCalled := false
	execCalled := false
	run := func(name string, args ...string) ([]byte, error) {
		runCalled = true
		return nil, nil
	}
	execServer := func([]string) error {
		execCalled = true
		return nil
	}

	if err := MaybeInjectInfisical(getenv, run, execServer); err != nil {
		t.Fatalf("erwartet nil, bekam %v", err)
	}
	if runCalled || execCalled {
		t.Fatal("No-Op darf weder run noch execServer aufrufen")
	}
}

// TestMaybeInjectInfisical_MissingEnvIsError prüft, dass ein fehlendes INFISICAL_ENV im
// Infisical-Modus einen Fehler liefert, BEVOR run/execServer aufgerufen werden (kein
// stillschweigender Fallback auf den CLI-Default "dev" — secret-environment-awareness).
func TestMaybeInjectInfisical_MissingEnvIsError(t *testing.T) {
	env := infisicalRequiredEnv()
	delete(env, "INFISICAL_ENV")
	getenv := func(k string) string { return env[k] }

	run := func(name string, args ...string) ([]byte, error) {
		t.Fatal("run darf bei fehlendem INFISICAL_ENV nicht aufgerufen werden")
		return nil, nil
	}
	execServer := func([]string) error {
		t.Fatal("execServer darf bei fehlendem INFISICAL_ENV nicht aufgerufen werden")
		return nil
	}

	err := MaybeInjectInfisical(getenv, run, execServer)
	if err == nil {
		t.Fatal("erwartet Fehler bei fehlendem INFISICAL_ENV")
	}
	if !strings.Contains(err.Error(), "INFISICAL_ENV") {
		t.Errorf("Fehlermeldung sollte INFISICAL_ENV nennen: %v", err)
	}
}

// TestMaybeInjectInfisical_HappyPath prüft den vollständigen Ablauf: login liefert ein Token,
// export liefert dotenv-Secrets, und execServer bekommt eine Umgebung mit den exportierten
// Secrets PLUS dem Re-Exec-Sentinel FILEEE_INFISICAL_REEXEC=1 — in dieser Reihenfolge
// (login vor export).
func TestMaybeInjectInfisical_HappyPath(t *testing.T) {
	env := infisicalRequiredEnv()
	getenv := func(k string) string { return env[k] }

	var calls []string
	run := func(name string, args ...string) ([]byte, error) {
		calls = append(calls, fmt.Sprintf("%s %s", name, strings.Join(args, " ")))
		switch {
		case len(args) > 0 && args[0] == "login":
			return []byte("mint-token-123\n"), nil
		case len(args) > 0 && args[0] == "export":
			// Verifiziert, dass der gemintete Token an export weitergereicht wird.
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, "--token=mint-token-123") {
				t.Errorf("export sollte den geminteten Token enthalten: %v", args)
			}
			return []byte("FILEEE_USERNAME=alice\nFILEEE_PASSWORD=\"s3cr3t\"\n"), nil
		default:
			t.Fatalf("unerwarteter run-Aufruf: %s %v", name, args)
			return nil, nil
		}
	}

	var gotEnv []string
	execServer := func(e []string) error {
		gotEnv = e
		return nil
	}

	if err := MaybeInjectInfisical(getenv, run, execServer); err != nil {
		t.Fatalf("erwartet nil, bekam %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("erwartet 2 run-Aufrufe (login, export), bekam %d: %v", len(calls), calls)
	}
	if !strings.HasPrefix(calls[0], "/infisical login") {
		t.Errorf("erster Aufruf sollte login sein, war %q", calls[0])
	}
	if !strings.HasPrefix(calls[1], "/infisical export") {
		t.Errorf("zweiter Aufruf sollte export sein, war %q", calls[1])
	}

	if !containsExact(gotEnv, "FILEEE_USERNAME=alice") {
		t.Errorf("execServer-Umgebung sollte FILEEE_USERNAME=alice enthalten: %v", gotEnv)
	}
	if !containsExact(gotEnv, "FILEEE_PASSWORD=s3cr3t") {
		t.Errorf("execServer-Umgebung sollte FILEEE_PASSWORD=s3cr3t enthalten: %v", gotEnv)
	}
	if !containsExact(gotEnv, "FILEEE_INFISICAL_REEXEC=1") {
		t.Errorf("execServer-Umgebung sollte den Re-Exec-Sentinel enthalten: %v", gotEnv)
	}
}

// TestMaybeInjectInfisical_LoginErrorPropagates prüft, dass ein Fehler von run beim login
// (z. B. Netzwerkfehler oder falsche Credentials) gewrappt zurückgegeben wird und export sowie
// execServer NICHT aufgerufen werden.
func TestMaybeInjectInfisical_LoginErrorPropagates(t *testing.T) {
	env := infisicalRequiredEnv()
	getenv := func(k string) string { return env[k] }

	wantErr := errors.New("network unreachable")
	run := func(name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "login" {
			return nil, wantErr
		}
		t.Fatal("export darf nach fehlgeschlagenem login nicht aufgerufen werden")
		return nil, nil
	}
	execServer := func([]string) error {
		t.Fatal("execServer darf nach fehlgeschlagenem login nicht aufgerufen werden")
		return nil
	}

	err := MaybeInjectInfisical(getenv, run, execServer)
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("erwartet gewrappten wantErr, bekam %v", err)
	}
}

// TestMaybeInjectInfisical_ExportErrorPropagates prüft, dass ein Fehler von run beim export
// (nach erfolgreichem login) gewrappt zurückgegeben wird und execServer NICHT aufgerufen wird.
func TestMaybeInjectInfisical_ExportErrorPropagates(t *testing.T) {
	env := infisicalRequiredEnv()
	getenv := func(k string) string { return env[k] }

	wantErr := errors.New("403 forbidden")
	run := func(name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "login" {
			return []byte("tok\n"), nil
		}
		return nil, wantErr
	}
	execServer := func([]string) error {
		t.Fatal("execServer darf nach fehlgeschlagenem export nicht aufgerufen werden")
		return nil
	}

	err := MaybeInjectInfisical(getenv, run, execServer)
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("erwartet gewrappten wantErr, bekam %v", err)
	}
}

// TestMaybeInjectInfisical_ExecServerErrorPropagates prüft, dass ein Fehler von execServer
// (z. B. syscall.Exec schlägt fehl, weil das Server-Binary fehlt) unverändert an den Aufrufer
// zurückgegeben wird.
func TestMaybeInjectInfisical_ExecServerErrorPropagates(t *testing.T) {
	env := infisicalRequiredEnv()
	getenv := func(k string) string { return env[k] }

	run := func(name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "login" {
			return []byte("tok\n"), nil
		}
		return []byte("A=1\n"), nil
	}
	wantErr := errors.New("exec format error")
	execServer := func([]string) error {
		return wantErr
	}

	err := MaybeInjectInfisical(getenv, run, execServer)
	if !errors.Is(err, wantErr) {
		t.Fatalf("erwartet wantErr unverändert, bekam %v", err)
	}
}

// TestMaybeInjectInfisical_UsesDefaultPath prüft, dass ohne gesetztes INFISICAL_PATH der
// Default "/" an export weitergereicht wird.
func TestMaybeInjectInfisical_UsesDefaultPath(t *testing.T) {
	env := infisicalRequiredEnv()
	getenv := func(k string) string { return env[k] }

	var exportArgs []string
	run := func(name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "login" {
			return []byte("tok\n"), nil
		}
		exportArgs = args
		return []byte(""), nil
	}
	execServer := func([]string) error { return nil }

	if err := MaybeInjectInfisical(getenv, run, execServer); err != nil {
		t.Fatalf("erwartet nil, bekam %v", err)
	}
	if !containsExact(exportArgs, "--path=/") {
		t.Errorf("export sollte --path=/ als Default enthalten: %v", exportArgs)
	}
}

// containsExact prüft, ob s ein Element enthält, das exakt want entspricht.
func containsExact(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

package main

import (
	"fmt"
	"os"
	"strings"
)

// infisicalBinary ist der Pfad zur statischen infisical-CLI im Runtime-Image (siehe deploy/
// Dockerfile — dort per COPY --chmod=755 neben das Server-Binary gelegt).
const infisicalBinary = "/infisical"

// fileeeServerBinary ist der Pfad zum Server-Binary selbst, mit dem sich der Prozess nach dem
// Infisical-Dual-Mode-Boot per syscall.Exec ersetzt (Server wird dabei PID 1).
const fileeeServerBinary = "/fileee-server"

// reexecSentinelEnv ist die Umgebungsvariable, die den bereits erfolgten Re-Exec markiert und
// eine Endlosschleife (erneutes Minten/Exportieren nach dem Re-Exec) verhindert.
const reexecSentinelEnv = "FILEEE_INFISICAL_REEXEC"

// wantInfisical entscheidet, ob fileee-server beim Start in den Infisical-Dual-Mode wechseln
// soll: aktiv, wenn SECRET_BACKEND=infisical explizit gesetzt ist, ODER wenn SECRET_BACKEND
// nicht auf "env" steht UND eine Universal-Auth-Client-ID konfiguriert ist (Auto-Erkennung).
// In jedem Fall unterdrückt der bereits gesetzte Re-Exec-Sentinel (reexecSentinelEnv) den Modus
// — sonst würde der re-exec'te Prozess erneut versuchen, Secrets zu minten/exportieren.
func wantInfisical(getenv func(string) string) bool {
	if getenv(reexecSentinelEnv) != "" {
		return false
	}
	switch getenv("SECRET_BACKEND") {
	case "infisical":
		return true
	case "env":
		return false
	default:
		return getenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID") != ""
	}
}

// parseDotenv zerlegt den dotenv-formatierten Output von `infisical export --format=dotenv`
// (KEY=VALUE-Zeilen) in eine Liste von "KEY=VALUE"-Strings, wie sie an syscall.Exec/os.Environ
// angehängt werden kann. Kommentarzeilen (mit "#" beginnend, nach Trim führender Leerzeichen)
// und Leerzeilen werden ignoriert; Schlüssel und Wert werden getrimmt, und ein Wert, der
// vollständig von einfachen oder doppelten Anführungszeichen umschlossen ist, wird davon
// befreit (dotenv-Konvention für Werte mit Leerzeichen/Sonderzeichen).
func parseDotenv(b []byte) []string {
	var out []string
	for _, rawLine := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = unquote(value)
		out = append(out, key+"="+value)
	}
	return out
}

// unquote entfernt ein einzelnes Paar umschließender Anführungszeichen (entweder " oder ')
// von s, falls s vollständig davon umschlossen ist — sonst wird s unverändert zurückgegeben.
func unquote(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// MaybeInjectInfisical prüft via wantInfisical, ob der Infisical-Dual-Mode aktiv sein soll, und
// führt ihn im Erfolgsfall komplett aus: Token minten (`infisical login`), Secrets als dotenv
// exportieren (`infisical export`), in die Prozess-Umgebung mergen, und den Server-Prozess per
// execServer ersetzen (der Server bleibt/wird dabei PID 1 — siehe fileeeServerBinary-Kommentar).
// Ist der Infisical-Modus nicht gewünscht (Env-Modus oder bereits re-exec't), ist der Aufruf ein
// reines No-Op (nil-Rückgabe, weder run noch execServer werden aufgerufen).
//
// run und execServer sind injiziert, damit Tests den echten CLI-Aufruf bzw. syscall.Exec durch
// Fakes ersetzen können, ohne einen echten Prozessersatz oder eine echte infisical-CLI zu
// benötigen. INFISICAL_ENV ist im Infisical-Modus Pflicht — die infisical-CLI würde sonst
// stillschweigend auf ihren Default "dev" zurückfallen, was in einer Produktionsumgebung
// fatal wäre (siehe secret-environment-awareness-Regel).
func MaybeInjectInfisical(
	getenv func(string) string,
	run func(name string, args ...string) ([]byte, error),
	execServer func(env []string) error,
) error {
	if !wantInfisical(getenv) {
		return nil
	}

	clientID := getenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID")
	clientSecret := getenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET")
	domain := getenv("INFISICAL_DOMAIN")
	projectID := getenv("INFISICAL_PROJECT_ID")
	env := getenv("INFISICAL_ENV")
	path := getString(getenv, "INFISICAL_PATH", "/")

	if env == "" {
		return fmt.Errorf("INFISICAL_ENV ist im Infisical-Modus Pflicht (CLI-Default \"dev\" wäre für prod falsch)")
	}

	tokenOut, err := run(infisicalBinary, "login", "--method=universal-auth",
		"--client-id="+clientID, "--client-secret="+clientSecret, "--domain="+domain,
		"--plain", "--silent")
	if err != nil {
		return fmt.Errorf("infisical login fehlgeschlagen: %w", err)
	}
	token := strings.TrimSpace(string(tokenOut))

	dotenv, err := run(infisicalBinary, "export", "--format=dotenv",
		"--token="+token, "--projectId="+projectID, "--env="+env, "--path="+path,
		"--domain="+domain)
	if err != nil {
		return fmt.Errorf("infisical export fehlgeschlagen: %w", err)
	}

	merged := append(append([]string{}, os.Environ()...), parseDotenv(dotenv)...)
	merged = append(merged, reexecSentinelEnv+"=1")

	return execServer(merged)
}

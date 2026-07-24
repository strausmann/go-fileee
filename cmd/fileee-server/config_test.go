package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// requiredEnv liefert die drei Pflicht-Variablen als Basis für Tests, die zusätzliche
// Overrides oder Defaults prüfen wollen.
func requiredEnv() map[string]string {
	return map[string]string{"FILEEE_USERNAME": "u", "FILEEE_PASSWORD": "p", "FILEEE_API_TOKEN": "t"}
}

func TestLoadConfig_DefaultsAndRequired(t *testing.T) {
	env := requiredEnv()
	c, err := LoadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":8080" || c.KeepAliveInterval != 15*time.Minute || c.RateRPS != 1 || !c.DocsPublic {
		t.Fatalf("Defaults falsch: %+v", c)
	}
	if _, err := LoadConfig(func(k string) string { return map[string]string{"FILEEE_USERNAME": "u"}[k] }); err == nil {
		t.Fatal("fehlender API-Token/Passwort muss Fehler sein")
	}
}

// TestLoadConfig_AllDefaults prüft jeden im Brief benannten Default-Wert einzeln — nicht nur
// die vier Stichproben aus dem Ausgangstest.
func TestLoadConfig_AllDefaults(t *testing.T) {
	env := requiredEnv()
	c, err := LoadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}

	want := Config{
		FileeeUsername:    "u",
		FileeePassword:    "p",
		FileeeTOTPSeed:    "",
		APIToken:          "t",
		AllowDestructive:  false,
		ListenAddr:        ":8080",
		SessionPath:       "/home/nonroot/session.json",
		KeepAliveInterval: 15 * time.Minute,
		WaitTimeout:       60 * time.Second,
		WaitMax:           300 * time.Second,
		RateRPS:           1,
		RateBurst:         3,
		TrustedProxies:    nil,
		ClientIPHeaders:   []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"},
		DocsPublic:        true,
		MaxUploadBytes:    32 << 20,
		WebhookURL:        "",
		WebhookSecret:     "",
		WatchInterval:     0,
		UserAgent:         "",
		LogLevel:          "info",
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("Defaults weichen ab:\n got: %+v\nwant: %+v", c, want)
	}
}

// TestLoadConfig_Overrides prüft, dass jede unterstützte Umgebungsvariable den zugehörigen
// Default tatsächlich überschreibt (Typ-Parsing inklusive).
func TestLoadConfig_Overrides(t *testing.T) {
	env := requiredEnv()
	env["FILEEE_TOTP_SEED"] = "SEED123"
	env["FILEEE_ALLOW_DESTRUCTIVE"] = "true"
	env["FILEEE_LISTEN_ADDR"] = ":9090"
	env["FILEEE_SESSION_PATH"] = "/tmp/session.json"
	env["FILEEE_KEEPALIVE_INTERVAL"] = "5m"
	env["FILEEE_WAIT_TIMEOUT"] = "10s"
	env["FILEEE_WAIT_MAX"] = "90s"
	env["FILEEE_RATE_RPS"] = "2.5"
	env["FILEEE_RATE_BURST"] = "7"
	env["FILEEE_TRUSTED_PROXIES"] = "10.0.0.1, 10.0.0.2 ,,10.0.0.3"
	env["FILEEE_CLIENT_IP_HEADERS"] = "X-Custom-IP, X-Real-IP"
	env["FILEEE_DOCS_PUBLIC"] = "false"
	env["FILEEE_MAX_UPLOAD_SIZE"] = "1048576"
	env["FILEEE_WEBHOOK_URL"] = "https://example.invalid/hook"
	env["FILEEE_WEBHOOK_SECRET"] = "whsec"
	env["FILEEE_WATCH_INTERVAL"] = "30s"
	env["FILEEE_USER_AGENT"] = "fileee-server-test/1.0"
	env["FILEEE_LOG_LEVEL"] = "debug"

	c, err := LoadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}

	// Sequentielle if-Blöcke statt eines bare switch: ein switch-ohne-Ausdruck wählt nur den
	// ERSTEN zutreffenden case und bricht danach ab — bei mehreren gleichzeitig falschen
	// Feldern sieht man in einem Testlauf nur die erste Abweichung, alle weiteren bleiben
	// verdeckt. Mit if-Blöcken (wie in TestLoadConfig_InvalidValuesFallBackToDefault) meldet
	// t.Errorf jede abweichende Zuweisung einzeln, ohne den Testlauf abzubrechen.
	if c.FileeeTOTPSeed != "SEED123" {
		t.Errorf("FileeeTOTPSeed = %q", c.FileeeTOTPSeed)
	}
	if !c.AllowDestructive {
		t.Error("AllowDestructive sollte true sein")
	}
	if c.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %q", c.ListenAddr)
	}
	if c.SessionPath != "/tmp/session.json" {
		t.Errorf("SessionPath = %q", c.SessionPath)
	}
	if c.KeepAliveInterval != 5*time.Minute {
		t.Errorf("KeepAliveInterval = %v", c.KeepAliveInterval)
	}
	if c.WaitTimeout != 10*time.Second {
		t.Errorf("WaitTimeout = %v", c.WaitTimeout)
	}
	if c.WaitMax != 90*time.Second {
		t.Errorf("WaitMax = %v", c.WaitMax)
	}
	if c.RateRPS != 2.5 {
		t.Errorf("RateRPS = %v", c.RateRPS)
	}
	if c.RateBurst != 7 {
		t.Errorf("RateBurst = %v", c.RateBurst)
	}
	if !reflect.DeepEqual(c.TrustedProxies, []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}) {
		t.Errorf("TrustedProxies = %v", c.TrustedProxies)
	}
	if !reflect.DeepEqual(c.ClientIPHeaders, []string{"X-Custom-IP", "X-Real-IP"}) {
		t.Errorf("ClientIPHeaders = %v", c.ClientIPHeaders)
	}
	if c.DocsPublic {
		t.Error("DocsPublic sollte false sein")
	}
	if c.MaxUploadBytes != 1048576 {
		t.Errorf("MaxUploadBytes = %v", c.MaxUploadBytes)
	}
	if c.WebhookURL != "https://example.invalid/hook" {
		t.Errorf("WebhookURL = %q", c.WebhookURL)
	}
	if c.WebhookSecret != "whsec" {
		t.Errorf("WebhookSecret = %q", c.WebhookSecret)
	}
	if c.WatchInterval != 30*time.Second {
		t.Errorf("WatchInterval = %v", c.WatchInterval)
	}
	if c.UserAgent != "fileee-server-test/1.0" {
		t.Errorf("UserAgent = %q", c.UserAgent)
	}
	if c.LogLevel != "debug" {
		t.Errorf("LogLevel = %q", c.LogLevel)
	}

	// FILEEE_ALLOW_DESTRUCTIVE="1" ist die zweite akzeptierte Bool-Schreibweise.
	env["FILEEE_ALLOW_DESTRUCTIVE"] = "1"
	c2, err := LoadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if !c2.AllowDestructive {
		t.Error(`AllowDestructive sollte bei "1" true sein`)
	}
}

// TestLoadConfig_InvalidValuesFallBackToDefault stellt sicher, dass nicht parsbare
// Umgebungswerte den jeweiligen Default liefern statt LoadConfig fehlschlagen zu lassen.
func TestLoadConfig_InvalidValuesFallBackToDefault(t *testing.T) {
	env := requiredEnv()
	env["FILEEE_RATE_RPS"] = "not-a-float"
	env["FILEEE_RATE_BURST"] = "not-an-int"
	env["FILEEE_KEEPALIVE_INTERVAL"] = "not-a-duration"
	env["FILEEE_MAX_UPLOAD_SIZE"] = "not-an-int64"
	env["FILEEE_ALLOW_DESTRUCTIVE"] = "yes-please"

	c, err := LoadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if c.RateRPS != 1 {
		t.Errorf("RateRPS = %v, erwartet Default 1", c.RateRPS)
	}
	if c.RateBurst != 3 {
		t.Errorf("RateBurst = %v, erwartet Default 3", c.RateBurst)
	}
	if c.KeepAliveInterval != 15*time.Minute {
		t.Errorf("KeepAliveInterval = %v, erwartet Default 15m", c.KeepAliveInterval)
	}
	if c.MaxUploadBytes != 32<<20 {
		t.Errorf("MaxUploadBytes = %v, erwartet Default 32MiB", c.MaxUploadBytes)
	}
	if c.AllowDestructive {
		t.Error(`AllowDestructive sollte bei "yes-please" false (Default) sein`)
	}
}

// TestLoadConfig_MissingRequiredFields prüft jede Pflichtvariable einzeln und in Kombination —
// die Fehlermeldung muss alle fehlenden Variablen benennen.
func TestLoadConfig_MissingRequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantErr []string
	}{
		{"nur Username", map[string]string{"FILEEE_USERNAME": "u"}, []string{"FILEEE_PASSWORD", "FILEEE_API_TOKEN"}},
		{"nur Passwort", map[string]string{"FILEEE_PASSWORD": "p"}, []string{"FILEEE_USERNAME", "FILEEE_API_TOKEN"}},
		{"nur Token", map[string]string{"FILEEE_API_TOKEN": "t"}, []string{"FILEEE_USERNAME", "FILEEE_PASSWORD"}},
		{"alles leer", map[string]string{}, []string{"FILEEE_USERNAME", "FILEEE_PASSWORD", "FILEEE_API_TOKEN"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfig(func(k string) string { return tc.env[k] })
			if err == nil {
				t.Fatal("erwartet Fehler, bekam nil")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Fehlermeldung %q enthält %q nicht", err.Error(), want)
				}
			}
		})
	}
}

// TestGetCSV prüft Trim- und Leer-Filter-Verhalten sowie den Default-Fallback direkt am Helper.
func TestGetCSV(t *testing.T) {
	env := map[string]string{
		"SET":    "a, b ,,c",
		"SINGLE": "x",
	}
	getenv := func(k string) string { return env[k] }

	if got := getCSV(getenv, "SET", nil); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("getCSV(SET) = %v", got)
	}
	if got := getCSV(getenv, "SINGLE", nil); !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("getCSV(SINGLE) = %v", got)
	}
	if got := getCSV(getenv, "UNSET", nil); got != nil {
		t.Errorf("getCSV(UNSET) = %v, erwartet nil-Default", got)
	}
	def := []string{"fallback"}
	if got := getCSV(getenv, "UNSET", def); !reflect.DeepEqual(got, def) {
		t.Errorf("getCSV(UNSET, def) = %v, erwartet %v", got, def)
	}
}

// TestGetCSV_DefaultNotAliased prüft, dass getCSV bei ungesetzter Variable eine defensive
// Kopie von def zurückgibt statt def selbst — sonst würde eine In-Place-Mutation des
// zurückgegebenen Slices den vom Aufrufer übergebenen Default (z. B. das package-globale
// defaultClientIPHeaders) korrumpieren.
func TestGetCSV_DefaultNotAliased(t *testing.T) {
	getenv := func(string) string { return "" }
	def := []string{"a", "b"}

	got := getCSV(getenv, "UNSET", def)
	got[0] = "MUTATED"

	if def[0] == "MUTATED" {
		t.Fatalf("getCSV gab def by reference zurück — Mutation von got hat def korrumpiert: %v", def)
	}
}

// TestLoadConfig_DefaultClientIPHeadersNotShared stellt sicher, dass zwei per LoadConfig
// erzeugte Configs mit unverändertem FILEEE_CLIENT_IP_HEADERS-Default kein gemeinsames
// Backing-Array für ClientIPHeaders besitzen. Ohne defensive Kopie in getCSV würden beide
// Configs auf das package-globale defaultClientIPHeaders-Array zeigen — eine In-Place-Mutation
// bei einer Config würde dann unbemerkt jede andere Config (und alle Tests) mitverändern.
func TestLoadConfig_DefaultClientIPHeadersNotShared(t *testing.T) {
	env := requiredEnv()
	getenv := func(k string) string { return env[k] }

	c1, err := LoadConfig(getenv)
	if err != nil {
		t.Fatal(err)
	}
	c1.ClientIPHeaders[0] = "MUTATED"

	c2, err := LoadConfig(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if c2.ClientIPHeaders[0] != "CF-Connecting-IP" {
		t.Fatalf("ClientIPHeaders von c2 wurde durch Mutation von c1 korrumpiert: %v", c2.ClientIPHeaders)
	}
}

// TestConfig_String_MasksSecrets prüft, dass Config.String() (und damit fmt.Sprintf("%v", ...))
// keinen der vier Secret-Werte im Klartext preisgibt, gesetzte Secrets aber als "***" und leere
// Secrets als "" markiert, während Nicht-Secret-Felder (z. B. ListenAddr) unverändert erscheinen.
func TestConfig_String_MasksSecrets(t *testing.T) {
	c := Config{
		FileeeUsername: "user1",
		FileeePassword: "supersecretpw",
		FileeeTOTPSeed: "SEEDVALUE123",
		APIToken:       "tok-abc123",
		WebhookSecret:  "whsec-xyz",
		ListenAddr:     ":9090",
	}

	secrets := []string{"supersecretpw", "SEEDVALUE123", "tok-abc123", "whsec-xyz"}

	s := c.String()
	for _, secret := range secrets {
		if strings.Contains(s, secret) {
			t.Errorf("String() enthält Secret-Wert %q im Klartext: %s", secret, s)
		}
	}
	if !strings.Contains(s, ":9090") {
		t.Errorf("String() sollte Nicht-Secret-Feld ListenAddr zeigen: %s", s)
	}
	if !strings.Contains(s, "***") {
		t.Errorf("String() sollte gesetzte Secrets als *** maskieren: %s", s)
	}

	// fmt.Sprintf("%v", ...) muss automatisch String() nutzen (Stringer-Interface) — kein
	// Secret darf auch über diesen Weg im Klartext landen.
	sprintf := fmt.Sprintf("%v", c)
	for _, secret := range secrets {
		if strings.Contains(sprintf, secret) {
			t.Errorf("fmt.Sprintf(%%v) enthält Secret-Wert %q im Klartext: %s", secret, sprintf)
		}
	}

	// Leere Secrets müssen als "" erscheinen, nicht als "***".
	empty := Config{ListenAddr: ":8080"}
	got := empty.String()
	if strings.Contains(got, "***") {
		t.Errorf("String() sollte leere Secrets nicht als *** maskieren: %s", got)
	}
}

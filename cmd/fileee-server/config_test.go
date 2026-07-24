package main

import (
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

	switch {
	case c.FileeeTOTPSeed != "SEED123":
		t.Errorf("FileeeTOTPSeed = %q", c.FileeeTOTPSeed)
	case !c.AllowDestructive:
		t.Error("AllowDestructive sollte true sein")
	case c.ListenAddr != ":9090":
		t.Errorf("ListenAddr = %q", c.ListenAddr)
	case c.SessionPath != "/tmp/session.json":
		t.Errorf("SessionPath = %q", c.SessionPath)
	case c.KeepAliveInterval != 5*time.Minute:
		t.Errorf("KeepAliveInterval = %v", c.KeepAliveInterval)
	case c.WaitTimeout != 10*time.Second:
		t.Errorf("WaitTimeout = %v", c.WaitTimeout)
	case c.WaitMax != 90*time.Second:
		t.Errorf("WaitMax = %v", c.WaitMax)
	case c.RateRPS != 2.5:
		t.Errorf("RateRPS = %v", c.RateRPS)
	case c.RateBurst != 7:
		t.Errorf("RateBurst = %v", c.RateBurst)
	case !reflect.DeepEqual(c.TrustedProxies, []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}):
		t.Errorf("TrustedProxies = %v", c.TrustedProxies)
	case !reflect.DeepEqual(c.ClientIPHeaders, []string{"X-Custom-IP", "X-Real-IP"}):
		t.Errorf("ClientIPHeaders = %v", c.ClientIPHeaders)
	case c.DocsPublic:
		t.Error("DocsPublic sollte false sein")
	case c.MaxUploadBytes != 1048576:
		t.Errorf("MaxUploadBytes = %v", c.MaxUploadBytes)
	case c.WebhookURL != "https://example.invalid/hook":
		t.Errorf("WebhookURL = %q", c.WebhookURL)
	case c.WebhookSecret != "whsec":
		t.Errorf("WebhookSecret = %q", c.WebhookSecret)
	case c.WatchInterval != 30*time.Second:
		t.Errorf("WatchInterval = %v", c.WatchInterval)
	case c.UserAgent != "fileee-server-test/1.0":
		t.Errorf("UserAgent = %q", c.UserAgent)
	case c.LogLevel != "debug":
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

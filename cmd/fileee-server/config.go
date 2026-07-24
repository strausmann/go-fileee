// Package main implementiert fileee-server, einen REST-API-Wrapper um die Core-Lib
// (github.com/strausmann/go-fileee/fileee) hinter einem statischen API-Token — gedacht für
// N8N-Workflows und CI-Automatisierung ohne direkten Fileee-Login.
package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Config bündelt die gesamte Laufzeit-Konfiguration von fileee-server. Sie wird ausschließlich
// über LoadConfig aus Umgebungsvariablen befüllt — kein Feld wird an anderer Stelle direkt aus
// os.Getenv gelesen (siehe Task 13/14), damit Defaults, Validierung und Tests an einer einzigen
// Stelle zusammenlaufen.
type Config struct {
	// FileeeUsername ist der Login-Benutzername des Fileee-Kontos (FILEEE_USERNAME, Pflicht).
	FileeeUsername string
	// FileeePassword ist das Login-Passwort des Fileee-Kontos (FILEEE_PASSWORD, Pflicht).
	FileeePassword string
	// FileeeTOTPSeed ist der Base32-TOTP-Seed für Zwei-Faktor-Konten (FILEEE_TOTP_SEED,
	// optional — leer bei Konten ohne Zwei-Faktor-Authentifizierung).
	FileeeTOTPSeed string
	// APIToken ist das statische Bearer-Token, mit dem Clients sich gegen fileee-server
	// authentifizieren (FILEEE_API_TOKEN, Pflicht).
	APIToken string

	// AllowDestructive schaltet zerstörende Operationen (z. B. Löschen) frei
	// (FILEEE_ALLOW_DESTRUCTIVE, Default false).
	AllowDestructive bool

	// ListenAddr ist die Adresse, auf der der HTTP-Server lauscht (FILEEE_LISTEN_ADDR,
	// Default ":8080").
	ListenAddr string
	// SessionPath ist der Pfad, unter dem die Fileee-Session persistiert wird
	// (FILEEE_SESSION_PATH, Default "/home/nonroot/session.json").
	SessionPath string

	// KeepAliveInterval steuert das Intervall des Session-Keepalive
	// (FILEEE_KEEPALIVE_INTERVAL, Default 15m).
	KeepAliveInterval time.Duration
	// WaitTimeout ist die Poll-Wartezeit für einzelne serverseitige Vorgänge
	// (FILEEE_WAIT_TIMEOUT, Default 60s).
	WaitTimeout time.Duration
	// WaitMax ist die maximale Gesamtwartezeit für serverseitige Vorgänge
	// (FILEEE_WAIT_MAX, Default 300s).
	WaitMax time.Duration

	// RateRPS ist die erlaubte Request-Rate pro Sekunde gegen die Fileee-API
	// (FILEEE_RATE_RPS, Default 1).
	RateRPS float64
	// RateBurst ist die Burst-Größe des Token-Buckets (FILEEE_RATE_BURST, Default 3).
	RateBurst int

	// TrustedProxies listet die IPs/CIDRs vertrauenswürdiger Reverse-Proxies
	// (FILEEE_TRUSTED_PROXIES, kommagetrennt, Default leer).
	TrustedProxies []string
	// ClientIPHeaders listet die Header, aus denen die Client-IP ermittelt wird, in
	// Prüfreihenfolge (FILEEE_CLIENT_IP_HEADERS, kommagetrennt, Default
	// CF-Connecting-IP, X-Real-IP, X-Forwarded-For).
	ClientIPHeaders []string

	// DocsPublic legt fest, ob die OpenAPI-Doku ohne Token erreichbar ist
	// (FILEEE_DOCS_PUBLIC, Default true).
	DocsPublic bool

	// MaxUploadBytes begrenzt die Größe eingehender Uploads (FILEEE_MAX_UPLOAD_SIZE,
	// Default 32 MiB).
	MaxUploadBytes int64

	// WebhookURL ist das Ziel für ausgehende Webhook-Benachrichtigungen
	// (FILEEE_WEBHOOK_URL, Default leer — Webhooks deaktiviert).
	WebhookURL string
	// WebhookSecret signiert ausgehende Webhook-Payloads (FILEEE_WEBHOOK_SECRET,
	// Default leer).
	WebhookSecret string

	// WatchInterval steuert das Polling-Intervall des Änderungs-Watchers
	// (FILEEE_WATCH_INTERVAL, Default 0 — Watcher deaktiviert).
	WatchInterval time.Duration

	// UserAgent überschreibt den User-Agent, mit dem die Core-Lib gegen Fileee spricht
	// (FILEEE_USER_AGENT, Default leer — Core-Lib-Default greift).
	UserAgent string
	// LogLevel steuert das Log-Level des Servers (FILEEE_LOG_LEVEL, Default "info").
	LogLevel string
}

// defaultClientIPHeaders sind die Header, aus denen ohne explizite Konfiguration die
// Client-IP ermittelt wird — in Prüfreihenfolge: Cloudflare zuerst, dann generische
// Reverse-Proxy-Header.
var defaultClientIPHeaders = []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"}

// LoadConfig liest die Konfiguration aus Umgebungsvariablen und wendet dabei Defaults an.
// getenv wird injiziert (statt direkt os.Getenv), damit Aufrufer wie Tests eine isolierte,
// deterministische Umgebung vorgeben können. FILEEE_USERNAME, FILEEE_PASSWORD und
// FILEEE_API_TOKEN sind Pflichtfelder — fehlen sie, liefert LoadConfig einen Fehler.
// FILEEE_TOTP_SEED bleibt für Konten ohne Zwei-Faktor-Authentifizierung optional.
func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{
		FileeeUsername:    getenv("FILEEE_USERNAME"),
		FileeePassword:    getenv("FILEEE_PASSWORD"),
		FileeeTOTPSeed:    getenv("FILEEE_TOTP_SEED"),
		APIToken:          getenv("FILEEE_API_TOKEN"),
		AllowDestructive:  getBool(getenv, "FILEEE_ALLOW_DESTRUCTIVE", false),
		ListenAddr:        getString(getenv, "FILEEE_LISTEN_ADDR", ":8080"),
		SessionPath:       getString(getenv, "FILEEE_SESSION_PATH", "/home/nonroot/session.json"),
		KeepAliveInterval: getDuration(getenv, "FILEEE_KEEPALIVE_INTERVAL", 15*time.Minute),
		WaitTimeout:       getDuration(getenv, "FILEEE_WAIT_TIMEOUT", 60*time.Second),
		WaitMax:           getDuration(getenv, "FILEEE_WAIT_MAX", 300*time.Second),
		RateRPS:           getFloat(getenv, "FILEEE_RATE_RPS", 1),
		RateBurst:         getInt(getenv, "FILEEE_RATE_BURST", 3),
		TrustedProxies:    getCSV(getenv, "FILEEE_TRUSTED_PROXIES", nil),
		ClientIPHeaders:   getCSV(getenv, "FILEEE_CLIENT_IP_HEADERS", defaultClientIPHeaders),
		DocsPublic:        getBool(getenv, "FILEEE_DOCS_PUBLIC", true),
		MaxUploadBytes:    getInt64(getenv, "FILEEE_MAX_UPLOAD_SIZE", 32<<20),
		WebhookURL:        getString(getenv, "FILEEE_WEBHOOK_URL", ""),
		WebhookSecret:     getString(getenv, "FILEEE_WEBHOOK_SECRET", ""),
		WatchInterval:     getDuration(getenv, "FILEEE_WATCH_INTERVAL", 0),
		UserAgent:         getString(getenv, "FILEEE_USER_AGENT", ""),
		LogLevel:          getString(getenv, "FILEEE_LOG_LEVEL", "info"),
	}

	var missing []string
	if c.FileeeUsername == "" {
		missing = append(missing, "FILEEE_USERNAME")
	}
	if c.FileeePassword == "" {
		missing = append(missing, "FILEEE_PASSWORD")
	}
	if c.APIToken == "" {
		missing = append(missing, "FILEEE_API_TOKEN")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("fehlende Pflicht-Umgebungsvariablen: %s", strings.Join(missing, ", "))
	}

	return c, nil
}

// getString liest key aus getenv und liefert def, falls die Variable leer/nicht gesetzt ist.
func getString(getenv func(string) string, key, def string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return def
}

// getBool liest key als Bool ("true" oder "1" gelten als wahr, alles andere als falsch) und
// liefert def, falls die Variable leer/nicht gesetzt ist.
func getBool(getenv func(string) string, key string, def bool) bool {
	v := getenv(key)
	if v == "" {
		return def
	}
	return v == "true" || v == "1"
}

// getInt liest key als int und liefert def, falls die Variable leer oder nicht parsbar ist.
func getInt(getenv func(string) string, key string, def int) int {
	v := getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// getInt64 liest key als int64 und liefert def, falls die Variable leer oder nicht parsbar ist.
func getInt64(getenv func(string) string, key string, def int64) int64 {
	v := getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// getFloat liest key als float64 und liefert def, falls die Variable leer oder nicht parsbar ist.
func getFloat(getenv func(string) string, key string, def float64) float64 {
	v := getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

// getDuration liest key als time.Duration (Go-Duration-Syntax, z. B. "15m", "60s") und liefert
// def, falls die Variable leer oder nicht parsbar ist.
func getDuration(getenv func(string) string, key string, def time.Duration) time.Duration {
	v := getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

// getCSV liest key als kommagetrennte Liste, trimmt Leerzeichen und lässt leere Einträge weg.
// Ist die Variable leer/nicht gesetzt, wird def zurückgegeben (auch wenn def nil ist).
func getCSV(getenv func(string) string, key string, def []string) []string {
	v := getenv(key)
	if v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

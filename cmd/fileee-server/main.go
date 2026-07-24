// Command fileee-server startet den REST-API-Wrapper: Boot-Reihenfolge Healthcheck-Subcommand →
// optionaler Infisical-Dual-Mode → Config → Core-Lib-Client/ShareClient → Keepalive → HTTP-Server →
// optionaler Watcher, mit Graceful-Shutdown über SIGINT/SIGTERM (siehe main-Doku unten).
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/strausmann/go-fileee/fileee"
)

// shutdownTimeout begrenzt, wie lange main auf den geordneten Abbau laufender HTTP-Requests
// wartet (http.Server.Shutdown), nachdem ein Beendigungssignal (SIGINT/SIGTERM) eingetroffen ist.
const shutdownTimeout = 10 * time.Second

// healthcheckTimeout begrenzt den einzelnen HTTP-GET, den der healthcheck-Subcommand gegen
// /healthz absetzt — ein hängender lokaler Server darf den Container-HEALTHCHECK nicht ewig
// blockieren.
const healthcheckTimeout = 5 * time.Second

// main ist der einzige Einstiegspunkt der Binary und deckt ZWEI Aufrufarten ab: (1) als
// `fileee-server healthcheck`, aufgerufen vom Docker-`HEALTHCHECK` des Distroless-Images — dieser
// Zweig braucht bewusst KEINE Secrets/Config-Validierung, nur den Listen-Port, und muss deshalb VOR
// jeder Infisical-/Config-Arbeit geprüft werden; (2) als eigentlicher Server-Boot. Die Reihenfolge
// im Server-Zweig ist korrektheitskritisch: MaybeInjectInfisical (kann den Prozess per
// syscall.Exec ersetzen und kehrt dann nie zurück) MUSS vor LoadConfig laufen, damit LoadConfig
// bereits die injizierten Secrets sieht.
func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck(healthcheckAddr(os.Getenv)))
	}

	if err := MaybeInjectInfisical(os.Getenv, runInfisicalCommand, execFileeeServer); err != nil {
		fatal("Infisical-Dual-Mode fehlgeschlagen", err)
	}

	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		fatal("Konfiguration ungültig", err)
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel(cfg.LogLevel)}))

	fc, err := fileee.New(
		fileee.Credentials{
			Username: cfg.FileeeUsername,
			Password: cfg.FileeePassword,
			TOTPSeed: cfg.FileeeTOTPSeed,
		},
		fileee.WithRateLimit(cfg.RateRPS, cfg.RateBurst),
		fileee.WithSessionStore(fileee.NewFileSessionStore(cfg.SessionPath)),
		fileee.WithSessionFreshness(cfg.KeepAliveInterval),
		fileee.WithUserAgent(cfg.UserAgent),
		fileee.WithLogger(log),
	)
	if err != nil {
		fatal("Fileee-Client konnte nicht erstellt werden", err)
	}

	// Config trägt aktuell KEIN eigenes Feld für den Static-Host (siehe config.go) — die
	// Umbrella-Spec kennt genau einen öffentlichen Static-Host (static.fileee.com), daher wird
	// fileee.WithStaticBaseURL hier bewusst NICHT gesetzt und NewShareClient verwendet seinen
	// eingebauten Default (siehe fileee/shareclient.go: defaultStaticBaseURL).
	sc := fileee.NewShareClient(
		fileee.WithRateLimit(cfg.RateRPS, cfg.RateBurst),
		fileee.WithUserAgent(cfg.UserAgent),
		fileee.WithLogger(log),
	)

	rootCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	stopKeepAlive := fc.StartKeepAlive(rootCtx, cfg.KeepAliveInterval)
	defer stopKeepAlive()

	apiServer := NewServer(cfg, fc, sc, log)

	srv := &http.Server{Addr: cfg.ListenAddr, Handler: apiServer.Handler()}
	serveErr := make(chan error, 1)
	go func() {
		log.Info("fileee-server startet", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	stopWatch := apiServer.StartWatch(rootCtx, cfg.WebhookURL, cfg.WatchInterval)
	defer stopWatch()

	select {
	case <-rootCtx.Done():
		log.Info("Beendigungssignal empfangen, fahre geordnet herunter")
	case err := <-serveErr:
		if err != nil {
			log.Error("HTTP-Server unerwartet beendet", "error", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("HTTP-Server-Shutdown fehlgeschlagen", "error", err)
	}

	// stopSignals/stopKeepAlive/stopWatch laufen über die defer-Kette oben: stopSignals hebt die
	// SIGINT/SIGTERM-Weiterleitung auf und (bei Bedarf) cancelt rootCtx, was den Keepalive- und den
	// Watch-Poller beendet; stopKeepAlive/stopWatch blockieren zusätzlich explizit, bis ihre jeweilige
	// Goroutine tatsächlich beendet ist (siehe StartKeepAlive/StartWatch-Doku) — kein Goroutine-Leak
	// bleibt nach main() zurück.
}

// runHealthcheck führt EINEN HTTP-GET gegen http://addr/healthz aus und liefert den Exit-Code für
// den healthcheck-Subcommand: 0 bei einer 2xx-Antwort, 1 in jedem anderen Fall (Nicht-2xx-Status,
// Verbindungsfehler, Timeout). addr ist bereits eine host:port-Adresse (siehe healthcheckAddr) —
// runHealthcheck selbst baut die URL nur noch per einfacher String-Konkatenation aus dem bereits
// validierten addr zusammen, NICHT aus rohen, ungeprüften Nutzereingaben.
func runHealthcheck(addr string) int {
	client := &http.Client{Timeout: healthcheckTimeout}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 1
	}
	return 0
}

// healthcheckAddr ermittelt die lokale host:port-Adresse, gegen die runHealthcheck den GET
// absetzt: FILEEE_LISTEN_ADDR wird per net.SplitHostPort zerlegt (NIEMALS per String-Concat), der
// Host-Teil wird bewusst verworfen — der Healthcheck läuft immer als lokaler Prozess IM selben
// Container und spricht deshalb ausdrücklich 127.0.0.1 an, unabhängig davon, ob ListenAddr einen
// Host trägt (z. B. ":8080" hat gar keinen). Ist FILEEE_LISTEN_ADDR leer oder nicht als
// host:port parsbar, greift derselbe Default-Port wie in LoadConfig (":8080").
func healthcheckAddr(getenv func(string) string) string {
	raw := getenv("FILEEE_LISTEN_ADDR")
	if raw == "" {
		raw = ":8080"
	}
	_, port, err := net.SplitHostPort(raw)
	if err != nil || port == "" {
		port = "8080"
	}
	return net.JoinHostPort("127.0.0.1", port)
}

// runInfisicalCommand ist die run-Injektion für MaybeInjectInfisical im echten Server-Boot: führt
// den infisical-Binary via exec.Command aus und liefert dessen Stdout (Output verhält sich wie von
// MaybeInjectInfisical erwartet — bei Nicht-Null-Exit inkl. Stderr im Fehler, siehe
// exec.Cmd.Output-Doku).
func runInfisicalCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// execFileeeServer ist die execServer-Injektion für MaybeInjectInfisical im echten Server-Boot:
// ersetzt den aktuellen Prozess per syscall.Exec durch fileeeServerBinary mit env als neue
// Prozess-Umgebung — kehrt bei Erfolg NIE zurück (der Server-Prozess läuft ab hier als PID 1
// weiter, siehe fileeeServerBinary-Doku in secrets.go). os.Args[1:] wird unverändert
// durchgereicht, damit ein etwaiges Subcommand (aktuell nur "healthcheck", der aber schon vor
// MaybeInjectInfisical behandelt wird) erhalten bliebe.
func execFileeeServer(env []string) error {
	return syscall.Exec(fileeeServerBinary, append([]string{fileeeServerBinary}, os.Args[1:]...), env)
}

// logLevel übersetzt Config.LogLevel ("debug"|"info"|"warn"|"error", case-insensitiv) in ein
// slog.Level; ein unbekannter oder leerer Wert fällt auf slog.LevelInfo zurück statt einen Fehler
// zu werfen — ein Tippfehler in FILEEE_LOG_LEVEL soll den Server nicht am Start hindern.
func logLevel(s string) slog.Level {
	switch s {
	case "debug", "DEBUG":
		return slog.LevelDebug
	case "warn", "WARN", "warning", "WARNING":
		return slog.LevelWarn
	case "error", "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// fatal loggt msg+err auf os.Stderr (noch bevor ein slog.Logger existieren kann, z. B. bei einem
// LoadConfig-Fehler) und beendet den Prozess mit Exit-Code 1.
func fatal(msg string, err error) {
	slog.New(slog.NewTextHandler(os.Stderr, nil)).Error(msg, "error", err)
	os.Exit(1)
}

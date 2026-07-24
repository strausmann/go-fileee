package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// statusRecorder wrappt einen http.ResponseWriter und merkt sich Status-Code und geschriebene
// Byte-Anzahl für das Access-Log — der http.ResponseWriter selbst gibt beides nach dem Request
// nicht mehr her. Ohne expliziten WriteHeader-Aufruf gilt (wie bei net/http üblich) Status 200.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

// WriteHeader merkt sich den Status-Code und reicht ihn an den zugrunde liegenden
// ResponseWriter weiter.
func (rec *statusRecorder) WriteHeader(status int) {
	rec.status = status
	rec.ResponseWriter.WriteHeader(status)
}

// Write zählt die geschriebenen Bytes mit und reicht den Aufruf an den zugrunde liegenden
// ResponseWriter weiter. Ruft der Handler kein WriteHeader auf, setzt net/http vor dem ersten
// Write intern Status 200 — rec.status bleibt dabei unverändert auf seinem Default 200 stehen.
func (rec *statusRecorder) Write(b []byte) (int, error) {
	n, err := rec.ResponseWriter.Write(b)
	rec.bytes += n
	return n, err
}

// parseTrustedNets wandelt trusted (CIDRs, z. B. "10.0.0.0/8") in *net.IPNet um. Ein Eintrag
// ohne "/"-Suffix (bloße IP wie "10.0.0.1") wird tolerant als /32- bzw. /128-Host-Netz
// behandelt. Nicht parsbare Einträge werden übersprungen (kein Fehler — ein einzelner
// Tippfehler in der Konfiguration soll den Server nicht funktionsunfähig machen).
func parseTrustedNets(trusted []string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(trusted))
	for _, entry := range trusted {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if !strings.Contains(entry, "/") {
			if ip := net.ParseIP(entry); ip != nil {
				bits := 32
				if ip.To4() == nil {
					bits = 128
				}
				entry = fmt.Sprintf("%s/%d", entry, bits)
			}
		}
		_, ipNet, err := net.ParseCIDR(entry)
		if err != nil {
			continue
		}
		nets = append(nets, ipNet)
	}
	return nets
}

// isTrustedIP prüft, ob host (reine IP, kein Port) in einem der nets liegt.
func isTrustedIP(host string, nets []*net.IPNet) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP ermittelt die tatsächliche Client-IP eines Requests unter Berücksichtigung
// vertrauenswürdiger Reverse-Proxies (Spec §7). Reverse-Proxy-Header werden NUR
// berücksichtigt, wenn der TCP-Quell-Host (r.RemoteAddr) in einem der trusted-CIDRs liegt —
// sonst würde jeder Client durch einen selbst gesetzten Header (z. B. X-Forwarded-For)
// beliebige IPs vortäuschen können (Spoofing). Ist die Quelle vertrauenswürdig, wird
// headerOrder der Reihe nach geprüft; der erste vorhandene Header gewinnt. Für
// X-Forwarded-For (kommagetrennte Hop-Kette, ältester Eintrag links) wird von rechts nach
// links gewandert und der erste NICHT-vertrauenswürdige Eintrag genommen — das ist der
// letzte Hop, der nicht selbst einer unserer Proxies war. Sind ausnahmslos alle Einträge
// vertrauenswürdig, wird der linkeste (älteste) Eintrag genommen. Ist trusted leer oder
// headerOrder leer, liefert clientIP immer den TCP-Quell-Host.
func clientIP(r *http.Request, trusted []string, headerOrder []string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	nets := parseTrustedNets(trusted)
	if len(nets) == 0 || !isTrustedIP(host, nets) {
		return host
	}

	for _, headerName := range headerOrder {
		v := r.Header.Get(headerName)
		if v == "" {
			continue
		}
		if strings.EqualFold(headerName, "X-Forwarded-For") {
			return firstUntrustedFromRight(v, nets)
		}
		return strings.TrimSpace(v)
	}

	return host
}

// firstUntrustedFromRight zerlegt eine X-Forwarded-For-Hop-Kette ("client, proxy1, proxy2",
// ältester Eintrag links) und liefert den rechtesten Eintrag, der NICHT in nets liegt — den
// letzten Hop vor dem ersten uns bekannten (vertrauenswürdigen) Proxy. Sind alle Einträge
// vertrauenswürdig, wird der linkeste (älteste, ursprüngliche) Eintrag zurückgegeben.
func firstUntrustedFromRight(xff string, nets []*net.IPNet) string {
	parts := strings.Split(xff, ",")
	trimmed := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			trimmed = append(trimmed, p)
		}
	}
	if len(trimmed) == 0 {
		return ""
	}
	for i := len(trimmed) - 1; i >= 0; i-- {
		if !isTrustedIP(trimmed[i], nets) {
			return trimmed[i]
		}
	}
	return trimmed[0]
}

// AccessLog liefert Middleware, die next umschließt und pro Request genau eine Zeile im
// NGINX-combined-Format nach out schreibt — Grundlage für CrowdSecs crowdsecurity/nginx-Parser
// (Bruteforce-/Probing-Erkennung über wiederholte 401/404). remote_user ist IMMER "-": der
// Server nutzt ein einziges statisches API-Token ohne Benutzer-Konzept, und ein Token-Wert
// darf niemals im Log landen. trusted und headerOrder werden unverändert an clientIP
// durchgereicht.
func AccessLog(out io.Writer, trusted, headerOrder []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()

		next.ServeHTTP(rec, r)

		ip := clientIP(r, trusted, headerOrder)
		referer := r.Referer()
		ua := r.UserAgent()
		fmt.Fprintf(out, "%s - %s [%s] \"%s %s %s\" %d %d \"%s\" \"%s\"\n",
			ip, "-", start.Format("02/Jan/2006:15:04:05 -0700"),
			r.Method, r.RequestURI, r.Proto, rec.status, rec.bytes, referer, ua)
	})
}

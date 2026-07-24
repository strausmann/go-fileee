// Package fileee ist ein Client für das interne Web-App-API von my.fileee.com.
//
// Fileee bietet kein offizielles, öffentlich dokumentiertes API. Dieses Paket spricht das interne
// API der Web-App an, wie es aus Mitschnitten einer eingeloggten Session rekonstruiert wurde. Es
// gibt keine Stabilitätsgarantie: Endpunkte oder Feldformate können sich jederzeit ändern.
//
// # Authentifizierung
//
// Fileee nutzt reine Cookie-Authentifizierung (kein Bearer-Token). Der Login schickt
// Benutzername, Passwort und – bei aktivierter Zwei-Faktor-Authentifizierung – einen aus dem
// TOTP-Seed erzeugten Code im selben Request. Für schreibende Requests wird zusätzlich ein
// CSRF-Header aus dem Session-Cookie gesetzt. Der Client verwaltet Cookie-Jar, CSRF-Header und
// automatische Re-Authentifizierung selbst; der Aufrufer ruft nur EnsureSession auf (oder lässt
// den ersten Service-Aufruf das übernehmen).
//
// Credentials gehören nicht in den Code. Der TOTPSeed bleibt leer, wenn das Konto kein
// Zwei-Faktor hat.
//
// # Schonender Betrieb
//
// Da das API nicht für Automatisierung ausgelegt ist, drosselt der Client Requests per Token-Bucket
// und macht bei 429/5xx exponentielles Backoff. Für Synchronisation den inkrementellen Diff dem
// wiederholten vollen Query vorziehen.
//
// # Beispiel
//
//	package main
//
//	import (
//		"context"
//		"fmt"
//		"log"
//
//		"github.com/strausmann/go-fileee/fileee"
//	)
//
//	func main() {
//		client, err := fileee.New(fileee.Credentials{
//			Username: "user@example.com",
//			Password: "geheim",
//			TOTPSeed: "", // Base32-Seed, falls Zwei-Faktor aktiv ist
//		})
//		if err != nil {
//			log.Fatal(err)
//		}
//
//		ctx := context.Background()
//		if err := client.EnsureSession(ctx); err != nil {
//			log.Fatal(err)
//		}
//
//		res, err := client.Documents.Search(ctx, "Rechnung", fileee.SearchOptions{Limit: 20})
//		if err != nil {
//			log.Fatal(err)
//		}
//		for _, id := range res.IDs {
//			doc, err := client.Documents.Get(ctx, id)
//			if err != nil {
//				log.Fatal(err)
//			}
//			fmt.Println(doc.ID)
//		}
//	}
//
// # Konfiguration
//
// New nimmt Option-Funktionen entgegen: WithBaseURL, WithHTTPClient, WithSessionStore,
// WithRateLimit, WithBackoff, WithLogger und WithUserAgent. Ohne Optionen gelten sinnvolle
// Defaults (my.fileee.com als Basis-URL, konservatives Rate-Limit, Session-Cache im Nutzerprofil).
package fileee

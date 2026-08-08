// Package fileee ist ein Client für das interne Web-App-API von my.fileee.com.
//
// Fileee bietet kein offizielles, öffentlich dokumentiertes API. Dieses Paket spricht das interne
// API der Web-App an, wie es aus Mitschnitten einer eingeloggten Session rekonstruiert wurde. Es
// gibt keine Stabilitätsgarantie: Endpunkte oder Feldformate können sich jederzeit ändern, und
// Fileee kann das API jederzeit ohne Ankündigung anpassen.
//
// Der [Client] deckt Dokumente (Query/Diff/Get/Update/Upload/Download/Search/Share/Export/OCR),
// die generischen Ressourcen Tags/Companies/DocumentTypes/DocumentTypeSchemes (read-only), sowie
// Contacts und Reminders (inkl. Create/Update/Delete), Boxen und Konversationen (Chat, Teilen,
// Einladungen) ab. Für anonyme Empfänger eines Freigabe-Links gibt es den credential-losen
// [ShareClient].
//
// # Fertiger Server statt Library
//
// Wer keinen eigenen Go-Code schreiben möchte, kann fileee-server verwenden — einen deploybaren
// REST-API-/Docker-Server um diese Library (Huma v2, OpenAPI 3.1, statisches Bearer-Token,
// Container-Image auf GHCR/Docker Hub), gedacht für N8N-/CI-/Automatisierungs-Anbindung:
// https://github.com/strausmann/fileee-server
//
// # Authentifizierung
//
// Fileee nutzt reine Cookie-Authentifizierung (kein Bearer-/Refresh-Token). Der Login schickt
// Benutzername, Passwort und – bei aktivierter Zwei-Faktor-Authentifizierung – einen aus dem
// TOTP-Seed erzeugten Code im selben Request. Für schreibende Requests wird zusätzlich ein
// CSRF-Header (x-xsrf-token, Double-Submit-Cookie) aus dem Session-Cookie gesetzt. Der Client
// verwaltet Cookie-Jar, CSRF-Header und automatische Re-Authentifizierung (inkl. Retry bei
// HTTP 403) selbst; der Aufrufer ruft nur EnsureSession auf (oder lässt den ersten Service-Aufruf
// das übernehmen).
//
// Credentials gehören nicht in den Code. Der TOTPSeed bleibt leer, wenn das Konto kein
// Zwei-Faktor hat.
//
// # Destruktive Operationen
//
// Documents.Delete, Contacts.Delete und Reminders.Delete führen ein echtes, serverseitiges
// Hard-DELETE ohne Papierkorb aus — es gibt keinen serverseitigen Bestätigungsschritt. Die Lib
// bietet sie bewusst als Opt-in an (kein automatischer Aufrufpfad); Aufrufer müssen selbst dafür
// sorgen, dass sie nicht versehentlich ausgelöst werden. Der Endpunkt revision-lock ist dagegen
// bewusst NICHT implementiert, da er ein Dokument in einer Live-Verifikation serverseitig
// unserialisierbar gemacht hat.
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
//		"os"
//
//		"github.com/strausmann/go-fileee/fileee"
//	)
//
//	func main() {
//		// Credentials aus einer Secret-Quelle laden, nie hartkodieren.
//		client, err := fileee.NewClient(fileee.Credentials{
//			Username: os.Getenv("FILEEE_USERNAME"),
//			Password: os.Getenv("FILEEE_PASSWORD"),
//			TOTPSeed: os.Getenv("FILEEE_TOTP_SEED"), // Base32-Seed, falls Zwei-Faktor aktiv ist
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
// NewClient nimmt Option-Funktionen entgegen: WithBaseURL, WithStaticBaseURL, WithHTTPClient,
// WithSessionStore, WithSessionFreshness, WithRateLimit, WithBackoff, WithLogger und
// WithUserAgent. Ohne Optionen gelten sinnvolle Defaults (my.fileee.com als Basis-URL,
// static.fileee.com als Static-Host für Freigaben, konservatives Rate-Limit von 1 Request/s mit
// Burst 3, Session-Cache im Nutzerprofil, kein Freshness-Fenster). Für einen selbst übergebenen
// *http.Client via WithHTTPClient mischt sich die Lib nicht in dessen Timeout/Transport ein; ohne
// eigenen Client setzt sie lediglich einen ResponseHeaderTimeout von 30s, damit lange
// Uploads/ZIP-Exports nicht durch ein pauschales Gesamt-Timeout abgeschnitten werden.
package fileee

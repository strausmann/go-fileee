package fileee

// Version ist die Release-Version der go-fileee-Library (SemVer, ohne "v"-Präfix). Wird für den
// User-Agent genutzt, damit die Fileee-Server-Logs Client-Name + Version sehen. Beim Taggen eines
// Releases (git tag vX.Y.Z) diese Konstante mit hochziehen.
const Version = "0.1.0"

// projectURL identifiziert die Library gegenüber dem Fileee-Server (Transparenz: kein anonymer
// Go-http-client, sondern ein benennbarer, kontaktierbarer Client — wichtig, weil das API
// reverse-engineered ist).
const projectURL = "https://github.com/strausmann/go-fileee"

// defaultUserAgent ist der User-Agent, den die Lib ohne WithUserAgent sendet.
func defaultUserAgent() string {
	return "go-fileee/" + Version + " (+" + projectURL + ")"
}

// composeUserAgent hängt die Lib-Kennung an einen optionalen Konsumenten-User-Agent an. Ein
// Scanner/CLI, das WithUserAgent("meintool/1.2") setzt, erscheint als
// "meintool/1.2 go-fileee/0.1.0 (+https://github.com/strausmann/go-fileee)" — Fileee sieht beide.
func composeUserAgent(consumer string) string {
	if consumer == "" {
		return defaultUserAgent()
	}
	return consumer + " " + defaultUserAgent()
}

package fileee

// Version ist die Release-Version der Library (SemVer ohne "v"-Präfix). Beim Taggen eines Releases
// mit hochziehen.
const Version = "0.1.0"

const projectURL = "https://github.com/strausmann/go-fileee"

func defaultUserAgent() string {
	return "go-fileee/" + Version + " (+" + projectURL + ")"
}

// composeUserAgent stellt einen optionalen Konsumenten-User-Agent der Lib-Kennung voran.
func composeUserAgent(consumer string) string {
	if consumer == "" {
		return defaultUserAgent()
	}
	return consumer + " " + defaultUserAgent()
}

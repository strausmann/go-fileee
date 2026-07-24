package fileee

import (
	"net/url"
	"strings"
)

// LinkKind unterscheidet die beiden Fileee-Dokument-Linkarten, die ParseDocumentLink erkennt.
type LinkKind int

const (
	// LinkKindUnknown = die URL passt auf keines der bekannten Muster.
	LinkKindUnknown LinkKind = iota
	// LinkKindInternal = interner Login-Link (…/documents/<documentId>) — Zugriff authentifiziert.
	LinkKindInternal
	// LinkKindShared = anonymer Share-Link (…/shared/<shareToken>) — Zugriff ohne Login.
	LinkKindShared
)

// String liefert die Kleinbuchstaben-Bezeichnung ("internal"/"shared"/"unknown").
func (k LinkKind) String() string {
	switch k {
	case LinkKindInternal:
		return "internal"
	case LinkKindShared:
		return "shared"
	default:
		return "unknown"
	}
}

// ParseDocumentLink erkennt anhand der URL, ob ein Fileee-Link auf ein internes Dokument
// (`…/documents/<documentId>`, authentifizierter Zugriff) oder auf eine anonyme Freigabe
// (`…/shared/<shareToken>`, Zugriff ohne Login) zeigt, und liefert die documentId bzw. den
// Share-Token. Für nicht erkannte Eingaben (leere/bloße ID, unbekannter Pfad) gibt sie
// (LinkKindUnknown, "") zurück — die Linkart lässt sich dann nicht bestimmen. Query/Fragment und
// abschließende Slashes werden ignoriert. Hinweis: shareToken == shareId, aber ungleich der
// documentId desselben Dokuments.
func ParseDocumentLink(link string) (LinkKind, string) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return LinkKindUnknown, ""
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(segs); i++ {
		switch segs[i] {
		case "documents":
			if segs[i+1] != "" {
				return LinkKindInternal, segs[i+1]
			}
		case "shared":
			if segs[i+1] != "" {
				return LinkKindShared, segs[i+1]
			}
		}
	}
	return LinkKindUnknown, ""
}

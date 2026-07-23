package fileee

// DocumentService kapselt die zentrale Ressource (Umbrella-Spec §3.4, vollständige
// Query/Diff/Get/Update/Upload/Download-Implementierung folgt in Task 15/16). Dieser Platzhalter
// existiert AUSSCHLIESSLICH, damit Client (Task 11) bereits jetzt vollständig verdrahtet werden
// kann — Task 11 referenziert *DocumentService und newDocumentService als Feldtyp/Konstruktor,
// die eigentliche Implementierung liegt aber außerhalb dieses Batches (Tasks 11-14).
type DocumentService struct {
	client *Client
}

func newDocumentService(c *Client) *DocumentService {
	return &DocumentService{client: c}
}

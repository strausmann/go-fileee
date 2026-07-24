package fileee

import "encoding/json"

// SchemaField beschreibt ein einzelnes Metadatenfeld eines Dokumenttyp-Schemas. Verschachtelte
// Gruppen (z. B. amount) tragen ihre Unterfelder in ComposingTypes.
type SchemaField struct {
	Key            string            `json:"key"`
	ConcreteType   string            `json:"concreteType"`
	Dispensable    bool              `json:"dispensable"`
	Hidden         bool              `json:"hidden"`
	ReadOnly       bool              `json:"readOnly"`
	ServerOnly     bool              `json:"serverOnly"`
	Constraints    []json.RawMessage `json:"constraints"`
	ComposingTypes []SchemaField     `json:"composingTypes"`
}

// DocumentTypeScheme ist das Feld-Schema eines Dokumenttyps (Ressource document-type-schemes,
// verknüpft mit DocumentType über documentTypeScheme). Die eigentlichen Felder stehen unter
// SchemaDefinition.ComposingTypes und sind bequem über Fields() erreichbar.
type DocumentTypeScheme struct {
	ID               string                     `json:"id"`
	Version          int64                      `json:"version"`
	Created          string                     `json:"created"`
	Modified         string                     `json:"modified"`
	Deleted          bool                       `json:"deleted"`
	I18nDictionary   map[string]json.RawMessage `json:"i18nDictionary"`
	SchemaDefinition SchemaField                `json:"schemaDefinition"`
}

// Fields liefert die Metadatenfelder des Dokumenttyps (die Unterfelder der Schema-Wurzel).
func (s DocumentTypeScheme) Fields() []SchemaField {
	return s.SchemaDefinition.ComposingTypes
}

func newDocumentTypeSchemeService(c *Client) ReadService[DocumentTypeScheme] {
	return &restService[DocumentTypeScheme]{client: c, resourcePath: "document-type-schemes"}
}

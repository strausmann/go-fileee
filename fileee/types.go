package fileee

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Operator ist die vollständige Query-DSL-Operator-Enum
// (io.fileee.shared.storage.query.Operator, 21 Werte, API.md §6.2).
type Operator string

const (
	OpAfter        Operator = "AFTER"
	OpBefore       Operator = "BEFORE"
	OpBiggerEqual  Operator = "BIGGER_EQUAL"
	OpBigger       Operator = "BIGGER"
	OpEQ           Operator = "EQ"
	OpNEQ          Operator = "NEQ"
	OpLike         Operator = "LIKE"
	OpFuzzy        Operator = "FUZZY"
	OpSmallerEqual Operator = "SMALLER_EQUAL"
	OpSmaller      Operator = "SMALLER"
	OpHas          Operator = "HAS"
	OpHasAny       Operator = "HAS_ANY"
	OpHasNone      Operator = "HAS_NONE"
	OpNotIn        Operator = "NOT_IN"
	OpIn           Operator = "IN"
	OpHasAll       Operator = "HAS_ALL"
	OpExists       Operator = "EXISTS"
	OpDoesNotExist Operator = "DOES_NOT_EXIST"
	OpOr           Operator = "OR"
	OpAnd          Operator = "AND"
	OpHasElements  Operator = "HAS_ELEMENTS"
)

// PublicDocumentStatus ist der reale Verarbeitungsstatus eines Dokuments
// (io.fileee.shared.enums.PublicDocumentStatus, 11 Werte, API.md §6.1).
type PublicDocumentStatus string

const (
	StatusUploading          PublicDocumentStatus = "UPLOADING"
	StatusIP                 PublicDocumentStatus = "IP"
	StatusOCR                PublicDocumentStatus = "OCR"
	StatusAnalysing          PublicDocumentStatus = "ANALYSING"
	StatusClassified         PublicDocumentStatus = "CLASSIFIED"
	StatusDone               PublicDocumentStatus = "DONE"
	StatusDeleted            PublicDocumentStatus = "DELETED"
	StatusDeletedPermanently PublicDocumentStatus = "DELETED_PERMANENTLY"
	StatusError              PublicDocumentStatus = "ERROR"
	StatusLocal              PublicDocumentStatus = "LOCAL"
	StatusNew                PublicDocumentStatus = "NEW"
)

// PDFMode steuert GET /api/v1/documents/:id/pdf?mode=... (API.md §4.1, vollständig belegt).
type PDFMode string

const (
	PDFModeDownload PDFMode = "download"
	PDFModePrint    PDFMode = "print"
)

// ImageSize steuert GET /api/v1/pages/:id/image?size=... (API.md §4.1, vollständig belegt).
type ImageSize string

const (
	ImageSizeSmedium ImageSize = "smedium"
	ImageSizeMedium  ImageSize = "medium"
)

// ContactType (io.fileee.shared.domain.dtos.ContactType, API.md §6.4).
type ContactType string

const (
	ContactTypeMe      ContactType = "ME"
	ContactTypeCompany ContactType = "COMPANY"
	ContactTypePerson  ContactType = "PERSON"
)

// ContactStatus (io.fileee.shared.domain.dtos.ContactStatus, API.md §6.5).
type ContactStatus string

const (
	ContactStatusManaged ContactStatus = "MANAGED"
	ContactStatusLinked  ContactStatus = "LINKED"
	ContactStatusCustom  ContactStatus = "CUSTOM"
)

// DocumentAction sind UI-Aktionen auf Dokumenten, u.a. in Document.ForbiddenActions
// (io.fileee.shared.enums.DocumentAction, 15 Werte, API.md §6.3).
type DocumentAction string

const (
	ActionMerge        DocumentAction = "MERGE"
	ActionDelete       DocumentAction = "DELETE"
	ActionSplit        DocumentAction = "SPLIT"
	ActionDownload     DocumentAction = "DOWNLOAD"
	ActionShare        DocumentAction = "SHARE"
	ActionExport       DocumentAction = "EXPORT"
	ActionExtractPages DocumentAction = "EXTRACT_PAGES"
	ActionEdit         DocumentAction = "EDIT"
	ActionEditReminder DocumentAction = "EDIT_REMINDER"
	ActionEditTags     DocumentAction = "EDIT_TAGS"
	ActionRotatePages  DocumentAction = "ROTATE_PAGES"
	ActionDeletePages  DocumentAction = "DELETE_PAGES"
	ActionReorderPages DocumentAction = "REORDER_PAGES"
	ActionView         DocumentAction = "VIEW"
	ActionRevisionLock DocumentAction = "REVISION_LOCK"
)

// CrudOperation ist das generische CRUD-Enum, u.a. für SSE-Push-Events (API.md §6.6).
// In dieser Lib-Version nicht aktiv genutzt (Push/SSE ist nicht Scope von Sub-Projekt A),
// aber Teil des belegten Wire-Vokabulars und daher hier bewusst mitgeführt.
type CrudOperation string

const (
	CrudCreate CrudOperation = "CREATE"
	CrudRead   CrudOperation = "READ"
	CrudUpdate CrudOperation = "UPDATE"
	CrudDelete CrudOperation = "DELETE"
)

// Document ist die zentrale Ressource (API.md §4.1). Attributes wird NICHT direkt von
// encoding/json behandelt (json:"-"), sondern über Document.UnmarshalJSON/MarshalJSON aus dem
// verschachtelten Pfad attributes.data gelesen/geschrieben (siehe unten).
type Document struct {
	ID               string               `json:"id"`
	Version          int64                `json:"version"`
	Created          time.Time            `json:"created"`
	Modified         time.Time            `json:"modified"`
	Deleted          bool                 `json:"deleted"`
	Status           PublicDocumentStatus `json:"status"`
	Type             string               `json:"type"`
	Pages            []Page               `json:"pages"`
	Attributes       DocumentAttributes   `json:"-"`
	UploadAttribute  UploadAttribute      `json:"uploadAttribute"`
	SharedSpaceIDs   []string             `json:"sharedSpaceIds"`
	ForbiddenActions []DocumentAction     `json:"forbiddenActions"`
}

// documentAttributesEnvelope spiegelt den verschachtelten Wire-Pfad {"attributes":{"data":{...}}}.
type documentAttributesEnvelope struct {
	Data DocumentAttributes `json:"data"`
}

func (d *Document) UnmarshalJSON(data []byte) error {
	type shadow Document
	aux := struct {
		Attributes documentAttributesEnvelope `json:"attributes"`
		*shadow
	}{shadow: (*shadow)(d)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("fileee: document decode: %w", err)
	}
	d.Attributes = aux.Attributes.Data
	return nil
}

func (d Document) MarshalJSON() ([]byte, error) {
	type shadow Document
	aux := struct {
		Attributes documentAttributesEnvelope `json:"attributes"`
		shadow
	}{
		Attributes: documentAttributesEnvelope{Data: d.Attributes},
		shadow:     shadow(d),
	}
	return json.Marshal(aux)
}

// Page ist eine einzelne Dokumentseite (API.md §4.1) — imageVersion/contentVersion steuern den
// Bild-Download und müssen IMMER frisch aus dem zuletzt geladenen pages[]-Array kommen, nicht
// gecacht werden (Skill-Troubleshooting "Seiten-Bild fehlt oder ist falsch aufgelöst").
//
// ImageVersion/ContentVersion sind bewusst flexInt64 statt int64: openapi.json deklariert
// DocumentPage.imageVersion/contentVersion als ["string","integer"] (Wire liefert BEIDE
// Varianten). Ein reiner int64-Feldtyp lässt encoding/json bei einer JSON-String-Zeile mit einem
// Typfehler abbrechen — bei restService.Query/Diff (service.go) reißt das den KOMPLETTEN Batch
// mit, nicht nur die eine betroffene Zeile (Whole-Branch-Review-Finding, Export-Abbruch-Bug).
type Page struct {
	ID             string    `json:"id"`
	ImageVersion   flexInt64 `json:"imageVersion"`
	ContentVersion flexInt64 `json:"contentVersion"`
}

// flexInt64 dekodiert einen Ganzzahlwert, der im Wire-Format als JSON-Zahl ODER als JSON-String
// kommen kann (openapi.json DocumentPage: imageVersion/contentVersion = ["string","integer"]).
// Ein leerer String dekodiert defensiv zu 0 (ADR-0003: reverse-engineertes API liefert nicht immer
// den erwarteten Wert) statt einen Fehler zu werfen — jeder andere, nicht-numerische String bleibt
// ein Fehler, damit echter Datenmüll nicht still verschluckt wird.
type flexInt64 int64

func (v *flexInt64) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*v = 0
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("fileee: flexInt64 string decode: %w", err)
		}
		if s == "" {
			*v = 0
			return nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("fileee: flexInt64 string parse %q: %w", s, err)
		}
		*v = flexInt64(n)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("fileee: flexInt64 number decode: %w", err)
	}
	*v = flexInt64(n)
	return nil
}

func (v flexInt64) MarshalJSON() ([]byte, error) {
	return json.Marshal(int64(v))
}

// rawAttribute deckt alle vier Attribute-Varianten aus API.md §5 strukturell ab (Feldnamen sind
// ein Superset — nicht jede Variante befüllt jedes Feld).
type rawAttribute struct {
	Value          json.RawMessage            `json:"value"`
	Modified       string                     `json:"modified"`
	Source         string                     `json:"source"`
	Type           string                     `json:"type"`
	ContainedType  string                     `json:"containedType"`
	EnumClassName  string                     `json:"enumClassName"`
	AttributeGroup string                     `json:"attributeGroup"`
	Data           map[string]json.RawMessage `json:"data"`
}

func decodeStringValue(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func decodeBoolValue(raw json.RawMessage) *bool {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil
	}
	return &b
}

func decodeIntValue(raw json.RawMessage) int {
	var f float64
	_ = json.Unmarshal(raw, &f)
	return int(f)
}

func decodeStringSliceValue(raw json.RawMessage) []string {
	var s []string
	_ = json.Unmarshal(raw, &s)
	return s
}

func decodeTimeValue(raw json.RawMessage) *time.Time {
	s := decodeStringValue(raw)
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

func decodeMoneyGroup(data map[string]json.RawMessage) *Money {
	if data == nil {
		return nil
	}
	m := &Money{}
	if v, ok := data["currency"]; ok {
		m.Currency = decodeStringValue(v)
	}
	if v, ok := data["value"]; ok {
		var f float64
		_ = json.Unmarshal(v, &f)
		m.Value = f
	}
	return m
}

func decodeBankAccountGroup(data map[string]json.RawMessage) *BankAccount {
	if data == nil {
		return nil
	}
	b := &BankAccount{}
	if v, ok := data["iban"]; ok {
		b.IBAN = decodeStringValue(v)
	}
	if v, ok := data["bic"]; ok {
		b.BIC = decodeStringValue(v)
	}
	if v, ok := data["bank"]; ok {
		b.Bank = decodeStringValue(v)
	}
	if v, ok := data["account_holder"]; ok {
		b.AccountHolder = decodeStringValue(v)
	}
	return b
}

// DocumentAttributes bildet attributes.data ab (API.md §5). Unbekannte Schlüssel werden NICHT
// verworfen (ADR-0003) — RawExtra hält sie unverändert als json.RawMessage.
type DocumentAttributes struct {
	Title            string
	DocumentTypeID   string
	TagIDs           []string
	SenderID         string
	ReceiverID       string
	InvoiceDate      *time.Time
	IssueDate        *time.Time
	InvoiceDueDate   *time.Time
	InvoiceID        string
	Amount           *Money
	GrossIncome      *Money
	NetIncome        *Money
	CustomerID       string
	BankAccount1     *BankAccount
	PaymentReference string
	Payed            *bool
	ContentLanguage  string
	TotalPageCount   int
	MaxPageNr        int
	Read             *bool
	Reviewed         *bool
	Secured          *bool
	RawExtra         map[string]json.RawMessage
}

func (d *DocumentAttributes) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("fileee: attributes.data decode: %w", err)
	}
	*d = DocumentAttributes{RawExtra: map[string]json.RawMessage{}}
	for key, val := range raw {
		var wrapper rawAttribute
		if err := json.Unmarshal(val, &wrapper); err != nil {
			d.RawExtra[key] = val
			continue
		}
		switch key {
		case "title":
			d.Title = decodeStringValue(wrapper.Value)
		case "documentTypeId":
			d.DocumentTypeID = decodeStringValue(wrapper.Value)
		case "senderId":
			d.SenderID = decodeStringValue(wrapper.Value)
		case "receiverId":
			d.ReceiverID = decodeStringValue(wrapper.Value)
		case "invoiceId":
			d.InvoiceID = decodeStringValue(wrapper.Value)
		case "paymentReference":
			d.PaymentReference = decodeStringValue(wrapper.Value)
		case "contentLanguage":
			d.ContentLanguage = decodeStringValue(wrapper.Value)
		case "customerId":
			d.CustomerID = decodeStringValue(wrapper.Value)
		case "tagIds":
			d.TagIDs = decodeStringSliceValue(wrapper.Value)
		case "invoiceDate":
			d.InvoiceDate = decodeTimeValue(wrapper.Value)
		case "issueDate":
			d.IssueDate = decodeTimeValue(wrapper.Value)
		case "invoiceDueDate":
			d.InvoiceDueDate = decodeTimeValue(wrapper.Value)
		case "read":
			d.Read = decodeBoolValue(wrapper.Value)
		case "reviewed":
			d.Reviewed = decodeBoolValue(wrapper.Value)
		case "secured":
			d.Secured = decodeBoolValue(wrapper.Value)
		case "payed":
			d.Payed = decodeBoolValue(wrapper.Value)
		case "totalPageCount":
			d.TotalPageCount = decodeIntValue(wrapper.Value)
		case "maxPageNr":
			d.MaxPageNr = decodeIntValue(wrapper.Value)
		case "amount":
			d.Amount = decodeMoneyGroup(wrapper.Data)
		case "grossIncome":
			d.GrossIncome = decodeMoneyGroup(wrapper.Data)
		case "netIncome":
			d.NetIncome = decodeMoneyGroup(wrapper.Data)
		case "bankAccount1":
			d.BankAccount1 = decodeBankAccountGroup(wrapper.Data)
		default:
			d.RawExtra[key] = val
		}
	}
	return nil
}

func setSimpleString(out map[string]json.RawMessage, key, value string) {
	if value == "" {
		return
	}
	b, err := json.Marshal(struct {
		Value string `json:"value"`
		Type  string `json:"type"`
	}{value, "String"})
	if err == nil {
		out[key] = b
	}
}

func setSimpleBool(out map[string]json.RawMessage, key string, value *bool) {
	if value == nil {
		return
	}
	b, err := json.Marshal(struct {
		Value bool   `json:"value"`
		Type  string `json:"type"`
	}{*value, "Boolean"})
	if err == nil {
		out[key] = b
	}
}

func setSimpleInt(out map[string]json.RawMessage, key string, value int) {
	if value == 0 {
		return
	}
	b, err := json.Marshal(struct {
		Value int    `json:"value"`
		Type  string `json:"type"`
	}{value, "String"})
	if err == nil {
		out[key] = b
	}
}

func setSimpleTime(out map[string]json.RawMessage, key string, value *time.Time) {
	if value == nil {
		return
	}
	b, err := json.Marshal(struct {
		Value string `json:"value"`
		Type  string `json:"type"`
	}{value.Format(time.RFC3339), "String"})
	if err == nil {
		out[key] = b
	}
}

func setListStrings(out map[string]json.RawMessage, key string, value []string) {
	if len(value) == 0 {
		return
	}
	b, err := json.Marshal(struct {
		Value         []string `json:"value"`
		ContainedType string   `json:"containedType"`
		Type          string   `json:"type"`
	}{value, "String", "List"})
	if err == nil {
		out[key] = b
	}
}

func setGroupMoney(out map[string]json.RawMessage, key string, value *Money) {
	if value == nil {
		return
	}
	b, err := json.Marshal(struct {
		AttributeGroup string         `json:"attributeGroup"`
		Data           map[string]any `json:"data"`
		Type           string         `json:"type"`
	}{key, map[string]any{"currency": value.Currency, "value": value.Value}, "AttributeGroup"})
	if err == nil {
		out[key] = b
	}
}

func setGroupBankAccount(out map[string]json.RawMessage, key string, value *BankAccount) {
	if value == nil {
		return
	}
	b, err := json.Marshal(struct {
		AttributeGroup string         `json:"attributeGroup"`
		Data           map[string]any `json:"data"`
		Type           string         `json:"type"`
	}{key, map[string]any{"iban": value.IBAN, "bic": value.BIC, "bank": value.Bank, "account_holder": value.AccountHolder}, "AttributeGroup"})
	if err == nil {
		out[key] = b
	}
}

func (d DocumentAttributes) MarshalJSON() ([]byte, error) {
	out := map[string]json.RawMessage{}
	for k, v := range d.RawExtra {
		out[k] = v
	}
	setSimpleString(out, "title", d.Title)
	setSimpleString(out, "documentTypeId", d.DocumentTypeID)
	setSimpleString(out, "senderId", d.SenderID)
	setSimpleString(out, "receiverId", d.ReceiverID)
	setSimpleString(out, "invoiceId", d.InvoiceID)
	setSimpleString(out, "paymentReference", d.PaymentReference)
	setSimpleString(out, "contentLanguage", d.ContentLanguage)
	setSimpleString(out, "customerId", d.CustomerID)
	setListStrings(out, "tagIds", d.TagIDs)
	setSimpleTime(out, "invoiceDate", d.InvoiceDate)
	setSimpleTime(out, "issueDate", d.IssueDate)
	setSimpleTime(out, "invoiceDueDate", d.InvoiceDueDate)
	setSimpleBool(out, "read", d.Read)
	setSimpleBool(out, "reviewed", d.Reviewed)
	setSimpleBool(out, "secured", d.Secured)
	setSimpleBool(out, "payed", d.Payed)
	setSimpleInt(out, "totalPageCount", d.TotalPageCount)
	setSimpleInt(out, "maxPageNr", d.MaxPageNr)
	setGroupMoney(out, "amount", d.Amount)
	setGroupMoney(out, "grossIncome", d.GrossIncome)
	setGroupMoney(out, "netIncome", d.NetIncome)
	setGroupBankAccount(out, "bankAccount1", d.BankAccount1)
	return json.Marshal(out)
}

type Money struct {
	Value    float64 `json:"value"`
	Currency string  `json:"currency"`
}

type BankAccount struct {
	IBAN          string `json:"iban"`
	BIC           string `json:"bic"`
	Bank          string `json:"bank"`
	AccountHolder string `json:"account_holder"`
}

type UploadAttribute struct {
	OriginalFileName string                     `json:"originalFileName"`
	OriginalFileType string                     `json:"originalFileType"`
	SourceName       string                     `json:"sourceName"`
	UploadDate       string                     `json:"uploadDate"`
	NewUpload        bool                       `json:"newUpload"`
	UploadMetaData   map[string]json.RawMessage `json:"uploadMetaData"`
}

type Tag struct {
	ID              string `json:"id"`
	Version         int64  `json:"version"`
	Created         string `json:"created"`
	Modified        string `json:"modified"`
	Deleted         bool   `json:"deleted"`
	Name            string `json:"name"`
	ColorCode       string `json:"colorCode"`
	DocumentCounter int    `json:"documentCounter"`
	LastAdded       string `json:"lastAdded"`
}

// CompanyAttributes bildet company.attributes.data ab (API.md §4.5) — ausschließlich
// Listen-Attribute im aktuellen Scope, Rest bleibt in RawExtra (ADR-0003).
type CompanyAttributes struct {
	IBANs        []string
	VATIDs       []string
	Emails       []string
	PhoneNumbers []string
	Websites     []string
	GermanTaxIDs []string
	RawExtra     map[string]json.RawMessage
}

func (c *CompanyAttributes) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("fileee: company attributes.data decode: %w", err)
	}
	*c = CompanyAttributes{RawExtra: map[string]json.RawMessage{}}
	for key, val := range raw {
		var wrapper rawAttribute
		if err := json.Unmarshal(val, &wrapper); err != nil {
			c.RawExtra[key] = val
			continue
		}
		switch key {
		case "ibans":
			c.IBANs = decodeStringSliceValue(wrapper.Value)
		case "vatIds":
			c.VATIDs = decodeStringSliceValue(wrapper.Value)
		case "emails":
			c.Emails = decodeStringSliceValue(wrapper.Value)
		case "phoneNumbers":
			c.PhoneNumbers = decodeStringSliceValue(wrapper.Value)
		case "websites":
			c.Websites = decodeStringSliceValue(wrapper.Value)
		case "germanTaxIds":
			c.GermanTaxIDs = decodeStringSliceValue(wrapper.Value)
		default:
			c.RawExtra[key] = val
		}
	}
	return nil
}

func (c CompanyAttributes) MarshalJSON() ([]byte, error) {
	out := map[string]json.RawMessage{}
	for k, v := range c.RawExtra {
		out[k] = v
	}
	setListStrings(out, "ibans", c.IBANs)
	setListStrings(out, "vatIds", c.VATIDs)
	setListStrings(out, "emails", c.Emails)
	setListStrings(out, "phoneNumbers", c.PhoneNumbers)
	setListStrings(out, "websites", c.Websites)
	setListStrings(out, "germanTaxIds", c.GermanTaxIDs)
	return json.Marshal(out)
}

// Company (API.md §4.5) — Attributes wird analog Document über attributes.data genestet.
type Company struct {
	ID              string            `json:"id"`
	Version         int64             `json:"version"`
	Created         string            `json:"created"`
	Modified        string            `json:"modified"`
	Deleted         bool              `json:"deleted"`
	CompanyName     string            `json:"companyName"`
	ContactType     string            `json:"contactType"`
	ContactStatus   string            `json:"contactStatus"`
	DocumentCounter int               `json:"documentCounter"`
	Connected       bool              `json:"connected"`
	FromUserDB      bool              `json:"fromUserDb"`
	Attributes      CompanyAttributes `json:"-"`
	HasLogo         bool              `json:"hasLogo"`
}

func (c *Company) UnmarshalJSON(data []byte) error {
	type shadow Company
	aux := struct {
		Attributes documentAttributesEnvelopeCompany `json:"attributes"`
		*shadow
	}{shadow: (*shadow)(c)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("fileee: company decode: %w", err)
	}
	c.Attributes = aux.Attributes.Data
	return nil
}

func (c Company) MarshalJSON() ([]byte, error) {
	type shadow Company
	aux := struct {
		Attributes documentAttributesEnvelopeCompany `json:"attributes"`
		shadow
	}{
		Attributes: documentAttributesEnvelopeCompany{Data: c.Attributes},
		shadow:     shadow(c),
	}
	return json.Marshal(aux)
}

type documentAttributesEnvelopeCompany struct {
	Data CompanyAttributes `json:"data"`
}

type Address struct {
	Street        string `json:"street"`
	SecondLine    string `json:"secondLine"`
	ZipCode       string `json:"zipCode"`
	City          string `json:"city"`
	State         string `json:"state"`
	Country       string `json:"country"`
	CountryLocale string `json:"countryLocale"`
}

// Contact (API.md §4.6) — flache Struktur, kein attributes.data-Wrapper.
type Contact struct {
	ID                   string        `json:"id"`
	Version              int64         `json:"version"`
	Created              string        `json:"created"`
	Modified             string        `json:"modified"`
	Deleted              bool          `json:"deleted"`
	CompanyID            string        `json:"companyId"`
	FirstName            string        `json:"firstName"`
	LastName             string        `json:"lastName"`
	CompanyName          string        `json:"companyName"`
	Email                string        `json:"email"`
	PhoneNumber          string        `json:"phoneNumber"`
	FaxNumber            string        `json:"faxNumber"`
	URL                  string        `json:"url"`
	SupportURL           string        `json:"supportURL"`
	UserPortalURL        string        `json:"userPortalURL"`
	Address              Address       `json:"address"`
	ContactType          ContactType   `json:"contactType"`
	ContactStatus        ContactStatus `json:"contactStatus"`
	ConnectedToOtherUser bool          `json:"connectedToOtherUser"`
	FromUserDB           bool          `json:"fromUserDb"`
	DocumentCounter      int           `json:"documentCounter"`
}

// DocumentType (API.md §4.7) — i18nDictionary bleibt roh, da serverseitig dynamisch (§9 Punkt g).
type DocumentType struct {
	ID                 string                     `json:"id"`
	Version            int64                      `json:"version"`
	Created            string                     `json:"created"`
	Modified           string                     `json:"modified"`
	Deleted            bool                       `json:"deleted"`
	I18NName           string                     `json:"i18NName"`
	I18nDictionary     map[string]json.RawMessage `json:"i18nDictionary"`
	DocumentTypeScheme string                     `json:"documentTypeScheme"`
	DocumentCounter    int                        `json:"documentCounter"`
}

// AccountStatus (API.md §2.9, GET /api/f/account-status — Domain-Form, siehe accountStatusWire
// in client.go für die Wire-Form-Übersetzung).
type AccountStatus struct {
	AccountTypeID      string
	SubscriptionName   string
	SubscriptionFreq   string
	SubscriptionAmount float64
	PayedUntil         *time.Time
	NextLicenseRefill  *time.Time
	Problem            string
}

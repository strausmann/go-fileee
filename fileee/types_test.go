package fileee

import (
	"encoding/json"
	"testing"
	"time"
)

func TestOperatorEnumIstVollstaendig(t *testing.T) {
	want := []Operator{
		OpAfter, OpBefore, OpBiggerEqual, OpBigger, OpEQ, OpNEQ, OpLike, OpFuzzy,
		OpSmallerEqual, OpSmaller, OpHas, OpHasAny, OpHasNone, OpNotIn, OpIn,
		OpHasAll, OpExists, OpDoesNotExist, OpOr, OpAnd, OpHasElements,
	}
	if len(want) != 21 {
		t.Fatalf("Testliste selbst unvollständig: %d von 21 Operatoren", len(want))
	}
	seen := map[Operator]bool{}
	for _, op := range want {
		if op == "" {
			t.Fatalf("Operator-Konstante ist leerer String")
		}
		seen[op] = true
	}
	if len(seen) != 21 {
		t.Fatalf("Operator-Konstanten sind nicht paarweise verschieden: %d eindeutige von 21", len(seen))
	}
	if OpEQ != "EQ" || OpIn != "IN" || OpNEQ != "NEQ" || OpOr != "OR" {
		t.Fatalf("aus Live-Traffic beobachtete Operator-Werte stimmen nicht: EQ=%q IN=%q NEQ=%q OR=%q", OpEQ, OpIn, OpNEQ, OpOr)
	}
}

func TestPublicDocumentStatusEnum(t *testing.T) {
	want := []PublicDocumentStatus{
		StatusUploading, StatusIP, StatusOCR, StatusAnalysing, StatusClassified, StatusDone,
		StatusDeleted, StatusDeletedPermanently, StatusError, StatusLocal, StatusNew,
	}
	if len(want) != 11 {
		t.Fatalf("PublicDocumentStatus: erwartet 11 Werte, Testliste hat %d", len(want))
	}
	if StatusNew != "NEW" || StatusDone != "DONE" {
		t.Fatalf("Enum-Werte weichen vom Core-SDK-Bytecode ab: NEW=%q DONE=%q", StatusNew, StatusDone)
	}
}

func TestPDFModeUndImageSizeSindBinaer(t *testing.T) {
	if PDFModeDownload != "download" || PDFModePrint != "print" {
		t.Fatalf("PDFMode-Werte weichen von imageHelper-*.js ab: download=%q print=%q", PDFModeDownload, PDFModePrint)
	}
	if ImageSizeSmedium != "smedium" || ImageSizeMedium != "medium" {
		t.Fatalf("ImageSize-Werte weichen von imageHelper.js ab: smedium=%q medium=%q", ImageSizeSmedium, ImageSizeMedium)
	}
}

func TestContactUndDocumentActionEnums(t *testing.T) {
	if ContactTypeMe != "ME" || ContactTypeCompany != "COMPANY" || ContactTypePerson != "PERSON" {
		t.Fatalf("ContactType-Werte falsch: %q %q %q", ContactTypeMe, ContactTypeCompany, ContactTypePerson)
	}
	if ContactStatusManaged != "MANAGED" || ContactStatusLinked != "LINKED" || ContactStatusCustom != "CUSTOM" {
		t.Fatalf("ContactStatus-Werte falsch")
	}
	actions := []DocumentAction{
		ActionMerge, ActionDelete, ActionSplit, ActionDownload, ActionShare, ActionExport,
		ActionExtractPages, ActionEdit, ActionEditReminder, ActionEditTags, ActionRotatePages,
		ActionDeletePages, ActionReorderPages, ActionView, ActionRevisionLock,
	}
	if len(actions) != 15 {
		t.Fatalf("DocumentAction: erwartet 15 Werte, Testliste hat %d", len(actions))
	}
}

func TestDocumentAttributesUnmarshalDecktAlleWrapperVariantenAb(t *testing.T) {
	raw := []byte(`{
		"title": {"value": "Testrechnung", "modified": "2026-01-01T00:00:00Z", "source": "manual", "type": "String"},
		"tagIds": {"value": ["tag-1", "tag-2"], "containedType": "String", "type": "List"},
		"senderId": {"value": "company-42", "type": "String"},
		"read": {"value": true, "type": "Boolean"},
		"amount": {"attributeGroup": "amount", "data": {"currency": "EUR", "value": 19.99}, "type": "AttributeGroup"},
		"bankAccount1": {"attributeGroup": "bankAccount1", "data": {"iban": "DE00TEST00000000000000", "bic": "TESTDEXXX", "bank": "Testbank", "account_holder": "Max Testmann"}, "type": "AttributeGroup"},
		"invoiceDate": {"value": "2026-01-02T00:00:00Z", "type": "String"},
		"totalPageCount": {"value": 3, "type": "String"},
		"autoProcessingStatus": {"value": "DONE", "enumClassName": "io.fileee.shared.enums.AutoProcessingStatus", "type": "Enum"}
	}`)

	var attrs DocumentAttributes
	if err := json.Unmarshal(raw, &attrs); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if attrs.Title != "Testrechnung" {
		t.Errorf("Title = %q, erwartet Testrechnung", attrs.Title)
	}
	if len(attrs.TagIDs) != 2 || attrs.TagIDs[0] != "tag-1" {
		t.Errorf("TagIDs = %v, erwartet [tag-1 tag-2]", attrs.TagIDs)
	}
	if attrs.SenderID != "company-42" {
		t.Errorf("SenderID = %q", attrs.SenderID)
	}
	if attrs.Read == nil || !*attrs.Read {
		t.Errorf("Read = %v, erwartet true", attrs.Read)
	}
	if attrs.Amount == nil || attrs.Amount.Currency != "EUR" || attrs.Amount.Value != 19.99 {
		t.Errorf("Amount = %+v, erwartet {19.99 EUR}", attrs.Amount)
	}
	if attrs.BankAccount1 == nil || attrs.BankAccount1.IBAN != "DE00TEST00000000000000" {
		t.Errorf("BankAccount1 = %+v", attrs.BankAccount1)
	}
	if attrs.InvoiceDate == nil || attrs.InvoiceDate.Year() != 2026 {
		t.Errorf("InvoiceDate = %v", attrs.InvoiceDate)
	}
	if attrs.TotalPageCount != 3 {
		t.Errorf("TotalPageCount = %d, erwartet 3", attrs.TotalPageCount)
	}
	// autoProcessingStatus ist kein benanntes Feld -> muss unveraendert in RawExtra landen (ADR-0003).
	if _, ok := attrs.RawExtra["autoProcessingStatus"]; !ok {
		t.Errorf("RawExtra fehlt 'autoProcessingStatus' -> Daten waeren verloren gegangen")
	}
}

func TestDocumentAttributesMarshalRoundTripBenannteFelder(t *testing.T) {
	in := DocumentAttributes{
		Title:    "Neuer Titel",
		TagIDs:   []string{"tag-9"},
		SenderID: "company-1",
		RawExtra: map[string]json.RawMessage{},
	}
	out, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back DocumentAttributes
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("Re-Unmarshal: %v", err)
	}
	if back.Title != in.Title || back.SenderID != in.SenderID || len(back.TagIDs) != 1 || back.TagIDs[0] != "tag-9" {
		t.Fatalf("Round-Trip verlor Daten: %+v", back)
	}
}

func TestDocumentUnmarshalMapptAttributesDataKorrekt(t *testing.T) {
	raw := []byte(`{
		"id": "doc-1", "version": 3, "created": "2026-01-01T00:00:00Z", "modified": "2026-01-02T00:00:00Z",
		"deleted": false, "status": "DONE", "type": "Document",
		"pages": [{"id": "page-1", "imageVersion": 1, "contentVersion": 1}],
		"attributes": {"data": {"title": {"value": "Testdoc", "type": "String"}}},
		"uploadAttribute": {"originalFileName": "scan.pdf", "newUpload": true},
		"sharedSpaceIds": [], "forbiddenActions": ["DELETE"]
	}`)
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if doc.ID != "doc-1" || doc.Version != 3 || doc.Status != StatusDone {
		t.Fatalf("Basisfelder falsch: %+v", doc)
	}
	if doc.Attributes.Title != "Testdoc" {
		t.Fatalf("Attributes.Title = %q, erwartet Testdoc (attributes.data-Nesting nicht korrekt aufgeloest)", doc.Attributes.Title)
	}
	if len(doc.ForbiddenActions) != 1 || doc.ForbiddenActions[0] != ActionDelete {
		t.Fatalf("ForbiddenActions = %v", doc.ForbiddenActions)
	}

	backRaw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back Document
	if err := json.Unmarshal(backRaw, &back); err != nil {
		t.Fatalf("Re-Unmarshal: %v", err)
	}
	if back.Attributes.Title != "Testdoc" {
		t.Fatalf("Round-Trip verlor attributes.data.title: %+v", back.Attributes)
	}
}

func TestDocumentAttributesMarshalRoundTripGruppenUndSimpleFelder(t *testing.T) {
	gelesen := true
	rechnungsdatum := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	in := DocumentAttributes{
		Amount: &Money{Currency: "EUR", Value: 42.5},
		BankAccount1: &BankAccount{
			IBAN:          "DE00TEST00000000000003",
			BIC:           "TESTDEXXX",
			Bank:          "Testbank",
			AccountHolder: "Test Testmann",
		},
		Read:           &gelesen,
		InvoiceDate:    &rechnungsdatum,
		TotalPageCount: 7,
		RawExtra:       map[string]json.RawMessage{},
	}

	out, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back DocumentAttributes
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("Re-Unmarshal: %v", err)
	}

	if back.Amount == nil || back.Amount.Currency != "EUR" || back.Amount.Value != 42.5 {
		t.Errorf("Amount (setGroupMoney) Round-Trip verloren: %+v", back.Amount)
	}
	if back.BankAccount1 == nil ||
		back.BankAccount1.IBAN != in.BankAccount1.IBAN ||
		back.BankAccount1.BIC != in.BankAccount1.BIC ||
		back.BankAccount1.Bank != in.BankAccount1.Bank ||
		back.BankAccount1.AccountHolder != in.BankAccount1.AccountHolder {
		t.Errorf("BankAccount1 (setGroupBankAccount) Round-Trip verloren: %+v", back.BankAccount1)
	}
	if back.Read == nil || !*back.Read {
		t.Errorf("Read (setSimpleBool) Round-Trip verloren: %v", back.Read)
	}
	if back.InvoiceDate == nil || !back.InvoiceDate.Equal(rechnungsdatum) {
		t.Errorf("InvoiceDate (setSimpleTime) Round-Trip verloren: %v", back.InvoiceDate)
	}
	if back.TotalPageCount != 7 {
		t.Errorf("TotalPageCount (setSimpleInt) Round-Trip verloren: %d, erwartet 7", back.TotalPageCount)
	}
}

func TestCompanyAttributesMarshalRoundTrip(t *testing.T) {
	in := CompanyAttributes{
		IBANs:        []string{"DE00TEST00000000000004"},
		VATIDs:       []string{"DE000000000"},
		Emails:       []string{"kontakt@example.invalid"},
		PhoneNumbers: []string{"+49000000000"},
		Websites:     []string{"https://example.invalid"},
		GermanTaxIDs: []string{"00000000000"},
		RawExtra:     map[string]json.RawMessage{},
	}

	out, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back CompanyAttributes
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("Re-Unmarshal: %v", err)
	}

	if len(back.IBANs) != 1 || back.IBANs[0] != in.IBANs[0] {
		t.Errorf("IBANs Round-Trip verloren: %v", back.IBANs)
	}
	if len(back.VATIDs) != 1 || back.VATIDs[0] != in.VATIDs[0] {
		t.Errorf("VATIDs Round-Trip verloren: %v", back.VATIDs)
	}
	if len(back.Emails) != 1 || back.Emails[0] != in.Emails[0] {
		t.Errorf("Emails Round-Trip verloren: %v", back.Emails)
	}
	if len(back.PhoneNumbers) != 1 || back.PhoneNumbers[0] != in.PhoneNumbers[0] {
		t.Errorf("PhoneNumbers Round-Trip verloren: %v", back.PhoneNumbers)
	}
	if len(back.Websites) != 1 || back.Websites[0] != in.Websites[0] {
		t.Errorf("Websites Round-Trip verloren: %v", back.Websites)
	}
	if len(back.GermanTaxIDs) != 1 || back.GermanTaxIDs[0] != in.GermanTaxIDs[0] {
		t.Errorf("GermanTaxIDs Round-Trip verloren: %v", back.GermanTaxIDs)
	}
}

func TestCompanyMarshalJSONRoundTrip(t *testing.T) {
	in := Company{
		ID:            "company-1",
		Version:       2,
		CompanyName:   "Testfirma GmbH",
		ContactType:   string(ContactTypeCompany),
		ContactStatus: string(ContactStatusManaged),
		Attributes: CompanyAttributes{
			IBANs:    []string{"DE00TEST00000000000005"},
			Emails:   []string{"info@example.invalid"},
			RawExtra: map[string]json.RawMessage{},
		},
	}

	out, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back Company
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("Re-Unmarshal: %v", err)
	}

	if back.ID != in.ID || back.Version != in.Version || back.CompanyName != in.CompanyName {
		t.Fatalf("Basisfelder Round-Trip verloren: %+v", back)
	}
	if back.ContactType != in.ContactType || back.ContactStatus != in.ContactStatus {
		t.Fatalf("ContactType/ContactStatus Round-Trip verloren: %+v", back)
	}
	if len(back.Attributes.IBANs) != 1 || back.Attributes.IBANs[0] != in.Attributes.IBANs[0] {
		t.Fatalf("Attributes.IBANs (attributes.data-Nesting) Round-Trip verloren: %+v", back.Attributes)
	}
	if len(back.Attributes.Emails) != 1 || back.Attributes.Emails[0] != in.Attributes.Emails[0] {
		t.Fatalf("Attributes.Emails Round-Trip verloren: %+v", back.Attributes)
	}
}

func TestCompanyAttributesUnmarshalListenFelder(t *testing.T) {
	raw := []byte(`{"attributes": {"data": {
		"ibans": {"value": ["DE00TEST00000000000001"], "containedType": "String", "type": "List"},
		"emails": {"value": ["kontakt@example.invalid"], "containedType": "String", "type": "List"},
		"bonusPoints": {"value": 42, "type": "String"}
	}}, "id": "company-1", "companyName": "Testfirma GmbH"}`)
	var c Company
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(c.Attributes.IBANs) != 1 || c.Attributes.IBANs[0] != "DE00TEST00000000000001" {
		t.Fatalf("IBANs = %v", c.Attributes.IBANs)
	}
	if len(c.Attributes.Emails) != 1 {
		t.Fatalf("Emails = %v", c.Attributes.Emails)
	}
	if _, ok := c.Attributes.RawExtra["bonusPoints"]; !ok {
		t.Fatalf("RawExtra fehlt 'bonusPoints'")
	}
}

// TestPageImageVersionUndContentVersionAkzeptierenStringOderZahl deckt den im finalen
// Whole-Branch-Review gefundenen Export-Abbruch-Bug ab: openapi.json deklariert
// DocumentPage.imageVersion/contentVersion als ["string","integer"] — ein reiner int64-Feldtyp
// lässt encoding/json bei einem JSON-String-Wert mit einem Typfehler abbrechen. flexInt64 muss
// beide Varianten dekodieren, inklusive des leeren Strings ("" -> 0).
func TestPageImageVersionUndContentVersionAkzeptierenStringOderZahl(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{"Zahl", `{"id":"page-1","imageVersion":5,"contentVersion":5}`, 5},
		{"String", `{"id":"page-1","imageVersion":"5","contentVersion":"5"}`, 5},
		{"LeererString", `{"id":"page-1","imageVersion":"","contentVersion":""}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p Page
			if err := json.Unmarshal([]byte(tc.raw), &p); err != nil {
				t.Fatalf("Unmarshal(%s): %v", tc.raw, err)
			}
			if int64(p.ImageVersion) != tc.want {
				t.Errorf("ImageVersion = %d, erwartet %d", int64(p.ImageVersion), tc.want)
			}
			if int64(p.ContentVersion) != tc.want {
				t.Errorf("ContentVersion = %d, erwartet %d", int64(p.ContentVersion), tc.want)
			}
		})
	}
}

// TestPageImageVersionUngueltigerStringLiefertFehler stellt sicher, dass ein nicht-numerischer
// String (weder Ganzzahl noch Leerstring) weiterhin einen Fehler liefert statt still 0 zu
// dekodieren — defensiv nur für den belegten Leerstring-Fall, nicht für beliebigen Datenmüll.
func TestPageImageVersionUngueltigerStringLiefertFehler(t *testing.T) {
	var p Page
	err := json.Unmarshal([]byte(`{"id":"page-1","imageVersion":"nicht-numerisch","contentVersion":1}`), &p)
	if err == nil {
		t.Fatalf("erwartet Fehler bei nicht-numerischem imageVersion-String, bekommen nil")
	}
}

// TestPageMarshalEmittiertZahl stellt sicher, dass ein zuvor aus einem JSON-String dekodierter
// Wert beim erneuten Marshal wieder als reine JSON-Zahl herauskommt (Round-Trip bleibt numerisch,
// kein Zurückschreiben als String).
func TestPageMarshalEmittiertZahl(t *testing.T) {
	var p Page
	if err := json.Unmarshal([]byte(`{"id":"page-1","imageVersion":"7","contentVersion":"3"}`), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("Re-Unmarshal in map: %v", err)
	}
	if _, isString := decoded["imageVersion"].(string); isString {
		t.Fatalf("imageVersion wurde als JSON-String re-emittiert, erwartet Zahl: %s", out)
	}
	if got, ok := decoded["imageVersion"].(float64); !ok || got != 7 {
		t.Fatalf("imageVersion nach Round-Trip = %v, erwartet Zahl 7", decoded["imageVersion"])
	}
}

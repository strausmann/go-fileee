package fileee

import (
	"encoding/json"
	"testing"
)

func TestCompanyRetainsPreferredAndMainContactReferences(t *testing.T) {
	var company Company
	input := []byte(`{"id":"company-1","companyName":"Beispiel GmbH","mainContactId":"contact-main","userPreferredContactId":"contact-preferred","attributes":{"data":{}}}`)
	if err := json.Unmarshal(input, &company); err != nil {
		t.Fatal(err)
	}
	if company.MainContactID != "contact-main" || company.UserPreferredContactID != "contact-preferred" {
		t.Fatalf("falsche Kontaktverweise: main=%q preferred=%q", company.MainContactID, company.UserPreferredContactID)
	}
	encoded, err := json.Marshal(company)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["mainContactId"]) != `"contact-main"` || string(fields["userPreferredContactId"]) != `"contact-preferred"` {
		t.Fatalf("Kontaktverweise fehlen im JSON: main=%s preferred=%s", fields["mainContactId"], fields["userPreferredContactId"])
	}
}

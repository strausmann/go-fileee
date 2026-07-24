package fileee

import (
	"context"
	"testing"
)

func TestDocumentTypeSchemes_Query(t *testing.T) {
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"POST /api/document-type-schemes/rest/query": {Status: 200, Body: []byte(`{"rows":[
			{"id":"bill","schemaDefinition":{"concreteType":null,"composingTypes":[
				{"key":"amount","concreteType":"amount","dispensable":false,"hidden":false,"readOnly":false},
				{"key":"invoiceId","concreteType":null,"dispensable":false,"hidden":false,"readOnly":false},
				{"key":"customerId","concreteType":null,"dispensable":false,"hidden":false,"readOnly":false,"constraints":[{"constraintType":"SIZE","max":100}]}
			]}}
		],"totalRows":1}`)},
	})
	c := newTestClientAgainstMockServer(t, newMockServer(t, jsonHandler(t, routes)))

	res, err := c.DocumentTypeSchemes.Query(context.Background(), QueryOptions{Limit: 50})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Rows) != 1 || res.Rows[0].ID != "bill" {
		t.Fatalf("Scheme nicht dekodiert: %+v", res.Rows)
	}
	fields := res.Rows[0].Fields()
	if len(fields) != 3 {
		t.Fatalf("erwartet 3 Felder, bekam %d: %+v", len(fields), fields)
	}
	if fields[0].Key != "amount" || fields[0].ConcreteType != "amount" {
		t.Errorf("Feld 0 falsch: %+v", fields[0])
	}
	if len(fields[2].Constraints) != 1 {
		t.Errorf("customerId sollte 1 Constraint haben: %+v", fields[2])
	}
}

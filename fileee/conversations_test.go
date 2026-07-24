package fileee

import (
	"context"
	"net/http"
	"testing"
)

const convGetBody = `{"id":"c1","title":"Shared: Rechnung","conversationType":"DOCUMENT_SHARE","kind":"SHARE",
  "participants":[
    {"id":"p1","name":"Owner","type":"USER","invited":"2026-07-24T10:00:00Z","joined":"2026-07-24T10:00:00Z"},
    {"id":"p2","name":"Empfaenger","type":"CONTACT","invited":"2026-07-24T11:00:00Z","joined":""}
  ],
  "formerParticipants":[{"id":"p3","name":"Weg","type":"CONTACT","invited":"2026-07-20T10:00:00Z","joined":""}],
  "roles":{"u1":"OWNER"},
  "state":{"read":true,"role":"OWNER","dateOfLastMessage":"2026-07-24T12:00:00Z","sharedDocumentIds":["d1"]},
  "version":3}`

func TestConversations_Get_AcceptanceStatus(t *testing.T) {
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"GET /api/conversations/rest/c1": {Status: 200, Body: []byte(convGetBody)},
	})
	c := newTestClientAgainstMock(t, routes)
	conv, err := c.Conversations.Get(context.Background(), "c1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(conv.Participants) != 2 {
		t.Fatalf("erwartet 2 Teilnehmer, bekommen %d", len(conv.Participants))
	}
	if !conv.Participants[0].Accepted() {
		t.Error("Teilnehmer 0 hat joined gesetzt → sollte angenommen sein")
	}
	if conv.Participants[1].Accepted() {
		t.Error("Teilnehmer 1 ohne joined → sollte ausstehend sein")
	}
	if len(conv.State.SharedDocumentIDs) != 1 || conv.State.SharedDocumentIDs[0] != "d1" {
		t.Errorf("sharedDocumentIds falsch: %+v", conv.State.SharedDocumentIDs)
	}
	if len(conv.FormerParticipants) != 1 {
		t.Errorf("formerParticipants falsch: %+v", conv.FormerParticipants)
	}
}

func TestDocuments_Conversations_FilterByDocument(t *testing.T) {
	diffBody := `{"rows":[
      {"id":"c1","state":{"sharedDocumentIds":["d1","dX"]}},
      {"id":"c2","state":{"sharedDocumentIds":["d9"]}},
      {"id":"c3","state":{"sharedDocumentIds":["d1"]}}
    ],"totalRows":3,"idsToDelete":[]}`
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"POST /api/conversations/rest/diff": {Status: 200, Body: []byte(diffBody)},
	})
	c := newTestClientAgainstMock(t, routes)
	convs, err := c.Documents.Conversations(context.Background(), "d1")
	if err != nil {
		t.Fatalf("Conversations: %v", err)
	}
	if len(convs) != 2 {
		t.Fatalf("erwartet 2 Konversationen für d1, bekommen %d", len(convs))
	}
	for _, cv := range convs {
		if cv.ID != "c1" && cv.ID != "c3" {
			t.Errorf("unerwartete Konversation %s (nicht mit d1 geteilt)", cv.ID)
		}
	}
}

func TestDocument_ShareInformation_ShareIDs(t *testing.T) {
	body := `{"id":"d1","shareInformation":{"shareIds":["s1","s2"]},"sharedSpaceIds":["sp1"]}`
	routes := mergeRoutes(ensureSessionRoutes(), map[string]mockRoute{
		"GET /api/documents/rest/d1": {Status: 200, Body: []byte(body)},
	})
	c := newTestClientAgainstMock(t, routes)
	doc, err := c.Documents.Get(context.Background(), "d1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(doc.ShareInformation.ShareIDs) != 2 || doc.ShareInformation.ShareIDs[0] != "s1" {
		t.Fatalf("shareInformation.shareIds falsch: %+v", doc.ShareInformation)
	}
	if len(doc.SharedSpaceIDs) != 1 {
		t.Errorf("sharedSpaceIds falsch: %+v", doc.SharedSpaceIDs)
	}
}

var _ = http.MethodGet

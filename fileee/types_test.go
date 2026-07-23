package fileee

import "testing"

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

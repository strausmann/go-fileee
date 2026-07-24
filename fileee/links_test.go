package fileee

import "testing"

func TestParseDocumentLink(t *testing.T) {
	cases := []struct {
		in       string
		wantKind LinkKind
		wantID   string
	}{
		{"https://my.fileee.com/documents/6a61ea3fa7e5832500000e2c", LinkKindInternal, "6a61ea3fa7e5832500000e2c"},
		{"https://my.fileee.com/documents/6a61ea3fa7e5832500000e2c/", LinkKindInternal, "6a61ea3fa7e5832500000e2c"},
		{"https://my.fileee.com/documents/6a61ea3fa7e5832500000e2c?x=1#p", LinkKindInternal, "6a61ea3fa7e5832500000e2c"},
		{"https://my.fileee.com/shared/6a634b970791730001db5d50", LinkKindShared, "6a634b970791730001db5d50"},
		{"https://my.fileee.com/shared/6a634b970791730001db5d50/", LinkKindShared, "6a634b970791730001db5d50"},
		{"https://my.fileee.com/", LinkKindUnknown, ""},
		{"6a61ea3fa7e5832500000e2c", LinkKindUnknown, ""},
		{"", LinkKindUnknown, ""},
	}
	for _, c := range cases {
		k, id := ParseDocumentLink(c.in)
		if k != c.wantKind || id != c.wantID {
			t.Errorf("ParseDocumentLink(%q) = (%v,%q), erwartet (%v,%q)", c.in, k, id, c.wantKind, c.wantID)
		}
	}
}

func TestLinkKindString(t *testing.T) {
	for k, want := range map[LinkKind]string{
		LinkKindUnknown:  "unknown",
		LinkKindInternal: "internal",
		LinkKindShared:   "shared",
	} {
		if k.String() != want {
			t.Errorf("LinkKind(%d).String() = %q, erwartet %q", k, k.String(), want)
		}
	}
}

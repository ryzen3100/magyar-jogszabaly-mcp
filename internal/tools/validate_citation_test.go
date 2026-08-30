package tools

import (
	"strings"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/store/storetest"
)

func TestParseCitation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		citation    string
		wantNil     bool
		documentRef string
		sectionRef  string
		structured  bool
	}{
		{name: "hungarian full", citation: "2011. évi CXII. törvény 3. §",
			documentRef: "2011. évi CXII. törvény", sectionRef: "3", structured: true},
		{name: "hungarian full with colon", citation: "2013. évi V. törvény 6:272. §",
			documentRef: "2013. évi V. törvény", sectionRef: "6:272", structured: true},
		{name: "hungarian full with slash", citation: "2012. évi I. törvény 116/A. §",
			documentRef: "2012. évi I. törvény", sectionRef: "116/A", structured: true},
		{name: "hungarian doc only", citation: "2012. évi I. törvény", documentRef: "2012. évi I. törvény", structured: true},
		{name: "db id with section", citation: "hu-law-2012-1-00-00 s116",
			documentRef: "hu-law-2012-1-00-00", sectionRef: "116", structured: true},
		{name: "db id with colon section", citation: "hu-law-2013-5-00-00 s6:272",
			documentRef: "hu-law-2013-5-00-00", sectionRef: "6:272", structured: true},
		{name: "db id only", citation: "hu-law-2012-1-00-00", documentRef: "hu-law-2012-1-00-00", structured: true},
		{name: "section first with comma", citation: "Section 3, Infotörvény", documentRef: "Infotörvény", sectionRef: "3"},
		{name: "section first plain", citation: "Section 999 In Force Act", documentRef: "In Force Act", sectionRef: "999"},
		{name: "section last s", citation: "Infotörvény s 3", documentRef: "Infotörvény", sectionRef: "3"},
		{name: "section last comma s", citation: "Infotörvény, s 3", documentRef: "Infotörvény", sectionRef: "3"},
		{name: "section last s dot", citation: "In Force Act s. 3", documentRef: "In Force Act", sectionRef: "3"},
		{name: "section last Section word", citation: "In Force Act Section 3", documentRef: "In Force Act", sectionRef: "3"},
		{name: "plain document", citation: "Infotörvény", documentRef: "Infotörvény"},
		{name: "empty is nil", citation: "   ", wantNil: true},
		{name: "trims whitespace", citation: "  Infotörvény s 3  ", documentRef: "Infotörvény", sectionRef: "3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parsed := ParseCitation(tc.citation)
			if tc.wantNil {
				if parsed != nil {
					t.Fatalf("expected nil, got %+v", parsed)
				}
				return
			}
			if parsed == nil {
				t.Fatalf("expected parse, got nil")
			}
			if parsed.DocumentRef != tc.documentRef || parsed.SectionRef != tc.sectionRef || parsed.Structured != tc.structured {
				t.Errorf("got %+v, want ref=%q section=%q structured=%v", parsed, tc.documentRef, tc.sectionRef, tc.structured)
			}
		})
	}
}

// FuzzParseCitation pins ParseCitation's documented contract: nil only for an
// empty/whitespace string, a non-nil parse otherwise.
func FuzzParseCitation(f *testing.F) {
	for _, seed := range []string{
		"2011. évi CXII. törvény 3. §",
		"2013. évi V. törvény 6:272. §",
		"2012. évi I. törvény 116/A. §",
		"2011. évi CXII. törvény",
		"hu-law-2012-1-00-00 s116",
		"hu-law-2013-5-00-00 s6:272",
		"Section 3, Infotörvény",
		"Infotörvény s 3",
		"In Force Act Section 3",
		"Infotörvény",
		"   ",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, citation string) {
		got := ParseCitation(citation)
		if strings.TrimSpace(citation) == "" {
			if got != nil {
				t.Fatalf("ParseCitation(%q) = %+v, want nil for empty/whitespace input", citation, got)
			}
			return
		}
		if got == nil {
			t.Fatalf("ParseCitation(%q) = nil, want a parse for non-empty input", citation)
		}
	})
}

func TestValidateCitationParseFailure(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	out, err := runHandlerJSON(t, ValidateCitation, db, `{"citation": "   "}`)
	if err != nil {
		t.Fatal(err)
	}
	want := `"results":{"valid":false,"citation":"   ","warnings":["Could not parse citation format"]}`
	if !strings.Contains(out, want) {
		t.Errorf("got %s\nwant substring %s", out, want)
	}
}

func TestValidateCitationUnknownDocument(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	results, meta, err := ValidateCitation(t.Context(), db, testArgs(t, `{"citation": "Section 1 Missing Act"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Note) != 0 {
		t.Errorf("unexpected note %q", meta.Note)
	}
	res, ok := results.(ValidateCitationResult)
	if !ok {
		t.Fatalf("unexpected results type %T", results)
	}
	if res.Valid {
		t.Error("expected valid=false")
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != `Document not found: "Missing Act"` {
		t.Errorf("warnings = %#v", res.Warnings)
	}
}

func TestValidateCitationValidSection(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	out, err := runHandlerJSON(t, ValidateCitation, db, `{"citation": "In Force Act s 1"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"valid":true`,
		`"provision_ref":"s1"`,
		`"normalized":"In Force Act 1. § (Section 1)"`,
		`"document_id":"doc-inforce"`,
		`"status":"in_force"`,
		`"warnings":[]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
}

func TestValidateCitationSectionWord(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	out, err := runHandlerJSON(t, ValidateCitation, db, `{"citation": "In Force Act Section 1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"provision_ref":"s1"`) || !strings.Contains(out, `"valid":true`) {
		t.Errorf("got %s", out)
	}
}

func TestValidateCitationProvisionNotFound(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	results, _, err := ValidateCitation(t.Context(), db, testArgs(t, `{"citation": "Section 999 In Force Act"}`))
	if err != nil {
		t.Fatal(err)
	}
	res, ok := results.(ValidateCitationResult)
	if !ok {
		t.Fatalf("unexpected results type %T", results)
	}
	if res.Valid {
		t.Error("expected valid=false")
	}
	if res.DocumentID != "doc-inforce" || res.DocumentTitle != "In Force Act" {
		t.Errorf("doc = %q/%q", res.DocumentID, res.DocumentTitle)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != `Provision "999. §" not found in In Force Act` {
		t.Errorf("warnings = %#v", res.Warnings)
	}
}

func TestValidateCitationStatusWarnings(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	out, err := runHandlerJSON(t, ValidateCitation, db, `{"citation": "Amended Act"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := `Note: This statute has been amended. Verify you are referencing the current version.`
	if !strings.Contains(out, want) || !strings.Contains(out, `"valid":true`) {
		t.Errorf("got %s", out)
	}

	out, err = runHandlerJSON(t, ValidateCitation, db, `{"citation": "Repealed Act"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "WARNING: This statute has been repealed.") {
		t.Errorf("got %s", out)
	}
}

func TestValidateCitationStatusWarningBeforeProvisionCheck(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	results, _, err := ValidateCitation(t.Context(), db, testArgs(t, `{"citation": "Repealed Act s 99"}`))
	if err != nil {
		t.Fatal(err)
	}
	res, ok := results.(ValidateCitationResult)
	if !ok {
		t.Fatalf("unexpected results type %T", results)
	}
	if res.Valid {
		t.Error("expected valid=false")
	}
	if len(res.Warnings) != 2 {
		t.Fatalf("warnings = %#v", res.Warnings)
	}
	if res.Warnings[0] != "WARNING: This statute has been repealed." {
		t.Errorf("warnings[0] = %q", res.Warnings[0])
	}
	if res.Warnings[1] != `Provision "99. §" not found in Repealed Act` {
		t.Errorf("warnings[1] = %q — status warning must come before provision check", res.Warnings[1])
	}
}

func TestValidateCitationMissingRequiredArg(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	_, _, err := ValidateCitation(t.Context(), db, testArgs(t, `{}`))
	if err == nil || err.Error() != `missing required argument "citation"` {
		t.Errorf("err = %v, want missing required argument", err)
	}
}

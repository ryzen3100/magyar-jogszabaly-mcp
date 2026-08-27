package builddb

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestExtractEUReferences(t *testing.T) {
	simple := EURef{
		Type: "regulation", Community: "EU", Year: 2016, Number: 679,
		EUDocumentID: "regulation:2016/679", EUArticle: "",
		FullCitation: "Regulation (EU) 2016/679", ReferenceContext: "Regulation (EU) 2016/679",
		ReferenceType: "references",
	}

	tests := []struct {
		name string
		text string
		want []EURef
	}{
		{"empty text", "", nil},
		{"whitespace-only text", "  \n\t ", nil},
		{"no match", "a rendelet 2016/679 számú rendelkezése", nil},
		{"pattern 1 community in parens", "Regulation (EU) 2016/679", []EURef{simple}},
		{"pattern 2 trailing community", "a Directive 2011/83/EU szerint", []EURef{{
			Type: "directive", Community: "EU", Year: 2011, Number: 83,
			EUDocumentID: "directive:2011/83", EUArticle: "",
			FullCitation: "Directive 2011/83/EU", ReferenceContext: "a Directive 2011/83/EU szerint",
			ReferenceType: "references",
		}}},
		{"two-digit year pivots to 1900s", "a Directive 78/660/EEC szerint", []EURef{{
			Type: "directive", Community: "EEC", Year: 1978, Number: 660,
			EUDocumentID: "directive:1978/660", EUArticle: "",
			FullCitation: "Directive 78/660/EEC", ReferenceContext: "a Directive 78/660/EEC szerint",
			ReferenceType: "references",
		}}},
		{"two-digit year pivots to 2000s", "Directive 05/29/EC", []EURef{{
			Type: "directive", Community: "EC", Year: 2005, Number: 29,
			EUDocumentID: "directive:2005/29", EUArticle: "",
			FullCitation: "Directive 05/29/EC", ReferenceContext: "Directive 05/29/EC",
			ReferenceType: "references",
		}}},
		{"pattern 3 defaults community to EU", "említett Directive 2019/2161 rendelkezés", []EURef{{
			Type: "directive", Community: "EU", Year: 2019, Number: 2161,
			EUDocumentID: "directive:2019/2161", EUArticle: "",
			FullCitation: "Directive 2019/2161", ReferenceContext: "említett Directive 2019/2161 rendelkezés",
			ReferenceType: "references",
		}}},
		{"zero number skipped", "Directive 2019/0 vég", nil},
		{
			"case-insensitive, citation keeps raw casing",
			"REGULATION (EU) 2016/679",
			[]EURef{func() EURef {
				r := simple
				r.FullCitation = "REGULATION (EU) 2016/679"
				r.ReferenceContext = "REGULATION (EU) 2016/679"
				return r
			}()},
		},
		{
			"article extracted and implements detected in context",
			"A rendelet rendelkezéseit implement kell alkalmazni, lásd Article 5(1) szerint, továbbá Regulation (EU) 2016/679",
			[]EURef{func() EURef {
				r := simple
				r.EUArticle = "5(1)"
				r.ReferenceType = "implements"
				r.ReferenceContext = "A rendelet rendelkezéseit implement kell alkalmazni, lásd Article 5(1) szerint, továbbá Regulation (EU) 2016/679"
				return r
			}()},
		},
		{
			"whitespace collapsed in context",
			"sor\n\tsor\t  Regulation (EU) 2016/679",
			[]EURef{func() EURef {
				r := simple
				r.ReferenceContext = "sor sor Regulation (EU) 2016/679"
				return r
			}()},
		},
		{
			"context window cuts on runes, not bytes",
			strings.Repeat("ő", 130) + "Regulation (EU) 2016/679",
			[]EURef{func() EURef {
				r := simple
				r.ReferenceContext = strings.Repeat("ő", 120) + "Regulation (EU) 2016/679"
				return r
			}()},
		},
		{
			"same document deduped across patterns",
			"Regulation (EU) 2016/679 és továbbá a Regulation 2016/679 rendelet",
			[]EURef{func() EURef {
				r := simple
				// The ±120-char window around the first citation spans the
				// whole (short) text, including the later repeat.
				r.ReferenceContext = "Regulation (EU) 2016/679 és továbbá a Regulation 2016/679 rendelet"
				return r
			}()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractEUReferences(tt.text)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ExtractEUReferences() =\n  %#v\nwant\n  %#v", got, tt.want)
			}
		})
	}
}

func TestExtractEUReferencesDistinctArticles(t *testing.T) {
	// Same EU document cited twice, >120 chars apart so the context windows
	// do not overlap; both references must survive dedupe with their own
	// article number.
	text := "Article 5 of Regulation (EU) 2016/679. " +
		strings.Repeat("kitöltő ", 30) +
		"Article 14 of Regulation (EU) 2016/679"
	got := ExtractEUReferences(text)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2: %#v", len(got), got)
	}
	for i, wantArticle := range []string{"5", "14"} {
		r := got[i]
		if r.EUArticle != wantArticle || r.EUDocumentID != "regulation:2016/679" || r.ReferenceType != "references" {
			t.Errorf("got[%d] = %#v, want article %q of regulation:2016/679", i, r, wantArticle)
		}
	}
}

func TestCollapseSpace(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"outer whitespace trimmed", "  szóköz  ", "szóköz"},
		{"newlines, tabs and runs collapse", "a\n\tb   c", "a b c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := collapseSpace(tt.in); got != tt.want {
				t.Errorf("collapseSpace(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// FuzzExtractEUReferences checks the extractor's output invariants on random
// input: every reference has a positive year/number, a known type and
// reference kind, an empty-free citation/context, and a document id
// consistent with its own fields. Seeds are real statute-style snippets
// (GDPR, Consumer Rights Directive, two-digit-year EEC citations, multi-byte
// padding that must not corrupt the rune-indexed context window).
func FuzzExtractEUReferences(f *testing.F) {
	seeds := []string{
		"",
		"  \n\t ",
		"az Európai Parlament és a Tanács 2016/679 rendeletének megfelelően",
		"A 2011/83/EU irányelv 5. cikkében foglalt kötelezettségek",
		"Regulation (EU) 2016/679 Article 5(1), which the act implements",
		"a 78/660/EGK irányelvben foglaltakkal összhangban",
		"Directive 2019/2161 és azt megelőzően Regulation (EU) 2016/679",
		"no citation here, csak szöveg",
		strings.Repeat("ő", 150) + "Directive 05/29/EC",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		seen := map[string]bool{}
		for _, r := range ExtractEUReferences(text) {
			if r.Year <= 0 || r.Number <= 0 {
				t.Errorf("non-positive year/number: %+v", r)
			}
			if r.Type != "regulation" && r.Type != "directive" {
				t.Errorf("unexpected type %q: %+v", r.Type, r)
			}
			if r.ReferenceType != "references" && r.ReferenceType != "implements" {
				t.Errorf("unexpected reference type %q: %+v", r.ReferenceType, r)
			}
			if want := fmt.Sprintf("%s:%d/%d", r.Type, r.Year, r.Number); r.EUDocumentID != want {
				t.Errorf("document id %q, want %q: %+v", r.EUDocumentID, want, r)
			}
			if r.FullCitation == "" || r.ReferenceContext == "" {
				t.Errorf("empty citation/context: %+v", r)
			}
			key := r.EUDocumentID + ":" + r.EUArticle
			if seen[key] {
				t.Errorf("duplicate reference %q returned twice", key)
			}
			seen[key] = true
		}
	})
}

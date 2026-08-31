package fts

import (
	"reflect"
	"strings"
	"testing"
)

func TestHasBooleanOperators(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty string", "", false},
		{"plain terms", "személyes adat", false},
		{"AND", "adat AND védelem", true},
		{"OR", "gdpr OR adat", true},
		{"NOT", "NOT cookie", true},
		{"lowercase is an operator too (case-insensitive)", "adat and védelem", true},
		{"mixed case is an operator", "gdpr Or adat", true},
		{"no boundary inside GRAND", "GRAND", false},
		{"no boundary inside ORDER", "ORDER", false},
		{"boundary after punctuation", "x;AND", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := HasBooleanOperators(tt.input); got != tt.want {
				t.Errorf("HasBooleanOperators(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"strips quotes parens keeps trailing star (vitest)", ` "GDPR" (Article) 6* `, "GDPR Article 6*"},
		{"preserves trailing wildcard for prefix search", "control*", "control*"},
		{"mid-word wildcard becomes space", "a*b", "a b"},
		{"boolean mode keeps quotes parens", `"adat" AND (védelem)`, `"adat" AND (védelem)`},
		{"boolean mode strips dangerous chars, keeps quotes", `adat* ^2 OR "x:y"`, `adat 2 OR "x y"`},
		{"non-boolean strips quotes parens colon", `'gdpr' "article" 6:1(2)`, "gdpr article 6 1 2"},
		{"quotes become spaces, star stays trailing", `"foo"* `, "foo *"},
		{"collapses and trims whitespace", "  adat\t\n védelem  ", "adat védelem"},
		{"trailing question mark is stripped (natural-language question)",
			"Hány nap szabadság jár egy 42 éves munkavállalónak?",
			"Hány nap szabadság jár egy 42 éves munkavállalónak"},
		{"question mark inside boolean query also stripped", `adat? AND védelem`, `adat AND védelem`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := SanitizeInput(tt.input); got != tt.want {
				t.Errorf("SanitizeInput(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildQueryVariants(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		sanitized string
		want      []string
	}{
		{"empty", "", []string{}},
		{"whitespace only", "   ", []string{}},
		{"single short term", "x", []string{"x"}},
		{"single term gets prefix variant", "adat", []string{"adat", "adat*"}},
		{"vitest hungarian phrase", "személyes adat", []string{
			`"személyes adat"`,
			"személyes AND adat",
			"személyes AND adat*",
			"személyes* AND adat*", // deliberate TS divergence: prefix-all tier
			"személyes* OR adat*",  // deliberate TS divergence: prefixed OR tier
		}},
		{
			// Deliberate TS divergence: prefix-all tier inserted before OR, and
			// "a" (definite article) is filtered as a Hungarian stopword.
			"three terms, article filtered", "a b c", []string{
				`"b c"`,
				"b AND c",
				"b AND c*",
				"b* AND c*",
				"b OR c",
			}},
		{"boolean passthrough verbatim, whitespace not collapsed", "a AND  b", []string{"a AND  b"}},
		// "év" is 2 UTF-16 units but 3 UTF-8 bytes: no star variant (TS length semantics)
		{"two-rune hungarian term gets no prefix variant", "év", []string{"év"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := BuildQueryVariants(tt.sanitized)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BuildQueryVariants(%q) = %#v, want %#v", tt.sanitized, got, tt.want)
			}
		})
	}
}

// FuzzSanitizeInput pins the sanitizer's structural invariants for every
// input: whitespace comes out fully normalized (trimmed, single-spaced, no
// raw tabs/newlines — NBSP etc. are the documented \s ceiling) and no
// character targeted by the active strip pattern survives into the output.
func FuzzSanitizeInput(f *testing.F) {
	// Seeds from the table tests plus regex edge cases: repeated stars, bare
	// operators, control bytes, multi-byte Hungarian.
	for _, s := range []string{
		` "GDPR" (Article) 6* `,
		"control*",
		"a*b",
		`"adat" AND (védelem)`,
		`adat* ^2 OR "x:y"`,
		`'gdpr' "article" 6:1(2)`,
		"  adat\t\n védelem  ",
		"a**b",
		"**",
		"NOT",
		"and",
		"\x00",
		"év",
		"személyes adat",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		got := SanitizeInput(input)

		if got != strings.TrimSpace(got) ||
			strings.Contains(got, "  ") ||
			strings.ContainsAny(got, "\t\n\f\r") {
			t.Fatalf("SanitizeInput(%q) = %q: whitespace not normalized", input, got)
		}

		// Which strip pattern ran is decided by the raw input's boolean mode.
		if HasBooleanOperators(input) {
			if strings.ContainsAny(got, "{}[]^~*:") {
				t.Fatalf("SanitizeInput(%q) = %q: boolean-strip char survived", input, got)
			}
			return
		}
		if strings.ContainsAny(got, `'"(){}[]^~:`) {
			t.Fatalf("SanitizeInput(%q) = %q: non-boolean-strip char survived", input, got)
		}
	})
}

func TestBuildLikePattern(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"empty", "", "%"},
		{"whitespace only", "   ", "%"},
		{"single term", "penalty", "%penalty%"},
		{"multi term", "penalty offence", "%penalty%offence%"},
		{"collapses surrounding and inner whitespace", "  személyes   adat  ", "%személyes%adat%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := BuildLikePattern(tt.query); got != tt.want {
				t.Errorf("BuildLikePattern(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

// Regression: natural-language questions carry punctuation (",", "?", "!"),
// and any of it surviving into an FTS5 bareword kills every MATCH variant —
// the query then degrades to the sentence-length LIKE tier (zero hits).
func TestSanitizeInputStripsPunctuationFromQuestions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"Milyen engedély kell ahhoz, hogy nyissak egy kávézót?",
			"Milyen engedély kell ahhoz hogy nyissak egy kávézót"},
		{"Hány nap szabadság jár?", "Hány nap szabadság jár"},
		{"adó-vám, 1,5%", "adó vám 1 5"},
	}
	for _, tt := range tests {
		if got := SanitizeInput(tt.input); got != tt.want {
			t.Errorf("SanitizeInput(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// The natural-language-question tiers: Hungarian function words are dropped,
// every term gets a prefix wildcard, and inflected terms get a stemmed
// variant — so "Hány nap szabadság jár egy 42 éves munkavállalónak?" no
// longer depends on the user knowing dictionary forms (audit follow-up to
// the `?`-punctuation fix).
func TestBuildQueryVariantsNaturalLanguageQuestion(t *testing.T) {
	t.Parallel()
	got := BuildQueryVariants("Hány nap szabadság jár egy 42 éves munkavállalónak")
	want := []string{
		`"nap szabadság jár 42 éves munkavállalónak"`,
		"nap AND szabadság AND jár AND 42 AND éves AND munkavállalónak",
		"nap AND szabadság AND jár AND 42 AND éves AND munkavállalónak*",
		"nap* AND szabadság* AND jár* AND 42* AND éves* AND munkavállalónak*",
		"nap AND szabadság AND jár AND 42 AND éves AND munkavállaló",
		// OR tiers drop ≤3-rune terms (nap, jár, 42): their doclists are so
		// broad they dominate query latency without contributing signal.
		"szabadság* OR éves* OR munkavállalónak*",
		"szabadság* OR éves* OR munkavállaló*",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildQueryVariants(question) = %#v, want %#v", got, want)
	}

	// All-stopword input falls back to the terms as typed (no empty query).
	got = BuildQueryVariants("hogy kell egy")
	want = []string{
		`"hogy kell egy"`,
		"hogy AND kell AND egy",
		"hogy AND kell AND egy*",
		"hogy* AND kell* AND egy*",
		"hogy* OR kell*", // egy (3 runes) dropped from the OR tier; longer terms stay
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildQueryVariants(all stopwords) = %#v, want %#v", got, want)
	}
}

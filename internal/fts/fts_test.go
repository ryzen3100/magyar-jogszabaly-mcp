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

func TestSanitizeFtsInput(t *testing.T) {
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := SanitizeFtsInput(tt.input); got != tt.want {
				t.Errorf("SanitizeFtsInput(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildFtsQueryVariants(t *testing.T) {
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
			"személyes OR adat",
		}},
		{"three terms, only last starred", "a b c", []string{
			`"a b c"`,
			"a AND b AND c",
			"a AND b AND c*",
			"a OR b OR c",
		}},
		{"boolean passthrough verbatim, whitespace not collapsed", "a AND  b", []string{"a AND  b"}},
		// "év" is 2 UTF-16 units but 3 UTF-8 bytes: no star variant (TS length semantics)
		{"two-rune hungarian term gets no prefix variant", "év", []string{"év"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := BuildFtsQueryVariants(tt.sanitized)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BuildFtsQueryVariants(%q) = %#v, want %#v", tt.sanitized, got, tt.want)
			}
		})
	}
}

// FuzzSanitizeFtsInput pins the sanitizer's structural invariants for every
// input: whitespace comes out fully normalized (trimmed, single-spaced, no
// raw tabs/newlines — NBSP etc. are the documented \s ceiling) and no
// character targeted by the active strip pattern survives into the output.
func FuzzSanitizeFtsInput(f *testing.F) {
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
		got := SanitizeFtsInput(input)

		if got != strings.TrimSpace(got) ||
			strings.Contains(got, "  ") ||
			strings.ContainsAny(got, "\t\n\f\r") {
			t.Fatalf("SanitizeFtsInput(%q) = %q: whitespace not normalized", input, got)
		}

		// Which strip pattern ran is decided by the raw input's boolean mode.
		if HasBooleanOperators(input) {
			if strings.ContainsAny(got, "{}[]^~*:") {
				t.Fatalf("SanitizeFtsInput(%q) = %q: boolean-strip char survived", input, got)
			}
			return
		}
		if strings.ContainsAny(got, `'"(){}[]^~:`) {
			t.Fatalf("SanitizeFtsInput(%q) = %q: non-boolean-strip char survived", input, got)
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

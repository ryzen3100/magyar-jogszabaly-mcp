// Package fts provides FTS5 query helpers for Hungarian Law MCP.
//
// Handles query sanitization and variant generation for SQLite FTS5.
// Port of src/utils/fts-query.ts.
package fts

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Case-insensitive: lowercase and/or/not are operators too and must reach the
// boolean tier instead of silently degrading the search to the LIKE fallback.
var booleanOpsRe = regexp.MustCompile(`(?i)\b(AND|OR|NOT)\b`)

// Dangerous chars stripped in boolean mode (quotes and parens preserved).
var booleanStripRe = regexp.MustCompile(`[{}\[\]^~*:]`)

// Chars stripped in non-boolean mode (quotes/parens/colons included).
var nonBooleanStripRe = regexp.MustCompile(`['"(){}\[\]^~:]`)

// Mid-word `*` — a `*` NOT followed by whitespace or end-of-string.
// RE2 has no negative lookahead, so the offending char is captured and
// re-emitted by the replacement (" $1").
var midWordStarRe = regexp.MustCompile(`\*(\S)`)

// ponytail: Go \s is ASCII ([\t\n\f\r ]); JS \s also folds NBSP and other
// Unicode spaces — no corpus query relies on those. Upgrade path:
// [\s\p{Zs}\x{00A0}\x{FEFF}] in the two patterns.
var whitespaceRe = regexp.MustCompile(`\s+`)

// HasBooleanOperators detects whether input contains FTS5 boolean operators.
func HasBooleanOperators(input string) bool {
	return booleanOpsRe.MatchString(input)
}

// SanitizeFtsInput sanitizes user input for safe FTS5 queries.
// Preserves boolean operators (AND, OR, NOT) when detected.
func SanitizeFtsInput(input string) string {
	var stripped string
	if HasBooleanOperators(input) {
		// Preserve boolean structure: only strip dangerous chars, keep quotes and parens
		stripped = booleanStripRe.ReplaceAllString(input, " ")
	} else {
		// Preserve trailing * on words (FTS5 prefix search) but strip other special chars
		stripped = midWordStarRe.ReplaceAllString(nonBooleanStripRe.ReplaceAllString(input, " "), " $1")
	}
	return strings.TrimSpace(whitespaceRe.ReplaceAllString(stripped, " "))
}

// BuildFtsQueryVariants builds FTS5 query variants for a search term.
// Returns variants in order of specificity (most specific first):
// 1. Exact phrase match
// 2. All terms required (AND)
// 3. Prefix AND (last term gets prefix wildcard)
// 4. Any term matches (OR) — broad fallback
//
// When boolean operators are detected, passes query through as-is.
func BuildFtsQueryVariants(sanitized string) []string {
	if strings.TrimSpace(sanitized) == "" {
		return []string{}
	}

	// Boolean passthrough — user knows what they want
	if HasBooleanOperators(sanitized) {
		return []string{sanitized}
	}

	terms := strings.Fields(sanitized)
	if len(terms) == 0 {
		return []string{}
	}

	variants := []string{}

	if len(terms) > 1 {
		// Exact phrase
		variants = append(variants, `"`+strings.Join(terms, " ")+`"`)
		// AND query
		variants = append(variants, strings.Join(terms, " AND "))
		// Prefix AND on last term
		last := terms[len(terms)-1]
		prefixTerms := append(append([]string{}, terms[:len(terms)-1]...), last+"*")
		variants = append(variants, strings.Join(prefixTerms, " AND "))
		// OR fallback — any term matches (broadest)
		variants = append(variants, strings.Join(terms, " OR "))
	} else {
		// Single term
		variants = append(variants, terms[0])
		// TS .length counts UTF-16 units; rune count matches it for BMP text
		// (the whole corpus), so Hungarian multi-byte words compare correctly.
		if utf8.RuneCountInString(terms[0]) >= 3 {
			variants = append(variants, terms[0]+"*")
		}
	}

	return variants
}

// BuildLikePattern builds a SQL LIKE pattern from search terms.
// Used as a final fallback when FTS5 returns no results.
// Example: "penalty offence" -> "%penalty%offence%".
func BuildLikePattern(query string) string {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return "%"
	}
	return "%" + strings.Join(terms, "%") + "%"
}

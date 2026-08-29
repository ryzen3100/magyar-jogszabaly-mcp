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
// Punctuation must go: a stray `?` or `,` is an FTS5 query-syntax error in a
// bareword, killing every variant down to the LIKE tier.
var booleanStripRe = regexp.MustCompile(`[{}\[\]^~*:,.;!?–—]`)

// Chars stripped in non-boolean mode: everything that is not a letter
// (any script — Hungarian accents must survive), a digit, whitespace, or a
// meaningful `*` wildcard. Punctuation like `?` or a comma ("ahhoz, hogy")
// is an FTS5 bareword-syntax error that kills every MATCH variant.
var nonBooleanStripRe = regexp.MustCompile(`[^\p{L}\p{N}\s*]+`)

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

// SanitizeInput sanitizes user input for safe FTS5 queries.
// Preserves boolean operators (AND, OR, NOT) when detected.
func SanitizeInput(input string) string {
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

// BuildQueryVariants builds FTS5 query variants for a search term.
// Returns variants in order of specificity (most specific first):
//  1. Exact phrase match
//  2. All terms required (AND)
//  3. Prefix AND (last term gets prefix wildcard)
//  4. Prefix AND on every term — base-form queries match inflected corpus
//     tokens ("kávézó*" finds "kávézót")
//  5. Stemmed AND — light Hungarian suffix stripping turns inflected query
//     terms into their base form ("munkavállalónak" → "munkavállaló")
//  6. Any term matches (OR) — broad fallback
//
// Hungarian function words (házszintű kötőszavak: "hány", "milyen", "kell",
// "hogy"…) are dropped from the term variants first — they occur in nearly
// every provision, so AND-ing them makes every tier miss.
//
// When boolean operators are detected, passes query through as-is.
func BuildQueryVariants(sanitized string) []string {
	if strings.TrimSpace(sanitized) == "" {
		return []string{}
	}

	// Boolean passthrough — user knows what they want
	if HasBooleanOperators(sanitized) {
		return []string{sanitized}
	}

	terms := filterStopWords(strings.Fields(sanitized))
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
		// Prefix AND on every term
		allPrefixed := make([]string, len(terms))
		for i, t := range terms {
			allPrefixed[i] = t + "*"
		}
		variants = append(variants, strings.Join(allPrefixed, " AND "))
		// Stemmed AND — skips when nothing actually stemmed
		if stemmed := stemAll(terms); strings.Join(stemmed, " ") != strings.Join(terms, " ") {
			variants = append(variants, strings.Join(stemmed, " AND "))
		}
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
		if stem := stemTerm(terms[0]); stem != terms[0] {
			variants = append(variants, stem)
			if utf8.RuneCountInString(stem) >= 3 {
				variants = append(variants, stem+"*")
			}
		}
	}

	return variants
}

// hungarianStopWords are function words with no discriminating power over the
// corpus: nearly every provision contains them, so keeping them in AND/PREFIX
// variants makes those tiers miss and pushes every natural-language question
// down to the OR tier. "nem" and the definite article "a"/"az" are included —
// they are the most frequent tokens in Hungarian legal prose and dominate
// OR-tier ranking otherwise (the "a b c" variant-shape parity with the TS
// tests is deliberately diverged from for this; see the pinned test).
// Bounded hand-picked list — ponytail ceiling: a real frequency-weighted
// stop list (or corpus-derived one) is the upgrade path.
var hungarianStopWords = map[string]bool{
	"hány": true, "mennyi": true, "milyen": true, "hogyan": true, "miért": true,
	"hogy": true, "hogyha": true, "ahhoz": true, "ehhez": true, "egy": true,
	"kell": true, "kellene": true, "lehet": true, "van": true, "vannak": true,
	"az": true, "a": true, "nem": true,
}

func filterStopWords(terms []string) []string {
	kept := terms[:0:0]
	for _, t := range terms {
		if !hungarianStopWords[strings.ToLower(t)] {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		return terms // all function words — search them as typed
	}
	return kept
}

// Light Hungarian query stemming: strips ONE common suffix from a term so
// inflected query forms reach their base ("munkavállalónak" → "munkavállaló",
// "kávézót" → "kávézó", "szabadságát" → "szabadság"). ponytail ceiling: a
// suffix list is a heuristic — it over-stems occasional words and knows
// nothing about vowel harmony or compounding (alapszabadság stays opaque);
// a real stemmer (e.g. a snowball hu port) is the upgrade path. The stem is
// only ever an ADDITIONAL variant tier, so a wrong strip costs recall of one
// fallback tier, never precision of the earlier ones.
var hungarianSuffixes = []string{
	// 3-char first (longest match wins)
	"nak", "nek", "ban", "ben", "ból", "ből", "tól", "től", "ról", "ről",
	"val", "vel", "juk", "jük", "hoz", "hez", "höz",
	// 2-char
	"ok", "ek", "ök", "ak", "ot", "et", "öt", "at", "ba", "be", "ra", "re",
	"on", "en", "ön", "át", "uk", "ük",
	// 1-char accusative, last resort
	"t",
}

// minStemRuneCount keeps the strip from mangling short words ("hát" → "há").
const minStemRuneCount = 4

func stemTerm(term string) string {
	lower := strings.ToLower(term)
	lowerRunes := []rune(lower)
	for _, suffix := range hungarianSuffixes {
		s := []rune(suffix)
		if len(lowerRunes) > len(s) && len(lowerRunes)-len(s) >= minStemRuneCount &&
			string(lowerRunes[len(lowerRunes)-len(s):]) == suffix {
			runes := []rune(term)
			return string(runes[:len(runes)-len(s)])
		}
	}
	return term
}

func stemAll(terms []string) []string {
	stemmed := make([]string, len(terms))
	for i, t := range terms {
		stemmed[i] = stemTerm(t)
	}
	return stemmed
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

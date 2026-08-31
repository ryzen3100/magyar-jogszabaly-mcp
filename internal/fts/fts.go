// Package fts provides FTS5 query helpers for Hungarian Law MCP.
//
// Handles query sanitization and variant generation for SQLite FTS5.
// Port of src/utils/fts-query.ts.
package fts

import (
	"context"
	"database/sql"
	"fmt"
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
		stemmed := stemAll(terms)
		if strings.Join(stemmed, " ") != strings.Join(terms, " ") {
			variants = append(variants, strings.Join(stemmed, " AND "))
		}
		// OR fallback — any term matches (broadest). Prefixed like the
		// prefix-AND tiers: with raw tokens this tier floods the candidate pool
		// with provisions matching one generic token ("engedély"), while documents
		// matching several rare query terms — the actual answers — rank below the
		// per-tier cutoff and never reach the re-ranker. Short tokens (≤3 runes —
		// numbers, "Ha", "és", "nap") are dropped from the OR tiers: their doclists
		// cover hundreds of thousands of provisions, which makes the tier's bm25
		// ranking scan the whole index for near-zero ranking signal. The term-
		// coverage re-ranker still scores every dropped term via DocTermHits.
		variants = append(variants, strings.Join(prefixed(orTerms(terms)), " OR "))
		// Stemmed OR — inflected query words ("kávézót") must still reach
		// their base form in the broadest tier, or provisions using only the
		// base token stay invisible to the last recall tier.
		if strings.Join(stemmed, " ") != strings.Join(terms, " ") {
			variants = append(variants, strings.Join(prefixed(orTerms(stemmed)), " OR "))
		}
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

// QueryTerms returns the sanitized query's terms after stop-word filtering —
// the same term list BuildQueryVariants builds its variants from. Used by the
// search tool to score candidate documents by distinct term matches.
func QueryTerms(sanitized string) []string {
	return filterStopWords(strings.Fields(sanitized))
}

// HasORVariant reports whether an FTS query variant is an OR-disjunction
// ("a OR b OR c"), the broadest recall tier. Search treats these specially:
// their huge doclists make the bm25 sort dominate search latency, so the
// tier query pre-filters to in-force non-noise documents and their snippets
// skip the re-MATCH entirely.
func HasORVariant(v string) bool {
	return strings.Contains(v, " OR ")
}

// DocTermHits returns, per query term, the set of document IDs that have at
// least one provision matching the term, restricted to docIDs (the candidate
// pool). Each term is matched via its stemmed form: prefixed for long terms
// (base form reaches inflected corpus tokens, "kávézó*" hits "kávézót"),
// exact for short ones (prefixing "nap" would inflate generic provisions via
// "napján"/"napellenző"). Terms with an empty pool entry simply have no hits.
//
// The search tool uses this for document-level ranking: how many DISTINCT
// query terms a document covers anywhere in its text, weighted by term
// rarity, is a far better relevance signal on the 72k-document corpus than
// per-provision match counts. Restricting the scan to the pool (≤ a few
// hundred doc IDs) keeps it O(pool) per term instead of O(all matching
// documents) — a generic term like "engedély*" matches hundreds of thousands
// of rows corpus-wide.
func DocTermHits(ctx context.Context, db *sql.DB, terms []string, docIDs []string) (map[string]map[string]bool, error) {
	hits := make(map[string]map[string]bool, len(terms))
	if len(docIDs) == 0 {
		return hits, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(docIDs)), ",")
	for _, term := range terms {
		// ≤3-rune terms ("Ha", "és", "20") match a near-constant fraction of
		// the corpus, so every pool document covers them: scoring them adds a
		// near-constant offset to every document while their MATCH costs a
		// full doclist scan. Skip them — see orTerms for the sibling rule.
		if utf8.RuneCountInString(strings.TrimSuffix(term, "*")) < 4 {
			continue
		}
		stem := stemTerm(strings.TrimSuffix(term, "*"))
		if stem == "" {
			continue
		}
		pattern := stem
		if utf8.RuneCountInString(stem) >= 5 {
			pattern = stem + "*"
		}
		params := make([]any, 0, len(docIDs)+1)
		params = append(params, pattern)
		for _, id := range docIDs {
			params = append(params, id)
		}
		// keyed by the ORIGINAL term — callers look hits up with the same
		// term list they passed in
		rows, err := db.QueryContext(ctx,
			"SELECT DISTINCT lp.document_id FROM provisions_fts"+
				" JOIN legal_provisions lp ON lp.id = provisions_fts.rowid"+
				" WHERE provisions_fts MATCH ? AND lp.document_id IN ("+placeholders+")",
			params...)
		if err != nil {
			return nil, fmt.Errorf("doc term hits: %w", err)
		}
		for rows.Next() {
			var docID string
			if err := rows.Scan(&docID); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan doc term hit: %w", err)
			}
			if hits[term] == nil {
				hits[term] = make(map[string]bool)
			}
			hits[term][docID] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("iterate doc term hits: %w", err)
		}
	}
	return hits, nil
}

// TermDocFreq returns, per query term, the number of documents in docIDs
// (the candidate pool) that match the term (same stemmed/prefixed pattern as
// DocTermHits). Pool-restricted df approximates corpus-wide idf: rare query
// terms stay rare inside the candidate pool, and ranking only ever compares
// pool documents against each other, so idf ORDERING between terms — what
// the re-rank consumes — is preserved. Each term costs one bounded
// MATCH ... IN (pool) aggregate instead of the unbounded full-index scan the
// pre-fix code paid per term.
func TermDocFreq(ctx context.Context, db *sql.DB, terms []string, docIDs []string) (map[string]int, error) {
	df := make(map[string]int, len(terms))
	if len(terms) == 0 || len(docIDs) == 0 {
		return df, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(docIDs)), ",")
	for _, term := range terms {
		// Mirror DocTermHits: short terms are skipped for scoring (they would
		// add a near-constant idf offset to every document), so their df is
		// not needed either.
		if utf8.RuneCountInString(strings.TrimSuffix(term, "*")) < 4 {
			continue
		}
		stem := stemTerm(strings.TrimSuffix(term, "*"))
		if stem == "" {
			continue
		}
		pattern := stem
		if utf8.RuneCountInString(stem) >= 5 {
			pattern = stem + "*"
		}
		params := make([]any, 0, len(docIDs)+1)
		params = append(params, pattern)
		for _, id := range docIDs {
			params = append(params, id)
		}
		var count int
		err := db.QueryRowContext(ctx,
			"SELECT COUNT(DISTINCT lp.document_id) FROM provisions_fts"+
				" JOIN legal_provisions lp ON lp.id = provisions_fts.rowid"+
				" WHERE provisions_fts MATCH ? AND lp.document_id IN ("+placeholders+")",
			params...).Scan(&count)
		if err != nil {
			return nil, fmt.Errorf("term doc freq: %w", err)
		}
		df[term] = count
	}
	return df, nil
}

// orTerms drops ≤3-rune tokens from an OR-tier term list: their doclists are
// so broad that ranking them costs seconds while contributing no relevance
// signal (the re-ranker's term-coverage pass still sees them). If every term
// is short, they are all kept — a short-only query has nothing else to match.
func orTerms(terms []string) []string {
	kept := make([]string, 0, len(terms))
	for _, t := range terms {
		if utf8.RuneCountInString(t) >= 4 {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		return terms
	}
	return kept
}

// prefixed appends the FTS5 prefix wildcard to every term of at least 3
// runes (same threshold as the single-term variant). Short tokens — numbers,
// single letters — stay exact: "3*" would prefix-match millions of provisions
// and drown the tier.
func prefixed(terms []string) []string {
	out := make([]string, len(terms))
	for i, t := range terms {
		if utf8.RuneCountInString(t) >= 3 {
			out[i] = t + "*"
		} else {
			out[i] = t
		}
	}
	return out
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

// minStemRuneCount keeps the strip from mangling short words ("hát" → "há",
// "fizet" → "fize", which no longer matches anything).
const minStemRuneCount = 5

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

// StemTerm exposes the light Hungarian stemmer to other packages (title-match
// scoring uses the same stem form as the query variants).
func StemTerm(term string) string { return stemTerm(term) }

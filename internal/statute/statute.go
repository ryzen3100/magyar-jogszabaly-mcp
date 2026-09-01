// Package statute provides statute ID resolution for Hungarian Law MCP.
//
// Resolves fuzzy document references (titles, IDs) to database document IDs.
// Port of src/utils/statute-id.ts.
package statute

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var hungarianRefRe = regexp.MustCompile(`(?i)(\d{4})\.\s*évi\s+([IVXLCDM]+)\.\s*törvény`)

// Unanchored: matches a hu-law ID prefix, so trailing extra characters are stripped.
var huLawPrefixRe = regexp.MustCompile(`^(hu-law-\d{4}-\d+-\d{2}-\d{2})`)

// Escapes SQL LIKE wildcards in user input so % and _ match literally; paired
// with the ESCAPE '\' clause in the substring query below (unescaped
// wildcards turn a title search into a corpus-wide scan).
var likeWildcards = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

var romanValues = map[byte]int{'I': 1, 'V': 5, 'X': 10, 'L': 50, 'C': 100, 'D': 500, 'M': 1000}

// RomanToArabic converts a Roman numeral string to an Arabic number.
// Unknown characters count as 0 (preserved TS quirk).
func RomanToArabic(roman string) int {
	upper := strings.ToUpper(roman)
	result := 0
	for i := range len(upper) {
		current := romanValues[upper[i]]
		next := 0
		if i+1 < len(upper) {
			next = romanValues[upper[i+1]]
		}
		if current < next {
			result -= current
		} else {
			result += current
		}
	}
	return result
}

// ParseHungarianReference tries to parse a Hungarian formal reference like
// "2012. évi I. törvény" and convert it to the database ID format
// "hu-law-2012-1-00-00". Returns "" when the input does not match
// (TS null equivalent; a match can never produce an empty string).
func ParseHungarianReference(input string) string {
	m := hungarianRefRe.FindStringSubmatch(input)
	if m == nil {
		return ""
	}
	return fmt.Sprintf("hu-law-%s-%d-00-00", m[1], RomanToArabic(m[2]))
}

// ResolveDocumentID resolves a document identifier to a database document ID.
// Supports:
// - Direct ID match (e.g., "hu-law-2012-1-00-00")
// - Hungarian formal format (e.g., "2012. évi I. törvény")
// - Title match (e.g., "Infotörvény", "Data Protection Act")
// - Short name/abbreviation match (e.g., "Ptk.", "Btk.")
// - Fuzzy title substring match
//
// Returns "" (TS null equivalent) with a nil error when nothing matches.
func ResolveDocumentID(ctx context.Context, db *sql.DB, input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", nil
	}

	// Exact-ID candidates in priority order: trimmed input, Hungarian formal
	// reference ("2012. évi I. törvény" → "hu-law-2012-1-00-00"), hu-law
	// prefix with trailing extra characters stripped.
	candidates := []string{trimmed}
	if hungarianID := ParseHungarianReference(trimmed); hungarianID != "" {
		candidates = append(candidates, hungarianID)
	}
	if m := huLawPrefixRe.FindStringSubmatch(trimmed); m != nil {
		candidates = append(candidates, m[1])
	}

	for _, candidate := range candidates {
		var id string
		err := db.QueryRowContext(ctx, `SELECT id FROM legal_documents WHERE id = ?`, candidate).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}

	// Title/short_name substring match — single case-insensitive pass
	// (LOWER() is a superset of plain LIKE: it folds ASCII case like LIKE does).
	// ESCAPE '\' + likeWildcards keep user % and _ literal instead of letting
	// them widen the scan.
	pattern := "%" + likeWildcards.Replace(trimmed) + "%"
	var id string
	err := db.QueryRowContext(ctx, `
		SELECT id FROM legal_documents
		WHERE LOWER(title) LIKE LOWER(?) ESCAPE '\'
		   OR LOWER(short_name) LIKE LOWER(?) ESCAPE '\'
		   OR LOWER(title_en) LIKE LOWER(?) ESCAPE '\'
		LIMIT 1`,
		pattern,
		pattern,
		pattern,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return resolveDecreeRef(ctx, db, trimmed)
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

// resolveDecreeRef resolves the decree identifier shorthand (see decreeRefRe)
// after the ordinary substring pass missed. Exactly one hit resolves; two or
// more mean the year/number pair is shared (ministry rendeletek renumber per
// ministry), and the shorthand is treated as not-found instead of guessing.
func resolveDecreeRef(ctx context.Context, db *sql.DB, input string) (string, error) {
	m := decreeRefRe.FindStringSubmatch(input)
	if m == nil {
		return "", nil
	}
	// Anchored at the title start: njt titles always open with the
	// identifier, and amendment decrees CITE other identifiers mid-title
	// ("457/2017. … a … 210/2009. (IX. 29.) Korm. rendelet
	// módosításáról") — an unanchored pattern would flag those as
	// ambiguous second hits. A typed date must match the title verbatim
	// (LOWER() folds the case; LIKE wildcards in the typed date stay
	// escaped literal).
	pattern := likeWildcards.Replace(m[1]) + "." + likeWildcards.Replace(m[2]) + "%" + likeWildcards.Replace(m[3]) + "%"
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM legal_documents WHERE LOWER(title) LIKE LOWER(?) ESCAPE '\' LIMIT 2`, pattern)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var id string
	for rows.Next() {
		if id != "" {
			return "", nil // ambiguous year/number pair
		}
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return id, nil
}

// Section-ref grammar after punctuation stripping: plain ("3"),
// colon-structured ("6:272", "3:99/A"), letter-suffixed ("116/A"),
// ranges ("1–290"), and the Ptk structure prefix ("Ptk4:1").
var sectionRefGrammarRe = regexp.MustCompile(`^(?:[Pp]tk)?\d+(?::\d+)*(?:/[A-Za-z])?(?:[–-]\d+(?::\d+)*)*$`)

// Annex refs: "4. melléklet", "4 melléklet", "3/A. melléklet",
// "6. számú melléklet" (old layout) — the forms the parser stores annex
// provisions under (section "<n>. melléklet").
var annexRefRe = regexp.MustCompile(`(?i)^(\d+(?:/[A-Za-z])?)\s*\.?\s*(?:számú\s+)?melléklet\.?$`)

// Collapses annex refs to the parser's provision_ref shape ("s" + lowercased
// ASCII alnum, i.e. the toProvisionRef form: "4. melléklet" → "s4mellklet").
var annexRefCompactRe = regexp.MustCompile(`[^0-9A-Za-z]`)

// Decree identifier shorthand: "210/2009. Korm. rendelet",
// "210/2009. (IX. 29.) Korm. rendelet". Corpus titles insert the promulgation
// date ("210/2009. (IX. 29.) Korm. rendelet a kereskedelmi …"), so the plain
// substring pass misses; the year/number identifier and the rendelet type are
// matched as two ordered substrings — both must appear verbatim, keeping the
// exact-form contract (no fuzzy widening). A typed promulgation date is
// captured too: several decrees share a year/number with different dates is
// impossible, but the typed date can be WRONG ("73/2016. (XII. 2.)" vs the
// existing "73/2016. (III. 31.)"), and a mismatched date must not resolve.
var decreeRefRe = regexp.MustCompile(`(?i)^(\d{1,4}/\d{4})\.?(\s*\([^)]{1,60}\))?\s+(.{1,60}rendelet)\.?$`)

// Matches a trailing subsection marker: "3. § (2)" → "3. §".
var trailingSubsectionRe = regexp.MustCompile(`^(.+?)\s*\(\d+\)$`)

// Removed when building the compact provision_ref form ("6:272" → "s6272").
var compactSectionRe = strings.NewReplacer(":", "", "/", "", "–", "")

// cutset for stripping citation punctuation around the reference itself.
const sectionRefPunctuation = " \t\u00a0§."

// Matches a Btk-style part prefix: "6:272" → "272".
var partPrefixRe = regexp.MustCompile(`^\d+:(.+)$`)

// SectionRefCandidates normalizes a user-typed section/provision reference —
// "3", "3. §", "3.§ (2)", "s13", "116/A. §", "6:272. §", "1-290" — into the
// section column ("3", "116/A", "6:272", "1–290", "Ptk4:1") and
// provision_ref ("s" + the section with ':', '/' and '–' removed and
// lowercased: "s3", "s11a", "s6272", "s1290"). Part-prefixed forms
// ("6:272") also yield the prefix-dropped form ("272"), which is how
// part-prefixed acts (Btk) store the same section in the corpus.
// Candidates come back exact-match ready and deduplicated; a nil result
// means the input carries no usable reference, and callers answer "not
// found" instead of guessing (zero-hallucination: candidates only narrow to
// canonical stored forms, never widen to fuzzy matches).
func SectionRefCandidates(input string) []string {
	clean := strings.Trim(strings.TrimSpace(input), sectionRefPunctuation)
	if m := trailingSubsectionRe.FindStringSubmatch(clean); m != nil {
		clean = strings.Trim(m[1], sectionRefPunctuation)
	}
	if clean == "" {
		return nil
	}
	// Typed as a provision ref: strip the leading "s" marker ("s13" → "13").
	base := clean
	if len(base) >= 2 && (base[0] == 's' || base[0] == 'S') {
		base = strings.Trim(base[1:], sectionRefPunctuation)
	}
	// Annex ref: emit the stored section label ("<n>. melléklet") and the
	// parser's provision_ref shape so either column resolves ("4. melléklet"
	// and case-variant labels "3/A" resolve via the ref form).
	if m := annexRefRe.FindStringSubmatch(base); m != nil {
		section := m[1] + ". melléklet"
		ref := "s" + strings.ToLower(annexRefCompactRe.ReplaceAllString(section, ""))
		return dedupe([]string{section, ref})
	}
	tidy := strings.NewReplacer(" ", "", "\u00a0", "", "-", "–", ".", "").Replace(base)
	if strings.HasPrefix(strings.ToLower(tidy), "ptk") {
		tidy = "Ptk" + tidy[3:] // the corpus stores the structure prefix capitalized
	}
	if !sectionRefGrammarRe.MatchString(tidy) {
		// Not a known section form: keep the typed text (and its "s"-marked
		// variant) as exact candidates so valid stored provision_refs that
		// fall outside the grammar ("s13a" style) still resolve.
		lower := strings.ToLower(strings.NewReplacer(" ", "", "\u00a0", "").Replace(base))
		return dedupe([]string{lower, "s" + lower})
	}
	compact := compactSectionRe.Replace(strings.ToLower(tidy))
	candidates := []string{tidy, "s" + compact}
	if colon := strings.ReplaceAll(tidy, ":", ""); colon != tidy {
		candidates = append(candidates, colon)
	}
	// Part-prefixed acts (Btk) store sections without the "6:" part
	// prefix, so also try the prefix-dropped canonical form.
	if m := partPrefixRe.FindStringSubmatch(tidy); m != nil {
		dropped := m[1]
		candidates = append(candidates, dropped, "s"+compactSectionRe.Replace(strings.ToLower(dropped)))
	}
	return dedupe(candidates)
}

// dedupe removes duplicates preserving first-seen order (candidate lists are
// bounded, so a map is not worth it).
func dedupe(candidates []string) []string {
	out := candidates[:0]
	for i, c := range candidates {
		if !slices.Contains(candidates[:i], c) {
			out = append(out, c)
		}
	}
	return out
}

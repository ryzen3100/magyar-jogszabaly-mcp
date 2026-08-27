// Package statute provides statute ID resolution for Hungarian Law MCP.
//
// Resolves fuzzy document references (titles, IDs) to database document IDs.
// Port of src/utils/statute-id.ts.
package statute

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var hungarianRefRe = regexp.MustCompile(`(?i)(\d{4})\.\s*évi\s+([IVXLCDM]+)\.\s*törvény`)

// Unanchored: matches a hu-law ID prefix, so trailing extra characters are stripped.
var huLawPrefixRe = regexp.MustCompile(`^(hu-law-\d{4}-\d+-\d{2}-\d{2})`)

var romanValues = map[byte]int{'I': 1, 'V': 5, 'X': 10, 'L': 50, 'C': 100, 'D': 500, 'M': 1000}

// RomanToArabic converts a Roman numeral string to an Arabic number.
// Unknown characters count as 0 (preserved TS quirk).
func RomanToArabic(roman string) int {
	upper := strings.ToUpper(roman)
	result := 0
	for i := 0; i < len(upper); i++ {
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

// ResolveDocumentId resolves a document identifier to a database document ID.
// Supports:
// - Direct ID match (e.g., "hu-law-2012-1-00-00")
// - Hungarian formal format (e.g., "2012. évi I. törvény")
// - Title match (e.g., "Infotörvény", "Data Protection Act")
// - Short name/abbreviation match (e.g., "Ptk.", "Btk.")
// - Fuzzy title substring match
//
// Returns "" (TS null equivalent) with a nil error when nothing matches.
func ResolveDocumentId(db *sql.DB, input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", nil
	}

	// Exact-ID candidates in priority order: trimmed input, Hungarian formal
	// reference ("2012. évi I. törvény" → "hu-law-2012-1-00-00"), hu-law
	// prefix with trailing extra characters stripped.
	candidates := []string{trimmed}
	if hungarianId := ParseHungarianReference(trimmed); hungarianId != "" {
		candidates = append(candidates, hungarianId)
	}
	if m := huLawPrefixRe.FindStringSubmatch(trimmed); m != nil {
		candidates = append(candidates, m[1])
	}

	for _, candidate := range candidates {
		var id string
		err := db.QueryRow(`SELECT id FROM legal_documents WHERE id = ?`, candidate).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}

	// Title/short_name substring match — single case-insensitive pass
	// (LOWER() is a superset of plain LIKE: it folds ASCII case like LIKE does)
	pattern := "%" + trimmed + "%"
	var id string
	err := db.QueryRow(
		`SELECT id FROM legal_documents WHERE LOWER(title) LIKE LOWER(?) OR LOWER(short_name) LIKE LOWER(?) OR LOWER(title_en) LIKE LOWER(?) LIMIT 1`,
		pattern, pattern, pattern,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

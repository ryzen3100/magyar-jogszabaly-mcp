package builddb

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// EURef is one EU legal instrument reference extracted from provision text —
// port of ExtractedEUReference in scripts/build-db.ts:57-67.
type EURef struct {
	Type             string // "regulation" | "directive" (lowercased)
	Community        string // "EU" | "EC" | "EEC" | "Euratom"
	Year             int
	Number           int
	EUDocumentID     string // "<type>:<year>/<number>"
	EUArticle        string // empty where the TS original had null
	FullCitation     string // raw matched text
	ReferenceContext string // ±120 chars around the match, whitespace-collapsed
	ReferenceType    string // "implements" | "references"
}

// The three citation patterns from build-db.ts:256-259, tried in order.
// Copy of the JS regexes verbatim; all constructs are RE2-compatible.
var euPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(Regulation|Directive)\s*\((EU|EC|EEC|Euratom)\)\s*(?:No\.?\s*)?(\d{2,4})\/(\d{1,4})\b`),
	regexp.MustCompile(`(?i)\b(Regulation|Directive)\s*(?:No\.?\s*)?(\d{2,4})\/(\d{1,4})\/(EU|EC|EEC|Euratom)\b`),
	regexp.MustCompile(`(?i)\b(Regulation|Directive)\s*(?:No\.?\s*)?(\d{2,4})\/(\d{1,4})\b`),
}

var (
	euArticleRe  = regexp.MustCompile(`(?i)\bArticle\s+(\d+[A-Za-z]?(?:\(\d+\))?)`)
	implementsRe = regexp.MustCompile(`(?i)\b(implement|align|transpos|equivalent)\b`)
)

// collapseSpace replaces runs of whitespace with a single space and trims —
// port of normalizeWhitespace (JS text.replace(/\s+/g, ' ').trim()).
// unicode.IsSpace covers the JS \s set except U+FEFF; neither occurs in the
// njt.hu corpus.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ExtractEUReferences finds EU directive/regulation citations in text — port
// of extractEUReferences in scripts/build-db.ts:250-302. Patterns are applied
// whole-text in order, sharing one dedupe key space, exactly like the
// sequential JS exec loops.
func ExtractEUReferences(text string) []EURef {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	var refs []EURef
	seen := map[string]bool{}
	var runes []rune // lazily built on first match; only matched provisions pay

	for pi, pattern := range euPatterns {
		// Ascending byte offsets let us convert match offsets to rune indices
		// incrementally: JS indexes UTF-16 code units, Go bytes, and the ±120
		// context window must cut on characters, not bytes (the corpus is
		// BMP-only Hungarian, where runes == UTF-16 units).
		prevByte, prevRune := 0, 0
		runeAt := func(b int) int {
			prevRune += utf8.RuneCountInString(text[prevByte:b])
			prevByte = b
			return prevRune
		}

		for _, m := range pattern.FindAllStringSubmatchIndex(text, -1) {
			// m[0..1] whole match, then group pairs. Group layout differs per
			// pattern; group 1 is always the Regulation|Directive word.
			var yearIdx, numIdx, commIdx int
			switch pi {
			case 0: // type (community) (year) (number)
				commIdx, yearIdx, numIdx = 4, 6, 8
			case 1: // type (year) (number) (community)
				yearIdx, numIdx, commIdx = 4, 6, 8
			default: // type (year) (number)
				yearIdx, numIdx = 4, 6
				commIdx = -1
			}

			// Directives are cited year-first ("Directive 95/46/EC"),
			// regulations number-first ("Regulation (EC) No 561/2006" =
			// number 561, year 2006); modern regulations switched back to
			// year-first. Parse in citation order and swap only when the
			// first number cannot be a year.
			// ponytail: deliberate TS-parity deviation; ambiguity remains
			// where BOTH orders yield plausible years ("Regulation 95/93").
			rawA := text[m[yearIdx]:m[yearIdx+1]]
			rawB := text[m[numIdx]:m[numIdx+1]]
			a, _ := strconv.Atoi(rawA)
			b, _ := strconv.Atoi(rawB)
			year, number := euYear(rawA), b
			if !plausibleEUYear(year) {
				if swapped := euYear(rawB); plausibleEUYear(swapped) {
					year, number = swapped, a
				}
			}
			if year <= 0 || number <= 0 {
				continue
			}

			community := "EU"
			if commIdx >= 0 {
				community = strings.ToUpper(text[m[commIdx]:m[commIdx+1]])
				// The eu_documents schema CHECK stores the community
				// canonically ('Euratom'); citations spell it uppercase
				// ("…REGULATION 302/2005/EURATOM"). Left as-is, the CHECK
				// violation is silently dropped by the OR-IGNORE
				// eu_documents insert and the eu_reference insert then fails
				// on the foreign key — the two swallowed build failures
				// this normalization fixes (hu-law-2007-7-20-1u:s39,
				// hu-law-2022-4-20-8l:s39 → regulation:2005/302).
				if community == "EURATOM" {
					community = "Euratom"
				}
			}
			refType := strings.ToLower(text[m[2]:m[3]])
			euDocumentID := fmt.Sprintf("%s:%d/%d", refType, year, number)

			startRune := runeAt(m[0])
			endRune := startRune + utf8.RuneCountInString(text[m[0]:m[1]])
			if runes == nil {
				runes = []rune(text)
			}
			referenceContext := contextWindow(runes, startRune, endRune)

			article := ""
			if am := euArticleRe.FindStringSubmatch(referenceContext); am != nil {
				article = am[1]
			}
			refKind := "references"
			if implementsRe.MatchString(referenceContext) {
				refKind = "implements"
			}

			dedupeKey := euDocumentID + ":" + article
			if seen[dedupeKey] {
				continue
			}
			seen[dedupeKey] = true

			refs = append(refs, EURef{
				Type:             refType,
				Community:        community,
				Year:             year,
				Number:           number,
				EUDocumentID:     euDocumentID,
				EUArticle:        article,
				FullCitation:     text[m[0]:m[1]],
				ReferenceContext: referenceContext,
				ReferenceType:    refKind,
			})
		}
	}

	return refs
}

// euYear applies the two-digit year pivot (build-db.ts:277) to a citation
// number: "78" becomes 1978, "05" becomes 2005.
func euYear(raw string) int {
	n, _ := strconv.Atoi(raw) // patterns capture digits only
	if len(raw) != 2 {
		return n
	}
	if n >= 50 {
		return 1900 + n
	}
	return 2000 + n
}

// plausibleEUYear reports whether y can be the year of an EU instrument; the
// future bound is what exposes number-first citations like "No 2027/97".
// ponytail: clock-dependent — a rebuild in a later year re-parses
// first-numbers in (buildYear, 2100] as years.
func plausibleEUYear(y int) bool {
	return y >= 1957 && y <= time.Now().Year()
}

// contextWindow returns the ±120-character window around [startRune,endRune),
// whitespace-collapsed — port of the TS
// text.slice(start-120, end+120).replace(/\s+/g, ' ').trim().
func contextWindow(runes []rune, startRune, endRune int) string {
	lo := max(startRune-120, 0)
	hi := min(endRune+120, len(runes))
	return collapseSpace(string(runes[lo:hi]))
}

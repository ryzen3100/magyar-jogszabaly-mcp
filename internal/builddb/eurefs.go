package builddb

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
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

			rawYear := text[m[yearIdx]:m[yearIdx+1]]
			rawNumber := text[m[numIdx]:m[numIdx+1]]
			parsedYear, _ := strconv.Atoi(rawYear)
			year := parsedYear
			if len(rawYear) == 2 { // two-digit year pivot, build-db.ts:277
				if parsedYear >= 50 {
					year = 1900 + parsedYear
				} else {
					year = 2000 + parsedYear
				}
			}
			number, err := strconv.Atoi(rawNumber)
			if year <= 0 || err != nil || number <= 0 {
				continue
			}

			community := "EU"
			if commIdx >= 0 {
				community = strings.ToUpper(text[m[commIdx]:m[commIdx+1]])
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

// contextWindow returns the ±120-character window around [startRune,endRune),
// whitespace-collapsed — port of the TS
// text.slice(start-120, end+120).replace(/\s+/g, ' ').trim().
func contextWindow(runes []rune, startRune, endRune int) string {
	lo := startRune - 120
	if lo < 0 {
		lo = 0
	}
	hi := endRune + 120
	if hi > len(runes) {
		hi = len(runes)
	}
	return collapseSpace(string(runes[lo:hi]))
}

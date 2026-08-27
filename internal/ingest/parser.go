package ingest

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/seed"
)

// ActIndexEntry is one statute in the ingestion corpus.
type ActIndexEntry struct {
	ID          string
	Title       string
	TitleEn     string
	ShortName   string
	Status      string // in_force | amended | repealed | not_yet_in_force
	IssuedDate  string
	InForceDate string
	URL         string
	Description string
}

// jsSpace matches the exact character set of JavaScript's \s so text
// normalization behaves identically to the TS parser.
const jsSpace = `[\s\x{000b}\x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

var (
	numericEntityPattern = regexp.MustCompile(`&#([0-9]+);`)
	hexEntityPattern     = regexp.MustCompile(`&#x([0-9a-fA-F]+);`)

	spaceRunesPattern          = regexp.MustCompile(`[\x{00a0}\x{2000}-\x{200a}\x{202f}\x{205f}\x{3000}]`)
	whitespacePattern          = regexp.MustCompile(jsSpace + `+`)
	spaceBeforePunct           = regexp.MustCompile(`(?:` + jsSpace + `+)([,.;:!?])`)
	openParenSpace             = regexp.MustCompile(`\(` + jsSpace + `+`)
	spaceCloseParen            = regexp.MustCompile(jsSpace + `+\)`)
	fnSupPattern               = regexp.MustCompile(`(?i)<sup[^>]*class="fnSup"[^>]*>[\s\S]*?</sup>`)
	brPattern                  = regexp.MustCompile(`(?i)<br\s*/?>`)
	blockTagPattern            = regexp.MustCompile(`(?i)</?(?:p|div|li|ul|ol|tr|td|th|table|tbody|thead|tfoot|h[1-6])[^>]*>`)
	anyTagPattern              = regexp.MustCompile(`<[^>]+>`)
	digitsSuffixPatt           = regexp.MustCompile(`^(\d+)([A-Z]+)?$`)
	sectionPattern             = regexp.MustCompile(`^(\d+)(?:/([A-Za-z]+))?$`)
	nonAlnumPattern            = regexp.MustCompile(`[^0-9A-Za-z]`)
	szBlockIDPattern           = regexp.MustCompile(`^SZ(\d+)([A-Z]+)?(?:@.*)?$`)
	sectionTextPattern         = regexp.MustCompile(`^(\d+[A-Za-z]?(?:/[A-Za-z]+)?)\s*\.?\s*§`)
	articleTextPatt            = regexp.MustCompile(`(?i)^([IVXLCDM]+|\d+)\s*\.\s*(?:Czikk|Cikk|CZIKK)\.?`)
	jhIDMarkerPattern          = regexp.MustCompile(`<span class="jhId" id="([^"]+)"></span>`)
	blockClassPattern          = regexp.MustCompile(`(?i)^<(?:div|h1|h2)\b[^>]*class="([^"]*)"`)
	szakaszJelPattern          = regexp.MustCompile(`(?i)<span class="szakasz-jel">([\s\S]*?)</span>`)
	legacyClassPattern         = regexp.MustCompile(`(?i)preambulum`)
	sectionContentClassPattern = regexp.MustCompile(`(?i)szakasz|bekezdes|pont|alpont|mondat|szoveg|szelet`)
	leadingDigitsPatt          = regexp.MustCompile(`^\d+`)

	alkalmazasPattern = regexp.MustCompile(`(?i)alkalmazásában`)
	// definitionPattern is the body of the TS lookahead pattern
	// /\b\d+\.\s*([^:;]{2,120}):\s*([^;]{10,500})(?=;\s*\d+\.|$)/g. RE2 has
	// no lookahead, so the guard is restructured into a manual tail check in
	// extractDefinitions: the greedy body match is accepted only when the
	// bytes after it are absent (end of content) or match
	// definitionFollowPattern. See extractDefinitions for why this is
	// equivalent.
	definitionPattern     = regexp.MustCompile(`\b\d+\.\s*([^:;]{2,120}):\s*([^;]{10,500})`)
	definitionFollowPatt  = regexp.MustCompile(`^;\s*\d+\.`)
	mainTitlePattern      = regexp.MustCompile(`(?i)<h1[^>]*class="[^"]*jogszabalyMainTitle[^"]*"[^>]*>([\s\S]*?)</h1>`)
	subtitlePattern       = regexp.MustCompile(`(?i)<h2[^>]*class="([^"]*jogszabalySubtitle[^"]*)"[^>]*>([\s\S]*?)</h2>`)
	mainTitleClassPattern = regexp.MustCompile(`(?i)\bmainTitle\b`)
)

// DecodeHTMLEntities decodes the named entities njt.hu HTML uses, then
// numeric (&#dd;) and hex (&#xhh;) character references. Port of
// decodeHtmlEntities: replacements run in the same order, so e.g.
// "&amp;lt;" decodes further to "<".
func DecodeHTMLEntities(input string) string {
	s := input
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&ndash;", "-")
	s = strings.ReplaceAll(s, "&mdash;", "-")
	s = strings.ReplaceAll(s, "&shy;", "")

	s = numericEntityPattern.ReplaceAllStringFunc(s, func(m string) string {
		code, err := strconv.ParseUint(m[2:len(m)-1], 10, 32)
		// TS String.fromCodePoint throws on out-of-range code points; here
		// an invalid reference decodes to U+FFFD instead of aborting.
		if err != nil || code > utf8.MaxRune {
			return "\uFFFD"
		}
		return string(rune(code))
	})
	return hexEntityPattern.ReplaceAllStringFunc(s, func(m string) string {
		code, err := strconv.ParseUint(m[3:len(m)-1], 16, 32)
		if err != nil || code > utf8.MaxRune {
			return "\uFFFD"
		}
		return string(rune(code))
	})
}

func normalizeExtractedText(input string) string {
	s := spaceRunesPattern.ReplaceAllString(input, " ")
	s = whitespacePattern.ReplaceAllString(s, " ")
	s = spaceBeforePunct.ReplaceAllString(s, "$1")
	s = openParenSpace.ReplaceAllString(s, "(")
	s = spaceCloseParen.ReplaceAllString(s, ")")
	return strings.TrimSpace(s)
}

// HTMLToText converts an HTML fragment to normalized plain text. Port of
// htmlToText.
func HTMLToText(html string) string {
	text := fnSupPattern.ReplaceAllString(html, "")
	text = brPattern.ReplaceAllString(text, "\n")
	text = blockTagPattern.ReplaceAllString(text, "\n")
	text = anyTagPattern.ReplaceAllString(text, "")
	return normalizeExtractedText(DecodeHTMLEntities(text))
}

func parseSectionFromMarker(rawMarker string) string {
	marker := HTMLToText(rawMarker)
	marker = strings.ReplaceAll(marker, "§", "")
	marker = strings.ReplaceAll(marker, ".", "")
	marker = whitespacePattern.ReplaceAllString(marker, "")
	return strings.TrimSpace(marker)
}

func parseSectionFromKey(key string) string {
	if rest, ok := strings.CutPrefix(key, "ART_"); ok {
		return rest
	}
	if rest, ok := strings.CutPrefix(key, "LEGACY_"); ok {
		return rest
	}
	m := digitsSuffixPatt.FindStringSubmatch(key)
	if m == nil {
		return key
	}
	if m[2] == "" {
		return m[1]
	}
	return m[1] + "/" + m[2]
}

func sectionToKey(section string) string {
	if m := sectionPattern.FindStringSubmatch(section); m != nil {
		return m[1] + strings.ToUpper(m[2])
	}
	return nonAlnumPattern.ReplaceAllString(section, "")
}

func parseSectionKeyFromBlockID(blockID string) string {
	m := szBlockIDPattern.FindStringSubmatch(blockID)
	if m == nil {
		return ""
	}
	return m[1] + m[2]
}

func parseSectionFromText(blockHTML string) string {
	m := sectionTextPattern.FindStringSubmatch(HTMLToText(blockHTML))
	if m == nil {
		return ""
	}
	return strings.ToUpper(m[1])
}

func parseArticleFromText(blockHTML string) string {
	m := articleTextPatt.FindStringSubmatch(HTMLToText(blockHTML))
	if m == nil {
		return ""
	}
	return strings.ToUpper(m[1])
}

func articleToKey(article string) string {
	return "ART_" + strings.ToUpper(nonAlnumPattern.ReplaceAllString(article, ""))
}

type njtBlock struct {
	blockID    string
	blockClass string
	blockHTML  string
	blockPos   int
}

func extractNjtBlocks(html string) []njtBlock {
	locations := jhIDMarkerPattern.FindAllStringSubmatchIndex(html, -1)
	blocks := make([]njtBlock, 0, len(locations))
	for i, loc := range locations {
		chunkStart := loc[1]
		nextStart := len(html)
		if i+1 < len(locations) {
			nextStart = locations[i+1][0]
		}

		blockHTML := strings.TrimSpace(html[chunkStart:nextStart])
		if blockHTML == "" {
			continue
		}

		blockClass := ""
		if m := blockClassPattern.FindStringSubmatch(blockHTML); m != nil {
			blockClass = m[1]
		}

		blocks = append(blocks, njtBlock{
			blockID:    html[loc[2]:loc[3]],
			blockClass: blockClass,
			blockHTML:  blockHTML,
			blockPos:   loc[0],
		})
	}
	return blocks
}

func isSectionContentClass(blockClass string) bool {
	return sectionContentClassPattern.MatchString(blockClass)
}

func isLegacyContentClass(blockClass string) bool {
	return legacyClassPattern.MatchString(blockClass) || isSectionContentClass(blockClass)
}

func provisionTitleFromKey(key, section string) string {
	switch {
	case strings.HasPrefix(key, "ART_"):
		return section + ". Cikk"
	case strings.HasPrefix(key, "LEGACY_"):
		return section
	default:
		return section + ". §"
	}
}

func toProvisionRef(section string) string {
	return "s" + strings.ToLower(nonAlnumPattern.ReplaceAllString(section, ""))
}

// shouldIncludeSection ports the hardcoded corpus-scope special cases: the
// public-data alias keeps only Chapter III (26-39) of the Infotörvény and
// the cybercrime alias keeps only Btk. 422-424.
func shouldIncludeSection(actID, section string) bool {
	base := 0
	if m := leadingDigitsPatt.FindString(section); m != "" {
		base, _ = strconv.Atoi(m)
	}

	switch actID {
	case "act-cxii-2011-public-data":
		return base >= 26 && base <= 39
	case "criminal-code-cybercrime":
		return section == "422" || section == "423" || section == "424"
	default:
		return true
	}
}

// extractDefinitions appends definitions found in content to defs. Port of
// extractDefinitions with the lookahead restructured away: the TS pattern
// requires group 2 (greedy, no ';') to be followed by `;<digits>.` or the
// end of content. Greedy group 2 always ends just before the next ';' (or at
// the 500-cap / end of content), and shortening it would place a non-';'
// byte at the lookahead position, which can never satisfy the guard — so
// there is at most one candidate per start position and validating the bytes
// after the greedy match is exactly the TS lookahead.
func extractDefinitions(content, sourceProvision string, defs *[]seed.DefinitionSeed) {
	if !alkalmazasPattern.MatchString(content) {
		return
	}

	for _, loc := range definitionPattern.FindAllStringSubmatchIndex(content, -1) {
		tail := content[loc[1]:]
		if tail != "" && !definitionFollowPatt.MatchString(tail) {
			continue
		}

		term := strings.TrimSpace(content[loc[2]:loc[3]])
		definition := strings.TrimSpace(content[loc[4]:loc[5]])
		// TS .length counts UTF-16 units; all Hungarian text is BMP, where
		// rune counts agree.
		if utf8.RuneCountInString(term) < 2 || utf8.RuneCountInString(definition) < 10 {
			continue
		}

		*defs = append(*defs, seed.DefinitionSeed{
			Term:            term,
			Definition:      definition,
			SourceProvision: sourceProvision,
		})
	}
}

func extractOfficialTitle(html string) string {
	main := ""
	if m := mainTitlePattern.FindStringSubmatch(html); m != nil {
		main = m[1]
	}

	subtitleMatches := subtitlePattern.FindAllStringSubmatch(html, -1)
	subtitle := ""
	for _, m := range subtitleMatches {
		if !mainTitleClassPattern.MatchString(m[1]) {
			subtitle = m[2]
			break
		}
	}
	if subtitle == "" && len(subtitleMatches) > 0 {
		subtitle = subtitleMatches[len(subtitleMatches)-1][2]
	}

	mainText := ""
	if main != "" {
		mainText = HTMLToText(main)
	}
	subtitleText := ""
	if subtitle != "" {
		subtitleText = HTMLToText(subtitle)
	}

	combined := mainText
	if subtitleText != "" && subtitleText != mainText {
		combined = strings.TrimSpace(mainText + " " + subtitleText)
	}
	return combined
}

type sectionAccumulator struct {
	key      string
	section  string
	chapter  string
	firstPos int
	blocks   []string
}

func joinChapter(number, title string) string {
	parts := make([]string, 0, 2)
	if number != "" {
		parts = append(parts, number)
	}
	if title != "" {
		parts = append(parts, title)
	}
	return strings.Join(parts, " - ")
}

// ParseHungarianHTML parses njt.hu HTML into a seed-compatible structure.
// Port of parseHungarianHtml.
func ParseHungarianHTML(html string, act ActIndexEntry) seed.DocumentSeed {
	sections := map[string]*sectionAccumulator{}
	var definitions []seed.DefinitionSeed
	blocks := extractNjtBlocks(html)

	currentChapterNumber := ""
	currentChapterTitle := ""
	activeSectionKey := ""

	for _, block := range blocks {
		if block.blockClass == "fejezet" {
			currentChapterNumber = HTMLToText(block.blockHTML)
		} else if block.blockClass == "fejezetCim" {
			currentChapterTitle = HTMLToText(block.blockHTML)
		}

		// An existing but empty szakasz-jel marker yields "" and must NOT
		// fall through to parseSectionFromText (TS `??` only skips null).
		sectionFromText := ""
		if m := szakaszJelPattern.FindStringSubmatch(block.blockHTML); m != nil {
			sectionFromText = parseSectionFromMarker(m[1])
		} else {
			sectionFromText = parseSectionFromText(block.blockHTML)
		}
		articleFromText := parseArticleFromText(block.blockHTML)
		keyFromID := parseSectionKeyFromBlockID(block.blockID)

		key := ""
		switch {
		case keyFromID != "":
			key = keyFromID
			activeSectionKey = key
		case sectionFromText != "":
			key = sectionToKey(sectionFromText)
			activeSectionKey = key
		case articleFromText != "":
			key = articleToKey(articleFromText)
			activeSectionKey = key
		case activeSectionKey != "" && isSectionContentClass(block.blockClass):
			key = activeSectionKey
		}
		if key == "" {
			continue
		}

		acc, ok := sections[key]
		if !ok {
			acc = &sectionAccumulator{
				key:      key,
				chapter:  joinChapter(currentChapterNumber, currentChapterTitle),
				firstPos: block.blockPos,
			}
			sections[key] = acc
		}

		if sectionFromText != "" {
			acc.section = sectionFromText
		} else if articleFromText != "" {
			acc.section = articleFromText
		} else if acc.section == "" {
			acc.section = parseSectionFromKey(key)
		}

		if isSectionContentClass(block.blockClass) || sectionFromText != "" || articleFromText != "" {
			acc.blocks = append(acc.blocks, block.blockHTML)
		}
	}

	if len(sections) == 0 {
		legacyIndex := 0
		for _, block := range blocks {
			if !isLegacyContentClass(block.blockClass) {
				continue
			}
			if HTMLToText(block.blockHTML) == "" {
				continue
			}

			legacyIndex++
			key := fmt.Sprintf("LEGACY_%d", legacyIndex)
			sections[key] = &sectionAccumulator{
				key:      key,
				section:  strconv.Itoa(legacyIndex),
				firstPos: block.blockPos,
				blocks:   []string{block.blockHTML},
			}
		}
	}

	sortedSections := slices.SortedFunc(maps.Values(sections), func(a, b *sectionAccumulator) int {
		return cmp.Compare(a.firstPos, b.firstPos)
	})

	provisions := []seed.ProvisionSeed{}
	for _, sectionData := range sortedSections {
		section := sectionData.section
		if section == "" {
			section = parseSectionFromKey(sectionData.key)
		}
		if !shouldIncludeSection(act.ID, section) {
			continue
		}

		contentParts := make([]string, 0, len(sectionData.blocks))
		for _, blockHTML := range sectionData.blocks {
			if part := HTMLToText(blockHTML); part != "" {
				contentParts = append(contentParts, part)
			}
		}
		if len(contentParts) == 0 {
			continue
		}

		content := strings.TrimSpace(whitespacePattern.ReplaceAllString(strings.Join(contentParts, " "), " "))
		if content == "" {
			continue
		}

		provisionRef := toProvisionRef(section)
		provisions = append(provisions, seed.ProvisionSeed{
			ProvisionRef: provisionRef,
			Chapter:      sectionData.chapter,
			Section:      section,
			Title:        provisionTitleFromKey(sectionData.key, section),
			Content:      content,
		})

		extractDefinitions(content, provisionRef, &definitions)
	}

	dedupDefinitions := make([]seed.DefinitionSeed, 0, len(definitions))
	seenDefinitions := map[string]struct{}{}
	for _, def := range definitions {
		dedupKey := strings.ToLower(def.Term) + "|" + def.SourceProvision
		if _, seen := seenDefinitions[dedupKey]; seen {
			continue
		}
		seenDefinitions[dedupKey] = struct{}{}
		dedupDefinitions = append(dedupDefinitions, def)
	}

	officialTitle := extractOfficialTitle(html)
	keepCustomTitle := act.ID == "act-cxii-2011-public-data" || act.ID == "criminal-code-cybercrime"
	title := act.Title
	if officialTitle != "" && !keepCustomTitle {
		title = officialTitle
	}

	return seed.DocumentSeed{
		ID:          act.ID,
		Type:        "statute",
		Title:       title,
		TitleEn:     act.TitleEn,
		ShortName:   act.ShortName,
		Status:      act.Status,
		IssuedDate:  act.IssuedDate,
		InForceDate: act.InForceDate,
		URL:         act.URL,
		Description: act.Description,
		Provisions:  provisions,
		Definitions: dedupDefinitions,
	}
}

// KeyHungarianActs are the curated statutes covered by the MCP, copied
// verbatim (including the Kbt. entry's id/title mismatch and date quirks)
// from parser.ts.
var KeyHungarianActs = []ActIndexEntry{
	{
		ID:          "act-cxii-2011-info-self-determination",
		Title:       "2011. évi CXII. törvény az információs önrendelkezési jogról és az információszabadságról",
		TitleEn:     "Act CXII of 2011 on Informational Self-Determination and Freedom of Information",
		ShortName:   "Infotörvény",
		Status:      "in_force",
		IssuedDate:  "2011-07-26",
		InForceDate: "2012-01-01",
		URL:         "https://njt.hu/jogszabaly/2011-112-00-00",
		Description: "Hungary's primary data protection and freedom of information statute, including GDPR-aligned provisions.",
	},
	{
		ID:          "act-cxii-2011-public-data",
		Title:       "2011. évi CXII. törvény - Közérdekű adatok megismerése (III. fejezet)",
		TitleEn:     "Act CXII of 2011 - Access to Public Interest Data (Chapter III)",
		ShortName:   "Infotörvény - Public Data",
		Status:      "in_force",
		IssuedDate:  "2011-07-26",
		InForceDate: "2012-01-01",
		URL:         "https://njt.hu/jogszabaly/2011-112-00-00",
		Description: "Public-data access provisions extracted from the Infotörvény (sections 26-39).",
	},
	{
		ID:          "act-l-2013-electronic-info-security",
		Title:       "2013. évi L. törvény az állami és önkormányzati szervek elektronikus információbiztonságáról",
		TitleEn:     "Act L of 2013 on Electronic Information Security of State and Municipal Bodies",
		ShortName:   "Ibtv.",
		Status:      "in_force",
		IssuedDate:  "2013-04-25",
		InForceDate: "2013-07-01",
		URL:         "https://njt.hu/jogszabaly/2013-50-00-00",
		Description: "Core Hungarian public-sector cybersecurity framework (Ibtv.).",
	},
	{
		ID:          "act-cviii-2001-electronic-commerce",
		Title:       "2001. évi CVIII. törvény az elektronikus kereskedelmi szolgáltatások, valamint az információs társadalommal összefüggő szolgáltatások egyes kérdéseiről",
		TitleEn:     "Act CVIII of 2001 on Electronic Commerce and Certain Information Society Services",
		ShortName:   "Ekertv.",
		Status:      "in_force",
		IssuedDate:  "2001-12-21",
		InForceDate: "2002-01-16",
		URL:         "https://njt.hu/jogszabaly/2001-108-00-00",
		Description: "Hungarian e-commerce and intermediary liability statute.",
	},
	{
		ID:          "act-c-2003-electronic-communications",
		Title:       "2003. évi C. törvény az elektronikus hírközlésről",
		TitleEn:     "Act C of 2003 on Electronic Communications",
		ShortName:   "Eht.",
		Status:      "in_force",
		IssuedDate:  "2003-11-17",
		InForceDate: "2004-01-01",
		URL:         "https://njt.hu/jogszabaly/2003-100-00-00",
		Description: "Primary telecommunications statute (Eht.).",
	},
	{
		ID:          "act-clxvi-2012-critical-infrastructure",
		Title:       "2012. évi CLXVI. törvény a létfontosságú rendszerek és létesítmények azonosításáról, kijelöléséről és védelméről",
		TitleEn:     "Act CLXVI of 2012 on Identification, Designation and Protection of Vital Systems and Facilities",
		ShortName:   "Lrtv.",
		Status:      "in_force",
		IssuedDate:  "2012-11-12",
		InForceDate: "2012-12-01",
		URL:         "https://njt.hu/jogszabaly/2012-166-00-00",
		Description: "Critical infrastructure statute.",
	},
	{
		ID:          "act-liv-2018-trade-secrets",
		Title:       "2018. évi LIV. törvény az üzleti titok védelméről",
		TitleEn:     "Act LIV of 2018 on the Protection of Trade Secrets",
		ShortName:   "Üzleti titok tv.",
		Status:      "in_force",
		IssuedDate:  "2018-06-29",
		InForceDate: "2018-08-08",
		URL:         "https://njt.hu/jogszabaly/2018-54-00-00",
		Description: "Hungarian trade secrets statute (EU 2016/943 implementation context).",
	},
	{
		ID:          "act-ccxxii-2015-trust-services",
		Title:       "2015. évi CCXXII. törvény az elektronikus ügyintézés és a bizalmi szolgáltatások általános szabályairól",
		TitleEn:     "Act CCXXII of 2015 on Electronic Administration and Trust Services",
		ShortName:   "E-ügyintézési tv.",
		Status:      "in_force",
		IssuedDate:  "2015-12-21",
		InForceDate: "2016-07-01",
		URL:         "https://njt.hu/jogszabaly/2015-222-00-00",
		Description: "Electronic administration and trust services statute.",
	},
	{
		ID:          "act-lxiii-1999-public-procurement",
		Title:       "2015. évi CXLIII. törvény a közbeszerzésekről",
		TitleEn:     "Act CXLIII of 2015 on Public Procurement",
		ShortName:   "Kbt.",
		Status:      "in_force",
		IssuedDate:  "2015-11-02",
		InForceDate: "2015-11-01",
		URL:         "https://njt.hu/jogszabaly/2015-143-00-00",
		Description: "Public procurement statute.",
	},
	{
		ID:          "criminal-code-cybercrime",
		Title:       "2012. évi C. törvény a Büntető Törvénykönyvről - Informatikai bűncselekmények",
		TitleEn:     "Act C of 2012 on the Criminal Code - Cybercrime Provisions",
		ShortName:   "Btk. (Cybercrime)",
		Status:      "in_force",
		IssuedDate:  "2012-07-13",
		InForceDate: "2013-07-01",
		URL:         "https://njt.hu/jogszabaly/2012-100-00-00",
		Description: "Cybercrime-relevant sections (422-424) from the Criminal Code.",
	},
}

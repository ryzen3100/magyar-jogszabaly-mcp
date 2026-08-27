package ingest

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/seed"
)

func sampleActHTML(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/act-sample.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(data)
}

func TestDecodeHTMLEntities(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no entities", "sima szöveg", "sima szöveg"},
		{"named set", "&nbsp;&amp;&lt;&gt;&quot;&#39;&ndash;&mdash;&shy;x", " &<>\"'--x"},
		{"numeric", "&#71;", "G"},
		{"hex", "&#x71;", "q"},
		{"mixed numeric and named", "R&#233;szlet &quot;A&quot;", "Részlet \"A\""},
		{"non-BMP hex", "&#x1F600;", "\U0001F600"},
		// TS decodes in order, so "&amp;lt;" -> "&lt;" -> "<".
		{"double decode", "&amp;lt;", "<"},
		{"shy removed", "Közérdekű&shy;adat", "Közérdekűadat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DecodeHTMLEntities(tt.input); got != tt.want {
				t.Errorf("DecodeHTMLEntities(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestHTMLToText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "szöveg", "szöveg"},
		{"footnote removed", `alapja<sup class="fnSup">[1]</sup> folytatása`, "alapja folytatása"},
		{"footnote uppercase tag", `alap<sup CLASS="fnSup">2</sup>vég`, "alapvég"},
		{"br to space", "a<br>b", "a b"},
		{"br with slash", "a<br/>b", "a b"},
		{"block tags break", "<p>egy</p><div>kettő</div>", "egy kettő"},
		{"remaining tags stripped", "szöveg <b>vastag</b> vége", "szöveg vastag vége"},
		{"nbsp and unicode spaces", "a\u00a0b\u2003c\u3000d", "a b c d"},
		{"whitespace collapsed", "a \n\t b", "a b"},
		{"space before punctuation", "vége . x , y ; z !", "vége. x, y; z!"},
		{"paren spacing", "( ilyen )", "(ilyen)"},
		{"entities decoded", "R&#233;szlet &quot;A&quot;&nbsp;- &lt;tag&gt;", `Részlet "A" - <tag>`},
		{"footnote inside section", "<div>422. §.szöveg<sup class=\"fnSup\">[9]</sup>.</div>", "422. §.szöveg."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HTMLToText(tt.input); got != tt.want {
				t.Errorf("HTMLToText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseSectionKeyFromBlockID(t *testing.T) {
	tests := []struct {
		name    string
		blockID string
		want    string
	}{
		{"digits", "SZ422", "422"},
		{"digits and letter", "SZ422A", "422A"},
		{"at suffix", "SZ422@1", "422"},
		{"multiple at signs", "SZ422@valami@2", "422"},
		{"underscore block", "SZ1_B1", ""},
		{"missing SZ prefix", "X1", ""},
		{"SZ only", "SZ", ""},
		{"letters only", "SZABC", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseSectionKeyFromBlockID(tt.blockID); got != tt.want {
				t.Errorf("parseSectionKeyFromBlockID(%q) = %q, want %q", tt.blockID, got, tt.want)
			}
		})
	}
}

func TestSectionKeyRoundTrip(t *testing.T) {
	tests := []struct {
		section string
		wantKey string
	}{
		{"422", "422"},
		{"422/A", "422A"},
		{"1/B", "1B"},
	}
	for _, tt := range tests {
		t.Run(tt.section, func(t *testing.T) {
			key := sectionToKey(tt.section)
			if key != tt.wantKey {
				t.Errorf("sectionToKey(%q) = %q, want %q", tt.section, key, tt.wantKey)
			}
			if back := parseSectionFromKey(key); back != tt.section {
				t.Errorf("parseSectionFromKey(%q) = %q, want %q", key, back, tt.section)
			}
		})
	}

	// Non-numeric keys pass through; prefixed keys are stripped.
	if got := parseSectionFromKey("ART_I"); got != "I" {
		t.Errorf("parseSectionFromKey(ART_I) = %q", got)
	}
	if got := parseSectionFromKey("LEGACY_3"); got != "3" {
		t.Errorf("parseSectionFromKey(LEGACY_3) = %q", got)
	}
	if got := parseSectionFromKey("szoveg-blokk"); got != "szoveg-blokk" {
		t.Errorf("parseSectionFromKey passthrough = %q", got)
	}
	if got := articleToKey("I"); got != "ART_I" {
		t.Errorf("articleToKey(I) = %q", got)
	}
}

func TestToProvisionRef(t *testing.T) {
	tests := []struct{ section, want string }{
		{"422", "s422"},
		{"422/A", "s422a"},
		{"1", "s1"},
		{"I", "si"},
		{"12/B", "s12b"},
	}
	for _, tt := range tests {
		t.Run(tt.section, func(t *testing.T) {
			if got := toProvisionRef(tt.section); got != tt.want {
				t.Errorf("toProvisionRef(%q) = %q, want %q", tt.section, got, tt.want)
			}
		})
	}
}

func TestShouldIncludeSection(t *testing.T) {
	tests := []struct {
		actID   string
		section string
		want    bool
	}{
		{"act-cxii-2011-public-data", "25", false},
		{"act-cxii-2011-public-data", "26", true},
		{"act-cxii-2011-public-data", "39", true},
		{"act-cxii-2011-public-data", "40", false},
		{"act-cxii-2011-public-data", "422", false},
		{"act-cxii-2011-public-data", "422/A", false},
		{"criminal-code-cybercrime", "421", false},
		{"criminal-code-cybercrime", "422", true},
		{"criminal-code-cybercrime", "422/A", false},
		{"criminal-code-cybercrime", "423", true},
		{"criminal-code-cybercrime", "424", true},
		{"criminal-code-cybercrime", "425", false},
		{"act-cxii-2011-info-self-determination", "1", true},
		{"act-cxii-2011-info-self-determination", "999", true},
		{"act-cxii-2011-info-self-determination", "I", true},
	}
	for _, tt := range tests {
		t.Run(tt.actID+" "+tt.section, func(t *testing.T) {
			if got := shouldIncludeSection(tt.actID, tt.section); got != tt.want {
				t.Errorf("shouldIncludeSection(%q, %q) = %v, want %v", tt.actID, tt.section, got, tt.want)
			}
		})
	}
}

func TestExtractNjtBlocks(t *testing.T) {
	blocks := extractNjtBlocks(sampleActHTML(t))

	wantIDs := []string{"Fej1", "FejCim1", "SZ422", "SZ422A", "SZ422@1", "Cikk1", "SZ1", "SZ1_B1", "SZ2", "SZ25", "SZ26", "SZ423"}
	if len(blocks) != len(wantIDs) {
		t.Fatalf("got %d blocks, want %d", len(blocks), len(wantIDs))
	}
	for i, want := range wantIDs {
		if blocks[i].blockID != want {
			t.Errorf("block %d id = %q, want %q", i, blocks[i].blockID, want)
		}
		if i > 0 && blocks[i].blockPos <= blocks[i-1].blockPos {
			t.Errorf("block positions not ascending at %d", i)
		}
	}

	if blocks[0].blockClass != "fejezet" || blocks[1].blockClass != "fejezetCim" {
		t.Errorf("chapter block classes = %q, %q", blocks[0].blockClass, blocks[1].blockClass)
	}
	if blocks[2].blockClass != "szakasz" {
		t.Errorf("SZ422 class = %q, want szakasz", blocks[2].blockClass)
	}
	if blocks[5].blockClass != "preambulum" {
		t.Errorf("Cikk1 class = %q, want preambulum", blocks[5].blockClass)
	}
	if !strings.Contains(blocks[2].blockHTML, "szakasz-jel") {
		t.Errorf("SZ422 html should contain the marker span")
	}
}

func TestParseHungarianHTML(t *testing.T) {
	act := ActIndexEntry{
		ID:          "act-cxii-2011-info-self-determination",
		Title:       "curated fallback title",
		TitleEn:     "Act EN",
		ShortName:   "Infotörvény",
		Status:      "in_force",
		IssuedDate:  "2011-07-26",
		InForceDate: "2012-01-01",
		URL:         "https://njt.hu/jogszabaly/2011-112-00-00",
		Description: "desc",
	}
	doc := ParseHungarianHTML(sampleActHTML(t), act)

	// Official title from h1 + first non-main h2 subtitle.
	wantTitle := "2011. évi CXII. törvény az információs önrendelkezési jogról és az információszabadságról"
	if doc.Title != wantTitle {
		t.Errorf("title = %q, want %q", doc.Title, wantTitle)
	}
	if doc.ID != act.ID || doc.Type != "statute" || doc.TitleEn != act.TitleEn || doc.Status != act.Status ||
		doc.IssuedDate != act.IssuedDate || doc.InForceDate != act.InForceDate || doc.URL != act.URL ||
		doc.ShortName != act.ShortName || doc.Description != act.Description {
		t.Errorf("metadata not carried through: %+v", doc)
	}

	wantRefs := []string{"s422", "s422a", "si", "s1", "s2", "s25", "s26", "s423"}
	wantSections := []string{"422", "422/A", "I", "1", "2", "25", "26", "423"}
	wantTitles := []string{"422. §", "422/A. §", "I. Cikk", "1. §", "2. §", "25. §", "26. §", "423. §"}
	if len(doc.Provisions) != len(wantRefs) {
		t.Fatalf("got %d provisions, want %d", len(doc.Provisions), len(wantRefs))
	}
	chapter := "III. Fejezet - Közérdekű adatok megismerése"
	for i, p := range doc.Provisions {
		if p.ProvisionRef != wantRefs[i] {
			t.Errorf("provision %d ref = %q, want %q", i, p.ProvisionRef, wantRefs[i])
		}
		if p.Section != wantSections[i] {
			t.Errorf("provision %d section = %q, want %q", i, p.Section, wantSections[i])
		}
		if p.Title != wantTitles[i] {
			t.Errorf("provision %d title = %q, want %q", i, p.Title, wantTitles[i])
		}
		if p.Chapter != chapter {
			t.Errorf("provision %d chapter = %q, want %q", i, p.Chapter, chapter)
		}
	}

	// Spot-check contents: footnote removal, entity decoding, text
	// normalization, and multi-block joining.
	p422 := doc.Provisions[0].Content
	if !strings.Contains(p422, "422. § Aki jogosulatlanul rendelkezik adattal, bűntettet követ el.") {
		t.Errorf("422 content unexpected: %q", p422)
	}
	if strings.Contains(p422, "[3]") {
		t.Errorf("422 content kept the footnote: %q", p422)
	}
	if !strings.Contains(p422, "Az (elsődleges) szabálysértési 'rész' esetén az elkövető <büntetendő> - reputed.") {
		t.Errorf("422 second block not normalized as expected: %q", p422)
	}
	if got := doc.Provisions[1].Content; got != `422/A. § A felhasználására vonatkozó "súlyos" szabályt megsértők is büntetendők.` {
		t.Errorf("422/A content = %q", got)
	}
	if got := doc.Provisions[3].Content; got != "1. § Ez a teszt törvény célja aqadatvédelem. A hivatkozás (ilyen) jelölés." {
		t.Errorf("1 content = %q", got)
	}
	if got := doc.Provisions[4].Content; got != "2. § E törvény alkalmazásában 1. adatkezelő: az a természetes vagy jogi személy, aki adatot kezel; 2. adatkezelés: az adatokon végzett bármely művelet." {
		t.Errorf("2 content = %q", got)
	}
	if got := doc.Provisions[5].Content; got != "25. § Adatállomány & nyilvántartás kezelése." {
		t.Errorf("25 content = %q", got)
	}
	if got := doc.Provisions[6].Content; got != "26. § Közérdekűadat megismerése szabadsága - mindenkinek - jár." {
		t.Errorf("26 content = %q", got)
	}
	if got := doc.Provisions[7].Content; got != "423. § Az adatrendszerbe való jogosulatlan beavatkozás büntetendő." {
		t.Errorf("423 content = %q", got)
	}

	// Definitions extracted from the "alkalmazásában" passage in section 2.
	// The first term keeps the leading section-header text: the TS pattern
	// matches the leftmost "\d+\." (the "2." of the section marker itself).
	if len(doc.Definitions) != 2 {
		t.Fatalf("got %d definitions, want 2: %+v", len(doc.Definitions), doc.Definitions)
	}
	if doc.Definitions[0].Term != "§ E törvény alkalmazásában 1. adatkezelő" {
		t.Errorf("def 0 term = %q", doc.Definitions[0].Term)
	}
	if doc.Definitions[0].Definition != "az a természetes vagy jogi személy, aki adatot kezel" {
		t.Errorf("def 0 definition = %q", doc.Definitions[0].Definition)
	}
	if doc.Definitions[1].Term != "adatkezelés" {
		t.Errorf("def 1 term = %q", doc.Definitions[1].Term)
	}
	if doc.Definitions[1].Definition != "az adatokon végzett bármely művelet." {
		t.Errorf("def 1 definition = %q", doc.Definitions[1].Definition)
	}
	for _, def := range doc.Definitions {
		if def.SourceProvision != "s2" {
			t.Errorf("definition source_provision = %q, want s2", def.SourceProvision)
		}
	}
}

func TestParseHungarianHTML_SpecialCases(t *testing.T) {
	html := sampleActHTML(t)

	t.Run("criminal-code-cybercrime keeps 422/423/424 and custom title", func(t *testing.T) {
		act := ActIndexEntry{ID: "criminal-code-cybercrime", Title: "2012. évi C. törvény a Büntető Törvénykönyvről - Informatikai bűncselekmények", Status: "in_force"}
		doc := ParseHungarianHTML(html, act)
		var got []string
		for _, p := range doc.Provisions {
			got = append(got, p.Section)
		}
		if strings.Join(got, ",") != "422,423" {
			t.Errorf("sections = %v, want [422 423]", got)
		}
		if doc.Title != act.Title {
			t.Errorf("custom title not kept: %q", doc.Title)
		}
	})

	t.Run("act-cxii-2011-public-data keeps 26-39 and custom title", func(t *testing.T) {
		act := ActIndexEntry{ID: "act-cxii-2011-public-data", Title: "2011. évi CXII. törvény - Közérdekű adatok megismerése (III. fejezet)", Status: "in_force"}
		doc := ParseHungarianHTML(html, act)
		if len(doc.Provisions) != 1 || doc.Provisions[0].Section != "26" {
			t.Errorf("sections = %+v, want only 26", doc.Provisions)
		}
		if doc.Title != act.Title {
			t.Errorf("custom title not kept: %q", doc.Title)
		}
	})
}

func TestParseHungarianHTML_LegacyFallback(t *testing.T) {
	html := `<html><body>
<span class="jhId" id="Pre1"></span><div class="preambulum">Első bekezdés szövege.</div>
<span class="jhId" id="Ures1"></span><div class="egyeb">Nem tartalom blokk.</div>
<span class="jhId" id="Pre2"></span><div class="bekezdes">Második tartalom.</div>
<span class="jhId" id="Pre3"></span><div class="preambulum"><br></div>
</body></html>`

	act := ActIndexEntry{ID: "some-legacy-act", Title: "Legacy Act", Status: "amended"}
	doc := ParseHungarianHTML(html, act)

	if len(doc.Provisions) != 2 {
		t.Fatalf("got %d provisions, want 2: %+v", len(doc.Provisions), doc.Provisions)
	}
	if doc.Provisions[0].ProvisionRef != "s1" || doc.Provisions[0].Section != "1" || doc.Provisions[0].Title != "1" {
		t.Errorf("legacy provision 0 = %+v", doc.Provisions[0])
	}
	if doc.Provisions[0].Content != "Első bekezdés szövege." {
		t.Errorf("legacy provision 0 content = %q", doc.Provisions[0].Content)
	}
	if doc.Provisions[1].ProvisionRef != "s2" || doc.Provisions[1].Section != "2" || doc.Provisions[1].Title != "2" {
		t.Errorf("legacy provision 1 = %+v", doc.Provisions[1])
	}
	// Non-legacy blocks (and empty legacy blocks) are skipped entirely.
	if doc.Provisions[1].Content != "Második tartalom." {
		t.Errorf("legacy provision 1 content = %q", doc.Provisions[1].Content)
	}
}

func TestParseHungarianHTML_NoMarkers(t *testing.T) {
	doc := ParseHungarianHTML("<html>no structured content</html>", ActIndexEntry{ID: "x", Title: "T", Status: "in_force"})
	if len(doc.Provisions) != 0 || len(doc.Definitions) != 0 {
		t.Errorf("expected empty provisions/definitions, got %+v", doc)
	}
}

func TestExtractOfficialTitle(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{"main only", `<h1 class="jogszabalyMainTitle">Cím</h1>`, "Cím"},
		{"main and subtitle", `<h1 class="jogszabalyMainTitle">Cím</h1><h2 class="jogszabalySubtitle">Alcím</h2>`, "Cím Alcím"},
		{"subtitle equal to main is dropped", `<h1 class="jogszabalyMainTitle">Cím</h1><h2 class="jogszabalySubtitle">Cím</h2>`, "Cím"},
		{"mainTitle-class subtitle skipped", `<h1 class="jogszabalyMainTitle">Cím</h1><h2 class="jogszabalySubtitle mainTitle">X</h2><h2 class="jogszabalySubtitle">Jó</h2>`, "Cím Jó"},
		{"fallback to last subtitle when all mainTitle", `<h1 class="jogszabalyMainTitle">Cím</h1><h2 class="jogszabalySubtitle mainTitle">Utolsó</h2>`, "Cím Utolsó"},
		{"no title", `<p>semmi</p>`, ""},
		{"entities and tags in title", `<h1 class="jogszabalyMainTitle">Cím&nbsp;<b>vastag</b></h1>`, "Cím vastag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractOfficialTitle(tt.html); got != tt.want {
				t.Errorf("extractOfficialTitle = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractDefinitions(t *testing.T) {
	run := func(content string) []seed.DefinitionSeed {
		var defs []seed.DefinitionSeed
		extractDefinitions(content, "s7", &defs)
		return defs
	}

	tests := []struct {
		name    string
		content string
		want    []seed.DefinitionSeed
	}{
		{
			name:    "gate requires alkalmazásában",
			content: "1. adatkezelő: az a személy, aki adatot kezel",
			want:    nil,
		},
		{
			name:    "final definition at end of content accepted",
			content: "E törvény alkalmazásában 1. adatkezelő: az a természetes vagy jogi személy, aki kezel",
			want: []seed.DefinitionSeed{
				{Term: "adatkezelő", Definition: "az a természetes vagy jogi személy, aki kezel", SourceProvision: "s7"},
			},
		},
		{
			name:    "mid definition followed by ; 2. accepted",
			content: "alkalmazásában 1. adatkezelő: az a természetes vagy jogi személy; 2. adatkezelés: az adatokon végzett bármely művelet",
			want: []seed.DefinitionSeed{
				{Term: "adatkezelő", Definition: "az a természetes vagy jogi személy", SourceProvision: "s7"},
				{Term: "adatkezelés", Definition: "az adatokon végzett bármely művelet", SourceProvision: "s7"},
			},
		},
		{
			// The lookahead needs ";\s*\d+\." — plain text after the ';' fails.
			name:    "mid definition followed by non-number rejected",
			content: "alkalmazásában 1. adatkezelő: az a természetes vagy jogi személy; egyéb szöveg következik itt",
			want:    nil,
		},
		{
			name:    "short definition rejected",
			content: "alkalmazásában 1. fogalom: rövid",
			want:    nil,
		},
		{
			name:    "short term rejected",
			content: "alkalmazásában 1. x: ez_pontosan_tíz_kar",
			want:    nil,
		},
		{
			name:    "broken chain keeps surrounding valid definitions",
			content: "alkalmazásában 1. első: ez_a_definíció_elég_hosszú; 2. rövid: nope; 3. harmadik: ez_is_elég_hosszú_definíció",
			want: []seed.DefinitionSeed{
				{Term: "első", Definition: "ez_a_definíció_elég_hosszú", SourceProvision: "s7"},
				{Term: "harmadik", Definition: "ez_is_elég_hosszú_definíció", SourceProvision: "s7"},
			},
		},
		{
			name:    "colon inside definition is kept",
			content: "alkalmazásában 1. fogalom: belső:kettőspont is megengedett",
			want: []seed.DefinitionSeed{
				{Term: "fogalom", Definition: "belső:kettőspont is megengedett", SourceProvision: "s7"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := run(tt.content)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d defs (%+v), want %d", len(got), got, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("def %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractDefinitions_Dedupe(t *testing.T) {
	act := ActIndexEntry{ID: "act", Title: "T", Status: "in_force"}
	html := `<html><body>
<span class="jhId" id="SZ1"></span><div class="szakasz"><span class="szakasz-jel">1. §</span><span class="szoveg">E törvény alkalmazásában 1. adatkezelő: az a természetes vagy jogi személy, aki kezel.</span></div>
<span class="jhId" id="SZ2"></span><div class="szakasz"><span class="szakasz-jel">2. §</span><span class="szoveg">E törvény alkalmazásában 1. adatkezelő: az a természetes vagy jogi személy, aki kezel; 1. ADATKEZELŐ: az a természetes vagy jogi személy, aki kezel.</span></div>
</body></html>`

	doc := ParseHungarianHTML(html, act)
	if len(doc.Definitions) != 3 {
		t.Fatalf("got %d definitions, want 3: %+v", len(doc.Definitions), doc.Definitions)
	}
	// Cross-section repeats are kept (dedupe key includes source_provision);
	// same-term repeats within one provision are dropped (lowercased key).
	if doc.Definitions[0].SourceProvision != "s1" || doc.Definitions[1].SourceProvision != "s2" || doc.Definitions[2].SourceProvision != "s2" {
		t.Errorf("unexpected source provisions: %+v", doc.Definitions)
	}
	if strings.EqualFold(doc.Definitions[1].Term, doc.Definitions[2].Term) {
		t.Errorf("case-variant duplicate within one provision was not deduped: %+v", doc.Definitions[1:])
	}
}

// tsDefinitionExtract is a slow reference implementation of the original TS
// lookahead pattern /\b\d+\.\s*([^:;]{2,120}):\s*([^;]{10,500})(?=;\s*\d+\.|$)/g
// with literal greedy-then-backtrack semantics at every start position. It is
// intentionally independent of the restructured tail check in
// extractDefinitions so the equivalence test below is meaningful.
func tsDefinitionExtract(content string) [][2]string {
	isWord := func(c byte) bool {
		return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	wordBoundary := func(i int) bool {
		before := i > 0 && isWord(content[i-1])
		after := i < len(content) && isWord(content[i])
		return before != after
	}
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
	spaceRun := func(from int) int {
		i := from
		for i < len(content) {
			r, size := utf8.DecodeRuneInString(content[i:])
			switch r {
			case ' ', '\t', '\n', '\v', '\f', '\r', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
				i += size
				continue
			}
			if r >= 0x2000 && r <= 0x200a {
				i += size
				continue
			}
			break
		}
		return i - from
	}

	var out [][2]string
	for start := 0; start < len(content); start++ {
		end, pair, ok := tsMatchAt(content, start, wordBoundary, isDigit, spaceRun)
		if !ok {
			continue
		}
		out = append(out, pair)
		start = end - 1 // loop increment resumes scanning at the match end (zero-width lookahead)
	}
	return out
}

func tsMatchAt(
	content string,
	start int,
	wordBoundary func(int) bool,
	isDigit func(byte) bool,
	spaceRun func(int) int,
) (int, [2]string, bool) {
	if !wordBoundary(start) {
		return 0, [2]string{}, false
	}
	// \d+\.
	i := start
	for i < len(content) && isDigit(content[i]) {
		i++
	}
	if i == start || i >= len(content) || content[i] != '.' {
		return 0, [2]string{}, false
	}
	afterDot := i + 1

	// \s* greedy then backtracking.
	for ws1 := afterDot + spaceRun(afterDot); ws1 >= afterDot; ws1-- {
		// Group 1: [^:;]{2,120} followed by ':'. Greedy order means the
		// only viable length ends at the first ':' (a ':' or ';' inside
		// invalidates every longer length; shorter lengths miss the ':').
		colon := -1
		for j := ws1; j < len(content) && j <= ws1+120; j++ {
			if content[j] == ':' {
				colon = j
				break
			}
			if content[j] == ';' {
				break
			}
		}
		if colon == -1 || colon-ws1 < 2 {
			continue
		}
		afterColon := colon + 1
		// \s* greedy then backtracking.
		for ws2 := afterColon + spaceRun(afterColon); ws2 >= afterColon; ws2-- {
			// Group 2: [^;]{10,500}, greedy.
			run := 0
			for run < 500 && ws2+run < len(content) && content[ws2+run] != ';' {
				run++
			}
			for l2 := run; l2 >= 10; l2-- {
				end := ws2 + l2
				// Lookahead (?=;\s*\d+\.|$)
				ok := end == len(content)
				if !ok && content[end] == ';' {
					j := end + 1 + spaceRun(end+1)
					k := j
					for k < len(content) && isDigit(content[k]) {
						k++
					}
					ok = k > j && k < len(content) && content[k] == '.'
				}
				if !ok {
					continue
				}
				return end, [2]string{content[ws1:colon], content[ws2:end]}, true
			}
		}
	}
	return 0, [2]string{}, false
}

// TestExtractDefinitions_TSLookaheadEquivalence verifies the restructured
// no-lookahead extraction against the literal backtracking simulation of the
// original TS lookahead regex on a combinatorial corpus of definition
// passages (valid chains, broken chains, multi-digit numbering, colons and
// digits inside definitions, missing terminators).
func TestExtractDefinitions_TSLookaheadEquivalence(t *testing.T) {
	segments := []string{
		"1. adatkezelő: az a természetes vagy jogi személy, aki adatot kezel",
		"2. adat: az adatkezelés során kezelt bármely információ",
		"3. adatkezelés: az adatokon végzett művelet",
		"4. x: rövid",
		"5. t: mindenképp_tíz_karakternél_hosszabb",
		"6. fogalom: belső:kettőspontot is tartalmaz",
		"7. 2-es szám: számjegyek a fogalomban is",
		"8. röviddef: áb",
		"12. többszámjegyű: sorszám is lehet több számjegyű",
		"9. nevélegybezárt: nincs lezáró karakter",
	}
	separators := []string{"; ", ";2. ", " ; ", "; 12. ", "; x. ", "; 2 ", "", ", ", " vég ", ";  "}
	suffixes := []string{"", ".", ";", " folyt.", "; 9. "}
	prefixes := []string{"E törvény alkalmazásában ", "A rendelet alkalmazásában:\n", "alkalmazásában "}

	runGo := func(content string) [][2]string {
		var defs []seed.DefinitionSeed
		extractDefinitions(content, "s1", &defs)
		pairs := make([][2]string, len(defs))
		for i, d := range defs {
			pairs[i] = [2]string{d.Term, d.Definition}
		}
		return pairs
	}

	cases := 0
	for _, prefix := range prefixes {
		for _, a := range segments {
			for _, sep := range separators {
				for _, b := range segments {
					for _, suffix := range suffixes {
						content := prefix + a + sep + b + suffix

						// The reference must model the full TS pipeline: raw
						// regex groups, then trim, then the
						// term>=2/definition>=10 length filter.
						raw := tsDefinitionExtract(content)
						want := make([][2]string, 0, len(raw))
						for _, p := range raw {
							term := strings.TrimSpace(p[0])
							def := strings.TrimSpace(p[1])
							if utf8.RuneCountInString(term) < 2 || utf8.RuneCountInString(def) < 10 {
								continue
							}
							want = append(want, [2]string{term, def})
						}
						got := runGo(content)

						cases++
						if len(got) != len(want) {
							t.Errorf("case %d (%q): got %d defs %+v, want %d %+v", cases, content, len(got), got, len(want), want)
							continue
						}
						for i := range got {
							if got[i] != want[i] {
								t.Errorf("case %d (%q): def %d = %+v, want %+v", cases, content, i, got[i], want[i])
							}
						}
					}
				}
			}
		}
	}
	t.Logf("equivalence verified on %d generated contents", cases)
}

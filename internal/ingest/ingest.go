package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/seed"
)

const (
	// DefaultBaseURL is the official njt.hu corpus origin. njt.hu now
	// 301-redirects here and its POST endpoints fail across the redirect
	// (HTTP 405), so the corpus origin is used directly (verified 2026-08-29).
	DefaultBaseURL = "https://njt.jog.gov.hu"
	ajaxSearchPath = "/ajax/get_search_url.json"
	ajaxBlockPath  = "/ajax/njtGetBlock.json"

	deferredBlockChunkSize  = 20
	metadataOnlyDescription = "Metadata-only entry: section-level text could not be " +
		"extracted from public njt.hu HTML for this statute."
	discoveredDescription = "Official Hungarian statute text from Nemzeti Jogszabalytar (njt.hu)."
)

// Options carries the CLI flags of cmd/ingest.
type Options struct {
	Full             bool
	Resume           bool
	RefreshDiscovery bool
	SkipFetch        bool
	DiscoverOnly     bool
	InForceOnly      bool
	// AuthorTypes selects the njt.hu jogszabálytípus filter codes passed to
	// discovery (one search per code, results merged). "0000" = törvény,
	// "2220" = Korm. rendelet; the empty string means every type. Empty
	// slice falls back to DefaultAuthorTypes.
	AuthorTypes []string
}

// DefaultAuthorTypes is the default discovery scope: every jogszabálytípus on
// njt.hu (all 273 dropdown codes, census 2026-08-30: ~82,400 docs). Before
// 2026-08-29 discovery used only "0000" — which the dropdown reveals means
// "törvény", not "all types" — so every decree was silently missing from the
// corpus; the 2026-08-30 update added "2220" (Korm. rendelet). The full list
// below was extracted from the site's type dropdown.
var DefaultAuthorTypes = []string{
	"2230", // Korm. határozat,
	"7630", // KE határozat,
	"2220", // Korm. rendelet,
	"0000", // törvény,
	"6330", // ME határozat,
	"4130", // OGY határozat,
	"7530", // AB határozat,
	"15B0", // HM utasítás,
	"0040", // helyesbítés,
	"0A20", // BM rendelet,
	"2C20", // MNB rendelet,
	"7R20", // AM rendelet,
	"2W20", // NFM rendelet,
	"2X20", // NGM rendelet,
	"5H20", // EMMI rendelet,
	"8220", // FVM rendelet,
	"8EB0", // ORFK utasítás,
	"0620", // IM rendelet,
	"0AB0", // BM utasítás,
	"2Y20", // VM rendelet,
	"6QC0", // KKM közlemény,
	"1520", // HM rendelet,
	"5HB0", // EMMI utasítás,
	"5320", // PM rendelet,
	"0010", // törvényerejű rendelet,
	"1120", // FM rendelet,
	"2XB0", // NGM utasítás,
	"3MB0", // BVOP utasítás,
	"E9B0", // LÜ utasítás,
	"2730", // KÜM határozat,
	"2WB0", // NFM utasítás,
	"6N20", // MvM rendelet,
	"6NB0", // MvM utasítás,
	"6QB0", // KKM utasítás,
	"0B20", // EÜM rendelet,
	"0N20", // KVVM rendelet,
	"2T20", // KIM_ rendelet,
	"2TB0", // KIM_ utasítás,
	"3KC0", // NAV közlemény,
	"4TB0", // OBH utasítás,
	"5Z20", // MEKH rendelet,
	"7Q20", // ITM rendelet,
	"06B0", // IM utasítás,
	"0L20", // GKM rendelet,
	"1U20", // IRM rendelet,
	"27C0", // KÜM közlemény,
	"3H20", // NMHH rendelet,
	"6520", // MT rendelet,
	"6Q20", // KKM rendelet,
	"77B0", // GVH utasítás,
	"8X20", // ÉKM rendelet,
	"8Y20", // EM rendelet,
	"1530", // HM határozat,
	"27B0", // KÜM utasítás,
	"2M20", // KHEM rendelet,
	"2V20", // NEFMI rendelet,
	"3IB0", // SZTNH utasítás,
	"4VB0", // BM OKF utasítás,
	"53B0", // PM utasítás,
	"6D20", // KTM_ rendelet,
	"6F20", // TNM rendelet,
	"6H30", // NVB határozat,
	"6HC0", // NVB közlemény,
	"6YB0", // NKFIH utasítás,
	"7G20", // MK rendelet,
	"7GB0", // MK utasítás,
	"7QB0", // ITM utasítás,
	"8K20", // SZTFH rendelet,
	"8P20", // GFM rendelet,
	"0G20", // PSZÁF rendelet,
	"0M20", // ESZCSM rendelet,
	"11B0", // FM utasítás,
	"1W20", // OKM rendelet,
	"1X20", // SZMM rendelet,
	"2930", // AB TÜ határozat,
	"2CB0", // MNB utasítás,
	"2K20", // NFGM rendelet,
	"2KB0", // NFGM utasítás,
	"2MB0", // KHEM utasítás,
	"2VB0", // NEFMI utasítás,
	"2YB0", // VM utasítás,
	"63B0", // ME utasítás,
	"6JB0", // NVI utasítás,
	"74B0", // ÁSZ utasítás,
	"7P20", // NVTNM rendelet,
	"7RB0", // AM utasítás,
	"82B0", // FVM utasítás,
	"8420", // MEHVM rendelet,
	"8430", // MEHVM határozat,
	"8620", // NKÖM rendelet,
	"8O20", // KIM rendelet,
	"8XB0", // ÉKM utasítás,
	"8YB0", // EM utasítás,
	"9130", // KKB határozat,
	"9DB0", // KTM utasítás,
	"0002", // Alaptörvény,
	"0003", // Alaptörvény Átmeneti Rendelkezései,
	"0004", // Alaptörvénymódosítás,
	"00AM", // Alkotmánymódosítás,
	"0630", // IM határozat,
	"0A30", // BM határozat,
	"0B30", // EÜM határozat,
	"0BB0", // EÜM utasítás,
	"0L30", // GKM határozat,
	"0LB0", // GKM utasítás,
	"0MB0", // ESZCSM utasítás,
	"0N30", // KVVM határozat,
	"0NB0", // KVVM utasítás,
	"0P20", // IHM rendelet,
	"0PB0", // IHM utasítás,
	"1020", // ÉVM rendelet,
	"1920", // IPM rendelet,
	"1F20", // KkM rendelet,
	"1I20", // ICSSZEM rendelet,
	"1U30", // IRM határozat,
	"1UB0", // IRM utasítás,
	"1V20", // ÖTM rendelet,
	"1VB0", // ÖTM utasítás,
	"1W30", // OKM határozat,
	"1WB0", // OKM utasítás,
	"1XB0", // SZMM utasítás,
	"2020", // KM rendelet,
	"2420", // KPM rendelet,
	"2720", // KÜM rendelet,
	"2A20", // MÉM rendelet,
	"2B20", // MM rendelet,
	"2B30", // MM határozat,
	"2K30", // NFGM határozat,
	"2L20", // ÖM rendelet,
	"2L30", // ÖM határozat,
	"2LB0", // ÖM utasítás,
	"2M30", // KHEM határozat,
	"2NB0", // KFTNM utasítás,
	"2R20", // PTNM rendelet,
	"2S20", // TM rendelet,
	"2SB0", // TM utasítás,
	"2T30", // KIM_ határozat,
	"2UB0", // MeG utasítás,
	"2V30", // NEFMI határozat,
	"2W30", // NFM határozat,
	"2X30", // NGM határozat,
	"2Y30", // VM határozat,
	"3120", // AÉM rendelet,
	"31B0", // AÉM utasítás,
	"3220", // ÉKFM rendelet,
	"32B0", // ÉKFM utasítás,
	"32C0", // ÉKFM közlemény,
	"3920", // MÜM rendelet,
	"3D20", // NM rendelet,
	"3J20", // K M rendelet,
	"3KB0", // NAV utasítás,
	"41H1", // OGY politikai nyilatkozat,
	"4520", // OM rendelet,
	"4530", // OM határozat,
	"45B0", // OM utasítás,
	"4SB0", // AJB utasítás,
	"4WB0", // NEFMI KÁT utasítás,
	"4XB0", // NGM KÁT utasítás,
	"5330", // PM határozat,
	"5E20", // KÖHÉM rendelet,
	"5F20", // KVM rendelet,
	"5H30", // EMMI határozat,
	"5JB0", // VM KÁT utasítás,
	"5MB0", // KIM KÁT_ utasítás,
	"5PB0", // AJBH utasítás,
	"5QB0", // EBH utasítás,
	"5RB0", // EMMI KÁT utasítás,
	"5TB0", // BM KÁT utasítás,
	"5UB0", // KÜM KÁT utasítás,
	"5VB0", // NFM KÁT utasítás,
	"5ZB0", // MEKH utasítás,
	"6320", // ME rendelet,
	"6620", // KÖM rendelet,
	"66B0", // KÖM utasítás,
	"6720", // MKM rendelet,
	"67B0", // MKM utasítás,
	"6A20", // IKM rendelet,
	"6B20", // KHVM rendelet,
	"6BB0", // KHVM utasítás,
	"6DB0", // KTM_utasítás,
	"6FB0", // TNM utasítás,
	"6MB0", // KIFÜ utasítás,
	"6N30", // MvM határozat,
	"6OB0", // KKM KÁT utasítás,
	"6PB0", // FM KÁT utasítás,
	"6Q30", // KKM határozat,
	"6RB0", // ME KÁT utasítás,
	"6SB0", // IM KÁT utasítás,
	"6ZB0", // SZGYF utasítás,
	"76B0", // KE utasítás,
	"7G30", // MK határozat,
	"7HB0", // MK KÁT utasítás,
	"7K20", // PTNM_ rendelet,
	"7MB0", // MBFSZ utasítás,
	"7NB0", // NEAK utasítás,
	"7OB0", // KH utasítás,
	"7P30", // NVTNM határozat,
	"7PB0", // NVTNM utasítás,
	"7Q30", // ITM határozat,
	"7QC2", // ITM _közlemény,
	"7TB0", // ITM KÁT utasítás,
	"7UB0", // PM KÁT utasítás,
	"7VB0", // MKI KÁT utasítás,
	"7WB0", // AM KÁT utasítás,
	"7YB0", // NFK utasítás,
	"7ZB0", // OIF utasítás,
	"8020", // IKIM rendelet,
	"80B0", // IKIM utasítás,
	"8230", // FVM határozat,
	"8320", // GM rendelet,
	"83B0", // GM utasítás,
	"84B0", // MEHVM utasítás,
	"8520", // SZCSM rendelet,
	"85B0", // SZCSM utasítás,
	"86B0", // NKÖM utasítás,
	"8C20", // ISM rendelet,
	"8C30", // ISM határozat,
	"8CB0", // ISM utasítás,
	"8EC2", // ORFK _közlemény,
	"8H20", // CSTNM rendelet,
	"8HB0", // CSTNM utasítás,
	"8IB0", // OKFŐ utasítás,
	"8KB0", // SZTFH utasítás,
	"8L20", // OAH rendelet,
	"8LB0", // OAH utasítás,
	"8M20", // ÉBM rendelet,
	"8MB0", // ÉBM utasítás,
	"8N20", // TIM rendelet,
	"8NB0", // TIM utasítás,
	"8O30", // KIM határozat,
	"8OB0", // KIM utasítás,
	"8P30", // GFM határozat,
	"8PB0", // GFM utasítás,
	"8Q20", // TFM rendelet,
	"8QB0", // TFM utasítás,
	"8UB0", // ÉBM KÁT utasítás,
	"8Y30", // EM határozat,
	"8ZB0", // EUTAF utasítás,
	"9320", // KÖVIM rendelet,
	"94C2", // OTFO _közlemény,
	"95B0", // GFM KÁT utasítás,
	"9820", // EUM rendelet,
	"98B0", // EUM utasítás,
	"9AB0", // KIM KÁT utasítás,
	"9BB0", // EUM KÁT utasítás,
	"9CB0", // EM KÁT utasítás,
	"9D20", // KTM rendelet,
	"9EB0", // KTM KÁT utasítás,
	"9FB0", // SZH utasítás,
	"9GB0", // ÉKM KÁT utasítás,
	"9HB0", // SP utasítás,
	"9J20", // KBM rendelet,
	"9JB0", // KBM utasítás,
	"9K20", // GEM rendelet,
	"9KB0", // GEM utasítás,
	"9LB0", // TKKM utasítás,
	"9M20", // OGYM rendelet,
	"9MB0", // OGYM utasítás,
	"9N20", // TTM rendelet,
	"9N30", // TTM határozat,
	"9NB0", // TTM utasítás,
	"9OB0", // VTM utasítás,
	"9PB0", // VTM KÁT utasítás,
	"F6B0", // SZF utasítás,
	"HK30", // HM KÁT határozat,
	"HKB0", // HM KÁT utasítás,
	"JAB0", // MÁK utasítás,
	"PP20", // GYISM rendelet,
	"PP30", // GYISM határozat,
	"PPB0", // GYISM utasítás,
	"STB0", // HVKF utasítás,
	"SY20", // FMM rendelet,
	"SYB0", // FMM utasítás
}

// Pipeline wires the fetcher, on-disk directories and output sink together.
type Pipeline struct {
	// BaseURL is the njt.hu origin; overriding it points discovery, block
	// hydration and njt.hu act URLs at a mirror or test server.
	BaseURL   string
	SourceDir string
	SeedDir   string
	Fetcher   *Fetcher
	Stdout    io.Writer
}

// NewPipeline returns a Pipeline with production defaults.
func NewPipeline(sourceDir, seedDir string) *Pipeline {
	p := &Pipeline{
		BaseURL:   DefaultBaseURL,
		SourceDir: sourceDir,
		SeedDir:   seedDir,
		Fetcher:   NewFetcher(),
		Stdout:    os.Stdout,
	}
	// Retry lines share the pipeline's sink so an injected test harness
	// captures them; a standalone Fetcher keeps its fmt.Printf default.
	p.Fetcher.Logf = p.printf
	return p
}

func (p *Pipeline) printf(format string, args ...any) {
	fmt.Fprintf(p.Stdout, format, args...)
}

// resolveURL rewrites njt.hu URLs to the pipeline's base origin so the whole
// flow can be pointed at a mirror/test server. URLs that are neither on
// njt.hu nor on the configured base origin return "" so a hostile act URL
// cannot send the fetcher off-origin; callers treat "" as a rejection.
func (p *Pipeline) resolveURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	base, baseErr := url.Parse(p.BaseURL)
	if err != nil || baseErr != nil {
		return ""
	}
	if u.Host == "njt.hu" {
		u.Scheme = base.Scheme
		u.Host = base.Host
	}
	if u.Scheme != base.Scheme || u.Host != base.Host {
		return ""
	}
	return u.String()
}

// Run executes the ingestion pipeline. Port of the TS main().
func (p *Pipeline) Run(ctx context.Context, opts Options) error {
	p.printf("Hungarian Law MCP -- Ingestion Pipeline\n")
	p.printf("======================================\n\n")
	p.printf("  Source: %s (official Hungarian legal portal)\n", p.BaseURL)
	p.printf("  Parse target: section-level text (szakasz, \"§\")\n")
	p.printf("  Rate limit: %dms/request\n", DefaultMinDelay.Milliseconds())
	mode := "curated corpus"
	if opts.Full {
		mode = "full corpus discovery"
	}
	p.printf("  Mode: %s\n", mode)

	if opts.Full {
		if len(opts.AuthorTypes) == 0 {
			opts.AuthorTypes = DefaultAuthorTypes
		}
		p.printf("  Author types: %s\n", strings.Join(opts.AuthorTypes, ", "))
		p.printf("  In-force only: %s\n", yesNo(opts.InForceOnly))
	}
	if opts.SkipFetch {
		p.printf("  --skip-fetch\n")
	}
	if opts.Resume {
		p.printf("  --resume\n")
	}
	if opts.DiscoverOnly {
		p.printf("  --discover-only\n")
	}
	if opts.RefreshDiscovery {
		p.printf("  --refresh-discovery\n")
	}

	var acts []ActIndexEntry

	if opts.Full {
		var discovered []DiscoveredLaw
		if !opts.RefreshDiscovery {
			discovered = p.readDiscoveryCache(opts.InForceOnly, opts.AuthorTypes)
		}

		if discovered == nil {
			p.printf("\nDiscovering laws from njt.hu search index...\n")
			var err error
			discovered, err = p.discoverLaws(ctx, opts.InForceOnly, opts.AuthorTypes)
			if err != nil {
				return err
			}
		} else {
			p.printf("\nLoaded discovery cache (%d laws): %s\n", len(discovered), p.discoveryCachePath(opts.InForceOnly, opts.AuthorTypes))
		}

		acts = BuildFullCorpusActList(discovered)

		p.printf("  Discovered laws: %d\n", len(discovered))
		p.printf("  Ingestion act list: %d (includes compatibility aliases where needed)\n", len(acts))
		p.printf("  Discovery cache: %s\n", p.discoveryCachePath(opts.InForceOnly, opts.AuthorTypes))
	} else {
		acts = slices.Clone(KeyHungarianActs)
	}

	if opts.DiscoverOnly {
		p.printf("\nDiscovery-only run completed. Selected acts for ingestion would be: %d\n", len(acts))
		return nil
	}

	return p.FetchAndParseActs(ctx, acts, opts.SkipFetch, opts.Resume)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

var subsetOnlyActIDs = map[string]struct{}{
	"act-cxii-2011-public-data": {},
	"criminal-code-cybercrime":  {},
}

// BuildFullCorpusActList merges the discovered laws with the curated act
// list: the first non-subset curated act per njt document id wins (in
// KeyHungarianActs order), unknown laws become hu-law-* entries, and the
// curated subset aliases are appended for compatibility with existing
// document IDs. Port of buildFullCorpusActList.
func BuildFullCorpusActList(discovered []DiscoveredLaw) []ActIndexEntry {
	// First non-subset curated act per njt doc ID wins, in
	// KeyHungarianActs order.
	curatedByDocID := map[string]ActIndexEntry{}
	for _, act := range KeyHungarianActs {
		if _, subset := subsetOnlyActIDs[act.ID]; subset {
			continue
		}
		docID := ExtractNjtDocumentID(act.URL)
		if docID == "" {
			continue
		}
		if _, seen := curatedByDocID[docID]; !seen {
			curatedByDocID[docID] = act
		}
	}

	result := make([]ActIndexEntry, 0, len(discovered)+len(subsetOnlyActIDs))

	for _, law := range discovered {
		if curatedFull, curated := curatedByDocID[law.DocumentID]; curated {
			merged := curatedFull
			merged.URL = law.URL
			merged.Status = law.Status
			if law.InForceDate != "" {
				merged.InForceDate = law.InForceDate
			}
			result = append(result, merged)
			continue
		}

		description := law.Description
		if description == "" {
			description = discoveredDescription
		}
		result = append(result, ActIndexEntry{
			ID:          "hu-law-" + strings.ToLower(law.DocumentID),
			Title:       law.Title,
			TitleEn:     law.TitleEn,
			Status:      law.Status,
			IssuedDate:  law.IssuedDate,
			InForceDate: law.InForceDate,
			URL:         law.URL,
			Description: description,
		})
	}

	// Preserve curated subset aliases for compatibility with existing
	// document IDs/tools.
	for _, act := range KeyHungarianActs {
		if _, subset := subsetOnlyActIDs[act.ID]; subset {
			result = append(result, act)
		}
	}

	return result
}

// ToMetadataOnlyAct builds a metadata-only seed document for statutes whose
// section-level text could not be extracted. Port of toMetadataOnlyAct.
func ToMetadataOnlyAct(act ActIndexEntry) seed.DocumentSeed {
	description := act.Description
	if description == "" {
		description = metadataOnlyDescription
	}
	return seed.DocumentSeed{
		ID:          act.ID,
		Type:        "statute",
		Title:       act.Title,
		TitleEn:     act.TitleEn,
		ShortName:   act.ShortName,
		Status:      act.Status,
		IssuedDate:  act.IssuedDate,
		InForceDate: act.InForceDate,
		URL:         act.URL,
		Description: description,
		Provisions:  []seed.ProvisionSeed{},
		Definitions: []seed.DefinitionSeed{},
	}
}

var deferredBlockStartPattern = regexp.MustCompile(`class="pH borderStart"data-show-order="(\d+)"`)

// ExtractDeferredBlockStarts returns the sorted show-order values of the
// deferred blocks in an act page.
func ExtractDeferredBlockStarts(html string) []int {
	var starts []int
	for _, m := range deferredBlockStartPattern.FindAllStringSubmatch(html, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		starts = append(starts, n)
	}
	slices.Sort(starts)
	return starts
}

type blockRange struct {
	Start int  `json:"start"`
	Last  *int `json:"last,omitempty"` // omitted for the final range (TS: null omitted)
}

type blockRequestBody struct {
	DocumentID string       `json:"documentId"`
	Data       []blockRange `json:"data"`
}

// HydrateDeferredBlocks fetches the deferred blocks of an act page via the
// njtGetBlock.json endpoint and appends their HTML. Port of
// hydrateDeferredBlocks.
func (p *Pipeline) HydrateDeferredBlocks(
	ctx context.Context, html string, act ActIndexEntry, logHydration bool,
) (string, error) {
	starts := ExtractDeferredBlockStarts(html)
	if len(starts) == 0 {
		return html, nil
	}

	documentID := ExtractNjtDocumentID(act.URL)
	if documentID == "" {
		return html, nil
	}

	ranges := make([]blockRange, len(starts))
	for i, start := range starts {
		ranges[i].Start = start
		if i+1 < len(starts) {
			ranges[i].Last = &starts[i+1]
		}
	}

	var appended strings.Builder
	for i := 0; i < len(ranges); i += deferredBlockChunkSize {
		end := min(i+deferredBlockChunkSize, len(ranges))

		payload, err := json.Marshal(blockRequestBody{DocumentID: documentID, Data: ranges[i:end]})
		if err != nil {
			return "", err
		}

		resp, err := p.Fetcher.Fetch(ctx, p.resolveURL(p.BaseURL+ajaxBlockPath), &RequestOptions{
			Method: http.MethodPost,
			Body:   string(payload),
			Headers: map[string]string{
				"Accept":       "text/html,application/json,*/*",
				"Content-Type": "application/json; charset=utf-8",
			},
		})
		if err != nil {
			return "", err
		}
		if resp.Status != 200 {
			return "", fmt.Errorf("deferred block fetch failed for %s (HTTP %d)", act.ID, resp.Status)
		}

		appended.WriteString("\n")
		appended.WriteString(resp.Body)
	}

	// ranges is never empty here: empty starts returned early above.
	if logHydration {
		p.printf("    -> hydrated %d deferred block ranges\n", len(ranges))
	}

	return html + "\n" + appended.String(), nil
}

// ParseSourceCacheKey is the source-cache file stem for an act: the njt.hu
// document id when available, else the act id.
func ParseSourceCacheKey(act ActIndexEntry) string {
	if documentID := ExtractNjtDocumentID(act.URL); documentID != "" {
		return documentID
	}
	return act.ID
}

// cacheKeyPattern is the filename alphabet allowed for cache/seed stems;
// anything else is rejected before it reaches the filesystem.
var cacheKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func loadExistingSeedCounts(seedFile string) (provisions, definitions int, err error) {
	data, err := os.ReadFile(seedFile)
	if err != nil {
		return 0, 0, err
	}
	var existing seed.DocumentSeed
	if err := json.Unmarshal(data, &existing); err != nil {
		return 0, 0, err
	}
	return len(existing.Provisions), len(existing.Definitions), nil
}

type ingestionRow struct {
	act         string
	provisions  int
	definitions int
	status      string
}

// actRunner carries the state shared by the per-act steps of
// FetchAndParseActs: the output pipeline, the verbose/compact logging
// helpers and the report tallies mutated while processing each act.
type actRunner struct {
	p        *Pipeline
	verbose  bool
	progress func() string

	results          []ingestionRow
	processed        int
	cached           int
	failed           int
	success          int
	totalProvisions  int
	totalDefinitions int
}

// log prints verboseMsg in verbose mode and compactMsg otherwise.
func (r *actRunner) log(verboseMsg, compactMsg string) {
	if r.verbose {
		r.p.printf("%s\n", verboseMsg)
	} else {
		r.p.printf("%s\n", compactMsg)
	}
}

// FetchAndParseActs fetches every act (unless cached) and writes its seed
// file. Port of fetchAndParseActs.
func (p *Pipeline) FetchAndParseActs(ctx context.Context, acts []ActIndexEntry, skipFetch, resume bool) error {
	p.printf("\nProcessing %d Hungarian statutes from njt.hu...\n\n", len(acts))

	if err := os.MkdirAll(p.SourceDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(p.SeedDir, 0o755); err != nil {
		return err
	}

	r := &actRunner{p: p, verbose: len(acts) <= 20}
	r.progress = func() string { return fmt.Sprintf("[%d/%d]", r.processed+1, len(acts)) }
	label := func(act ActIndexEntry) string {
		if act.ShortName != "" {
			return act.ShortName
		}
		return act.ID
	}

	for _, act := range acts {
		name := label(act)
		cacheKey := ParseSourceCacheKey(act)
		seedFile := filepath.Join(p.SeedDir, act.ID+".json")

		// Filename stems are validated before any filepath use: a hostile
		// act URL must not steer cache/seed paths outside the data dirs.
		if !cacheKeyPattern.MatchString(cacheKey) || !cacheKeyPattern.MatchString(act.ID) {
			r.log(
				fmt.Sprintf("  ERROR %s: unsafe filename stem (act %q, cache key %q)", name, act.ID, cacheKey),
				fmt.Sprintf("  %s %s -> ERROR: unsafe filename stem", r.progress(), name),
			)
			r.results = append(r.results, ingestionRow{act: name, status: "ERROR: unsafe filename stem"})
			r.failed++
			r.processed++
			continue
		}

		if resume {
			if _, err := os.Stat(seedFile); err == nil {
				provisions, definitions, countsErr := loadExistingSeedCounts(seedFile)
				if countsErr != nil {
					// A corrupt cached seed must not abort the whole
					// --resume run; re-fetch that act instead.
					r.log(
						fmt.Sprintf("  WARNING: cached seed for %s is unreadable (%v), re-fetching", name, countsErr),
						fmt.Sprintf("  %s %s -> cached seed unreadable, re-fetching", r.progress(), name),
					)
				} else {
					r.totalProvisions += provisions
					r.totalDefinitions += definitions
					r.results = append(r.results, ingestionRow{
						act:         name,
						provisions:  provisions,
						definitions: definitions,
						status:      "cached",
					})
					r.cached++
					r.processed++
					continue
				}
			}
		}

		if err := r.ingestOneAct(ctx, act, name, cacheKey, seedFile, skipFetch); err != nil {
			message := err.Error()
			r.log(
				fmt.Sprintf("  ERROR %s: %s", name, truncateRunes(message, 120)),
				fmt.Sprintf("  %s %s -> ERROR: %s", r.progress(), name, truncateRunes(message, 120)),
			)
			r.results = append(r.results, ingestionRow{
				act: name, status: fmt.Sprintf("ERROR: %s", truncateRunes(message, 80)),
			})
			r.failed++
		}

		r.processed++
	}

	separator := strings.Repeat("=", 72)
	p.printf("\n%s\n", separator)
	p.printf("Ingestion Report\n")
	p.printf("%s\n", separator)
	p.printf("\n  Source:       %s\n", p.BaseURL)
	p.printf("  Authority:    Nemzeti Jogszabalytar / Magyar Kozlony\n")
	p.printf("  Processed:    %d\n", r.processed)
	p.printf("  Cached:       %d\n", r.cached)
	p.printf("  Failed:       %d\n", r.failed)
	p.printf("  Provisions:   %d\n", r.totalProvisions)
	p.printf("  Definitions:  %d\n", r.totalDefinitions)

	if len(r.results) <= 20 {
		p.printf("\n  Per-Act breakdown:\n")
		p.printf("  %-32s %12s %13s %16s\n", "Act", "Provisions", "Definitions", "Status")
		p.printf("  %s %s %s %s\n",
			strings.Repeat("-", 32), strings.Repeat("-", 12), strings.Repeat("-", 13), strings.Repeat("-", 16))

		for _, result := range r.results {
			p.printf("  %-32s %12d %13d %16s\n", result.act, result.provisions, result.definitions, result.status)
		}
	} else {
		metadataOnlyRows := 0
		errorRows := []ingestionRow{}
		for _, result := range r.results {
			switch {
			case result.status == "METADATA_ONLY":
				metadataOnlyRows++
			case result.status != "OK" && result.status != "cached":
				errorRows = append(errorRows, result)
			}
		}
		p.printf("  Window summary: %d OK, %d cached, %d metadata-only, %d failed/skipped\n",
			r.success, r.cached, metadataOnlyRows, r.failed)
		if metadataOnlyRows > 0 {
			p.printf("  Metadata-only entries in this window: %d\n", metadataOnlyRows)
		}
		if len(errorRows) > 0 {
			p.printf("  Non-OK entries in this window:\n")
			for i, row := range errorRows {
				if i >= 10 {
					p.printf("    ... and %d more\n", len(errorRows)-10)
					break
				}
				p.printf("    - %s: %s\n", row.act, row.status)
			}
		}
	}
	p.printf("\n")
	if r.failed > 0 {
		return fmt.Errorf("%d of %d acts failed", r.failed, len(acts))
	}
	return nil
}

// stripHTMLBody keeps only the law-content region of an njt.hu document page:
// from the #jogszab container through the closing of the class="clbo" marker
// div. Without this, the last jhId block runs to end-of-file and swallows the
// page footer and embedded scripts into the final provision. Returns the
// input unchanged when the region is absent (e.g. metadata-only pages).
func stripHTMLBody(html string) string {
	i := strings.Index(html, `id="jogszab"`)
	if i < 0 {
		return html
	}
	j := strings.Index(html[i:], `class="clbo"`)
	if j < 0 {
		return html
	}
	j += i
	if d := strings.LastIndex(html[i:j], "<div"); d >= 0 {
		j = i + d // cut before the opening tag of the clbo div, not mid-tag
	}
	return html[i:j]
}

// ingestOneAct fetches (or reuses the cached) HTML for one act, hydrates the
// deferred blocks, parses it and writes the seed file, appending the report
// row. The returned error is a fetch/parse/write failure; the caller logs it
// and records the ERROR row. Extracted from the former per-act closure.
func (r *actRunner) ingestOneAct(
	ctx context.Context, act ActIndexEntry, name, cacheKey, seedFile string, skipFetch bool,
) error {
	p := r.p
	sourceFile := filepath.Join(p.SourceDir, cacheKey+".html")
	var html string

	if skipFetch {
		if data, readErr := os.ReadFile(sourceFile); readErr == nil {
			html = stripHTMLBody(string(data)) // legacy caches may still be full pages
			if r.verbose {
				p.printf("  Using cached HTML for %s\n", name)
			} else {
				p.printf("  %s %s -> cached\n", r.progress(), name)
			}
		}
	}

	if html == "" {
		fetchURL := p.resolveURL(act.URL)
		if fetchURL == "" {
			return fmt.Errorf("act url %q is not on origin %s", act.URL, p.BaseURL)
		}
		r.log(fmt.Sprintf("  Fetching %s (%s)...", name, act.URL), fmt.Sprintf("  %s %s ...", r.progress(), name))
		result, fetchErr := p.Fetcher.Fetch(ctx, fetchURL, nil)
		if fetchErr != nil {
			return fetchErr
		}
		if result.Status != 200 {
			r.log(fmt.Sprintf(" HTTP %d", result.Status),
				fmt.Sprintf("  %s %s -> HTTP %d", r.progress(), name, result.Status))
			r.results = append(r.results, ingestionRow{act: name, status: fmt.Sprintf("HTTP %d", result.Status)})
			r.failed++
			return nil
		}

		html = stripHTMLBody(result.Body)
		if err := writeFileAtomic(sourceFile, []byte(html), 0o644); err != nil {
			return err
		}

		if !strings.Contains(html, "jogszabalyMainTitle") || !strings.Contains(html, `class="jhId"`) {
			if err := writeJSONFile(seedFile, ToMetadataOnlyAct(act)); err != nil {
				return err
			}
			r.log(" NO_SECTION_CONTENT -> METADATA_ONLY",
				fmt.Sprintf("  %s %s -> METADATA_ONLY (NO_SECTION_CONTENT)", r.progress(), name))
			r.results = append(r.results, ingestionRow{act: name, status: "METADATA_ONLY"})
			return nil
		}

		r.log(fmt.Sprintf(" OK (%d KB)", len(html)/1024), "")
	}

	hydratedHTML, err := p.HydrateDeferredBlocks(ctx, html, act, r.verbose)
	if err != nil {
		return err
	}
	parsed := ParseHungarianHTML(hydratedHTML, act)

	if len(parsed.Provisions) == 0 {
		metadataAct := act
		metadataAct.Title = parsed.Title
		if err := writeJSONFile(seedFile, ToMetadataOnlyAct(metadataAct)); err != nil {
			return err
		}

		r.log("    -> 0 provisions extracted, stored as METADATA_ONLY",
			fmt.Sprintf("  %s %s -> METADATA_ONLY (NO_SECTION_CONTENT)", r.progress(), name))
		r.results = append(r.results, ingestionRow{act: name, status: "METADATA_ONLY"})
		return nil
	}

	if err := writeJSONFile(seedFile, parsed); err != nil {
		return err
	}

	r.totalProvisions += len(parsed.Provisions)
	r.totalDefinitions += len(parsed.Definitions)
	r.success++

	r.results = append(r.results, ingestionRow{
		act:         name,
		provisions:  len(parsed.Provisions),
		definitions: len(parsed.Definitions),
		status:      "OK",
	})

	if r.verbose || (r.processed+1)%25 == 0 {
		r.log(
			fmt.Sprintf("    -> %d provisions, %d definitions extracted",
				len(parsed.Provisions), len(parsed.Definitions)),
			fmt.Sprintf("  %s ok=%d failed=%d cached=%d provisions=%d defs=%d",
				r.progress(), r.success, r.failed, r.cached, r.totalProvisions, r.totalDefinitions),
		)
	}
	return nil
}

// truncateRunes mirrors TS String.prototype.substring on UTF-16 units; rune
// counts agree for all Hungarian (BMP) text.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

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
	"sort"
	"strconv"
	"strings"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/seed"
)

const (
	// DefaultBaseURL is the official njt.hu origin.
	DefaultBaseURL = "https://njt.hu"
	ajaxSearchPath = "/ajax/get_search_url.json"
	ajaxBlockPath  = "/ajax/njtGetBlock.json"

	deferredBlockChunkSize  = 20
	metadataOnlyDescription = "Metadata-only entry: section-level text could not be extracted from public njt.hu HTML for this statute."
	discoveredDescription   = "Official Hungarian statute text from Nemzeti Jogszabalytar (njt.hu)."
)

// Options carries the CLI flags of cmd/ingest.
type Options struct {
	Full             bool
	Resume           bool
	RefreshDiscovery bool
	SkipFetch        bool
	DiscoverOnly     bool
	InForceOnly      bool
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
	return &Pipeline{
		BaseURL:   DefaultBaseURL,
		SourceDir: sourceDir,
		SeedDir:   seedDir,
		Fetcher:   NewFetcher(),
		Stdout:    os.Stdout,
	}
}

func (p *Pipeline) printf(format string, args ...any) {
	fmt.Fprintf(p.Stdout, format, args...)
}

// resolveURL rewrites njt.hu URLs to the pipeline's base origin so the whole
// flow can be pointed at a mirror/test server.
func (p *Pipeline) resolveURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host != "njt.hu" {
		return rawURL
	}
	base, err := url.Parse(p.BaseURL)
	if err != nil {
		return rawURL
	}
	u.Scheme = base.Scheme
	u.Host = base.Host
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
			discovered = p.readDiscoveryCache(opts.InForceOnly)
		}

		if discovered == nil {
			p.printf("\nDiscovering laws from njt.hu search index...\n")
			var err error
			discovered, err = p.discoverLaws(ctx, opts.InForceOnly)
			if err != nil {
				return err
			}
		} else {
			p.printf("\nLoaded discovery cache (%d laws): %s\n", len(discovered), p.discoveryCachePath(opts.InForceOnly))
		}

		acts = BuildFullCorpusActList(discovered)

		p.printf("  Discovered laws: %d\n", len(discovered))
		p.printf("  Ingestion act list: %d (includes compatibility aliases where needed)\n", len(acts))
		p.printf("  Discovery cache: %s\n", p.discoveryCachePath(opts.InForceOnly))
	} else {
		acts = append([]ActIndexEntry(nil), KeyHungarianActs...)
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
	sort.Ints(starts)
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
func (p *Pipeline) HydrateDeferredBlocks(ctx context.Context, html string, act ActIndexEntry, logHydration bool) (string, error) {
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
		end := i + deferredBlockChunkSize
		if end > len(ranges) {
			end = len(ranges)
		}

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

	if logHydration && len(ranges) > 0 {
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

	processed, cached, failed := 0, 0, 0
	totalProvisions, totalDefinitions, success := 0, 0, 0

	results := []ingestionRow{}
	verbosePerAct := len(acts) <= 20
	progress := func() string { return fmt.Sprintf("[%d/%d]", processed+1, len(acts)) }
	log := func(verboseMsg, compactMsg string) {
		if verbosePerAct {
			p.printf("%s\n", verboseMsg)
		} else {
			p.printf("%s\n", compactMsg)
		}
	}
	label := func(act ActIndexEntry) string {
		if act.ShortName != "" {
			return act.ShortName
		}
		return act.ID
	}

	for _, act := range acts {
		name := label(act)
		sourceFile := filepath.Join(p.SourceDir, ParseSourceCacheKey(act)+".html")
		seedFile := filepath.Join(p.SeedDir, act.ID+".json")

		if resume {
			if _, err := os.Stat(seedFile); err == nil {
				provisions, definitions, err := loadExistingSeedCounts(seedFile)
				if err != nil {
					return err
				}
				totalProvisions += provisions
				totalDefinitions += definitions
				results = append(results, ingestionRow{act: name, provisions: provisions, definitions: definitions, status: "cached"})
				cached++
				processed++
				continue
			}
		}

		err := func() error {
			var html string

			if skipFetch {
				if data, readErr := os.ReadFile(sourceFile); readErr == nil {
					html = string(data)
					log(fmt.Sprintf("  Using cached HTML for %s", name), fmt.Sprintf("  %s %s -> cached", progress(), name))
				}
			}

			if html == "" {
				log(fmt.Sprintf("  Fetching %s (%s)...", name, act.URL), fmt.Sprintf("  %s %s ...", progress(), name))
				result, fetchErr := p.Fetcher.Fetch(ctx, p.resolveURL(act.URL), nil)
				if fetchErr != nil {
					return fetchErr
				}
				if result.Status != 200 {
					log(fmt.Sprintf(" HTTP %d", result.Status), fmt.Sprintf("  %s %s -> HTTP %d", progress(), name, result.Status))
					results = append(results, ingestionRow{act: name, status: fmt.Sprintf("HTTP %d", result.Status)})
					failed++
					return nil
				}

				html = result.Body
				if err := os.WriteFile(sourceFile, []byte(html), 0o644); err != nil {
					return err
				}

				if !strings.Contains(html, "jogszabalyMainTitle") || !strings.Contains(html, `class="jhId"`) {
					if err := writeJSONFile(seedFile, ToMetadataOnlyAct(act)); err != nil {
						return err
					}
					log(" NO_SECTION_CONTENT -> METADATA_ONLY", fmt.Sprintf("  %s %s -> METADATA_ONLY (NO_SECTION_CONTENT)", progress(), name))
					results = append(results, ingestionRow{act: name, status: "METADATA_ONLY"})
					return nil
				}

				log(fmt.Sprintf(" OK (%d KB)", len(html)/1024), "")
			}

			hydratedHTML, err := p.HydrateDeferredBlocks(ctx, html, act, verbosePerAct)
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

				log("    -> 0 provisions extracted, stored as METADATA_ONLY", fmt.Sprintf("  %s %s -> METADATA_ONLY (NO_SECTION_CONTENT)", progress(), name))
				results = append(results, ingestionRow{act: name, status: "METADATA_ONLY"})
				return nil
			}

			if err := writeJSONFile(seedFile, parsed); err != nil {
				return err
			}

			totalProvisions += len(parsed.Provisions)
			totalDefinitions += len(parsed.Definitions)
			success++

			results = append(results, ingestionRow{
				act:         name,
				provisions:  len(parsed.Provisions),
				definitions: len(parsed.Definitions),
				status:      "OK",
			})

			if verbosePerAct || (processed+1)%25 == 0 {
				log(
					fmt.Sprintf("    -> %d provisions, %d definitions extracted", len(parsed.Provisions), len(parsed.Definitions)),
					fmt.Sprintf("  %s ok=%d failed=%d cached=%d provisions=%d defs=%d", progress(), success, failed, cached, totalProvisions, totalDefinitions),
				)
			}
			return nil
		}()
		if err != nil {
			message := err.Error()
			log(
				fmt.Sprintf("  ERROR %s: %s", name, truncateRunes(message, 120)),
				fmt.Sprintf("  %s %s -> ERROR: %s", progress(), name, truncateRunes(message, 120)),
			)
			results = append(results, ingestionRow{act: name, status: fmt.Sprintf("ERROR: %s", truncateRunes(message, 80))})
			failed++
		}

		processed++
	}

	separator := strings.Repeat("=", 72)
	p.printf("\n%s\n", separator)
	p.printf("Ingestion Report\n")
	p.printf("%s\n", separator)
	p.printf("\n  Source:       %s\n", p.BaseURL)
	p.printf("  Authority:    Nemzeti Jogszabalytar / Magyar Kozlony\n")
	p.printf("  Processed:    %d\n", processed)
	p.printf("  Cached:       %d\n", cached)
	p.printf("  Failed:       %d\n", failed)
	p.printf("  Provisions:   %d\n", totalProvisions)
	p.printf("  Definitions:  %d\n", totalDefinitions)

	if len(results) <= 20 {
		p.printf("\n  Per-Act breakdown:\n")
		p.printf("  %-32s %12s %13s %16s\n", "Act", "Provisions", "Definitions", "Status")
		p.printf("  %s %s %s %s\n", strings.Repeat("-", 32), strings.Repeat("-", 12), strings.Repeat("-", 13), strings.Repeat("-", 16))

		for _, result := range results {
			p.printf("  %-32s %12d %13d %16s\n", result.act, result.provisions, result.definitions, result.status)
		}
	} else {
		metadataOnlyRows := 0
		errorRows := []ingestionRow{}
		for _, result := range results {
			switch {
			case result.status == "METADATA_ONLY":
				metadataOnlyRows++
			case result.status != "OK" && result.status != "cached":
				errorRows = append(errorRows, result)
			}
		}
		p.printf("  Window summary: %d OK, %d cached, %d metadata-only, %d failed/skipped\n", success, cached, metadataOnlyRows, failed)
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
	p.printf("")
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

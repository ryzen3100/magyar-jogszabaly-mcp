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
	// DefaultBaseURL is the official njt.hu origin.
	DefaultBaseURL = "https://njt.hu"
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
			html = string(data)
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

		html = result.Body
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

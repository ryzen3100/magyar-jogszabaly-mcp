package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// DiscoveryPageSize is the njt.hu search page size used during discovery.
const DiscoveryPageSize = 50

// maxDiscoveryPages caps the page count parsed from first-page HTML so a
// corrupt/hostile value cannot turn discovery into an unbounded crawl.
const maxDiscoveryPages = 10000

// DiscoveredLaw is one law row from the njt.hu search index.
type DiscoveredLaw struct {
	DocumentID  string `json:"documentId"`
	Title       string `json:"title"`
	TitleEn     string `json:"titleEn,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	IssuedDate  string `json:"issuedDate,omitempty"`
	InForceDate string `json:"inForceDate,omitempty"`
	URL         string `json:"url"`
}

// DiscoverySeed is the on-disk discovery cache format
// (data/source/law-discovery-{all,in-force}.json).
type DiscoverySeed struct {
	InForceOnly bool            `json:"inForceOnly"`
	PageSize    int             `json:"pageSize"`
	Laws        []DiscoveredLaw `json:"laws"`
}

type searchURLPayload struct {
	Evszam       string `json:"evszam"`
	Sorszam      string `json:"sorszam"`
	AuthorType   string `json:"author_type"`
	Szokereso    string `json:"szokereso"`
	CsakHatalyos bool   `json:"csak_hatalyos"`
	PontosSzora  bool   `json:"pontos_szora"`
	CsakCimben   bool   `json:"csak_cimben"`
	Targyszo     bool   `json:"targyszo"`
	GazetteState bool   `json:"gazette_state"`
}

type searchURLResponse struct {
	Success bool   `json:"success"`
	URL     string `json:"url"`
}

// searchPathPattern bounds the tokenized search path njt.hu's AJAX endpoint
// returns before it is embedded in discovery URLs (it is remote input); real
// paths are slash-separated segments like "tok/en".
var searchPathPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

var (
	njtDocIDPattern  = regexp.MustCompile(`/jogszabaly/([^/?#]+)`)
	pageCountPattern = regexp.MustCompile(`(?i)id="page-count">\s*/\s*(\d+)\s*<`)
	// njt.hu hosts all statute types in one ID space: acts end in -00-00,
	// decrees reuse year+number with non-zero blocks (210/2009. Korm. rendelet
	// = 2009-210-20-22). The trailing groups are always two chars in the
	// observed corpus.
	// ponytail: trailing groups pinned to [0-9A-Z]{2}; widen if njt.hu
	// introduces longer block numbers (the discovery census will show gaps).
	mainLinkPattern = regexp.MustCompile(
		`(?i)(<a[^>]*href="jogszabaly/([0-9]{4}-[0-9A-Z]+-[0-9A-Z]{2}-[0-9A-Z]{2})"[^>]*>)([\s\S]*?)</a>`)
	linkClassPattern   = regexp.MustCompile(`(?i)class="([^"]*)"`)
	descriptionPattern = regexp.MustCompile(`(?i)<p>([\s\S]*?)</p>`)
	titleEnPattern     = regexp.MustCompile(`(?i)class="resultItem translation"[^>]*title="([^"]+)"`)
	resultDatePattern  = regexp.MustCompile(`(?i)<span class="resultDate"[^>]*>([\s\S]*?)</span>`)
	datePattern        = regexp.MustCompile(`(\d{4})\.\s*(\d{2})\.\s*(\d{2})\.`)
)

// ExtractNjtDocumentID extracts the njt.hu document id from an act URL
// (".../jogszabaly/2011-112-00-00" -> "2011-112-00-00").
func ExtractNjtDocumentID(rawURL string) string {
	if m := njtDocIDPattern.FindStringSubmatch(rawURL); m != nil {
		return m[1]
	}
	return ""
}

// ExtractTotalPages reads the total page count from a search result page,
// clamped to maxDiscoveryPages so a corrupt/hostile count cannot drive an
// unbounded crawl.
func ExtractTotalPages(html string) int {
	m := pageCountPattern.FindStringSubmatch(html)
	if m == nil {
		return 1
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 1
	}
	return min(n, maxDiscoveryPages)
}

// ParseSearchResultPage extracts the discovered laws from one search result
// page. Port of parseSearchResultPage; law URLs use the given base origin.
func ParseSearchResultPage(html, baseURL string) []DiscoveredLaw {
	chunks := strings.Split(html, `<div class="resultItemWrapper">`)[1:]
	laws := []DiscoveredLaw{}

	for _, chunk := range chunks {
		m := mainLinkPattern.FindStringSubmatch(chunk)
		if m == nil {
			continue
		}

		linkTag := m[1]
		documentID := m[2]
		shortTitle := HTMLToText(m[3])

		linkClasses := ""
		if lm := linkClassPattern.FindStringSubmatch(linkTag); lm != nil {
			linkClasses = strings.ToLower(lm[1])
		}

		description := ""
		if dm := descriptionPattern.FindStringSubmatch(chunk); dm != nil {
			description = HTMLToText(dm[1])
		}
		fullTitle := shortTitle
		if description != "" {
			fullTitle = shortTitle + " " + description
		}

		titleEn := ""
		if tm := titleEnPattern.FindStringSubmatch(chunk); tm != nil {
			titleEn = HTMLToText(tm[1])
		}

		dateSpan := ""
		if dm := resultDatePattern.FindStringSubmatch(chunk); dm != nil {
			dateSpan = HTMLToText(dm[1])
		}
		inForceDate := ""
		if dm := datePattern.FindStringSubmatch(dateSpan); dm != nil {
			inForceDate = dm[1] + "-" + dm[2] + "-" + dm[3]
		}

		status := "amended"
		switch {
		case strings.Contains(linkClasses, "now"):
			status = "in_force"
		case strings.Contains(linkClasses, "future"):
			status = "not_yet_in_force"
		case strings.Contains(linkClasses, "past"):
			status = "repealed"
		}

		laws = append(laws, DiscoveredLaw{
			DocumentID:  documentID,
			Title:       fullTitle,
			TitleEn:     titleEn,
			Description: description,
			Status:      status,
			InForceDate: inForceDate,
			URL:         baseURL + "/jogszabaly/" + documentID,
		})
	}

	return laws
}

// fetchSearchPathForLaws resolves the tokenized search path for the discovery
// query via the get_search_url.json endpoint.
func (p *Pipeline) fetchSearchPathForLaws(ctx context.Context, inForceOnly bool) (string, error) {
	payload, err := json.Marshal(searchURLPayload{
		Evszam:       "",
		Sorszam:      "",
		AuthorType:   "0000",
		Szokereso:    "",
		CsakHatalyos: inForceOnly,
	})
	if err != nil {
		return "", err
	}

	resp, err := p.Fetcher.Fetch(ctx, p.resolveURL(p.BaseURL+ajaxSearchPath), &RequestOptions{
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
		return "", fmt.Errorf("search URL request failed (HTTP %d)", resp.Status)
	}

	var parsed searchURLResponse
	if err := json.Unmarshal([]byte(resp.Body), &parsed); err != nil {
		return "", fmt.Errorf("search URL response was not JSON: %v", err)
	}
	if !parsed.Success || parsed.URL == "" {
		return "", fmt.Errorf("search URL request did not return a valid path")
	}
	return parsed.URL, nil
}

func (p *Pipeline) discoveryCachePath(inForceOnly bool) string {
	suffix := "all"
	if inForceOnly {
		suffix = "in-force"
	}
	return filepath.Join(p.SourceDir, "law-discovery-"+suffix+".json")
}

// readDiscoveryCache returns the cached discovery results, or nil when the
// cache is missing, unreadable, or does not match the requested mode.
func (p *Pipeline) readDiscoveryCache(inForceOnly bool) []DiscoveredLaw {
	data, err := os.ReadFile(p.discoveryCachePath(inForceOnly))
	if err != nil {
		return nil
	}
	var parsed DiscoverySeed
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	if len(parsed.Laws) == 0 || parsed.InForceOnly != inForceOnly || parsed.PageSize != DiscoveryPageSize {
		return nil
	}
	return parsed.Laws
}

// discoverLaws walks the njt.hu search index and caches the results.
func (p *Pipeline) discoverLaws(ctx context.Context, inForceOnly bool) ([]DiscoveredLaw, error) {
	if err := os.MkdirAll(p.SourceDir, 0o755); err != nil {
		return nil, err
	}

	searchPath, err := p.fetchSearchPathForLaws(ctx, inForceOnly)
	if err != nil {
		return nil, err
	}
	if !searchPathPattern.MatchString(searchPath) || strings.Contains(searchPath, "..") {
		return nil, fmt.Errorf("search path %q has unexpected characters", searchPath)
	}
	firstURL := fmt.Sprintf("%s/search/%s/1/%d", p.BaseURL, searchPath, DiscoveryPageSize)

	firstPage, err := p.Fetcher.Fetch(ctx, firstURL, nil)
	if err != nil {
		return nil, err
	}
	if firstPage.Status != 200 {
		return nil, fmt.Errorf("discovery page fetch failed (HTTP %d)", firstPage.Status)
	}

	totalPages := ExtractTotalPages(firstPage.Body)
	discoveredMap := map[string]DiscoveredLaw{}

	for _, law := range ParseSearchResultPage(firstPage.Body, p.BaseURL) {
		discoveredMap[law.DocumentID] = law
	}

	for page := 2; page <= totalPages; page++ {
		url := fmt.Sprintf("%s/search/%s/%d/%d", p.BaseURL, searchPath, page, DiscoveryPageSize)
		resp, err := p.Fetcher.Fetch(ctx, url, nil)
		if err != nil {
			return nil, err
		}
		if resp.Status != 200 {
			return nil, fmt.Errorf("discovery page %d failed (HTTP %d)", page, resp.Status)
		}

		for _, law := range ParseSearchResultPage(resp.Body, p.BaseURL) {
			if _, seen := discoveredMap[law.DocumentID]; !seen {
				discoveredMap[law.DocumentID] = law
			}
		}

		if page%10 == 0 || page == totalPages {
			p.printf("  Discovery progress: page %d/%d (%d laws)\n", page, totalPages, len(discoveredMap))
		}
	}

	laws := slices.SortedFunc(maps.Values(discoveredMap), func(a, b DiscoveredLaw) int {
		return strings.Compare(a.DocumentID, b.DocumentID)
	})

	if err := writeJSONFile(p.discoveryCachePath(inForceOnly), DiscoverySeed{
		InForceOnly: inForceOnly,
		PageSize:    DiscoveryPageSize,
		Laws:        laws,
	}); err != nil {
		return nil, err
	}
	return laws, nil
}

// writeFileAtomic writes data to a temp file in path's directory and renames
// it into place, so an interrupted run cannot leave a truncated file behind
// (a truncated seed would also block --resume).
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}

// writeJSONFile writes v as indented JSON (HTML-unescaped, trailing newline),
// matching the TS JSON.stringify(v, null, 2) + "\n" output shape.
func writeJSONFile(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return writeFileAtomic(path, buf.Bytes(), 0o644)
}

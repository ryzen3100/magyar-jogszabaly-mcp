package ingest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/seed"
)

// TestStrippedHTMLReproducesSeeds verifies that the law-content region kept by
// stripHTMLBody is sufficient for the parser: re-parsing the stored HTML must
// reproduce the committed seed provisions. Gated behind HU_STRIP_VERIFY=1 so
// ordinary `go test ./...` stays fast (a full run walks all of data/source).
func TestStrippedHTMLReproducesSeeds(t *testing.T) {
	if os.Getenv("HU_STRIP_VERIFY") != "1" {
		t.Skip("set HU_STRIP_VERIFY=1 to walk data/source and verify against data/seed")
	}
	sourceDir, seedDir := "../../data/source", "../../data/seed"
	if v := os.Getenv("HU_DATA_DIR"); v != "" {
		sourceDir, seedDir = filepath.Join(v, "source"), filepath.Join(v, "seed")
	}

	seeds, err := filepath.Glob(filepath.Join(seedDir, "*.json"))
	if err != nil || len(seeds) == 0 {
		t.Fatalf("no seed files found under %s", seedDir)
	}

	// Worker pool: the walk is CPU-bound (regex parsing) and a sequential
	// pass over all of data/source leaves the machine idle.
	var checked, skipped atomic.Int64
	work := make(chan string)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for seedFile := range work {
				data, err := os.ReadFile(seedFile)
				if err != nil {
					t.Error(err)
					continue
				}
				var doc seed.DocumentSeed
				if err := json.Unmarshal(data, &doc); err != nil {
					t.Errorf("%s: %v", seedFile, err)
					continue
				}
				if len(doc.Provisions) == 0 {
					continue // metadata-only seeds have no provisions to reproduce
				}
				cacheKey := ParseSourceCacheKey(ActIndexEntry{ID: doc.ID, URL: doc.URL})
				htmlBytes, err := os.ReadFile(filepath.Join(sourceDir, cacheKey+".html"))
				if err != nil {
					skipped.Add(1) // seed with no cached HTML (e.g. -resume reuse)
					continue
				}

				parsed := ParseHungarianHTML(string(htmlBytes), ActIndexEntry{ID: doc.ID, URL: doc.URL})
				if len(parsed.Provisions) != len(doc.Provisions) {
					t.Errorf("%s: parsed %d provisions, seed has %d", doc.ID, len(parsed.Provisions), len(doc.Provisions))
					continue
				}
				for i, got := range parsed.Provisions {
					want := doc.Provisions[i]
					if got.ProvisionRef != want.ProvisionRef || got.Section != want.Section ||
						strings.TrimSpace(got.Content) != strings.TrimSpace(want.Content) {
						t.Errorf("%s provision #%d: parsed ref/section/content mismatch (ref %q vs %q)",
							doc.ID, i, got.ProvisionRef, want.ProvisionRef)
						break
					}
				}
				checked.Add(1)
			}
		}()
	}
	for _, s := range seeds {
		work <- s
	}
	close(work)
	wg.Wait()

	c, s := checked.Load(), skipped.Load()
	t.Logf("verified %d seeds against cached HTML (%d skipped: no cached HTML)", c, s)
	if c == 0 {
		t.Fatal("no seeds verified — nothing was actually checked")
	}
}

package ingest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/seed"
)

// TestReparseAnnexSeeds is the offline re-parse used after a parser change
// (the PR #17/#18 flow): re-parse cached source HTML with the current parser
// and rewrite seed files whose provision output changed. Gated behind
// HU_REPARSE=1 so ordinary `go test ./...` never writes.
//
// Before rewriting, it proves content conservation: the word multiset of the
// OLD provisions must be a sub-multiset of the NEW one. The annex fix moves
// annex text out of the last § into its own provisions and recovers
// previously-dropped annex headers, so the new word total can only grow —
// any missing word is a content loss and aborts the rewrite for that seed.
func TestReparseAnnexSeeds(t *testing.T) {
	if os.Getenv("HU_REPARSE") != "1" {
		t.Skip("set HU_REPARSE=1 to re-parse data/source HTML and rewrite changed data/seed files")
	}
	sourceDir, seedDir := "../../data/source", "../../data/seed"
	if v := os.Getenv("HU_DATA_DIR"); v != "" {
		sourceDir, seedDir = filepath.Join(v, "source"), filepath.Join(v, "seed")
	}

	seeds, err := filepath.Glob(filepath.Join(seedDir, "*.json"))
	if err != nil || len(seeds) == 0 {
		t.Fatalf("no seed files found under %s", seedDir)
	}

	changed, skipped, losses := 0, 0, 0
	for _, seedFile := range seeds {
		data, err := os.ReadFile(seedFile)
		if err != nil {
			t.Fatal(err)
		}
		var doc seed.DocumentSeed
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("%s: %v", seedFile, err)
		}
		if len(doc.Provisions) == 0 {
			continue // metadata-only seeds have nothing to re-parse
		}
		cacheKey := ParseSourceCacheKey(ActIndexEntry{ID: doc.ID, URL: doc.URL})
		htmlBytes, err := os.ReadFile(filepath.Join(sourceDir, cacheKey+".html"))
		if err != nil {
			skipped++ // seed with no cached HTML
			continue
		}

		act := ActIndexEntry{
			ID: doc.ID, Title: doc.Title, TitleEn: doc.TitleEn, ShortName: doc.ShortName,
			Status: doc.Status, IssuedDate: doc.IssuedDate, InForceDate: doc.InForceDate,
			URL: doc.URL, Description: doc.Description,
		}
		parsed := ParseHungarianHTML(string(htmlBytes), act)
		if !provisionsDiffer(parsed.Provisions, doc.Provisions) {
			continue
		}

		lost := lostWords(doc.Provisions, parsed.Provisions)
		if len(lost) > 0 {
			losses++
			t.Errorf("%s: content lost after re-parse: %v", doc.ID, lost)
			continue
		}
		if err := writeJSONFile(seedFile, parsed); err != nil {
			t.Fatalf("%s: %v", seedFile, err)
		}
		changed++
	}

	t.Logf("rewrote %d seeds (%d skipped: no cached HTML, %d content losses)",
		changed, skipped, losses)
	if changed == 0 && skipped == 0 {
		t.Fatal("nothing re-parsed — no seeds were actually checked")
	}
}

// provisionsDiffer reports whether the parsed provisions differ from the
// committed ones in the fields strip-verify compares (ref, section, content).
func provisionsDiffer(got, want []seed.ProvisionSeed) bool {
	if len(got) != len(want) {
		return true
	}
	for i := range got {
		if got[i].ProvisionRef != want[i].ProvisionRef || got[i].Section != want[i].Section ||
			strings.TrimSpace(got[i].Content) != strings.TrimSpace(want[i].Content) {
			return true
		}
	}
	return false
}

// lostWords returns the words (with deficits) that the old provisions
// contained and the new provisions lost — the content-conservation check.
func lostWords(old, new []seed.ProvisionSeed) map[string]int {
	oldWords := wordMultiset(provisionContents(old))
	newWords := wordMultiset(provisionContents(new))
	lost := map[string]int{}
	for w, c := range oldWords {
		if d := c - newWords[w]; d > 0 {
			lost[w] = d
		}
	}
	return lost
}

func provisionContents(provisions []seed.ProvisionSeed) string {
	var b strings.Builder
	for _, p := range provisions {
		b.WriteString(p.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

func wordMultiset(s string) map[string]int {
	m := make(map[string]int)
	for _, w := range strings.Fields(s) {
		m[w]++
	}
	return m
}

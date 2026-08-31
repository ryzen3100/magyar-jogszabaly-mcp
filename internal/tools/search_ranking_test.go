package tools

import (
	"context"
	"testing"
	"time"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/store"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/store/storetest"
)

// searchLatencyBudget is the per-query wall-clock bound for the acceptance
// cases below. The PR #16 ranking fix initially shipped with 19-25 s searches
// (full-doclist bm25 sorts and unrestricted term scans) — this guard keeps
// that regression from landing silently again. Generous margin for slow CI
// hardware: healthy runs are ~0.1-2 s.
const searchLatencyBudget = 5 * time.Second

// Acceptance ranking checks against the real full-corpus database (guarded
// like TestSearchLegislationRealDb). These pin the FTS ranking regression fix
// (INGEST_PLAN.md "Known follow-ups"): natural-language questions must surface
// the regulating legislation, not KE/OGY határozatok and miniszteri utasítások.
func TestSearchRankingAcceptanceRealDb(t *testing.T) {
	// no t.Parallel: the shared read-only handle is closed by this test's
	// defer, which would run before parallel subtests resume
	if !storetest.RealDBAvailable() {
		dbSkippedTests++
		t.Skip("real database not available")
	}
	db, err := store.OpenReadOnly(storetest.RealDBPath())
	if err != nil {
		t.Fatalf("open real db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	cases := []struct {
		name    string
		query   string
		docID   string
		maxRank int
	}{
		// Natural-language forms — target act in the top 10.
		{"kávézó question", "Milyen engedély kell ahhoz, hogy nyissak egy kávézót?",
			"hu-law-2009-210-20-22", 10},
		{"szabadság question", "Hány nap szabadság jár egy 42 éves munkavállalónak?",
			"hu-law-2012-1-00-00", 10},
		{"bankjegy question", "Ha elszakítottam egy 20 000 forintos bankjegyet és celluxszal összeragasztom, használható?",
			"hu-law-2023-1-20-2c", 10},
		// Keyword forms (content keywords) — target act in the top 5.
		{"kávézó keyword", "kávézó engedély",
			"hu-law-2009-210-20-22", 5},
		{"szabadság keyword", "szabadság munkavállaló",
			"hu-law-2012-1-00-00", 5},
		{"bankjegy keyword", "összeragasztott bankjegy",
			"hu-law-2023-1-20-2c", 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			res, _, err := SearchLegislation(ctx, db, map[string]any{"query": tc.query, "limit": 50.0})
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			if elapsed := time.Since(start); elapsed > searchLatencyBudget {
				t.Errorf("search took %s, budget %s — latency regression (see PR #16 review)", elapsed, searchLatencyBudget)
			}
			results, ok := res.([]SearchLegislationResult)
			if !ok {
				t.Fatalf("unexpected result type %T", res)
			}
			for i, r := range results {
				if r.DocumentID == tc.docID {
					if i+1 > tc.maxRank {
						t.Errorf("target %s ranked %d, want <= %d", tc.docID, i+1, tc.maxRank)
					}
					return
				}
			}
			t.Errorf("target %s not in top %d (%d results)", tc.docID, tc.maxRank, len(results))
		})
	}
}

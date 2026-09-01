package tools

import (
	"testing"
	"time"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/store"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/store/storetest"
)

// searchLatencyBudget is the per-query wall-clock bound enforced by
// BenchmarkSearchRankingAcceptanceRealDb. The PR #16 ranking fix initially
// shipped with 19-25 s searches (full-doclist bm25 sorts and unrestricted
// term scans) — this guard keeps that regression from landing silently
// again. Healthy isolated runs are ~0.1-2 s, so the 2 s bound has ~10x
// margin over the known regression class. The bound is enforced only in the
// isolated benchmark: wall-clock assertions in the regular suite flaked
// under concurrent load (three independent review reproductions).
const searchLatencyBudget = 2 * time.Second

// rankingAcceptanceCases pin the FTS ranking regression fix (INGEST_PLAN.md
// "Known follow-ups"): natural-language questions must surface the regulating
// legislation, not KE/OGY határozatok and miniszteri utasítások.
var rankingAcceptanceCases = []struct {
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

// Acceptance ranking checks against the real full-corpus database (guarded
// like TestSearchLegislationRealDb).
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
	ctx := t.Context()

	for _, tc := range rankingAcceptanceCases {
		t.Run(tc.name, func(t *testing.T) {
			res, _, err := SearchLegislation(ctx, db, map[string]any{"query": tc.query, "limit": 50.0})
			if err != nil {
				t.Fatalf("search: %v", err)
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

// BenchmarkSearchRankingAcceptanceRealDb enforces the search-latency budget
// in isolation — run it standalone (no suite contention can inflate the
// wall-clock times):
//
//	go test ./internal/tools -run '^$' -bench BenchmarkSearchRankingAcceptanceRealDb
//
// Latency lives in a benchmark, not the suite test, because the suite runs
// many DB-backed tests concurrently and contention made the old in-test
// budget fail spuriously (reproduced in three review sessions). The budget
// itself is unchanged in spirit: a regression to PR #16's 19-25 s searches
// still fails, via b.Fatalf, with 10x margin.
func BenchmarkSearchRankingAcceptanceRealDb(b *testing.B) {
	if !storetest.RealDBAvailable() {
		b.Skip("real database not available")
	}
	db, err := store.OpenReadOnly(storetest.RealDBPath())
	if err != nil {
		b.Fatalf("open real db: %v", err)
	}
	defer db.Close()
	ctx := b.Context()

	for b.Loop() {
		for _, tc := range rankingAcceptanceCases {
			start := time.Now()
			_, _, err := SearchLegislation(ctx, db, map[string]any{"query": tc.query, "limit": 50.0})
			if err != nil {
				b.Fatalf("search %s: %v", tc.name, err)
			}
			if elapsed := time.Since(start); elapsed > searchLatencyBudget {
				b.Fatalf("query %q took %s, budget %s — latency regression (see PR #16 review)",
					tc.query, elapsed, searchLatencyBudget)
			}
		}
	}
}

package ingest

// Temporary live probe (deleted after use): verifies against the real
// njt.hu that the widened discovery regex finds decree-type document IDs.
// Run with: HU_LIVE_PROBE=1 go test ./internal/ingest -run TestLiveDiscoveryProbe -v

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestLiveDiscoveryProbe(t *testing.T) {
	if os.Getenv("HU_LIVE_PROBE") != "1" {
		t.Skip("set HU_LIVE_PROBE=1 to run")
	}

	for _, baseURL := range []string{"https://njt.jog.gov.hu"} {
		t.Run(baseURL, func(t *testing.T) {
			p := newFastPipeline(t.TempDir(), t.TempDir())
			p.BaseURL = baseURL

			searchPath, err := p.fetchSearchPathForLaws(t.Context(), false, "2220")
			if err != nil {
				t.Fatalf("search path: %v", err)
			}
			fmt.Printf("[%s] searchPath=%s\n", baseURL, searchPath)

			resp, err := p.Fetcher.Fetch(t.Context(), p.resolveURL(p.BaseURL+"/search/"+searchPath+"/1/50"), nil)
			if err != nil {
				t.Fatalf("page 1: %v", err)
			}
			fmt.Printf("[%s] page1 status=%d len=%d totalPages=%d\n", baseURL, resp.Status, len(resp.Body), ExtractTotalPages(resp.Body))

			laws := ParseSearchResultPage(resp.Body, p.BaseURL)
			acts, other := 0, 0
			otherIDs := []string{}
			for _, l := range laws {
				if strings.HasSuffix(l.DocumentID, "-00-00") {
					acts++
				} else {
					other++
					if len(otherIDs) < 10 {
						otherIDs = append(otherIDs, l.DocumentID+" | "+l.Title)
					}
				}
			}
			fmt.Printf("[%s] page1 laws=%d acts(00-00)=%d non-act=%d\n", baseURL, len(laws), acts, other)
			for _, id := range otherIDs {
				fmt.Printf("  non-act: %s\n", id)
			}
			if total := ExtractTotalPages(resp.Body); total > 0 {
				fmt.Printf("[%s] estimated corpus = %d pages x 50 = %d docs\n", baseURL, total, total*50)
			}
		})
	}
}

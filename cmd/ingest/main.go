// ingest fetches Hungarian legislation from the official Nemzeti Jogszabalytar
// portal (njt.hu), parses section-level provisions, and writes seed JSON
// files. Go port of scripts/ingest.ts.
//
// Usage:
//
//	ingest                                  # curated corpus (10 laws)
//	ingest -full                            # discover and ingest full corpus
//	ingest -full -in-force-only             # full discovery for in-force laws only
//	ingest -full -discover-only             # discover all laws metadata only
//	ingest -full -resume                    # skip already-generated seed files
//	ingest -skip-fetch                      # reuse locally cached HTML where available
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/ingest"
)

func main() {
	var (
		full             = flag.Bool("full", false, "discover and ingest the full corpus instead of the curated acts")
		resume           = flag.Bool("resume", false, "skip already-generated seed files")
		refreshDiscovery = flag.Bool("refresh-discovery", false, "ignore the discovery cache and re-run discovery")
		skipFetch        = flag.Bool("skip-fetch", false, "reuse locally cached HTML where available")
		discoverOnly     = flag.Bool("discover-only", false, "discover laws metadata only, without fetching acts")
		inForceOnly      = flag.Bool("in-force-only", false, "restrict full discovery to in-force laws")
		baseURL          = flag.String("base-url", ingest.DefaultBaseURL,
			"njt.hu origin (override points discovery and act fetches at a mirror/test server)")
		dataDir = flag.String("data-dir", "data",
			"root directory holding source HTML (<dir>/source) and seed JSON (<dir>/seed)")
	)
	flag.Parse()

	pipeline := ingest.NewPipeline(
		filepath.Join(*dataDir, "source"),
		filepath.Join(*dataDir, "seed"),
	)
	pipeline.BaseURL = strings.TrimRight(*baseURL, "/")

	// Ctrl-C / SIGTERM unwind the context already threaded through
	// Run -> Fetch instead of killing the process mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := pipeline.Run(ctx, ingest.Options{
		Full:             *full,
		Resume:           *resume,
		RefreshDiscovery: *refreshDiscovery,
		SkipFetch:        *skipFetch,
		DiscoverOnly:     *discoverOnly,
		InForceOnly:      *inForceOnly,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fatal ingestion error:", err)
		os.Exit(1)
	}
}

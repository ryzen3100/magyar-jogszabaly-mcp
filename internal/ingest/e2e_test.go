package ingest

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCmdIngestEndToEnd builds cmd/ingest and runs it against a fake njt.hu
// via the -base-url override, exercising the whole flow offline: discovery,
// act fetch, deferred-block hydration, parsing and seed writing. The real
// 1200ms rate limit is kept, so this is skipped in -short runs.
func TestCmdIngestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("rate-limited binary e2e")
	}

	ts := newFakeNjtServer(t)

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "ingest")
	build := exec.Command("go", "build", "-o", bin, "./cmd/ingest")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	dataDir := filepath.Join(t.TempDir(), "data")
	var stdout, stderr bytes.Buffer
	// CommandContext kills the child on timeout instead of leaking it.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--full", "-base-url", ts.URL, "-data-dir", dataDir)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			t.Fatalf("ingest timed out\nstdout:\n%s", stdout.String())
		}
		t.Fatalf("ingest exited %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	out := stdout.String()
	for _, want := range []string{
		"Hungarian Law MCP -- Ingestion Pipeline",
		"full corpus discovery",
		"Discovered laws: 3",
		"Ingestion act list: 5",
		"Ingestion Report",
		"hydrated 1 deferred block ranges",
		"-> METADATA_ONLY",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// Discovery cache written under <data-dir>/source.
	if _, err := os.Stat(filepath.Join(dataDir, "source", "law-discovery-all.json")); err != nil {
		t.Errorf("discovery cache: %v", err)
	}

	for _, seed := range []string{
		"act-cxii-2011-info-self-determination.json",
		"act-cxii-2011-public-data.json",
		"criminal-code-cybercrime.json",
		"hu-law-1992-100-00-00.json",
		"hu-law-2020-10-00-00.json",
	} {
		if _, err := os.Stat(filepath.Join(dataDir, "seed", seed)); err != nil {
			t.Errorf("seed %s: %v", seed, err)
		}
	}
}

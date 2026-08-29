package ingest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/seed"
)

func TestToMetadataOnlyAct(t *testing.T) {
	act := ActIndexEntry{ID: "hu-law-x", Title: "Valamilyen törvény", Status: "in_force", URL: "https://njt.hu/jogszabaly/2000-1-00-00"}
	doc := ToMetadataOnlyAct(act)
	if doc.Description != metadataOnlyDescription {
		t.Errorf("default description = %q", doc.Description)
	}
	if doc.Type != "statute" || doc.ID != act.ID || doc.Title != act.Title || len(doc.Provisions) != 0 || len(doc.Definitions) != 0 {
		t.Errorf("metadata-only doc = %+v", doc)
	}

	act.Description = "custom"
	if doc := ToMetadataOnlyAct(act); doc.Description != "custom" {
		t.Errorf("custom description = %q", doc.Description)
	}
}

func TestParseSourceCacheKey(t *testing.T) {
	tests := []struct {
		act  ActIndexEntry
		want string
	}{
		{ActIndexEntry{URL: "https://njt.hu/jogszabaly/2011-112-00-00", ID: "act-x"}, "2011-112-00-00"},
		{ActIndexEntry{URL: "https://example.com/nope", ID: "act-y"}, "act-y"},
	}
	for _, tt := range tests {
		t.Run(tt.act.ID, func(t *testing.T) {
			if got := ParseSourceCacheKey(tt.act); got != tt.want {
				t.Errorf("ParseSourceCacheKey(%+v) = %q, want %q", tt.act, got, tt.want)
			}
		})
	}
}

func TestExtractDeferredBlockStarts(t *testing.T) {
	tests := []struct {
		name string
		html string
		want []int
	}{
		{"single", `<div class="pH borderStart"data-show-order="12">`, []int{12}},
		{"sorted ascending", `<div class="pH borderStart"data-show-order="30"><div class="pH borderStart"data-show-order="4">`, []int{4, 30}},
		{"none", `nothing here`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractDeferredBlockStarts(tt.html)
			if len(got) != len(tt.want) {
				t.Fatalf("ExtractDeferredBlockStarts(%q) = %v, want %v", tt.html, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("start %d = %d, want %d", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestHydrateDeferredBlocks(t *testing.T) {
	var bodies []string
	blockNo := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/ajax/njtGetBlock.json", func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		if _, err := io.ReadFull(r.Body, buf); err != nil {
			t.Errorf("read block body: %v", err)
		}
		bodies = append(bodies, string(buf))
		blockNo++
		fmt.Fprintf(w, `<span class="jhId" id="SZ200B"></span><div class="bekezdes">Hidratált blokk %d.</div>`, blockNo)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	p := newFastPipeline(t.TempDir(), t.TempDir())
	p.BaseURL = ts.URL

	act := ActIndexEntry{ID: "act-x", URL: "https://njt.hu/jogszabaly/2000-1-00-00"}
	html := `page<div class="pH borderStart"data-show-order="5">mid<div class="pH borderStart"data-show-order="9">`

	got, err := p.HydrateDeferredBlocks(t.Context(), html, act, true)
	if err != nil {
		t.Fatalf("HydrateDeferredBlocks: %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("got %d POST bodies, want 1", len(bodies))
	}

	var req blockRequestBody
	if err := json.Unmarshal([]byte(bodies[0]), &req); err != nil {
		t.Fatalf("POST body not JSON: %v", err)
	}
	if req.DocumentID != "2000-1-00-00" {
		t.Errorf("documentId = %q, want 2000-1-00-00", req.DocumentID)
	}
	// Starts [5, 9] become the ranges {start:5,last:9} and {start:9}.
	if len(req.Data) != 2 || req.Data[0].Start != 5 || req.Data[0].Last == nil || *req.Data[0].Last != 9 ||
		req.Data[1].Start != 9 || req.Data[1].Last != nil {
		t.Errorf("data = %+v, want [{5 9} {9 <nil>}]", req.Data)
	}
	if !strings.HasSuffix(got, "\n"+`<span class="jhId" id="SZ200B"></span><div class="bekezdes">Hidratált blokk 1.</div>`) {
		t.Errorf("hydrated html = %q", got)
	}

	// No deferred blocks -> html unchanged, no request.
	bodies = nil
	same, err := p.HydrateDeferredBlocks(t.Context(), "plain", act, true)
	if err != nil || same != "plain" || len(bodies) != 0 {
		t.Errorf("no-op hydration = %q, %v, %d bodies", same, err, len(bodies))
	}
}

func TestHydrateDeferredBlocksChunking(t *testing.T) {
	var sizes []int
	var finalLast *int
	mux := http.NewServeMux()
	mux.HandleFunc("/ajax/njtGetBlock.json", func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		if _, err := io.ReadFull(r.Body, buf); err != nil {
			t.Errorf("read block body: %v", err)
		}
		var req blockRequestBody
		json.Unmarshal(buf, &req)
		sizes = append(sizes, len(req.Data))
		for _, d := range req.Data {
			if d.Last == nil {
				v := d.Start
				finalLast = &v
			}
		}
		w.Write([]byte("x"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	p := newFastPipeline(t.TempDir(), t.TempDir())
	p.BaseURL = ts.URL

	var html strings.Builder
	for i := 25; i >= 1; i-- {
		fmt.Fprintf(&html, `<div class="pH borderStart"data-show-order="%d">`, i)
	}

	act := ActIndexEntry{ID: "act-x", URL: "https://njt.hu/jogszabaly/2000-1-00-00"}
	if _, err := p.HydrateDeferredBlocks(t.Context(), html.String(), act, false); err != nil {
		t.Fatalf("HydrateDeferredBlocks: %v", err)
	}
	if len(sizes) != 2 || sizes[0] != 20 || sizes[1] != 5 {
		t.Errorf("chunk sizes = %v, want [20 5]", sizes)
	}
	if finalLast == nil || *finalLast != 25 {
		t.Errorf("final range last = %v, want 25", finalLast)
	}
}

func TestBuildFullCorpusActList(t *testing.T) {
	discovered := []DiscoveredLaw{
		{DocumentID: "1992-100-00-00", Title: "1992. évi törvény", Status: "repealed", InForceDate: "1992-07-01", URL: "https://njt.hu/jogszabaly/1992-100-00-00"},
		{DocumentID: "2011-112-00-00", Title: "2011. évi CXII. törvény", Status: "amended", InForceDate: "2012-01-01", Description: "desc from discovery", URL: "https://njt.hu/jogszabaly/2011-112-00-00"},
	}

	acts := BuildFullCorpusActList(discovered)

	// Two discovered laws + the two curated subset aliases at the end.
	if len(acts) != 4 {
		t.Fatalf("got %d acts, want 4: %+v", len(acts), acts)
	}

	if acts[0].ID != "hu-law-1992-100-00-00" {
		t.Errorf("unknown law id = %q", acts[0].ID)
	}
	if acts[0].Status != "repealed" || acts[0].InForceDate != "1992-07-01" {
		t.Errorf("unknown law fields = %+v", acts[0])
	}
	if acts[0].Description != discoveredDescription {
		t.Errorf("unknown law description = %q", acts[0].Description)
	}

	// The curated entry wins for the shared njt doc id and takes the
	// discovery URL/status; the public-data alias (same doc id, subset id)
	// must not shadow it.
	if acts[1].ID != "act-cxii-2011-info-self-determination" {
		t.Errorf("curated act id = %q", acts[1].ID)
	}
	if acts[1].URL != "https://njt.hu/jogszabaly/2011-112-00-00" || acts[1].Status != "amended" {
		t.Errorf("curated act override = %+v", acts[1])
	}
	if acts[1].Description == "desc from discovery" {
		t.Errorf("curated description should come from the curated entry: %q", acts[1].Description)
	}

	if acts[2].ID != "act-cxii-2011-public-data" || acts[3].ID != "criminal-code-cybercrime" {
		t.Errorf("subset aliases = %q, %q", acts[2].ID, acts[3].ID)
	}
}

// newFakeNjtServer builds an offline njt.hu: discovery endpoints, a rich act
// page (with one deferred block) for 2011-112-00-00 and a metadata-only page
// for 1992-100-00-00. The returned count func reports how often each endpoint
// was hit (mutex-guarded: handlers run on their own goroutines).
func newFakeNjtServer(t *testing.T) (*httptest.Server, func(string) int) {
	t.Helper()
	fixture := sampleActHTML(t)
	deferred := fixture + `<div class="pH borderStart"data-show-order="5"></div>`

	metadataOnly := `<html><head><title>1992. évi C. törvény</title></head><body><p>Nincs szakaszos tartalom.</p></body></html>`

	var mu sync.Mutex
	requests := map[string]int{}
	bump := func(name string) { mu.Lock(); requests[name]++; mu.Unlock() }
	count := func(name string) int { mu.Lock(); defer mu.Unlock(); return requests[name] }

	mux := http.NewServeMux()
	mux.HandleFunc("/ajax/get_search_url.json", func(w http.ResponseWriter, r *http.Request) {
		bump("search-url")
		w.Write([]byte(`{"success":true,"url":"tok/en"}`))
	})
	mux.HandleFunc("/search/tok/en/1/50", func(w http.ResponseWriter, r *http.Request) {
		bump("search-page")
		w.Write([]byte(searchPageHTML))
	})
	mux.HandleFunc("/jogszabaly/2011-112-00-00", func(w http.ResponseWriter, r *http.Request) {
		bump("act-2011")
		w.Write([]byte(deferred))
	})
	mux.HandleFunc("/jogszabaly/1992-100-00-00", func(w http.ResponseWriter, r *http.Request) {
		bump("act-1992")
		w.Write([]byte(metadataOnly))
	})
	mux.HandleFunc("/jogszabaly/2012-100-00-00", func(w http.ResponseWriter, r *http.Request) {
		bump("act-2012")
		w.Write([]byte(metadataOnly))
	})
	mux.HandleFunc("/jogszabaly/2020-10-00-00", func(w http.ResponseWriter, r *http.Request) {
		bump("act-2020")
		w.Write([]byte(metadataOnly))
	})
	mux.HandleFunc("/jogszabaly/2009-210-20-22", func(w http.ResponseWriter, r *http.Request) {
		bump("rendelet-2009")
		w.Write([]byte(metadataOnly))
	})
	mux.HandleFunc("/ajax/njtGetBlock.json", func(w http.ResponseWriter, r *http.Request) {
		bump("block")
		buf := make([]byte, r.ContentLength)
		if _, err := io.ReadFull(r.Body, buf); err != nil {
			t.Errorf("read block body: %v", err)
		}
		var req blockRequestBody
		if err := json.Unmarshal(buf, &req); err != nil {
			t.Errorf("block body: %v", err)
		}
		if req.DocumentID != "2011-112-00-00" || len(req.Data) != 1 || req.Data[0].Start != 5 {
			t.Errorf("block request = %+v", req)
		}
		w.Write([]byte(`<span class="jhId" id="SZ26B"></span><div class="bekezdes">Hidratált kiegészítő bekezdés.</div>`))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, count
}

// TestPipelineRun exercises Pipeline.Run in-process with the fake njt.hu:
// the full discover->fetch->parse->seed flow, the discovery-cache hit path
// and a --resume run over pre-existing seeds. Replaces what only the
// rate-limited binary e2e covered (TestCmdIngestEndToEnd).
func TestPipelineRun(t *testing.T) {
	sourceDir := filepath.Join(t.TempDir(), "source")
	seedDir := filepath.Join(t.TempDir(), "seed")
	ts, count := newFakeNjtServer(t)
	ctx := t.Context()

	seedNames := []string{
		"act-cxii-2011-info-self-determination.json",
		"act-cxii-2011-public-data.json",
		"criminal-code-cybercrime.json",
		"hu-law-1992-100-00-00.json",
		"hu-law-2009-210-20-22.json",
		"hu-law-2020-10-00-00.json",
	}

	// Full run: discovery, act fetch, hydration, parsing, seed writing.
	p := newFastPipeline(sourceDir, seedDir)
	p.BaseURL = ts.URL
	var out strings.Builder
	p.Stdout = &out
	if err := p.Run(ctx, Options{Full: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{
		"Hungarian Law MCP -- Ingestion Pipeline",
		"full corpus discovery",
		"Discovered laws: 4",
		"Ingestion act list: 6",
		"hydrated 1 deferred block ranges",
		"-> METADATA_ONLY",
		"Ingestion Report",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
	if count("search-url") != 1 || count("search-page") != 1 {
		t.Errorf("discovery requests = (%d, %d), want (1, 1)", count("search-url"), count("search-page"))
	}

	if _, err := os.Stat(filepath.Join(sourceDir, "law-discovery-all.json")); err != nil {
		t.Errorf("discovery cache: %v", err)
	}
	for _, name := range seedNames {
		if _, err := os.Stat(filepath.Join(seedDir, name)); err != nil {
			t.Errorf("seed %s: %v", name, err)
		}
	}

	// The rich act made it through fetch -> hydration -> parse -> seed.
	data, err := os.ReadFile(filepath.Join(seedDir, seedNames[0]))
	if err != nil {
		t.Fatal(err)
	}
	var rich seed.DocumentSeed
	if err := json.Unmarshal(data, &rich); err != nil {
		t.Fatal(err)
	}
	if len(rich.Provisions) != 9 {
		t.Errorf("got %d provisions, want 9 (8 parsed + 1 hydrated)", len(rich.Provisions))
	}

	// Second run hits the discovery cache: no discovery traffic, same acts.
	p2 := newFastPipeline(sourceDir, seedDir)
	p2.BaseURL = ts.URL
	var out2 strings.Builder
	p2.Stdout = &out2
	if err := p2.Run(ctx, Options{Full: true}); err != nil {
		t.Fatalf("cache-hit Run: %v", err)
	}
	if !strings.Contains(out2.String(), "Loaded discovery cache (4 laws)") {
		t.Errorf("output should name the loaded discovery cache:\n%s", out2.String())
	}
	if count("search-url") != 1 || count("search-page") != 1 {
		t.Errorf("cache-hit run re-discovered: (%d, %d), want (1, 1)", count("search-url"), count("search-page"))
	}

	// --resume over the existing seeds caches every act without network.
	p3 := newFastPipeline(sourceDir, seedDir)
	p3.BaseURL = "http://127.0.0.1:1" // nothing listens here
	var out3 strings.Builder
	p3.Stdout = &out3
	if err := p3.Run(ctx, Options{Full: true, Resume: true}); err != nil {
		t.Fatalf("resume Run: %v", err)
	}
	if !strings.Contains(out3.String(), "cached") {
		t.Errorf("resume output should mark cached rows:\n%s", out3.String())
	}
}

func TestFetchAndParseActsFullFlow(t *testing.T) {
	ts, _ := newFakeNjtServer(t)
	sourceDir := filepath.Join(t.TempDir(), "source")
	seedDir := filepath.Join(t.TempDir(), "seed")

	p := newFastPipeline(sourceDir, seedDir)
	p.BaseURL = ts.URL

	acts := BuildFullCorpusActList([]DiscoveredLaw{
		{DocumentID: "1992-100-00-00", Title: "1992. évi C. törvény", Status: "repealed", URL: ts.URL + "/jogszabaly/1992-100-00-00"},
		{DocumentID: "2011-112-00-00", Title: "2011. évi CXII. törvény", Status: "amended", URL: ts.URL + "/jogszabaly/2011-112-00-00"},
	})
	if err := p.FetchAndParseActs(t.Context(), acts, false, false); err != nil {
		t.Fatalf("FetchAndParseActs: %v", err)
	}

	// Seed for the rich act: parsed provisions including the hydrated block.
	data, err := os.ReadFile(filepath.Join(seedDir, "act-cxii-2011-info-self-determination.json"))
	if err != nil {
		t.Fatalf("rich seed: %v", err)
	}
	var rich seed.DocumentSeed
	if err := json.Unmarshal(data, &rich); err != nil {
		t.Fatalf("rich seed JSON: %v", err)
	}
	if len(rich.Provisions) != 9 {
		t.Fatalf("got %d provisions, want 9 (8 parsed + 1 hydrated)", len(rich.Provisions))
	}
	last := rich.Provisions[len(rich.Provisions)-1]
	if last.ProvisionRef != "s26b" || !strings.Contains(last.Content, "Hidratált kiegészítő bekezdés.") {
		t.Errorf("hydrated provision = %+v", last)
	}
	if rich.Title == "T" {
		t.Errorf("official title not extracted: %q", rich.Title)
	}
	if len(rich.Definitions) != 2 {
		t.Errorf("got %d definitions, want 2", len(rich.Definitions))
	}

	// Source HTML cached under the njt document id.
	if _, err := os.Stat(filepath.Join(sourceDir, "2011-112-00-00.html")); err != nil {
		t.Errorf("source cache: %v", err)
	}

	// Seed for the marker-less page: metadata-only (keeps the discovered
	// description, as in the TS pipeline).
	data, err = os.ReadFile(filepath.Join(seedDir, "hu-law-1992-100-00-00.json"))
	if err != nil {
		t.Fatalf("metadata-only seed: %v", err)
	}
	var meta seed.DocumentSeed
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("metadata-only seed JSON: %v", err)
	}
	if len(meta.Provisions) != 0 || meta.Description != discoveredDescription {
		t.Errorf("metadata-only doc = %+v", meta)
	}

	// Resume skips both acts without any HTTP traffic.
	p2 := newFastPipeline(sourceDir, seedDir)
	p2.BaseURL = "http://127.0.0.1:1" // nothing listens here
	var out strings.Builder
	p2.Stdout = &out
	if err := p2.FetchAndParseActs(t.Context(), acts, false, true); err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if !strings.Contains(out.String(), "cached") {
		t.Errorf("resume output should mark cached rows:\n%s", out.String())
	}

	// --skip-fetch reads the cached HTML and re-parses offline.
	p3 := newFastPipeline(sourceDir, seedDir)
	p3.BaseURL = "http://127.0.0.1:1"
	out.Reset()
	p3.Stdout = &out
	if err := p3.FetchAndParseActs(t.Context(), acts[:1], true, false); err != nil {
		t.Fatalf("skip-fetch run: %v", err)
	}
	if !strings.Contains(out.String(), "Using cached HTML") {
		t.Errorf("skip-fetch output should note cached HTML:\n%s", out.String())
	}
}

func TestFetchAndParseActsHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer ts.Close()

	p := newFastPipeline(t.TempDir(), t.TempDir())
	p.BaseURL = ts.URL
	acts := []ActIndexEntry{{ID: "act-x", Title: "X", Status: "in_force", URL: ts.URL + "/jogszabaly/2000-1-00-00"}}
	err := p.FetchAndParseActs(t.Context(), acts, false, false)
	if err == nil {
		t.Fatal("expected a summary error when an act fails")
	}
	if !strings.Contains(err.Error(), "1 of 1 acts failed") {
		t.Errorf("err = %v, want a failed-acts summary", err)
	}
	// A non-200 act page writes no seed file.
	entries, err := os.ReadDir(p.SeedDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no seed files, got %v", entries)
	}
}

func TestResolveURLOffOriginRejected(t *testing.T) {
	p := newFastPipeline(t.TempDir(), t.TempDir())
	p.BaseURL = "https://njt.hu"

	if got := p.resolveURL("https://njt.hu/jogszabaly/2011-112-00-00"); got != "https://njt.hu/jogszabaly/2011-112-00-00" {
		t.Errorf("on-origin URL rewritten: %q", got)
	}
	if got := p.resolveURL("http://evil.example.com/jogszabaly/2011-112-00-00"); got != "" {
		t.Errorf("off-origin URL = %q, want rejection", got)
	}
	if got := p.resolveURL("notaurl"); got != "" {
		t.Errorf("unparseable URL = %q, want rejection", got)
	}
}

func TestFetchAndParseActsResumeCorruptSeedRefetches(t *testing.T) {
	ts, _ := newFakeNjtServer(t)
	sourceDir := filepath.Join(t.TempDir(), "source")
	seedDir := filepath.Join(t.TempDir(), "seed")

	// A corrupt cached seed must not abort the --resume run; the act is
	// re-fetched and its seed rewritten.
	seedPath := filepath.Join(seedDir, "hu-law-1992-100-00-00.json")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seedPath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := newFastPipeline(sourceDir, seedDir)
	p.BaseURL = ts.URL
	acts := []ActIndexEntry{{ID: "hu-law-1992-100-00-00", Title: "1992. évi C. törvény", Status: "repealed", URL: ts.URL + "/jogszabaly/1992-100-00-00"}}
	if err := p.FetchAndParseActs(t.Context(), acts, false, true); err != nil {
		t.Fatalf("FetchAndParseActs: %v", err)
	}

	data, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("seed not rewritten: %v", err)
	}
	var doc seed.DocumentSeed
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("seed still unreadable: %v", err)
	}
	if doc.ID != "hu-law-1992-100-00-00" || len(doc.Provisions) != 0 {
		t.Errorf("rewritten seed = %+v", doc)
	}
}

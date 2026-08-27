package ingest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractNjtDocumentID(t *testing.T) {
	tests := []struct{ url, want string }{
		{"https://njt.hu/jogszabaly/2011-112-00-00", "2011-112-00-00"},
		{"https://njt.hu/jogszabaly/2011-112-00-00?alaptermekek=true", "2011-112-00-00"},
		{"https://njt.hu/jogszabaly/2011-112-00-00#fejezet", "2011-112-00-00"},
		{"https://example.com/other/2011-112-00-00", ""},
		{"notaurl", ""},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := ExtractNjtDocumentID(tt.url); got != tt.want {
				t.Errorf("ExtractNjtDocumentID(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestExtractTotalPages(t *testing.T) {
	tests := []struct {
		name string
		html string
		want int
	}{
		{"spaced", `<span id="page-count"> / 7 </span>`, 7},
		{"tight", `<span id="page-count">/1</span>`, 1},
		{"case-insensitive id", `<span id="Page-Count"> / 3 </span>`, 3},
		{"no counter", `no counter`, 1},
		{"zero pages", `<span id="page-count"> / 0 </span>`, 1},
		{"clamped to max", `<span id="page-count"> / 424242 </span>`, maxDiscoveryPages},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractTotalPages(tt.html); got != tt.want {
				t.Errorf("ExtractTotalPages(%q) = %d, want %d", tt.html, got, tt.want)
			}
		})
	}
}

const searchPageHTML = `<div class="resultWrapper">
<span id="page-count"> / 1 </span>
<div class="resultItemWrapper">
  <a href="jogszabaly/2011-112-00-00" class="resultItemLink now">
    <span class="resultItem">2011. évi CXII. törvény</span>
  </a>
  <p>az információs önrendelkezési jogról</p>
  <div class="resultItem translation" title="Act CXII of 2011 on Informational Self-Determination">EN</div>
  <span class="resultDate" title="Hatálybalépés">2012. 01. 01.</span>
</div>
<div class="resultItemWrapper">
  <a href="jogszabaly/1992-100-00-00" class="resultItemLink past">
    <span class="resultItem">1992. évi C. törvény</span>
  </a>
  <span class="resultDate">1992. 06. 24.</span>
</div>
<div class="resultItemWrapper">
  <a href="jogszabaly/2020-10-00-00" class="resultItemLink future">
    <span class="resultItem">2020. évi X. törvény</span>
  </a>
</div>
</div>`

func TestParseSearchResultPage(t *testing.T) {
	laws := ParseSearchResultPage(searchPageHTML, "https://njt.hu")
	if len(laws) != 3 {
		t.Fatalf("got %d laws, want 3: %+v", len(laws), laws)
	}

	first := laws[0]
	if first.DocumentID != "2011-112-00-00" {
		t.Errorf("doc id = %q", first.DocumentID)
	}
	if first.Title != "2011. évi CXII. törvény az információs önrendelkezési jogról" {
		t.Errorf("title = %q", first.Title)
	}
	if first.TitleEn != "Act CXII of 2011 on Informational Self-Determination" {
		t.Errorf("titleEn = %q", first.TitleEn)
	}
	if first.Status != "in_force" {
		t.Errorf("status = %q", first.Status)
	}
	if first.InForceDate != "2012-01-01" {
		t.Errorf("inForceDate = %q", first.InForceDate)
	}
	if first.URL != "https://njt.hu/jogszabaly/2011-112-00-00" {
		t.Errorf("url = %q", first.URL)
	}

	if laws[1].Status != "repealed" || laws[1].InForceDate != "1992-06-24" {
		t.Errorf("second law = %+v", laws[1])
	}
	if laws[2].Status != "not_yet_in_force" || laws[2].InForceDate != "" {
		t.Errorf("third law = %+v", laws[2])
	}
}

func TestFetchSearchPathForLaws(t *testing.T) {
	var gotPath, gotPayload, gotContentType, gotAccept string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		buf := make([]byte, r.ContentLength)
		if _, err := io.ReadFull(r.Body, buf); err != nil {
			t.Errorf("read search-url body: %v", err)
		}
		gotPayload = string(buf)
		gotContentType = r.Header.Get("Content-Type")
		gotAccept = r.Header.Get("Accept")
		w.Write([]byte(`{"success":true,"url":"abc/def"}`))
	}))
	defer ts.Close()

	p := newFastPipeline(t.TempDir(), t.TempDir())
	p.BaseURL = ts.URL

	path, err := p.fetchSearchPathForLaws(t.Context(), true)
	if err != nil {
		t.Fatalf("fetchSearchPathForLaws: %v", err)
	}
	if path != "abc/def" {
		t.Errorf("path = %q", path)
	}
	if gotPath != "/ajax/get_search_url.json" {
		t.Errorf("request path = %q", gotPath)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(gotPayload), &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if payload["author_type"] != "0000" || payload["evszam"] != "" || payload["sorszam"] != "" ||
		payload["szokereso"] != "" || payload["csak_hatalyos"] != true ||
		payload["pontos_szora"] != false || payload["csak_cimben"] != false ||
		payload["targyszo"] != false || payload["gazette_state"] != false {
		t.Errorf("payload = %v", payload)
	}
	if gotContentType != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	if gotAccept != "text/html,application/json,*/*" {
		t.Errorf("Accept = %q", gotAccept)
	}
}

func TestFetchSearchPathForLawsErrors(t *testing.T) {
	t.Run("http error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
		}))
		defer ts.Close()
		p := newFastPipeline(t.TempDir(), t.TempDir())
		p.BaseURL = ts.URL
		if _, err := p.fetchSearchPathForLaws(t.Context(), false); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("not json", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("<html>nope</html>"))
		}))
		defer ts.Close()
		p := newFastPipeline(t.TempDir(), t.TempDir())
		p.BaseURL = ts.URL
		if _, err := p.fetchSearchPathForLaws(t.Context(), false); err == nil || !strings.Contains(err.Error(), "not JSON") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no valid path", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"success":false}`))
		}))
		defer ts.Close()
		p := newFastPipeline(t.TempDir(), t.TempDir())
		p.BaseURL = ts.URL
		if _, err := p.fetchSearchPathForLaws(t.Context(), false); err == nil || !strings.Contains(err.Error(), "valid path") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("unsafe search path", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"success":true,"url":"../escape?x=1"}`))
		}))
		defer ts.Close()
		p := newFastPipeline(t.TempDir(), t.TempDir())
		p.BaseURL = ts.URL
		if _, err := p.discoverLaws(t.Context(), false); err == nil || !strings.Contains(err.Error(), "unexpected characters") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestDiscoverLawsPaginationAndCache(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ajax/get_search_url.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"url":"tok/en"}`))
	})
	page2 := strings.Replace(searchPageHTML, " / 1 </span>", " / 2 </span>", 1)
	// Page 2 repeats one document (dedupe) and adds another.
	page2 = strings.Replace(page2, "1992-100-00-00", "1999-100-00-00", 1)
	mux.HandleFunc("/search/tok/en/1/50", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(page2))
	})
	mux.HandleFunc("/search/tok/en/2/50", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(page2))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	sourceDir := t.TempDir()
	p := newFastPipeline(sourceDir, t.TempDir())
	p.BaseURL = ts.URL

	laws, err := p.discoverLaws(t.Context(), false)
	if err != nil {
		t.Fatalf("discoverLaws: %v", err)
	}
	// Page 1 and page 2 serve the same body; the doc id rewritten on
	// "page 2" is deduped to three unique laws.
	if len(laws) != 3 {
		t.Fatalf("got %d laws, want 3: %+v", len(laws), laws)
	}
	for i := 1; i < len(laws); i++ {
		if laws[i-1].DocumentID >= laws[i].DocumentID {
			t.Errorf("laws not sorted: %q >= %q", laws[i-1].DocumentID, laws[i].DocumentID)
		}
	}

	cachePath := filepath.Join(sourceDir, "law-discovery-all.json")
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	var cached DiscoverySeed
	if err := json.Unmarshal(data, &cached); err != nil {
		t.Fatalf("cache not JSON: %v", err)
	}
	if cached.InForceOnly || cached.PageSize != DiscoveryPageSize || len(cached.Laws) != 3 {
		t.Errorf("cache = %+v", cached)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(data)), "}") || !strings.Contains(string(data), "\n  \"laws\"") {
		t.Errorf("cache not indented JSON: %q", data[:60])
	}

	// Same-mode cache is accepted; other modes and page sizes are rejected.
	if got := p.readDiscoveryCache(false); len(got) != 3 {
		t.Errorf("readDiscoveryCache(false) = %d laws, want 3", len(got))
	}
	if got := p.readDiscoveryCache(true); got != nil {
		t.Errorf("readDiscoveryCache(true) should reject mismatched mode, got %d", len(got))
	}
	broken := []byte(`{"inForceOnly":false,"pageSize":10,"laws":[]}`)
	if err := os.WriteFile(cachePath, broken, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := p.readDiscoveryCache(false); got != nil {
		t.Errorf("mismatched pageSize cache should be rejected, got %d", len(got))
	}
	if err := os.WriteFile(cachePath, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := p.readDiscoveryCache(false); got != nil {
		t.Errorf("corrupt cache should be rejected, got %d", len(got))
	}
}

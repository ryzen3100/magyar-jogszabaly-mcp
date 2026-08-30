package ingest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingServer serves the given statuses in order (the last repeats) and
// records request arrival times and headers.
type recordingServer struct {
	mu       sync.Mutex
	statuses []int
	times    []time.Time
	ua       []string
	accept   []string
	bodies   []string
}

func (s *recordingServer) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	status := s.statuses[len(s.statuses)-1]
	if len(s.times) < len(s.statuses) {
		status = s.statuses[len(s.times)]
	}
	body := make([]byte, r.ContentLength)
	if r.ContentLength > 0 {
		if _, err := io.ReadFull(r.Body, body); err != nil {
			s.mu.Unlock()
			http.Error(w, "read request body", http.StatusInternalServerError)
			return
		}
	}
	s.times = append(s.times, time.Now())
	s.ua = append(s.ua, r.Header.Get("User-Agent"))
	s.accept = append(s.accept, r.Header.Get("Accept"))
	s.bodies = append(s.bodies, string(body))
	s.mu.Unlock()
	w.WriteHeader(status)
	w.Write([]byte("ok-body"))
}

func newTestFetcher(minDelay time.Duration) *Fetcher {
	f := NewFetcher()
	f.MinDelay = minDelay
	return f
}

// newFastPipeline returns a pipeline whose fetcher has no rate-limit delay
// and no backoff sleeping, for tests that make many requests.
func newFastPipeline(sourceDir, seedDir string) *Pipeline {
	p := NewPipeline(sourceDir, seedDir)
	p.Fetcher.MinDelay = 0
	p.Fetcher.Sleep = func(time.Duration) {}
	p.Stdout = io.Discard
	return p
}

func TestNewFetcherDefaults(t *testing.T) {
	f := NewFetcher()
	if f.MinDelay != 1200*time.Millisecond {
		t.Errorf("MinDelay = %v, want 1200ms", f.MinDelay)
	}
	if f.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", f.MaxRetries)
	}
	if UserAgent != "Hungarian-Law-MCP/1.0 (+https://github.com/Ansvar-Systems/Hungarian-law-mcp; hello@ansvar.eu)" {
		t.Errorf("UserAgent = %q", UserAgent)
	}
	if DefaultAccept != "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8" {
		t.Errorf("DefaultAccept = %q", DefaultAccept)
	}
}

func TestFetchSendsDefaultHeadersAndBody(t *testing.T) {
	srv := &recordingServer{statuses: []int{200}}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	f := newTestFetcher(0)
	res, err := f.Fetch(t.Context(), ts.URL+"/x", &RequestOptions{
		Method: http.MethodPost,
		Body:   `{"a":1}`,
		Headers: map[string]string{
			"Content-Type": "application/json; charset=utf-8",
		},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Status != 200 || res.Body != "ok-body" {
		t.Errorf("result = %+v", res)
	}
	if srv.ua[0] != UserAgent {
		t.Errorf("User-Agent = %q", srv.ua[0])
	}
	if srv.accept[0] != DefaultAccept {
		t.Errorf("Accept = %q", srv.accept[0])
	}
	if srv.bodies[0] != `{"a":1}` {
		t.Errorf("body = %q", srv.bodies[0])
	}
}

func TestFetchHeaderOverride(t *testing.T) {
	srv := &recordingServer{statuses: []int{200}}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	f := newTestFetcher(0)
	if _, err := f.Fetch(t.Context(), ts.URL, &RequestOptions{
		Headers: map[string]string{"Accept": "application/json"},
	}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if srv.accept[0] != "application/json" {
		t.Errorf("Accept override not applied: %q", srv.accept[0])
	}
}

func TestFetchRateLimitsConsecutiveRequests(t *testing.T) {
	srv := &recordingServer{statuses: []int{200}}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	minDelay := 100 * time.Millisecond
	var sleeps []time.Duration
	f := newTestFetcher(minDelay)
	// Assert on the injected Sleep hook instead of wall-clock gaps between
	// server-side timestamps: real sleeps flake under -race scheduling.
	f.Sleep = func(d time.Duration) { sleeps = append(sleeps, d) }
	for i := range 3 {
		if _, err := f.Fetch(t.Context(), ts.URL, nil); err != nil {
			t.Fatalf("Fetch %d: %v", i, err)
		}
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.times) != 3 {
		t.Fatalf("got %d requests, want 3", len(srv.times))
	}
	// Request 1 seeds lastRequestTime without waiting; requests 2 and 3 must
	// each have waited the configured minimum (minus elapsed time).
	if len(sleeps) != 2 {
		t.Fatalf("got %d rate-limit sleeps, want 2 (before requests 2 and 3)", len(sleeps))
	}
	for i, d := range sleeps {
		if d <= 0 || d > minDelay {
			t.Errorf("rate-limit sleep %d = %v, want in (0, %v]", i+1, d, minDelay)
		}
	}
}

func TestFetchRetriesOn500And429(t *testing.T) {
	tests := []struct {
		name         string
		statuses     []int
		wantStatus   int
		wantRequests int
		wantBackoffs []time.Duration
	}{
		{"500 twice then ok", []int{500, 500, 200}, 200, 3, []time.Duration{2 * time.Second, 4 * time.Second}},
		{"429 once then ok", []int{429, 200}, 200, 2, []time.Duration{2 * time.Second}},
		{"5xx exhaustion returns last status", []int{500, 503, 500, 500}, 500, 4, []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &recordingServer{statuses: tt.statuses}
			ts := httptest.NewServer(http.HandlerFunc(srv.handler))
			defer ts.Close()

			var backoffs []time.Duration
			f := newTestFetcher(0)
			f.Sleep = func(d time.Duration) { backoffs = append(backoffs, d) }

			res, err := f.Fetch(t.Context(), ts.URL, nil)
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if res.Status != tt.wantStatus {
				t.Errorf("status = %d, want %d", res.Status, tt.wantStatus)
			}
			srv.mu.Lock()
			requests := len(srv.times)
			srv.mu.Unlock()
			if requests != tt.wantRequests {
				t.Errorf("requests = %d, want %d", requests, tt.wantRequests)
			}
			if len(backoffs) != len(tt.wantBackoffs) {
				t.Fatalf("backoffs = %v, want %v", backoffs, tt.wantBackoffs)
			}
			for i := range backoffs {
				if backoffs[i] != tt.wantBackoffs[i] {
					t.Errorf("backoff %d = %v, want %v", i, backoffs[i], tt.wantBackoffs[i])
				}
			}
		})
	}
}

func TestFetchRetriesOnNetworkErrorThenFails(t *testing.T) {
	calls := 0
	f := newTestFetcher(0)
	f.Client = &http.Client{Transport: failingTransport{&calls}}
	var backoffs []time.Duration
	f.Sleep = func(d time.Duration) { backoffs = append(backoffs, d) }
	f.Logf = func(string, ...any) {}

	_, err := f.Fetch(t.Context(), "http://unit.test/x", nil)
	if err == nil {
		t.Fatal("expected error after exhausted network retries")
	}
	if calls != 4 {
		t.Errorf("calls = %d, want 4 (initial + 3 retries)", calls)
	}
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}
	for i := range want {
		if backoffs[i] != want[i] {
			t.Errorf("backoff %d = %v, want %v", i, backoffs[i], want[i])
		}
	}
}

type failingTransport struct{ calls *int }

func (t failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	*t.calls++
	return nil, errors.New("boom")
}

func TestFetchDoesNotRetryCanceledContext(t *testing.T) {
	calls := 0
	f := newTestFetcher(0)
	f.Client = &http.Client{Transport: canceledTransport{&calls}}
	f.Logf = func(string, ...any) {}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Fetch(ctx, "http://unit.test/x", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry on a canceled context)", calls)
	}
}

type canceledTransport struct{ calls *int }

func (t canceledTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	*t.calls++
	return nil, r.Context().Err()
}

func TestFetchHonors429RetryAfter(t *testing.T) {
	tests := []struct {
		name       string
		retryAfter string
		want       time.Duration
	}{
		{"shorter than backoff", "1", 1 * time.Second},
		{"capped at normal backoff", "99", 2 * time.Second},
		{"unparseable", "soon", 2 * time.Second},
		{"absent", "", 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := true
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if first {
					first = false
					if tt.retryAfter != "" {
						w.Header().Set("Retry-After", tt.retryAfter)
					}
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				w.Write([]byte("ok-body"))
			}))
			defer ts.Close()

			var backoffs []time.Duration
			f := newTestFetcher(0)
			f.Sleep = func(d time.Duration) { backoffs = append(backoffs, d) }
			f.Logf = func(string, ...any) {}

			res, err := f.Fetch(t.Context(), ts.URL, nil)
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if res.Status != 200 {
				t.Errorf("status = %d, want 200", res.Status)
			}
			if len(backoffs) != 1 || backoffs[0] != tt.want {
				t.Errorf("backoffs = %v, want [%v]", backoffs, tt.want)
			}
		})
	}
}

func TestFetchRejectsOversizedBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, maxBodyBytes+1))
	}))
	defer ts.Close()

	f := newTestFetcher(0)
	f.Logf = func(string, ...any) {}
	if _, err := f.Fetch(t.Context(), ts.URL, nil); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err = %v, want a body-size limit error", err)
	}
}

func TestBackoffFor(t *testing.T) {
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	for attempt, w := range want {
		if got := backoffFor(attempt); got != w {
			t.Errorf("backoffFor(%d) = %v, want %v", attempt, got, w)
		}
	}
}

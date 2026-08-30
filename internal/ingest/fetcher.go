// Package ingest ports scripts/ingest.ts and scripts/lib/{fetcher,parser}.ts:
// it fetches Hungarian legislation from the official Nemzeti Jogszabalytar
// portal (njt.hu), parses section-level provisions, and writes seed JSON
// files compatible with the database builder.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// UserAgent identifies this MCP to njt.hu.
	UserAgent = "Hungarian-Law-MCP/1.0 (+https://github.com/Ansvar-Systems/Hungarian-law-mcp; hello@ansvar.eu)"
	// DefaultAccept is the Accept header sent unless overridden per request.
	DefaultAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	// DefaultMinDelay is the minimum delay between any two requests
	// (government-friendly 1-2s window).
	DefaultMinDelay = 1200 * time.Millisecond
	// DefaultMaxRetries is the number of retries after the initial attempt
	// on network errors and 429/5xx responses.
	DefaultMaxRetries = 3
)

// FetchResult is a completed HTTP response.
type FetchResult struct {
	Status int
	Body   string
}

// RequestOptions carries the per-request init of requestWithRateLimit.
type RequestOptions struct {
	Method  string // default GET
	Body    string
	Headers map[string]string // merged over the defaults; per-request values win
}

// Fetcher is a rate-limited HTTP client for njt.hu: a global minimum delay
// between any two requests, plus retries with exponential backoff on network
// errors and 429/5xx responses. Port of scripts/lib/fetcher.ts.
//
// The zero value performs no rate limiting and no retries; use NewFetcher.
type Fetcher struct {
	Client     *http.Client // nil: default client (follows redirects)
	MinDelay   time.Duration
	MaxRetries int
	// Sleep backs both the rate-limit wait and retry backoff; tests replace
	// it to observe durations without waiting.
	Sleep func(time.Duration)
	// Logf receives retry progress messages. The fmt.Printf default is
	// CLI-only: NewPipeline rewires it to the pipeline's Stdout sink so
	// injected test harnesses capture retry lines too.
	Logf func(format string, args ...any)

	mu              sync.Mutex
	lastRequestTime time.Time
}

// NewFetcher returns a Fetcher with the production njt.hu settings.
func NewFetcher() *Fetcher {
	return &Fetcher{MinDelay: DefaultMinDelay, MaxRetries: DefaultMaxRetries}
}

// defaultHTTPClient stands in for http.DefaultClient (which has no Timeout)
// so a stalled njt.hu connection cannot hang ingestion forever.
var defaultHTTPClient = &http.Client{Timeout: 60 * time.Second}

func (f *Fetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return defaultHTTPClient
}

func (f *Fetcher) sleep(d time.Duration) {
	if f.Sleep != nil {
		f.Sleep(d)
		return
	}
	time.Sleep(d)
}

// sleepCtx is the retry-backoff variant of sleep: without the test hook it
// returns early when ctx is done instead of sleeping through a cancellation.
func (f *Fetcher) sleepCtx(ctx context.Context, d time.Duration) {
	if f.Sleep != nil {
		f.Sleep(d)
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func (f *Fetcher) logf(format string, args ...any) {
	if f.Logf != nil {
		f.Logf(format, args...)
		return
	}
	fmt.Printf(format, args...)
}

// rateLimit enforces the global minimum delay between any two requests.
// The lock is held while sleeping so concurrent callers stay serialized.
func (f *Fetcher) rateLimit() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if wait := f.MinDelay - time.Since(f.lastRequestTime); wait > 0 {
		f.sleep(wait)
	}
	f.lastRequestTime = time.Now()
}

// backoffFor is 2^(attempt+1) seconds: 2s, 4s, 8s. The shift is clamped so
// an absurd attempt count cannot overflow.
func backoffFor(attempt int) time.Duration {
	return time.Duration(1<<min(attempt+1, 20)) * time.Second
}

// retryAfter parses a numeric Retry-After header (njt.hu sends seconds);
// the HTTP-date form and unparseable values return 0.
func retryAfter(h string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// maxBodyBytes caps how much of a response body is read into memory.
// ponytail: 20MB is ~20x the largest real njt.hu act page; a bigger body is
// a broken/hostile origin, so it errors instead of OOMing the pipeline.
// Upgrade path: stream to disk if a legitimate page ever outgrows this.
const maxBodyBytes = 20 << 20

// Fetch fetches a URL with rate limiting and retries on transient failures.
// The User-Agent is applied unless overridden via opt.Headers.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string, opt *RequestOptions) (FetchResult, error) {
	if opt == nil {
		opt = &RequestOptions{}
	}
	method := opt.Method
	if method == "" {
		method = http.MethodGet
	}
	header := http.Header{}
	header.Set("User-Agent", UserAgent)
	header.Set("Accept", DefaultAccept)
	for k, v := range opt.Headers {
		header.Set(k, v)
	}

	f.rateLimit()

	// No loop condition: the final attempt always returns or errors below.
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, rawURL, strings.NewReader(opt.Body))
		if err != nil {
			return FetchResult{}, err
		}
		req.Header = header.Clone()

		resp, err := f.client().Do(req)
		if err != nil {
			// A dead context never recovers; retrying would just burn
			// the backoff schedule.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return FetchResult{}, err
			}
			if attempt < f.MaxRetries {
				backoff := backoffFor(attempt)
				f.logf("  Network error for %s: %v. Retrying in %dms...\n", rawURL, err, backoff.Milliseconds())
				f.sleepCtx(ctx, backoff)
				continue
			}
			return FetchResult{}, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
		resp.Body.Close()
		if readErr != nil {
			return FetchResult{}, fmt.Errorf("read response body from %s: %w", rawURL, readErr)
		}
		if len(body) > maxBodyBytes {
			return FetchResult{}, fmt.Errorf("response body from %s exceeds the %d MB limit", rawURL, maxBodyBytes>>20)
		}

		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempt < f.MaxRetries {
			backoff := backoffFor(attempt)
			if resp.StatusCode == http.StatusTooManyRequests {
				if ra := retryAfter(resp.Header.Get("Retry-After")); ra > 0 {
					backoff = min(backoff, ra)
				}
			}
			f.logf("  HTTP %d for %s, retrying in %dms...\n", resp.StatusCode, rawURL, backoff.Milliseconds())
			f.sleepCtx(ctx, backoff)
			continue
		}
		return FetchResult{Status: resp.StatusCode, Body: string(body)}, nil
	}
}

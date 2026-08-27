package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store/storetest"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/tools"
)

// newTestHandler builds the route table with a nil DB — safe because tool
// handlers only touch the DB lazily, and these tests never invoke one.
func newTestHandler() http.Handler {
	return newHTTPHandler(nil, &tools.AboutContext{Version: "test"}, time.Now(), nil)
}

func TestMCPUnsupportedMethod405(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/mcp", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PATCH /mcp: got %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin: got %q, want *", got)
	}
}

func TestMaxBodyBytes413(t *testing.T) {
	t.Parallel()
	body := bytes.Repeat([]byte("x"), maxBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	// JSON content type + both Accept media types — otherwise the SDK
	// rejects the request (415/400) before ever reading the body.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized POST /mcp: got %d, want 413", rec.Code)
	}
}

func TestSessionTrackerCapAndPrune(t *testing.T) {
	t.Parallel()
	tr := newSessionTracker()
	for i := 0; i < maxSessions; i++ {
		tr.touch(fmt.Sprintf("s%d", i))
	}
	if tr.admit() {
		t.Fatal("admit returned true at the session cap")
	}
	// Age every entry past the idle TTL — the prune in admit must free
	// capacity again.
	for id, seen := range tr.lastSeen {
		tr.lastSeen[id] = seen.Add(-sessionIdleTTL - time.Second)
	}
	if !tr.admit() {
		t.Fatal("admit returned false although all sessions are idle past the TTL")
	}
}

func TestRenderPromptUnknownName(t *testing.T) {
	t.Parallel()
	_, err := renderPrompt(context.Background(), &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{Name: "nope"},
	})
	if err == nil || err.Error() != "unknown prompt: nope" {
		t.Fatalf("err = %v, want %q", err, "unknown prompt: nope")
	}
}

func TestStatusWriterFlushAndUnwrap(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec}
	sw.Flush()
	if !rec.Flushed {
		t.Fatal("statusWriter.Flush did not reach the underlying flusher")
	}
	if sw.Unwrap() != http.ResponseWriter(rec) {
		t.Fatal("Unwrap did not return the wrapped writer")
	}
}

// A bare Write (no WriteHeader) must record the implicit 200 the access log
// reports — net/http semantics.
func TestStatusWriterImplicitStatus(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec}
	if _, err := sw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if sw.status != http.StatusOK {
		t.Fatalf("status = %d after a bare Write, want implicit 200", sw.status)
	}
}

// --- CORS / OPTIONS preflight ------------------------------------------------

func TestOptionsPreflightOnAnyPath(t *testing.T) {
	t.Parallel()
	h := newTestHandler()
	for _, path := range []string{"/mcp", "/health", "/icon.png", "/anything"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, path, nil))
		if rec.Code != http.StatusNoContent {
			t.Errorf("OPTIONS %s: got %d, want 204", path, rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("OPTIONS %s: Access-Control-Allow-Origin = %q, want *", path, got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, DELETE, OPTIONS" {
			t.Errorf("OPTIONS %s: Access-Control-Allow-Methods = %q", path, got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "mcp-session-id") {
			t.Errorf("OPTIONS %s: Access-Control-Allow-Headers = %q, want mcp-session-id", path, got)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("OPTIONS %s: preflight must have no body, got %q", path, rec.Body.String())
		}
	}

	// CORS headers ride on error responses too.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/no-such-route", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /no-such-route: got %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("404: Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "mcp-session-id" {
		t.Errorf("404: Access-Control-Expose-Headers = %q, want mcp-session-id", got)
	}
}

// --- /health -----------------------------------------------------------------

func mustDropTable(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	if _, err := db.Exec("DROP TABLE " + table); err != nil {
		t.Fatalf("drop %s: %v", table, err)
	}
}

func TestHealthOk(t *testing.T) {
	t.Parallel()
	h := newHTTPHandler(storetest.NewTestDb(t), &tools.AboutContext{Version: "test"}, time.Now(), nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health: got %d, want 200", rec.Code)
	}
	var payload healthPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("payload %q: %v", rec.Body.String(), err)
	}
	if payload.Status != "ok" || payload.Server != serverName || payload.Version != serverVersion {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Uptime < 0 {
		t.Fatalf("uptime = %d, want >= 0", payload.Uptime)
	}
}

// A failed probe is cached for healthFailureTTL (15s): repairing the database
// inside the window must not flip /health back to ok — unauthenticated /health
// hits must not amplify into COUNT scans (S6).
func TestHealthDegradedFailureCached(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	mustDropTable(t, db, "provisions_fts")
	h := newHTTPHandler(db, &tools.AboutContext{Version: "test"}, time.Now(), nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("broken db: got %d, want 503", rec.Code)
	}
	var payload healthPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("payload %q: %v", rec.Body.String(), err)
	}
	if payload.Status != "degraded" {
		t.Fatalf("status = %q, want degraded", payload.Status)
	}

	// Repair the db — but the failure window is still open, so the cached
	// failure must keep /health degraded (no re-probe).
	if _, err := db.Exec(`CREATE TABLE provisions_fts (content TEXT, title TEXT)`); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("repaired db within failure TTL: got %d, want cached 503", rec.Code)
	}
}

// The first fully successful probe is cached for the process lifetime: later
// damage to the database must not flip /health to degraded.
func TestHealthSuccessCached(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	h := newHTTPHandler(db, &tools.AboutContext{Version: "test"}, time.Now(), nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("seeded db: got %d, want 200", rec.Code)
	}

	mustDropTable(t, db, "provisions_fts")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("broken db after cached success: got %d, want cached 200", rec.Code)
	}
}

// --- session cap and header validation at the /mcp route ---------------------

// initializeBody is a minimal well-formed initialize request the SDK
// transport accepts — sessions are only minted for real initializes.
const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize",` +
	`"params":{"protocolVersion":"2025-06-18","capabilities":{},` +
	`"clientInfo":{"name":"test","version":"1.0"}}}`

func newInitializeRequest() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(initializeBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	return req
}

// Filling the session cap with real initializes must reject the next
// sessionless POST with 429 + Retry-After before the SDK ever sees it (S4).
func TestSessionCapRejectsWith429AndRetryAfter(t *testing.T) {
	t.Parallel()
	h := newTestHandler()

	for i := 0; i < maxSessions; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newInitializeRequest())
		if rec.Code != http.StatusOK {
			t.Fatalf("initialize #%d: got %d, want 200", i+1, rec.Code)
		}
		if id := rec.Header().Get("mcp-session-id"); !uuidV4RE.MatchString(id) {
			t.Fatalf("initialize #%d minted session id %q, want a UUID v4", i+1, id)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newInitializeRequest())
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("POST at the session cap: got %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After = %q, want 60", got)
	}
	if got, want := rec.Body.String(), `{"error":"Too many sessions"}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

// The TS validSessionId guard: a malformed mcp-session-id is stripped from
// the request before the SDK transport sees it (the stripped POST then takes
// the sessionless path), while a well-formed one is touched and passed
// through to the SDK transport.
func TestSessionHeaderValidationFallbacks(t *testing.T) {
	t.Parallel()
	h := newTestHandler()

	// Malformed id on POST: the header is stripped before delegation, so the
	// request initializes a fresh session instead of hitting the SDK's
	// unknown-session lookup with a junk id.
	req := newInitializeRequest()
	req.Header.Set("mcp-session-id", "not-a-uuid")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := req.Header.Get("mcp-session-id"); got != "" {
		t.Fatalf("malformed mcp-session-id %q still on the request — must be stripped before delegation", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("POST with malformed session id: got %d, want 200 (stripped, sessionless initialize)", rec.Code)
	}
	if id := rec.Header().Get("mcp-session-id"); !uuidV4RE.MatchString(id) {
		t.Fatalf("POST with malformed session id minted %q, want a fresh UUID v4", id)
	}

	// Malformed id on DELETE never reaches the SDK — our own JSON 404.
	req = httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	req.Header.Set("mcp-session-id", "not-a-uuid")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE with malformed session id: got %d, want 404", rec.Code)
	}
	if got, want := rec.Body.String(), `{"error":"Session not found"}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}

	// Well-formed but unknown id → passed through; the SDK answers 404.
	req = httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	req.Header.Set("mcp-session-id", "3d8e1c2a-9f4b-4c67-8a2d-1e5f7a9b0c3d")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE with unknown UUID session: got %d, want 404", rec.Code)
	}

	// Sessionless GET answers the metadata doc, not an SSE stream.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("sessionless GET /mcp: got %d, want 200", rec.Code)
	}
	var meta mcpMetadataDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatalf("metadata payload %q: %v", rec.Body.String(), err)
	}
	if meta.Protocol != "mcp" || meta.Transport != "streamable-http" {
		t.Fatalf("metadata = %+v", meta)
	}
}

// --- renderPrompt ------------------------------------------------------------

func TestRenderPromptArguments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		prompt  string
		args    map[string]string
		want    []string // substrings of the rendered user message
		wantErr string
	}{
		{
			name:   "legal_review falls back to defaults",
			prompt: "legal_review",
			args:   nil,
			want:   []string{"Focus area: all", "Document:\n(no document provided)"},
		},
		{
			name:   "legal_review interpolates both arguments",
			prompt: "legal_review",
			args:   map[string]string{"document_text": "Szerződés szövege", "focus_area": "gdpr"},
			want:   []string{"Focus area: gdpr", "Document:\nSzerződés szövege"},
		},
		{
			name:   "legal_research falls back to its default",
			prompt: "legal_research",
			args:   nil,
			want:   []string{"Question: (no question provided)"},
		},
		{
			name:   "empty argument behaves like an absent one",
			prompt: "legal_research",
			args:   map[string]string{"question": ""},
			want:   []string{"Question: (no question provided)"},
		},
		{
			name:   "legal_research interpolates the question",
			prompt: "legal_research",
			args:   map[string]string{"question": "Milyen GDPR kötelezettségei vannak egy Kft.-nek?"},
			want:   []string{"Question: Milyen GDPR kötelezettségei vannak egy Kft.-nek?"},
		},
		{
			name:    "unknown prompt",
			prompt:  "nope",
			wantErr: "unknown prompt: nope",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res, err := renderPrompt(context.Background(), &mcp.GetPromptRequest{
				Params: &mcp.GetPromptParams{Name: tt.prompt, Arguments: tt.args},
			})
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Messages) != 1 {
				t.Fatalf("messages = %d, want 1", len(res.Messages))
			}
			msg := res.Messages[0]
			if msg.Role != mcp.Role("user") {
				t.Fatalf("role = %v, want user", msg.Role)
			}
			text, ok := msg.Content.(*mcp.TextContent)
			if !ok {
				t.Fatalf("content = %T, want *mcp.TextContent", msg.Content)
			}
			for _, want := range tt.want {
				if !strings.Contains(text.Text, want) {
					t.Errorf("rendered message missing %q:\n%s", want, text.Text)
				}
			}
		})
	}
}

// --- resource readers ----------------------------------------------------------

// TestResourceReaders drives both HTTP-mode resource readers end to end
// through an in-memory transport against the fixture database: list_sources
// enveloped, about bare.
func TestResourceReaders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := storetest.NewTestDb(t)
	about := &tools.AboutContext{Version: "test", Fingerprint: "fp", DBBuilt: "2026-02-21T00:00:00Z"}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	s := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: serverVersion}, nil)
	registerResources(s, db, about)
	if _, err := s.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("connect server: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer cs.Close()

	listed, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	uris := make(map[string]bool, len(listed.Resources))
	for _, r := range listed.Resources {
		uris[r.URI] = true
	}
	for _, want := range []string{"hungarian-law://sources", "hungarian-law://stats"} {
		if !uris[want] {
			t.Errorf("resource %q not listed, got %v", want, uris)
		}
	}

	// sources → the enveloped list_sources payload.
	sources, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "hungarian-law://sources"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources.Contents) != 1 {
		t.Fatalf("contents = %d, want 1", len(sources.Contents))
	}
	sc := sources.Contents[0]
	if sc.URI != "hungarian-law://sources" || sc.MIMEType != "application/json" {
		t.Fatalf("content meta = %q/%q", sc.URI, sc.MIMEType)
	}
	var sourcesEnv struct {
		Results struct {
			Sources  []map[string]any `json:"sources"`
			Database map[string]any   `json:"database"`
		} `json:"results"`
		Meta map[string]any `json:"_metadata"`
	}
	if err := json.Unmarshal([]byte(sc.Text), &sourcesEnv); err != nil {
		t.Fatalf("sources payload: %v\n%s", err, sc.Text)
	}
	if len(sourcesEnv.Results.Sources) != 1 || sourcesEnv.Results.Sources[0]["url"] != "https://njt.hu" {
		t.Fatalf("sources results = %v", sourcesEnv.Results.Sources)
	}
	if sourcesEnv.Meta["jurisdiction"] != "HU" {
		t.Fatalf("sources _metadata = %v", sourcesEnv.Meta)
	}

	// stats → the bare about payload, no envelope.
	stats, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "hungarian-law://stats"})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Contents) != 1 {
		t.Fatalf("contents = %d, want 1", len(stats.Contents))
	}
	stc := stats.Contents[0]
	if stc.URI != "hungarian-law://stats" {
		t.Fatalf("content uri = %q", stc.URI)
	}
	var statsDoc struct {
		Stats map[string]any `json:"stats"`
	}
	if err := json.Unmarshal([]byte(stc.Text), &statsDoc); err != nil {
		t.Fatalf("stats payload: %v\n%s", err, stc.Text)
	}
	// Fixture: 4 documents, 3 provisions.
	if statsDoc.Stats["documents"] != float64(4) || statsDoc.Stats["provisions"] != float64(3) {
		t.Fatalf("stats = %v", statsDoc.Stats)
	}
	if strings.Contains(stc.Text, `"results"`) || strings.Contains(stc.Text, `"_metadata"`) {
		t.Errorf("stats resource must be bare JSON without the envelope: %s", stc.Text)
	}

	// Unknown URI → an error, not a synthetic payload.
	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "hungarian-law://nope"}); err == nil {
		t.Error("expected error for unknown resource URI")
	}
}

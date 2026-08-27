package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/tools"
)

// newTestHandler builds the route table with a nil DB — safe because tool
// handlers only touch the DB lazily, and these tests never invoke one.
func newTestHandler() http.Handler {
	return newHTTPHandler(nil, &tools.AboutContext{Version: "test"}, time.Now(), nil)
}

func TestMCPUnsupportedMethod405(t *testing.T) {
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
	_, err := renderPrompt(context.Background(), &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{Name: "nope"},
	})
	if err == nil || err.Error() != "unknown prompt: nope" {
		t.Fatalf("err = %v, want %q", err, "unknown prompt: nope")
	}
}

func TestStatusWriterFlushAndUnwrap(t *testing.T) {
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

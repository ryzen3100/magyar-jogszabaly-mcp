// HTTP entrypoint — port of src/http-server.ts (Streamable HTTP transport for
// the Docker deployment).

package server

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/store"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/tools"
)

// Description fragments shared by the server card and the /mcp metadata
// endpoint (each phrases the middle sentence differently).
const (
	descriptionCoverage  = "Full-text search across 4,300+ Hungarian statutes and 130,000+ provisions"
	descriptionFreshness = "Database freshness is checked daily; new data is shipped with new container images."
)

// uuidV4RE is the TS UUID_RE — validates the session header before it is
// used for anything, preventing injection via mcp-session-id.
var uuidV4RE = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

const (
	// maxBodyBytes caps every request body. 1 MiB is ample for JSON-RPC
	// payloads; an oversized body fails the SDK's io.ReadAll with
	// http.MaxBytesError, which it answers with a 413 (S2).
	maxBodyBytes = 1 << 20

	// Session-cap knobs approximating the TS server's 500-session hard cap
	// with oldest-eviction (S4).
	maxSessions    = 500
	sessionIdleTTL = 30 * time.Minute
)

// Wire shapes, field order matching the TS JSON.stringify key order.

type cardServerInfo struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	DisplayName string   `json:"displayName"`
	Description string   `json:"description"`
	Homepage    string   `json:"homepage"`
	Keywords    []string `json:"keywords"`
	Author      string   `json:"author"`
	License     string   `json:"license"`
}

type cardCapabilities struct {
	Tools     bool `json:"tools"`
	Prompts   bool `json:"prompts"`
	Resources bool `json:"resources"`
}

type cardTransport struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type serverCard struct {
	ServerInfo   cardServerInfo   `json:"serverInfo"`
	Capabilities cardCapabilities `json:"capabilities"`
	Transport    cardTransport    `json:"transport"`
}

type mcpMetadataDoc struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Protocol    string `json:"protocol"`
	Transport   string `json:"transport"`
}

type healthPayload struct {
	Status  string `json:"status"`
	Server  string `json:"server"`
	Version string `json:"version"`
	Uptime  int    `json:"uptime_seconds"`
}

// RunHTTP serves the MCP server over Streamable HTTP and blocks until
// SIGINT/SIGTERM arrives. It returns nil on clean shutdown.
func RunHTTP() error {
	raw := cmp.Or(os.Getenv("PORT"), "3000")
	port, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("invalid PORT %q", raw)
	}
	// HOST defaults to loopback: the server has no in-process auth, so it
	// must not reach beyond the local machine unless the operator opts in
	// (docker-compose sets HOST=0.0.0.0 inside the container). LAN-only
	// posture.
	host := os.Getenv("HOST")
	if host == "" {
		host = "127.0.0.1"
	}

	db, path, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	logf("Database: %s", path)
	logf("Tier: %s", store.ReadDBMetadata(context.Background(), db).Tier)

	// Built once at startup, shared by every session.
	about := buildAboutContext(db, path)
	icon := loadIcon()

	handler := newHTTPHandler(
		db,
		about,
		time.Now(),
		icon,
	)
	// Explicit timeouts bound slow-client connection exhaustion (S3).
	// WriteTimeout also caps the SDK's SSE keepalive streams at 60s —
	// harmless here: the server pushes no notifications, clients simply
	// re-establish.
	srv := &http.Server{
		Addr:              net.JoinHostPort(host, strconv.Itoa(port)),
		Handler:           withAccessLog(handler),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	logf("v%s HTTP server listening on %s", serverVersion, srv.Addr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serveErr:
		return err
	case sig := <-sigCh:
		name := "SIGTERM"
		if sig == os.Interrupt {
			name = "SIGINT"
		}
		logf("Shutting down (%s)...", name)
		// Drain for at most 4s so the deferred db.Close() above actually
		// runs; the TS server's os.Exit(1) watchdog skipped cleanup. A
		// timed-out drain is logged, not fatal — the process exits either
		// way, which is what supervisors act on (K6).
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logf("Shutdown timed out: %v", err)
		}
		return nil
	}
}

// loadIcon reads icon.png once at startup (exe-dir/../icon.png first, then
// exe-dir/../../icon.png — the TS dist and repo-root layouts). Failure is
// tolerated: /icon.png serves 404.
func loadIcon() []byte {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	dir := filepath.Dir(exe)
	for _, cand := range []string{
		filepath.Join(dir, "..", "icon.png"),
		filepath.Join(dir, "..", "..", "icon.png"),
	} {
		if b, err := os.ReadFile(cand); err == nil {
			return b
		}
	}
	return nil
}

// sessionServer builds the MCP server — port of createMCPServer in
// src/http-server.ts. HTTP mode advertises tools+prompts+resources (stdio is
// tools-only). The transport keys session state itself, so one instance is
// shared by every session (K6).
func sessionServer(db *sql.DB, about *tools.AboutContext, sessions *sessionTracker) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: serverVersion}, &mcp.ServerOptions{
		// SDK diagnostics (keepalive failures, dropped notifications) go
		// to the shared stderr logger instead of being discarded.
		Logger: logger,
		// TS generated session IDs with crypto.randomUUID(); the SDK's
		// default generator uses a different shape that would fail the
		// UUID validation in handleMCP, breaking session termination.
		// Minted ids are registered with the session tracker so the S4
		// cap counts sessions even before their first follow-up request.
		GetSessionID: func() string {
			id := newSessionID()
			sessions.touch(id)
			return id
		},
		// Explicit empty structs reproduce the TS
		// capabilities: { tools: {}, prompts: {}, resources: {} } —
		// without them the SDK infers listChanged:true for each.
		Capabilities: &mcp.ServerCapabilities{
			Tools:     &mcp.ToolCapabilities{},
			Prompts:   &mcp.PromptCapabilities{},
			Resources: &mcp.ResourceCapabilities{},
		},
	})
	tools.Register(s, db, about)
	registerPrompts(s)
	registerResources(s, db, about)
	return s
}

// newSessionID returns a random UUID v4, the same shape the TS server
// produced with crypto.randomUUID().
func newSessionID() string {
	var b [16]byte
	rand.Read(b[:]) // never fails per the crypto/rand contract (Go >= 1.24)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// sessionTracker approximates the TS server's 500-session hard cap (S4).
// ponytail: this is a looser cap than the TS oldest-eviction one — new
// sessions get a 429 while full instead of evicting, admission is checked
// just before the SDK mints the session (a concurrent burst can overshoot
// the cap slightly), and only minted sessions or requests bearing a valid
// mcp-session-id are counted. Upgrade path: reject inside the GetSessionID
// hook if the SDK ever allows it to fail.
type sessionTracker struct {
	mu       sync.Mutex
	lastSeen map[string]time.Time
}

func newSessionTracker() *sessionTracker {
	return &sessionTracker{lastSeen: make(map[string]time.Time)}
}

// touch records the id as seen now — both for freshly minted sessions and
// for follow-up requests bearing a valid mcp-session-id.
func (t *sessionTracker) touch(id string) {
	t.mu.Lock()
	t.lastSeen[id] = time.Now()
	t.mu.Unlock()
}

// admit reports whether a new session may be created, first dropping
// entries idle past sessionIdleTTL (the SDK sweeps its own idle sessions at
// the same interval).
func (t *sessionTracker) admit() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for id, seen := range t.lastSeen {
		if now.Sub(seen) > sessionIdleTTL {
			delete(t.lastSeen, id)
		}
	}
	return len(t.lastSeen) < maxSessions
}

// newHTTPHandler builds the full route table — port of the createHttpServer
// callback in src/http-server.ts: CORS on every response, OPTIONS preflight,
// /health, /mcp, /icon.png, the server card, and a JSON 404.
func newHTTPHandler(db *sql.DB, about *tools.AboutContext, start time.Time, icon []byte) http.Handler {
	// Static payloads — stringify once (TS prebuilt SERVER_CARD_JSON).
	cardJSON, _ := json.Marshal(serverCard{
		ServerInfo: cardServerInfo{
			Name:        serverName,
			Version:     serverVersion,
			DisplayName: "Hungarian Law MCP",
			Description: descriptionCoverage + ". Covers the full corpus from Nemzeti Jogszabálytár (njt.hu) including Ptk., Infotv., Mt., Btk., and EU cross-references. " + descriptionFreshness,
			Homepage:    "https://ansvar.eu",
			Keywords:    []string{"hungarian-law", "legislation", "legal", "mcp", "gdpr", "data-protection", "cybersecurity", "compliance", "ptk", "infotv"},
			Author:      "Ansvar Systems / AVIAN Care Kft.",
			License:     "Apache-2.0",
		},
		Capabilities: cardCapabilities{Tools: true, Prompts: true, Resources: true},
		Transport:    cardTransport{Type: "streamable-http", URL: "/mcp"},
	})
	metaJSON, _ := json.Marshal(mcpMetadataDoc{
		Name:        serverName,
		Version:     serverVersion,
		Description: descriptionCoverage + " from Nemzeti Jogszabálytár (njt.hu). " + descriptionFreshness,
		Protocol:    "mcp",
		Transport:   "streamable-http",
	})

	// One shared Server instance: the transport keys session state itself,
	// so the per-request rebuild of 13 tools + prompts + resources was pure
	// waste (K6).
	sessions := newSessionTracker()
	shared := sessionServer(db, about, sessions)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return shared
	}, &mcp.StreamableHTTPOptions{SessionTimeout: sessionIdleTTL, Logger: logger}) // matches the TS server's 30-min idle sweep

	// /health COUNT pair is expensive on a large readonly DB — probe OUTSIDE
	// healthMu so concurrent hits coalesce on the DB instead of queueing
	// behind one scan (K3), cache the first fully successful probe, and
	// cache failures briefly so unauthenticated /health requests cannot
	// amplify into a COUNT scan per hit (S6).
	const healthFailureTTL = 15 * time.Second
	var (
		healthMu       sync.Mutex
		healthCounts   *[2]int
		healthFailedAt time.Time
	)
	probeHealth := func(ctx context.Context) bool {
		healthMu.Lock()
		if healthCounts != nil {
			healthMu.Unlock()
			return true
		}
		if !healthFailedAt.IsZero() && time.Since(healthFailedAt) < healthFailureTTL {
			healthMu.Unlock()
			return false
		}
		healthMu.Unlock()

		ok := store.CoreTablesReady(ctx, db)
		var counts [2]int
		if ok {
			err := db.QueryRowContext(ctx, `SELECT
					(SELECT COUNT(*) FROM legal_documents) AS documents,
					(SELECT COUNT(*) FROM legal_provisions) AS provisions`).Scan(&counts[0], &counts[1])
			ok = err == nil && counts[0] > 0 && counts[1] > 0
		}

		healthMu.Lock()
		defer healthMu.Unlock()
		if ok {
			healthCounts = &counts // cache success — failures retry after the TTL
		} else {
			healthFailedAt = time.Now()
		}
		return ok
	}

	handleMCP := func(w http.ResponseWriter, r *http.Request) {
		// UUID-validate the header before using it (TS validSessionId); a
		// malformed id is stripped so the SDK transport never sees it.
		sessionID := r.Header.Get("mcp-session-id")
		if !uuidV4RE.MatchString(sessionID) {
			r.Header.Del("mcp-session-id")
			sessionID = ""
		}

		// Existing session — refresh liveness, delegate any method to the
		// SDK transport.
		if sessionID != "" {
			sessions.touch(sessionID)
			mcpHandler.ServeHTTP(w, r)
			return
		}

		switch r.Method {
		case http.MethodDelete:
			writeJSON(w, http.StatusNotFound, errJSON("Session not found"))
		case http.MethodPost:
			// New session (initialize) — hold it to the cap before the
			// SDK creates one (S4).
			if !sessions.admit() {
				w.Header().Set("Retry-After", "60")
				writeJSON(w, http.StatusTooManyRequests, errJSON("Too many sessions"))
				return
			}
			mcpHandler.ServeHTTP(w, r)
		case http.MethodGet, http.MethodHead:
			// Sessionless GET — plain metadata doc, not an SSE stream.
			writeJSON(w, http.StatusOK, metaJSON)
		default:
			// 405, not 400: the route exists, the method does not (E10).
			writeJSON(w, http.StatusMethodNotAllowed, errJSON("Method not allowed"))
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Cap the body before anything reads it. The raw w (not the
		// wrapper below) must be passed so the server's requestTooLarge
		// hook still fires and the connection closes after the 413 (S2).
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

		// CORS on every response, errors included.
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, mcp-session-id, Authorization")
		h.Set("Access-Control-Expose-Headers", "mcp-session-id")

		// Track the response status so the panic handler knows whether a
		// 500 body can still be written (TS res.headersSent); the access
		// log middleware wraps the same writer from outside.
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			if rec := recover(); rec != nil {
				logf("Unhandled error: %v\n%s", rec, debug.Stack())
				if sw.status == 0 {
					writeJSON(sw, http.StatusInternalServerError, errJSON("Internal server error"))
				}
			}
		}()

		// OPTIONS — preflight on any path.
		if r.Method == http.MethodOptions {
			sw.WriteHeader(http.StatusNoContent)
			return
		}

		// GET/HEAD /health
		if r.URL.Path == "/health" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			status, state := http.StatusOK, "ok"
			if !probeHealth(r.Context()) {
				status, state = http.StatusServiceUnavailable, "degraded"
			}
			body, _ := json.Marshal(healthPayload{
				Status:  state,
				Server:  serverName,
				Version: serverVersion,
				Uptime:  int(time.Since(start).Seconds()),
			})
			writeJSON(sw, status, body)
			return
		}

		// /mcp — MCP Streamable HTTP transport.
		if r.URL.Path == "/mcp" {
			handleMCP(sw, r)
			return
		}

		// GET/HEAD /icon.png — server icon (read once at startup).
		if r.URL.Path == "/icon.png" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			if icon == nil {
				sw.WriteHeader(http.StatusNotFound)
				return
			}
			h.Set("Content-Type", "image/png")
			h.Set("Cache-Control", "public, max-age=86400")
			h.Set("Content-Length", strconv.Itoa(len(icon)))
			sw.WriteHeader(http.StatusOK)
			if r.Method != http.MethodHead {
				_, _ = sw.Write(icon)
			}
			return
		}

		// GET /.well-known/mcp/server-card.json — static, prebuilt.
		if r.URL.Path == "/.well-known/mcp/server-card.json" && r.Method == http.MethodGet {
			writeJSON(sw, http.StatusOK, cardJSON)
			return
		}

		writeJSON(sw, http.StatusNotFound, errJSON("Not found"))
	})
}

// withAccessLog writes one Info line per request — method, path, status,
// duration — so abuse of the unauthenticated surface is visible in serve
// mode. The stdio entrypoint has no HTTP surface, so it stays unwrapped.
func withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		start := time.Now()
		defer func() {
			logf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start))
		}()
		next.ServeHTTP(sw, r)
	})
}

// statusWriter records the numeric response status — for the access log and
// so the panic handler knows whether a 500 body can still be written.
type statusWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader records the status and forwards the write.
func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Write implies a 200 when WriteHeader was never called, matching net/http
// semantics.
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Flush forwards to the underlying flusher so the SDK's SSE keepalive
// (http.NewResponseController(w).Flush()) reaches the client instead of
// buffering behind proxies (C1).
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the wrapped writer's optional
// interfaces (deadlines, close notify, ...).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// writeJSON mirrors TS writeHead(status, {'Content-Type': 'application/json'})
// followed by res.end(body). net/http discards the body of HEAD responses
// itself.
func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func errJSON(msg string) []byte {
	b, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: msg})
	return b
}

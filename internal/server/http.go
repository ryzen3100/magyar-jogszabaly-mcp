// HTTP entrypoint — port of src/http-server.ts (Streamable HTTP transport for
// the Docker deployment). RunHTTP blocks until SIGINT/SIGTERM and returns nil
// on clean shutdown.
package server

import (
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
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/tools"
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

func RunHTTP() error {
	port := 3000
	if raw := os.Getenv("PORT"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("invalid PORT %q", raw)
		}
		port = p
	}

	db, path, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	logf("Database: %s", path)
	logf("Tier: %s", store.ReadDbMetadata(db).Tier)

	// Built once at startup, shared by every session.
	about := buildAboutContext(db, path)
	icon := loadIcon()

	handler := newHTTPHandler(db, about, time.Now(), icon)
	srv := &http.Server{Addr: ":" + strconv.Itoa(port), Handler: handler}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	fmt.Fprintf(os.Stderr, "%s v%s HTTP server listening on port %d\n", serverName, serverVersion, port)

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
		// Match the TS 5s forced exit when connections refuse to drain.
		timer := time.AfterFunc(5*time.Second, func() { os.Exit(1) })
		defer timer.Stop()
		return srv.Shutdown(context.Background())
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

// sessionServer builds a fresh MCP server per session — port of
// createMCPServer in src/http-server.ts. HTTP mode advertises
// tools+prompts+resources (stdio is tools-only).
func sessionServer(db *sql.DB, about *tools.AboutContext) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: serverVersion}, &mcp.ServerOptions{
		// TS generated session IDs with crypto.randomUUID(); the SDK's
		// default generator uses a different shape that would fail the
		// UUID validation in handleMCP, breaking session termination.
		GetSessionID: newSessionID,
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

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return sessionServer(db, about)
	}, nil)
	// ponytail: the TS server hand-rolled a 500-session cap, 30-min idle TTL
	// sweep and oldest-eviction around its transport; the SDK's
	// StreamableHTTPHandler owns session lifecycle now. If abandoned-session
	// memory ever matters, set StreamableHTTPOptions.SessionTimeout to
	// restore the TTL half.

	// /health COUNT pair is expensive on a large readonly DB — compute once on
	// the first fully successful probe; failures and stub/empty results retry.
	var (
		healthMu     sync.Mutex
		healthCounts *[2]int
	)
	probeHealth := func() bool {
		healthMu.Lock()
		defer healthMu.Unlock()
		if healthCounts != nil {
			return true
		}
		if !store.CoreTablesReady(db) {
			return false
		}
		var counts [2]int
		err := db.QueryRow(`SELECT
				(SELECT COUNT(*) FROM legal_documents) AS documents,
				(SELECT COUNT(*) FROM legal_provisions) AS provisions`).Scan(&counts[0], &counts[1])
		if err != nil || counts[0] == 0 || counts[1] == 0 {
			return false
		}
		healthCounts = &counts // cache success only — failures re-probe next call
		return true
	}

	handleMCP := func(w http.ResponseWriter, r *http.Request) {
		// UUID-validate the header before using it (TS validSessionId).
		sessionID := r.Header.Get("mcp-session-id")
		if !uuidV4RE.MatchString(sessionID) {
			sessionID = ""
		}

		// Existing session — delegate any method to the SDK transport.
		if sessionID != "" {
			mcpHandler.ServeHTTP(w, r)
			return
		}

		switch r.Method {
		case http.MethodDelete:
			writeJSON(w, http.StatusNotFound, errJSON("Session not found"))
		case http.MethodPost:
			// New session (initialize) — the SDK handler creates it.
			mcpHandler.ServeHTTP(w, r)
		case http.MethodGet, http.MethodHead:
			// Sessionless GET — plain metadata doc, not an SSE stream.
			writeJSON(w, http.StatusOK, metaJSON)
		default:
			writeJSON(w, http.StatusBadRequest, errJSON("Bad request — missing or invalid session"))
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS on every response, errors included.
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, mcp-session-id, Authorization")
		h.Set("Access-Control-Expose-Headers", "mcp-session-id")

		// Track header state so the panic handler knows whether a 500 body
		// can still be written (TS res.headersSent).
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			if rec := recover(); rec != nil {
				logf("Unhandled error: %v", rec)
				if !sw.wroteHeader {
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
			if !probeHealth() {
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

// statusWriter records whether response headers have been written.
type statusWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wroteHeader = true
	return w.ResponseWriter.Write(b)
}

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

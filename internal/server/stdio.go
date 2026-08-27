// Stdio entrypoint — port of src/index.ts.

package server

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/tools"
)

// RunStdio serves the MCP server over stdio and blocks until the transport
// closes or SIGINT/SIGTERM arrives. Stdout carries only JSON-RPC protocol
// traffic; all logging goes to stderr.
func RunStdio() error {
	db, path, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()

	aboutContext := buildAboutContext(db, path)

	// Stdio advertises the tools capability only — src/index.ts passes
	// { capabilities: { tools: {} } }. An empty ToolCapabilities{} overrides
	// the SDK's inferred {"listChanged":true} to match the TS payload.
	srv := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{
			Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
			// SDK diagnostics (keepalive failures, dropped notifications)
			// go to the shared stderr logger instead of being discarded.
			Logger: logger,
		},
	)
	tools.Register(srv, db, aboutContext)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logf("Server running on stdio")

	err = srv.Run(ctx, &mcp.StdioTransport{})
	// SIGINT/SIGTERM (context canceled), a peer-closed stdio session and the
	// SDK's own shutdown errors are clean shutdowns — src/index.ts exits 0 on
	// all of them.
	if isCleanShutdown(err) {
		return nil
	}
	return err
}

// isCleanShutdown reports whether err represents an orderly end of the stdio
// session rather than a failure. jsonrpc2.ErrClientClosing/ErrServerClosing
// are internal to the SDK, so their messages ("client is closing" /
// "server is closing") are matched textually.
func isCleanShutdown(err error) bool {
	if err == nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, mcp.ErrConnectionClosed) {
		return true
	}
	return strings.Contains(err.Error(), "is closing")
}

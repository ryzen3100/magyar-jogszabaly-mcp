// Package server implements the two MCP entrypoints — stdio and Streamable
// HTTP — and the plumbing they share; a port of the common pieces of
// src/index.ts, src/http-server.ts and src/db-info.ts.
package server

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/tools"
)

const (
	serverName    = "hungarian-law-mcp"
	serverVersion = "2.0.0"
)

// logger is the package-wide sink for logf — JSON lines on stderr, so stdout
// stays reserved for MCP protocol traffic in stdio mode. Both entrypoints
// pass the same logger to the SDK via ServerOptions.Logger.
var logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))

// logf writes a structured Info log line with the [hungarian-law-mcp] prefix.
// Stdout carries only MCP protocol traffic in stdio mode, so logging stays on
// stderr.
func logf(format string, args ...any) {
	logger.Info("[" + serverName + "] " + fmt.Sprintf(format, args...))
}

// openDB resolves and opens the readonly database, mirroring getDb() in
// src/index.ts / src/http-server.ts.
func openDB() (*sql.DB, string, error) {
	path, err := store.ResolveDBPath()
	if err != nil {
		return nil, "", err
	}
	db, err := store.OpenReadOnly(path)
	if err != nil {
		return nil, "", err
	}
	logf("DB opened: tier=%s", store.ReadDBMetadata(context.Background(), db).Tier)
	return db, path, nil
}

// buildAboutContext ports buildAboutContext in src/db-info.ts: server version
// + 12-char sha256 fingerprint of the DB file prefix + db_metadata.built_at
// with file-mtime fallback. Failures degrade to the TS defaults ('unknown' /
// now) — never fatal.
func buildAboutContext(db *sql.DB, path string) *tools.AboutContext {
	ctx := &tools.AboutContext{
		Version:     serverVersion,
		Fingerprint: "unknown",
	}
	if fp, err := store.DBFingerprint(path); err == nil {
		ctx.Fingerprint = fp
	}
	ctx.DBBuilt = store.DBBuiltOrMtime(context.Background(), db, path)
	return ctx
}

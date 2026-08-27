// Shared helpers for the stdio and HTTP entrypoints — port of the common
// pieces of src/index.ts, src/http-server.ts and src/db-info.ts.
package server

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/tools"
)

const (
	serverName    = "hungarian-law-mcp"
	serverVersion = "1.0.0"
	// isoLayout reproduces JavaScript Date.toISOString() (UTC, milliseconds).
	isoLayout = "2006-01-02T15:04:05.000Z07:00"
)

// logf writes a stderr log line with the [hungarian-law-mcp] prefix. Stdout
// carries only MCP protocol traffic in stdio mode, so logging stays on stderr.
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "["+serverName+"] "+format+"\n", args...)
}

// openDB resolves and opens the readonly database, mirroring getDb() in
// src/index.ts / src/http-server.ts.
func openDB() (*sql.DB, string, error) {
	path, err := store.ResolveDbPath()
	if err != nil {
		return nil, "", err
	}
	db, err := store.OpenReadOnly(path)
	if err != nil {
		return nil, "", err
	}
	logf("DB opened: tier=%s", store.ReadDbMetadata(db).Tier)
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
		DbBuilt:     time.Now().UTC().Format(isoLayout),
	}
	if fp, err := store.DbFingerprint(path); err == nil {
		ctx.Fingerprint = fp
	}
	if m := store.ReadDbMetadata(db); m.HasBuiltAt {
		ctx.DbBuilt = m.BuiltAt
	} else if st, err := os.Stat(path); err == nil {
		ctx.DbBuilt = st.ModTime().UTC().Format(isoLayout)
	}
	return ctx
}

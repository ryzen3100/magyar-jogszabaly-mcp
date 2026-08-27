package store

import (
	"context"
	"database/sql"
)

// DbMetadata is the db_metadata key/value table distilled to the fields the
// server uses. BuiltAt is only meaningful when HasBuiltAt is true — the Go
// equivalent of the TypeScript `built_at?: string` optionality.
type DbMetadata struct {
	Tier          string
	SchemaVersion string
	BuiltAt       string
	HasBuiltAt    bool
}

// metadataCache backs ReadDbMetadata; see the comment on cacheMu in store.go
// for why this is keyed by *sql.DB instead of being a WeakMap.
var metadataCache = map[*sql.DB]DbMetadata{}

// ReadDbMetadata returns the metadata for db, applying the TypeScript defaults
// (tier 'free', schema_version '1.0', built_at optional). A missing
// db_metadata table is swallowed, exactly as in src/capabilities.ts. Only a
// fully-read table is cached per db — the database is read-only, so complete
// metadata never changes — and a failed read returns the defaults uncached,
// so a later call retries instead of pinning degraded values for the process
// lifetime.
func ReadDbMetadata(ctx context.Context, db *sql.DB) DbMetadata {
	cacheMu.Lock()
	if m, ok := metadataCache[db]; ok {
		cacheMu.Unlock()
		return m
	}
	cacheMu.Unlock()

	defaults := DbMetadata{Tier: "free", SchemaVersion: "1.0"}
	meta, complete := readMetadataTable(ctx, db)
	if !complete {
		return defaults
	}
	m := defaults
	if v, ok := meta["tier"]; ok {
		m.Tier = v
	}
	if v, ok := meta["schema_version"]; ok {
		m.SchemaVersion = v
	}
	if v, ok := meta["built_at"]; ok {
		m.BuiltAt = v
		m.HasBuiltAt = true
	}
	cacheMu.Lock()
	metadataCache[db] = m
	cacheMu.Unlock()
	return m
}

// readMetadataTable reads the whole db_metadata table. complete is false when
// the table is missing or the read was truncated (a scan or iteration error) —
// such a degraded read must never be cached.
func readMetadataTable(ctx context.Context, db *sql.DB) (meta map[string]string, complete bool) {
	rows, err := db.QueryContext(ctx, "SELECT key, value FROM db_metadata")
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	meta = map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, false
		}
		meta[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, false
	}
	return meta, true
}

package store

import "database/sql"

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
// db_metadata table is swallowed, exactly as in src/capabilities.ts. The
// result is cached per db forever: the database is opened read-only, so
// metadata never changes on a connection.
func ReadDbMetadata(db *sql.DB) DbMetadata {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if m, ok := metadataCache[db]; ok {
		return m
	}
	meta := map[string]string{}
	if rows, err := db.Query("SELECT key, value FROM db_metadata"); err == nil {
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err == nil {
				meta[k] = v
			}
		}
		rows.Close()
	}
	m := DbMetadata{Tier: "free", SchemaVersion: "1.0"}
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
	metadataCache[db] = m
	return m
}

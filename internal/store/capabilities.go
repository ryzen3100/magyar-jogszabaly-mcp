package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// coreTables must all exist for the legislation tools to work.
var coreTables = []string{"legal_documents", "legal_provisions", "provisions_fts"}

// CoreTablesReady reports whether all core legislation tables exist — port of
// coreTablesReady in src/capabilities.ts.
func CoreTablesReady(ctx context.Context, db *sql.DB) bool {
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		return false
	}
	defer rows.Close()
	present := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false
		}
		present[name] = true
	}
	if err := rows.Err(); err != nil {
		return false
	}
	for _, t := range coreTables {
		if !present[t] {
			return false
		}
	}
	return true
}

// euCache backs EUAvailable, keyed per db then per table name; see the
// comment on cacheMu in store.go for the WeakMap equivalence.
var euCache = map[*sql.DB]map[string]bool{}

// EUAvailable probes whether an EU table can be queried — port of euAvailable
// in src/capabilities.ts. Callers pass compile-time table names only
// ('eu_references', 'eu_documents'); the identifier is interpolated into SQL,
// just as in the TypeScript original. An existing-but-empty table counts as
// available (the TS `.get()` returns undefined without throwing). Cached per
// (db, table): the database is read-only, so presence never changes. The
// probe runs outside cacheMu and the result is published under it.
func EUAvailable(ctx context.Context, db *sql.DB, table string) bool {
	cacheMu.Lock()
	if byTable, ok := euCache[db]; ok {
		if available, cached := byTable[table]; cached {
			cacheMu.Unlock()
			return available
		}
	}
	cacheMu.Unlock()

	var one int
	err := db.QueryRowContext(ctx, "SELECT 1 FROM "+table+" LIMIT 1").Scan(&one)
	available := err == nil || errors.Is(err, sql.ErrNoRows)

	cacheMu.Lock()
	byTable, ok := euCache[db]
	if !ok {
		byTable = make(map[string]bool)
		euCache[db] = byTable
	}
	byTable[table] = available
	cacheMu.Unlock()
	return available
}

// EUUnavailableNote builds the _metadata.note shown when an EU table is
// missing: 'EU references not available in this database tier' for
// eu_references, 'EU documents …' for eu_documents (the `EU ${table.slice(3)}
// …` string in src/capabilities.ts).
func EUUnavailableNote(table string) string {
	return "EU " + strings.TrimPrefix(table, "eu_") + " not available in this database tier"
}

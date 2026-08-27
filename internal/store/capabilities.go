package store

import (
	"database/sql"
	"errors"
	"strings"
)

// coreTables must all exist for the legislation tools to work.
var coreTables = []string{"legal_documents", "legal_provisions", "provisions_fts"}

// CoreTablesReady reports whether all core legislation tables exist — port of
// coreTablesReady in src/capabilities.ts.
func CoreTablesReady(db *sql.DB) bool {
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		return false
	}
	defer rows.Close()
	present := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			present[name] = true
		}
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
// (db, table): the database is read-only, so presence never changes.
func EUAvailable(db *sql.DB, table string) bool {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	byTable, ok := euCache[db]
	if !ok {
		byTable = make(map[string]bool)
		euCache[db] = byTable
	}
	if available, ok := byTable[table]; ok {
		return available
	}
	var one int
	err := db.QueryRow("SELECT 1 FROM " + table + " LIMIT 1").Scan(&one)
	available := err == nil || errors.Is(err, sql.ErrNoRows)
	byTable[table] = available
	return available
}

// EUUnavailableNote builds the _metadata.note shown when an EU table is
// missing: 'EU references not available in this database tier' for
// eu_references, 'EU documents …' for eu_documents (the `EU ${table.slice(3)}
// …` string in src/capabilities.ts).
func EUUnavailableNote(table string) string {
	return "EU " + strings.TrimPrefix(table, "eu_") + " not available in this database tier"
}

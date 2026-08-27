// Package store provides read-only access helpers for the law database:
// connection opening, cached metadata/capability probes, memoized count
// helpers, and database location/fingerprint utilities.
//
// It ports src/capabilities.ts, the DB-touching helpers of
// src/utils/metadata.ts (safeCount/cachedCount), and src/db-info.ts from the
// TypeScript server.
package store

import (
	"database/sql"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// cacheMu guards the per-DB caches in this package. The TypeScript originals
// used WeakMap<Database, ...> (entries die with the connection). Go has no
// equivalent here, so the caches are keyed by the *sql.DB pointer itself:
// entries can never collide across different DB handles, so stale data across
// handles is impossible. Unlike a WeakMap they are never garbage-collected,
// which is fine — the server opens its (read-only) database once per process.
var cacheMu sync.Mutex

// OpenReadOnly opens the SQLite database at path in read-only mode.
// mode=ro mirrors the TypeScript `{ readonly: true }` open (a missing file
// fails instead of being created); the query_only pragma is belt and braces.
func OpenReadOnly(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)")
}

// SafeCount runs a count query and returns the first column as an int, or 0
// on any error — port of safeCount in src/utils/metadata.ts (Number(row.count),
// catch → 0).
func SafeCount(db *sql.DB, query string) int {
	var v any
	if err := db.QueryRow(query).Scan(&v); err != nil {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return int(n)
	case float64:
		return int(n)
	case []byte:
		if i, err := strconv.Atoi(strings.TrimSpace(string(n))); err == nil {
			return i
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return i
		}
	}
	return 0
}

// countCache backs CachedCount, keyed per db then per query string.
var countCache = map[*sql.DB]map[string]int{}

// CachedCount is SafeCount memoized per (db, query). The DB is opened
// read-only, so counts are immutable facts — port of cachedCount in
// src/utils/metadata.ts (WeakMap<Database, Map<string, number>>).
func CachedCount(db *sql.DB, query string) int {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	byQuery, ok := countCache[db]
	if !ok {
		byQuery = make(map[string]int)
		countCache[db] = byQuery
	}
	if n, ok := byQuery[query]; ok {
		return n
	}
	n := SafeCount(db, query)
	byQuery[query] = n
	return n
}

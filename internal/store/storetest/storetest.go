// Package storetest provides an in-memory SQLite fixture for tests — the Go
// port of tests/helpers/test-db.ts (createTestDb + seedCoreFixtures + the
// real-database guards). It lives in a subpackage so test-only code never
// ships with the store package.
package storetest

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// NewTestDb builds a seeded in-memory database with the same simplified DDL
// as tests/helpers/test-db.ts. The TS version offers withEuTables /
// withDefinitionsTable / withMetadataTable flags; the Go fixture always
// creates every table — tests that need an absent table simply DROP it, which
// exercises the same code paths. Cleanup (db.Close) is registered on t,
// replacing the TS trackDb/afterEach drain.
func NewTestDb(t *testing.T) *sql.DB {
	t.Helper()
	rnd := make([]byte, 8)
	if _, err := rand.Read(rnd); err != nil {
		t.Fatalf("random db name: %v", err)
	}
	dsn := fmt.Sprintf("file:storetest_%s?mode=memory&cache=shared", hex.EncodeToString(rnd))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	// A single pooled connection keeps the shared in-memory database (and its
	// schema) alive for the whole test; an idle limit of 1 stops database/sql
	// from ever dropping that connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { db.Close() })

	for _, ddl := range []string{
		`CREATE TABLE legal_documents (
			id TEXT PRIMARY KEY,
			type TEXT,
			title TEXT NOT NULL,
			title_en TEXT,
			short_name TEXT,
			status TEXT NOT NULL,
			issued_date TEXT,
			in_force_date TEXT,
			url TEXT,
			description TEXT
		)`,
		`CREATE TABLE legal_provisions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			document_id TEXT NOT NULL,
			provision_ref TEXT NOT NULL,
			chapter TEXT,
			section TEXT NOT NULL,
			title TEXT,
			content TEXT NOT NULL,
			metadata TEXT
		)`,
		`CREATE VIRTUAL TABLE provisions_fts USING fts5(content, title)`,
		`CREATE TABLE definitions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			document_id TEXT NOT NULL,
			term TEXT NOT NULL,
			term_en TEXT,
			definition TEXT NOT NULL,
			source_provision TEXT
		)`,
		`CREATE TABLE eu_documents (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			year INTEGER NOT NULL,
			number INTEGER NOT NULL,
			title TEXT,
			short_name TEXT,
			description TEXT
		)`,
		`CREATE TABLE eu_references (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			document_id TEXT NOT NULL,
			provision_id INTEGER,
			eu_document_id TEXT NOT NULL,
			eu_article TEXT,
			reference_type TEXT NOT NULL,
			reference_context TEXT,
			full_citation TEXT,
			implementation_status TEXT,
			is_primary_implementation INTEGER DEFAULT 0
		)`,
		`CREATE TABLE db_metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}

	seedCoreFixtures(t, db)
	return db
}

// seedCoreFixtures ports seedCoreFixtures from tests/helpers/test-db.ts,
// string for string (Hungarian content included).
func seedCoreFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec := func(query string, args ...any) sql.Result {
		t.Helper()
		res, err := db.Exec(query, args...)
		if err != nil {
			t.Fatalf("seed exec %q: %v", query, err)
		}
		return res
	}

	docStmt := `INSERT INTO legal_documents (
		id, type, title, title_en, short_name, status, issued_date, in_force_date, url, description
	) VALUES (?, 'statute', ?, ?, ?, ?, ?, ?, ?, ?)`
	mustExec(docStmt,
		"doc-inforce", "In Force Act", "In Force Act EN", "IFA", "in_force",
		"2020-01-01", "2020-06-01", "https://njt.hu/jogszabaly/inforce", "In-force document")
	mustExec(docStmt,
		"doc-amended", "Amended Act", "Amended Act EN", "AA", "amended",
		"2018-01-01", "2018-06-01", "https://njt.hu/jogszabaly/amended", "Amended document")
	mustExec(docStmt,
		"doc-repealed", "Repealed Act", "Repealed Act EN", "RA", "repealed",
		"2010-01-01", "2010-06-01", "https://njt.hu/jogszabaly/repealed", "Repealed document")
	mustExec(docStmt,
		"doc-future", "Future Act", "Future Act EN", "FA", "not_yet_in_force",
		"2030-01-01", "2031-01-01", "https://njt.hu/jogszabaly/future", "Future document")

	provStmt := `INSERT INTO legal_provisions
		(document_id, provision_ref, chapter, section, title, content, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	p1 := lastInsertID(t, mustExec(provStmt,
		"doc-inforce", "s1", "I. Fejezet", "1", "1. §",
		"A személyes adat kezelése és elektronikus aláírás szabályai.", nil))
	p2 := lastInsertID(t, mustExec(provStmt,
		"doc-inforce", "s2", "I. Fejezet", "2", "2. §",
		"Kiberbiztonsági intézkedések és információs rendszer védelem.", nil))
	p3 := lastInsertID(t, mustExec(provStmt,
		"doc-amended", "s3", "II. Fejezet", "3", "3. §",
		"Üzleti titok és létfontosságú infrastruktúra védelme.", nil))

	ftsStmt := `INSERT INTO provisions_fts(rowid, content, title) VALUES (?, ?, ?)`
	mustExec(ftsStmt, p1, "A személyes adat kezelése és elektronikus aláírás szabályai.", "1. §")
	mustExec(ftsStmt, p2, "Kiberbiztonsági intézkedések és információs rendszer védelem.", "2. §")
	mustExec(ftsStmt, p3, "Üzleti titok és létfontosságú infrastruktúra védelme.", "3. §")

	mustExec(`INSERT INTO definitions (document_id, term, definition, source_provision)
		VALUES ('doc-inforce', 'személyes adat', 'Az érintettre vonatkozó adat.', 's1')`)

	mustExec(`INSERT INTO eu_documents (id, type, year, number, title, short_name, description)
		VALUES ('regulation:2016/679', 'regulation', 2016, 679, 'GDPR', 'GDPR', 'General Data Protection Regulation')`)
	mustExec(`INSERT INTO eu_documents (id, type, year, number, title, short_name, description)
		VALUES ('directive:2022/2555', 'directive', 2022, 2555, 'NIS2', 'NIS2', 'Network and Information Security')`)

	euStmt := `INSERT INTO eu_references (
		document_id, provision_id, eu_document_id, eu_article, reference_type,
		reference_context, full_citation, implementation_status, is_primary_implementation
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	mustExec(euStmt,
		"doc-inforce", p1, "regulation:2016/679", "Article 6", "implements",
		"Implements GDPR requirements.", "Regulation (EU) 2016/679", "complete", 1)
	mustExec(euStmt,
		"doc-amended", p3, "directive:2022/2555", nil, "references",
		"References NIS2 baseline.", "Directive (EU) 2022/2555", "partial", 0)
	mustExec(euStmt,
		"doc-amended", p3, "regulation:2016/679", nil, "references",
		"General GDPR reference.", "Regulation (EU) 2016/679", "unknown", 0)

	metaStmt := `INSERT INTO db_metadata(key, value) VALUES (?, ?)`
	mustExec(metaStmt, "tier", "free")
	mustExec(metaStmt, "schema_version", "1.0")
	mustExec(metaStmt, "built_at", "2026-02-21T00:00:00Z")
	mustExec(metaStmt, "builder", "test-suite")
}

func lastInsertID(t *testing.T, res sql.Result) int64 {
	t.Helper()
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

// ---------------------------------------------------------------------------
// Real-database (data/database.db) helpers shared by DB-backed suites
// ---------------------------------------------------------------------------

// realDBPath anchors on this file's location at compile time (the Go
// equivalent of the TS import.meta.url anchor), so it works regardless of the
// test binary's working directory. This file sits three levels below the repo
// root (internal/store/storetest), hence the three ".." hops.
var realDBPath = func() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return filepath.Join("data", "database.db")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "data", "database.db")
}()

// RealDBPath is the absolute path of the generated production database.
func RealDBPath() string { return realDBPath }

var (
	realDBOnce sync.Once
	realDBOK   bool
)

// RealDBAvailable reports whether the real database exists and is usable
// (has legal_documents) — the port of realDbExists/REAL_DB_AVAILABLE,
// computed once per process. DB-backed tests should t.Skip when false.
func RealDBAvailable() bool {
	realDBOnce.Do(func() {
		if _, err := os.Stat(realDBPath); err != nil {
			return
		}
		db, err := store.OpenReadOnly(realDBPath)
		if err != nil {
			return
		}
		defer db.Close()
		var cnt int
		if err := db.QueryRow(
			"SELECT COUNT(*) as cnt FROM sqlite_master WHERE type='table' AND name='legal_documents'",
		).Scan(&cnt); err != nil {
			return
		}
		realDBOK = cnt > 0
	})
	return realDBOK
}

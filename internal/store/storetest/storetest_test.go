package storetest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewTestDbSmoke verifies the fixture builds a queryable database with
// the seeded fixture data, including Hungarian content round-tripping
// through FTS5 and the rowid linkage between provisions and the FTS table.
func TestNewTestDbSmoke(t *testing.T) {
	db := NewTestDb(t)

	count := func(query string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(query).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return n
	}
	if got := count("SELECT COUNT(*) FROM legal_documents"); got != 4 {
		t.Fatalf("legal_documents = %d, want 4", got)
	}
	if got := count("SELECT COUNT(*) FROM legal_provisions"); got != 3 {
		t.Fatalf("legal_provisions = %d, want 3", got)
	}
	if got := count("SELECT COUNT(*) FROM definitions"); got != 1 {
		t.Fatalf("definitions = %d, want 1", got)
	}
	if got := count("SELECT COUNT(*) FROM eu_documents"); got != 2 {
		t.Fatalf("eu_documents = %d, want 2", got)
	}
	if got := count("SELECT COUNT(*) FROM eu_references"); got != 3 {
		t.Fatalf("eu_references = %d, want 3", got)
	}

	var builtAt string
	if err := db.QueryRow("SELECT value FROM db_metadata WHERE key = 'built_at'").Scan(&builtAt); err != nil || builtAt != "2026-02-21T00:00:00Z" {
		t.Fatalf("built_at = %q (err %v), want 2026-02-21T00:00:00Z", builtAt, err)
	}

	var content string
	if err := db.QueryRow(
		"SELECT content FROM provisions_fts WHERE provisions_fts MATCH 'személyes adat' LIMIT 1",
	).Scan(&content); err != nil {
		t.Fatalf("fts match: %v", err)
	}
	if !strings.Contains(content, "személyes adat") {
		t.Fatalf("fts content = %q", content)
	}

	var ftsRowid, provisionID int
	if err := db.QueryRow("SELECT rowid FROM provisions_fts WHERE title = '1. §'").Scan(&ftsRowid); err != nil {
		t.Fatalf("fts rowid: %v", err)
	}
	if err := db.QueryRow("SELECT id FROM legal_provisions WHERE provision_ref = 's1'").Scan(&provisionID); err != nil {
		t.Fatalf("provision id: %v", err)
	}
	if ftsRowid != provisionID {
		t.Fatalf("fts rowid %d != provision id %d", ftsRowid, provisionID)
	}
}

func TestRealDBPath(t *testing.T) {
	if !strings.HasSuffix(RealDBPath(), filepath.Join("data", "database.db")) {
		t.Fatalf("RealDBPath = %s, want .../data/database.db", RealDBPath())
	}
	if RealDBAvailable() {
		if _, err := os.Stat(RealDBPath()); err != nil {
			t.Fatalf("RealDBAvailable true but %s missing: %v", RealDBPath(), err)
		}
	}
}

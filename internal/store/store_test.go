package store_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/store"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/store/storetest"
)

// isoMillis mirrors the JavaScript Date#toISOString shape asserted below.
const isoMillis = "2006-01-02T15:04:05.000Z07:00"

// dbSkippedTests is incremented by every DB-backed skip site in this package;
// TestMain turns it into one loud stderr notice (T6) so a green run can never
// silently cover zero DB tests.
var dbSkippedTests int

func TestMain(m *testing.M) {
	code := m.Run()
	if dbSkippedTests > 0 {
		fmt.Fprintf(os.Stderr, "SKIP NOTICE: %d DB-backed tests skipped — data/database.db unavailable\n", dbSkippedTests)
	}
	os.Exit(code)
}

func mustDropTable(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	if _, err := db.Exec("DROP TABLE " + table); err != nil {
		t.Fatalf("drop %s: %v", table, err)
	}
}

// --- readDbMetadata / generateResponseMetadata (freshness) concerns --------

func TestReadDBMetadata(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	m := store.ReadDBMetadata(t.Context(), db)
	if m.Tier != "free" || m.SchemaVersion != "1.0" ||
		m.BuiltAt != "2026-02-21T00:00:00Z" || !m.HasBuiltAt {
		t.Fatalf("unexpected metadata: %+v", m)
	}
	if again := store.ReadDBMetadata(t.Context(), db); again != m {
		t.Fatalf("metadata not cached per db: %+v vs %+v", again, m)
	}
}

func TestReadDBMetadataDefaultsWhenTableMissing(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	mustDropTable(t, db, "db_metadata")
	m := store.ReadDBMetadata(t.Context(), db)
	if m.Tier != "free" || m.SchemaVersion != "1.0" || m.HasBuiltAt || m.BuiltAt != "" {
		t.Fatalf("expected free/1.0 defaults without built_at, got %+v", m)
	}
}

func TestReadDBMetadataCacheIsolation(t *testing.T) {
	t.Parallel()
	withMeta := storetest.NewTestDb(t)
	withoutMeta := storetest.NewTestDb(t)
	mustDropTable(t, withoutMeta, "db_metadata")

	if m := store.ReadDBMetadata(t.Context(), withMeta); m.BuiltAt != "2026-02-21T00:00:00Z" {
		t.Fatalf("intact db lost its built_at: %+v", m)
	}
	if m := store.ReadDBMetadata(t.Context(), withoutMeta); m.HasBuiltAt {
		t.Fatalf("cache leaked across *sql.DB handles: %+v", m)
	}
}

// A failed read (here: a cancelled context) must return the defaults uncached
// so a later call retries instead of pinning degraded metadata forever.
func TestReadDBMetadataFailedReadNotCached(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if m := store.ReadDBMetadata(cancelled, db); m.HasBuiltAt {
		t.Fatalf("failed read should return defaults, got %+v", m)
	}
	if m := store.ReadDBMetadata(t.Context(), db); m.BuiltAt != "2026-02-21T00:00:00Z" {
		t.Fatalf("expected retry after failed read, got %+v", m)
	}
}

// --- coreTablesReady --------------------------------------------------------

func TestCoreTablesReady(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	if !store.CoreTablesReady(t.Context(), db) {
		t.Fatal("expected core tables ready")
	}
	mustDropTable(t, db, "provisions_fts")
	if store.CoreTablesReady(t.Context(), db) {
		t.Fatal("expected not ready after dropping provisions_fts")
	}

	empty, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	empty.SetMaxOpenConns(1)
	defer empty.Close()
	if store.CoreTablesReady(t.Context(), empty) {
		t.Fatal("empty database should not be ready")
	}
}

// --- euAvailable / euUnavailable note ---------------------------------------

func TestEUAvailable(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	if !store.EUAvailable(t.Context(), db, "eu_references") {
		t.Fatal("eu_references should be available")
	}
	if !store.EUAvailable(t.Context(), db, "eu_documents") {
		t.Fatal("eu_documents should be available")
	}

	missing := storetest.NewTestDb(t)
	mustDropTable(t, missing, "eu_references")
	if store.EUAvailable(t.Context(), missing, "eu_references") {
		t.Fatal("missing table should be unavailable")
	}
	if !store.EUAvailable(t.Context(), missing, "eu_documents") {
		t.Fatal("eu_documents should still be available")
	}

	// An existing-but-empty table counts as available (TS .get() returns
	// undefined without throwing).
	empty := storetest.NewTestDb(t)
	mustDropTable(t, empty, "eu_references")
	if _, err := empty.Exec("CREATE TABLE eu_references (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if !store.EUAvailable(t.Context(), empty, "eu_references") {
		t.Fatal("existing-but-empty table should count as available")
	}
}

func TestEUUnavailableNote(t *testing.T) {
	t.Parallel()
	if got := store.EUUnavailableNote("eu_references"); got != "EU references not available in this database tier" {
		t.Fatalf("eu_references note = %q", got)
	}
	if got := store.EUUnavailableNote("eu_documents"); got != "EU documents not available in this database tier" {
		t.Fatalf("eu_documents note = %q", got)
	}
}

// --- safeCount / cachedCount -------------------------------------------------

func TestSafeCount(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	if got := store.SafeCount(t.Context(), db, "SELECT COUNT(*) as count FROM legal_documents"); got != 4 {
		t.Fatalf("legal_documents count = %d, want 4", got)
	}
	if got := store.SafeCount(t.Context(), db, "SELECT NULL as count"); got != 0 {
		t.Fatalf("NULL count = %d, want 0", got)
	}
	if got := store.SafeCount(t.Context(), db, "SELECT COUNT(*) FROM no_such_table"); got != 0 {
		t.Fatalf("error count = %d, want 0", got)
	}
}

func TestCachedCount(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	q := "SELECT COUNT(*) as count FROM legal_documents"
	if got := store.CachedCount(t.Context(), db, q); got != 4 {
		t.Fatalf("count = %d, want 4", got)
	}
	// Readonly-DB contract: the memoized value survives even though the
	// underlying table is now gone (mirrors the TS WeakMap memoization).
	mustDropTable(t, db, "legal_documents")
	if got := store.CachedCount(t.Context(), db, q); got != 4 {
		t.Fatalf("memoized count = %d, want 4", got)
	}

	// The cache is keyed per db: a second db without the table must compute
	// its own (0), not reuse the first db's 4.
	other := storetest.NewTestDb(t)
	mustDropTable(t, other, "legal_documents")
	if got := store.CachedCount(t.Context(), other, q); got != 0 {
		t.Fatalf("second db count = %d, want 0 (cache leaked across dbs?)", got)
	}
}

// --- openReadOnly ------------------------------------------------------------

// The DSN is built with net/url: '?' and '#' in the path must round-trip to
// the real filename instead of splitting off query/fragment DSN parameters.
func TestOpenReadOnlySpecialCharsInPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.db")
	db, err := sql.Open("sqlite", plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE legal_documents (id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO legal_documents (id) VALUES ('doc-in-force')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Built at a boring path, then renamed: a plain DSN could not even express
	// this filename (the driver splits non-file: DSNs at the first '?').
	weird := filepath.Join(dir, "law?#1.db")
	if err := os.Rename(plain, weird); err != nil {
		t.Fatal(err)
	}

	ro, err := store.OpenReadOnly(weird)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	var n int
	if err := ro.QueryRowContext(t.Context(), "SELECT COUNT(*) as count FROM legal_documents").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("legal_documents count = %d, want 1 (DSN did not round-trip the path)", n)
	}
}

// --- resolveDbPath -----------------------------------------------------------

func TestResolveDBPathEnvOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "custom", "law.sqlite")
	t.Setenv("HUNGARIAN_LAW_DB_PATH", want)
	p, err := store.ResolveDBPath()
	if err != nil {
		t.Fatal(err)
	}
	if p != want {
		t.Fatalf("env path = %q, want verbatim %q", p, want)
	}
}

func TestResolveDBPathError(t *testing.T) {
	t.Setenv("HUNGARIAN_LAW_DB_PATH", "")
	_, err := store.ResolveDBPath()
	if err == nil {
		t.Fatal("expected error when no candidate exists")
	}
	want := "database not found; set HUNGARIAN_LAW_DB_PATH or place database.db in data/"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// --- fingerprint / built-at helpers ------------------------------------------

func TestDBFingerprintSmallFileZeroPadded(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "small.db")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The TS original hashes the full 64 KiB zero-padded buffer.
	sample := make([]byte, 64*1024)
	copy(sample, "hello")
	sum := sha256.Sum256(sample)
	want := hex.EncodeToString(sum[:])[:12]

	got, err := store.DBFingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("fingerprint = %s, want %s", got, want)
	}
}

func TestDBFingerprintLargeFileFirst64KiBOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	big := make([]byte, 64*1024+7)
	for i := range big {
		big[i] = 'x'
	}
	path := filepath.Join(dir, "large.db")
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(big[:64*1024])
	want := hex.EncodeToString(sum[:])[:12]

	got, err := store.DBFingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || len(got) != 12 {
		t.Fatalf("fingerprint = %s, want %s (12 chars)", got, want)
	}
}

func TestDBFingerprintMissingFileErrors(t *testing.T) {
	t.Parallel()
	if _, err := store.DBFingerprint(filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestDBBuiltOrMtimePrefersBuiltAt(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	if got := store.DBBuiltOrMtime(t.Context(), db, t.TempDir()); got != "2026-02-21T00:00:00Z" {
		t.Fatalf("built_at = %q, want 2026-02-21T00:00:00Z", got)
	}
}

func TestDBBuiltOrMtimeFallsBackToMtime(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	mustDropTable(t, db, "db_metadata")

	path := filepath.Join(t.TempDir(), "any.db")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := st.ModTime().UTC().Format(isoMillis)
	if got := store.DBBuiltOrMtime(t.Context(), db, path); got != want {
		t.Fatalf("mtime fallback = %q, want %q", got, want)
	}
}

func TestDBBuiltOrMtimeFallsBackToNowWhenStatFails(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	mustDropTable(t, db, "db_metadata")
	got := store.DBBuiltOrMtime(t.Context(), db, filepath.Join(t.TempDir(), "missing.db"))
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`).MatchString(got) {
		t.Fatalf("now fallback = %q, want JS toISOString shape", got)
	}
}

// --- real database (data/database.db) ----------------------------------------

func TestRealDatabase(t *testing.T) {
	t.Parallel()
	if !storetest.RealDBAvailable() {
		dbSkippedTests++
		t.Skip("real database (data/database.db) not available")
	}
	db, err := store.OpenReadOnly(storetest.RealDBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if !store.CoreTablesReady(t.Context(), db) {
		t.Fatal("real database should have all core tables")
	}
	if n := store.SafeCount(t.Context(), db, "SELECT COUNT(*) as count FROM legal_documents"); n <= 0 {
		t.Fatalf("real legal_documents count = %d, want > 0", n)
	}
	m := store.ReadDBMetadata(t.Context(), db)
	if m.Tier == "" || m.SchemaVersion == "" {
		t.Fatalf("real database metadata missing defaults: %+v", m)
	}
}

package builddb

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/seed"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func queryInt(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var got int64
	if err := db.QueryRow(q, args...).Scan(&got); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return int(got)
}

func wantRow(t *testing.T, db *sql.DB, q string, want ...any) {
	t.Helper()
	got := make([]any, len(want))
	dest := make([]any, len(want))
	for i := range got {
		dest[i] = &got[i]
	}
	if err := db.QueryRow(q).Scan(dest...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	for i := range want {
		g := got[i]
		if n, ok := g.(int64); ok {
			g = int(n)
		}
		if !reflect.DeepEqual(g, want[i]) {
			t.Fatalf("%s: column %d = %#v, want %#v", q, i, g, want[i])
		}
	}
}

// TestBuild runs the full Build pipeline against a small fixture and checks
// the semantics ported from scripts/build-db.ts: provision dedupe, EU
// reference extraction/inserts, is-primary tracking, EU mappings (including
// the missing-document skip), definitions IGNORE, metadata, and FTS triggers.
func TestBuild(t *testing.T) {
	dir := t.TempDir()
	seedDir := filepath.Join(dir, "seed")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeJSON(t, filepath.Join(seedDir, "001-act.json"), map[string]any{
		"id": "act-test-1", "title": "Teszt törvény", "type": "statute", "status": "in_force",
		"provisions": []map[string]any{
			{"provision_ref": " 3. § ", "section": "3", "content": "rövid"},
			{"provision_ref": "3. §", "section": "3", "content": "hosszabb tartalom, implement Regulation (EU) 2016/679"},
			{"provision_ref": "4. §", "section": "4", "content": "Directive 2011/83/EU szerint"},
			{"provision_ref": "4. §", "section": "4", "content": "másik rövid"},
			{"provision_ref": "5. §", "section": "5", "content": "további hivatkozás: implement Directive 2011/83/EU"},
			{"provision_ref": "6. §", "section": "6", "content": "meta adatok", "metadata": map[string]any{"oldal": 5}},
		},
		"definitions": []map[string]any{
			{"term": "adatkezelő", "definition": "aki adatot kezel"},
			{"term": "adatkezelő", "definition": "második definíció"},
		},
	})
	writeJSON(t, filepath.Join(seedDir, "002-act.json"), map[string]any{
		"id": "act-test-2", "title": "Teszt törvény 2", // no type/status -> defaults
	})
	mappingsPath := filepath.Join(dir, "eu-mappings.json")
	writeJSON(t, mappingsPath, []map[string]any{
		{
			"hungarian_document_id": "act-test-1", "eu_document_id": "directive:2011/83",
			"eu_type": "directive", "eu_year": 2011, "eu_number": 83, "eu_community": "EU",
			"eu_title": "Consumer Rights Directive", "eu_short_name": "CRD",
			"reference_type": "implements", "is_primary": true, "implementation_status": "complete",
		},
		{
			"hungarian_document_id": "missing-doc", "eu_document_id": "directive:2030/42",
			"eu_type": "directive", "eu_year": 2030, "eu_number": 42, "eu_community": "EU",
			"eu_title": "Ghost Directive", "eu_short_name": "Ghost",
			"reference_type": "references", "is_primary": false, "implementation_status": "unknown",
		},
	})

	outPath := filepath.Join(dir, "built.db")
	var logs strings.Builder
	logf := func(format string, args ...any) { fmt.Fprintf(&logs, format+"\n", args...) }
	if err := Build(outPath, seedDir, mappingsPath, logf); err != nil {
		t.Fatalf("Build: %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+outPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Documents and defaults.
	wantRow(t, db, `SELECT type, status FROM legal_documents WHERE id = 'act-test-2'`, "statute", "in_force")

	// Provision dedupe: longer content wins, ref trimmed, tie keeps first;
	// metadata JSON-stringified.
	rows := map[string]string{}
	for _, id := range []string{"3. §", "4. §", "5. §", "6. §"} {
		var content string
		if err := db.QueryRow(`SELECT content FROM legal_provisions WHERE document_id = 'act-test-1' AND provision_ref = ?`, id).Scan(&content); err != nil {
			t.Fatalf("provision %q: %v", id, err)
		}
		rows[id] = content
	}
	if rows["3. §"] != "hosszabb tartalom, implement Regulation (EU) 2016/679" {
		t.Errorf(`dedupe kept %q, want the longer content`, rows["3. §"])
	}
	if rows["4. §"] != "Directive 2011/83/EU szerint" {
		t.Errorf(`dedupe tie/loss kept %q, want the first content`, rows["4. §"])
	}
	if n := queryInt(t, db, `SELECT COUNT(*) FROM legal_provisions`); n != 4 {
		t.Errorf("provisions = %d, want 4 (6 entries, 2 duplicate refs deduped)", n)
	}
	wantRow(t, db, `SELECT metadata FROM legal_provisions WHERE document_id = 'act-test-1' AND provision_ref = '6. §'`, `{"oldal":5}`)

	// Definitions: INSERT OR IGNORE keeps the first, term_en NULL.
	wantRow(t, db, `SELECT definition, term_en FROM definitions WHERE document_id = 'act-test-1' AND term = 'adatkezelő'`, "aki adatot kezel", nil)

	// EU documents: two auto-extracted + the one from the skipped mapping;
	// the manual mapping re-insert of directive:2011/83 is IGNOREd.
	wantRow(t, db, `SELECT title, short_name, url_eur_lex, description, community FROM eu_documents WHERE id = 'regulation:2016/679'`,
		"Regulation 2016/679", "Regulation 2016/679", "https://eur-lex.europa.eu/eli/reg/2016/679/oj", "Auto-extracted from Hungarian statute text", "EU")
	wantRow(t, db, `SELECT title, short_name, url_eur_lex, description FROM eu_documents WHERE id = 'directive:2030/42'`,
		"Ghost Directive", "Ghost", nil, nil)
	if n := queryInt(t, db, `SELECT COUNT(*) FROM eu_documents`); n != 3 {
		t.Errorf("eu_documents = %d, want 3", n)
	}

	// EU references: seed-phase rows plus the document-level mapping row.
	wantRow(t, db,
		`SELECT is_primary_implementation, implementation_status, eu_article IS NULL, reference_type
		   FROM eu_references WHERE source_id = 'act-test-1:3. §' AND eu_document_id = 'regulation:2016/679'`,
		1, "complete", 1, "implements")
	wantRow(t, db,
		`SELECT is_primary_implementation, implementation_status, reference_type
		   FROM eu_references WHERE source_id = 'act-test-1:4. §' AND eu_document_id = 'directive:2011/83'`,
		0, "unknown", "references")
	// First 'implements' per (document, eu document) is primary — the 4. §
	// reference was 'references', so the 5. § one becomes the primary.
	wantRow(t, db,
		`SELECT is_primary_implementation, implementation_status
		   FROM eu_references WHERE source_id = 'act-test-1:5. §' AND eu_document_id = 'directive:2011/83'`,
		1, "complete")
	wantRow(t, db,
		`SELECT is_primary_implementation, implementation_status, reference_context, full_citation, provision_id IS NULL
		   FROM eu_references WHERE source_type = 'document' AND source_id = 'act-test-1'`,
		1, "complete", "Manual mapping: CRD", "Consumer Rights Directive", 1)
	if n := queryInt(t, db, `SELECT COUNT(*) FROM eu_references`); n != 4 {
		t.Errorf("eu_references = %d, want 4", n)
	}

	// Build metadata.
	wantRow(t, db, `SELECT value FROM db_metadata WHERE key = 'builder'`, "build-db.go")
	wantRow(t, db, `SELECT value FROM db_metadata WHERE key = 'tier'`, "free")
	wantRow(t, db, `SELECT value FROM db_metadata WHERE key = 'schema_version'`, "2")
	wantRow(t, db, `SELECT value FROM db_metadata WHERE key = 'jurisdiction'`, "HU")
	wantRow(t, db, `SELECT value FROM db_metadata WHERE key = 'source'`, "official-source")
	wantRow(t, db, `SELECT value FROM db_metadata WHERE key = 'licence'`, "See sources.yml")
	var builtAt string
	if err := db.QueryRow(`SELECT value FROM db_metadata WHERE key = 'built_at'`).Scan(&builtAt); err != nil || builtAt == "" {
		t.Fatalf("built_at = %q, %v", builtAt, err)
	}

	// FTS triggers kept the external-content table in sync.
	if n := queryInt(t, db, `SELECT COUNT(*) FROM provisions_fts WHERE provisions_fts MATCH 'tartalom'`); n < 1 {
		t.Errorf("provisions_fts MATCH 'tartalom' = %d, want >= 1", n)
	}
	if n := queryInt(t, db, `SELECT COUNT(*) FROM definitions_fts WHERE definitions_fts MATCH 'adatkezelő'`); n < 1 {
		t.Errorf("definitions_fts MATCH 'adatkezelő' = %d, want >= 1", n)
	}

	// Console output shape and the missing-document warning.
	wantSummary := "Build complete: 2 documents, 4 provisions, 2 definitions, 3 EU documents, 4 EU references"
	if !strings.Contains(logs.String(), wantSummary) {
		t.Errorf("summary %q not found in log:\n%s", wantSummary, logs.String())
	}
	if !strings.Contains(logs.String(), `EU mapping skipped: Hungarian document "missing-doc" not found in database`) {
		t.Errorf("missing-document warning not found in log:\n%s", logs.String())
	}
}

func TestBuildMissingSeedDir(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "built.db")
	var logs strings.Builder
	if err := Build(outPath, filepath.Join(dir, "nope"), filepath.Join(dir, "eu-mappings.json"),
		func(format string, args ...any) { fmt.Fprintf(&logs, format+"\n", args...) }); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(logs.String(), "No seed directory at") {
		t.Errorf("log missing empty-database notice:\n%s", logs.String())
	}

	db, err := sql.Open("sqlite", "file:"+outPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if n := queryInt(t, db, `SELECT COUNT(*) FROM legal_documents`); n != 0 {
		t.Errorf("legal_documents = %d, want 0", n)
	}
	if n := queryInt(t, db, `SELECT COUNT(*) FROM db_metadata`); n != 0 {
		t.Errorf("db_metadata = %d, want 0 (early return before metadata writes, as in TS)", n)
	}
}

func TestBuildEmptySeedDir(t *testing.T) {
	dir := t.TempDir()
	seedDir := filepath.Join(dir, "seed")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "built.db")
	var logs strings.Builder
	if err := Build(outPath, seedDir, filepath.Join(dir, "eu-mappings.json"),
		func(format string, args ...any) { fmt.Fprintf(&logs, format+"\n", args...) }); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(logs.String(), "No seed files found.") {
		t.Errorf("log missing no-seed-files notice:\n%s", logs.String())
	}
	db, err := sql.Open("sqlite", "file:"+outPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if n := queryInt(t, db, `SELECT COUNT(*) FROM legal_documents`); n != 0 {
		t.Errorf("legal_documents = %d, want 0", n)
	}
}

// TestBuildKeepsExistingDatabaseOnFailure pins the atomic publish: a failed
// rebuild must leave the previous database byte-identical and no .tmp
// droppings behind.
func TestBuildKeepsExistingDatabaseOnFailure(t *testing.T) {
	dir := t.TempDir()
	seedDir := filepath.Join(dir, "seed")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(seedDir, "001-act.json"), map[string]any{
		"id": "act-good", "title": "Jó törvény",
	})
	outPath := filepath.Join(dir, "built.db")
	quiet := func(string, ...any) {}
	if err := Build(outPath, seedDir, filepath.Join(dir, "eu-mappings.json"), quiet); err != nil {
		t.Fatalf("initial Build: %v", err)
	}
	before, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}

	// Second build fails on an unparsable seed file, mid-transaction.
	if err := os.WriteFile(filepath.Join(seedDir, "002-broken.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Build(outPath, seedDir, filepath.Join(dir, "eu-mappings.json"), quiet); err == nil {
		t.Fatal("second Build succeeded, want a seed parse error")
	}
	after, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("outPath changed after a failed build; the last-good database was clobbered")
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, "built.db*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 1 || leftovers[0] != outPath {
		t.Errorf("after failed build, dir holds %v, want only %s", leftovers, outPath)
	}
}

// TestBuildMissingEUMappingsWarns covers the absent-mappings warning (E8); a
// non-empty seed dir is required so the flow reaches the mappings block.
func TestBuildMissingEUMappingsWarns(t *testing.T) {
	dir := t.TempDir()
	seedDir := filepath.Join(dir, "seed")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(seedDir, "001-act.json"), map[string]any{
		"id": "act-good", "title": "Jó törvény",
	})
	var logs strings.Builder
	outPath := filepath.Join(dir, "built.db")
	if err := Build(outPath, seedDir, filepath.Join(dir, "absent.json"),
		func(format string, args ...any) { fmt.Fprintf(&logs, format+"\n", args...) }); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(logs.String(), "No EU mappings file at") {
		t.Errorf("log missing EU-mappings warning:\n%s", logs.String())
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Errorf("outPath not published: %v", err)
	}
}

// TestIsUniqueViolation pins the duplicate classification behind the tolerated
// EU-reference failures.
func TestIsUniqueViolation(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE t (v TEXT UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES ('x')`); err != nil {
		t.Fatal(err)
	}
	_, dup := db.Exec(`INSERT INTO t VALUES ('x')`)
	if dup == nil || !isUniqueViolation(dup) {
		t.Errorf("isUniqueViolation(%v) = false, want true", dup)
	}
	if _, err := db.Exec(`CREATE TABLE c (v TEXT CHECK (v <> 'bad'))`); err != nil {
		t.Fatal(err)
	}
	_, chk := db.Exec(`INSERT INTO c VALUES ('bad')`)
	if chk == nil || isUniqueViolation(chk) {
		t.Errorf("isUniqueViolation(%v) = true, want false", chk)
	}
}

func TestDedupeProvisions(t *testing.T) {
	got := dedupeProvisions([]seed.ProvisionSeed{
		{ProvisionRef: " 2. § ", Content: "első"},
		{ProvisionRef: "2. §", Content: "hosszabb második"},
		{ProvisionRef: "1. §", Content: "mindig megmarad"},
		{ProvisionRef: "1. §", Content: "rövidebb"}, // keeps existing on loss
	})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %#v", len(got), got)
	}
	if got[0].ProvisionRef != "2. §" || got[0].Content != "hosszabb második" {
		t.Errorf("out[0] = %#v, want trimmed ref with longer content", got[0])
	}
	if got[1].ProvisionRef != "1. §" || got[1].Content != "mindig megmarad" {
		t.Errorf("out[1] = %#v, want first-occurrence order preserved", got[1])
	}
}

package builddb

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/seed"
)

// TestEUReferenceInsertsZeroFailures walks the real seed corpus and replicates
// the build's seed-loop EU-reference inserts (same schema, same PRAGMAs, same
// OR-IGNORE semantics) to prove the swallowed-failure count is zero. Gated
// behind HU_EU_VERIFY=1 like the strip-verify walk: a full pass reads all of
// data/source-sized seed JSON.
//
// Baseline (2026-09-01, before the community normalization): exactly 2
// failures, both FOREIGN KEY on regulation:2005/302 from the uppercase
// citation community "EURATOM" (hu-law-2007-7-20-1u:s39,
// hu-law-2022-4-20-8l:s39). Every other insert error class reported here must
// stay at zero.
func TestEUReferenceInsertsZeroFailures(t *testing.T) {
	if os.Getenv("HU_EU_VERIFY") != "1" {
		t.Skip("set HU_EU_VERIFY=1 to walk data/seed and verify EU-reference inserts")
	}
	seedDir := "../../data/seed"
	if v := os.Getenv("HU_DATA_DIR"); v != "" {
		seedDir = filepath.Join(v, "seed")
	}

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	// The four tables the EU insert path touches, with the production CHECK,
	// FK and UNIQUE constraints from schema. FTS/definitions are omitted:
	// their triggers only slow the walk and cannot affect these inserts
	// (the corpus failures reproduced on exactly this subset).
	for _, ddl := range []string{
		`CREATE TABLE legal_documents (id TEXT PRIMARY KEY, type TEXT, title TEXT, status TEXT)`,
		`CREATE TABLE legal_provisions (id INTEGER PRIMARY KEY AUTOINCREMENT, document_id TEXT NOT NULL, provision_ref TEXT NOT NULL, section TEXT, title TEXT, content TEXT,
			UNIQUE(document_id, provision_ref))`,
		`CREATE TABLE eu_documents (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL CHECK (type IN ('directive', 'regulation')),
			year INTEGER NOT NULL CHECK (year >= 1957 AND year <= 2100),
			number INTEGER NOT NULL CHECK (number > 0),
			community TEXT CHECK (community IN ('EU', 'EC', 'EEC', 'Euratom')),
			title TEXT, short_name TEXT, url_eur_lex TEXT, description TEXT)`,
		`CREATE TABLE eu_references (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_type TEXT NOT NULL CHECK (source_type IN ('provision', 'document', 'case_law')),
			source_id TEXT NOT NULL,
			document_id TEXT NOT NULL REFERENCES legal_documents(id),
			provision_id INTEGER REFERENCES legal_provisions(id),
			eu_document_id TEXT NOT NULL REFERENCES eu_documents(id),
			eu_article TEXT,
			reference_type TEXT NOT NULL CHECK (reference_type IN ('implements', 'supplements', 'applies', 'references', 'complies_with', 'derogates_from', 'amended_by', 'repealed_by', 'cites_article')),
			reference_context TEXT,
			full_citation TEXT,
			is_primary_implementation BOOLEAN DEFAULT 0,
			implementation_status TEXT CHECK (implementation_status IN ('complete', 'partial', 'pending', 'unknown')),
			created_at TEXT DEFAULT CURRENT_TIMESTAMP,
			last_verified TEXT,
			UNIQUE(source_id, eu_document_id, eu_article))`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}

	insertDoc, err := db.Prepare(`INSERT INTO legal_documents
		(id, type, title, status) VALUES (?, ?, ?, ?)`)
	if err != nil {
		t.Fatalf("prepare doc: %v", err)
	}
	insertProvision, err := db.Prepare(`INSERT INTO legal_provisions
		(document_id, provision_ref, section, title, content) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatalf("prepare provision: %v", err)
	}
	insertEUDocument, err := db.Prepare(euDocumentInsertSQL)
	if err != nil {
		t.Fatalf("prepare eu doc: %v", err)
	}
	insertEUReference, err := db.Prepare(euReferenceInsertSQL)
	if err != nil {
		t.Fatalf("prepare eu ref: %v", err)
	}

	seedFiles, err := filepath.Glob(filepath.Join(seedDir, "*.json"))
	if err != nil || len(seedFiles) == 0 {
		t.Fatalf("no seed files under %s", seedDir)
	}

	failures := 0
	for _, name := range seedFiles {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var s seed.DocumentSeed
		if err := json.Unmarshal(data, &s); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s.Status == "" {
			s.Status = "in_force"
		}
		if _, err := insertDoc.Exec(s.ID, s.Type, s.Title, s.Status); err != nil {
			t.Fatalf("insert document %s: %v", s.ID, err)
		}
		for _, p := range dedupeProvisions(s.Provisions) {
			res, err := insertProvision.Exec(s.ID, p.ProvisionRef, p.Section, p.Title, p.Content)
			if err != nil {
				t.Fatalf("insert provision %s:%s: %v", s.ID, p.ProvisionRef, err)
			}
			provisionID, err := res.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			sourceID := s.ID + ":" + p.ProvisionRef
			for _, ref := range ExtractEUReferences(p.Content) {
				if _, err := insertEUDocument.Exec(ref.EUDocumentID, ref.Type, ref.Year, ref.Number, ref.Community,
					ref.EUDocumentID, ref.EUDocumentID, nil, "Auto-extracted from Hungarian statute text"); err != nil {
					t.Fatalf("insert eu_document %s: %v", ref.EUDocumentID, err)
				}
				var article any
				if ref.EUArticle != "" {
					article = ref.EUArticle
				}
				if _, err := insertEUReference.Exec("provision", sourceID, s.ID, provisionID, ref.EUDocumentID,
					article, ref.ReferenceType, ref.ReferenceContext, ref.FullCitation, 0, "unknown", "test"); err != nil {
					if strings.Contains(err.Error(), "UNIQUE constraint failed") {
						// Tolerated by the build (deduped by extraction within
						// a provision; cross-seed keys are distinct by
						// construction), so a violation here means a real bug.
						t.Errorf("%s: UNIQUE violation (duplicate EU reference key) %s -> %s: %v",
							name, sourceID, ref.EUDocumentID, err)
						continue
					}
					failures++
					if failures <= 10 {
						t.Errorf("%s: EU reference insert failed %s -> %s: %v",
							filepath.Base(name), sourceID, ref.EUDocumentID, err)
					}
				}
			}
		}
	}
	if failures > 0 {
		t.Fatalf("EU reference insert failures = %d, want 0", failures)
	}
}

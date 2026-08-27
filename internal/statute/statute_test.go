package statute

import (
	"context"
	"database/sql"
	"testing"

	// SQLite driver for the in-memory fixture DB.
	_ "modernc.org/sqlite"
)

// newTestDB opens an in-memory SQLite DB with the minimal schema used by
// resolveDocumentId, seeded to mirror tests/helpers/test-db.ts
// (doc-inforce/doc-amended/doc-repealed/doc-future, provisions p1(s1),
// p2(s2) under doc-inforce, p3(s3) under doc-amended). One extra doc
// ("hu-law-2012-1-00-00") exercises the Hungarian-formal conversion path;
// "doc-percent" (a % in its title) exercises LIKE-wildcard escaping.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// A single connection keeps every statement on the same :memory: database.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	const schema = `
CREATE TABLE legal_documents (
	id TEXT PRIMARY KEY,
	title TEXT,
	short_name TEXT,
	title_en TEXT,
	status TEXT
);
CREATE TABLE legal_provisions (
	id INTEGER PRIMARY KEY,
	document_id TEXT,
	provision_ref TEXT,
	section TEXT,
	title TEXT,
	content TEXT
);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	const docs = `INSERT INTO legal_documents (id, title, short_name, title_en, status) VALUES
('doc-inforce', 'In Force Act', 'IFA', 'In Force Act EN', 'in_force'),
('doc-amended', 'Amended Act', 'AA', 'Amended Act EN', 'amended'),
('doc-repealed', 'Repealed Act', 'RA', 'Repealed Act EN', 'repealed'),
('doc-future', 'Future Act', 'FA', 'Future Act EN', 'not_yet_in_force'),
('hu-law-2012-1-00-00', '2012. évi I. törvény (canonical)', 'T2012', 'Act I of 2012 EN', 'in_force'),
('doc-percent', '100% Guarantee Act', '100P', '100 Percent Act EN', 'in_force');`
	if _, err := db.Exec(docs); err != nil {
		t.Fatalf("seed docs: %v", err)
	}

	const provisions = `INSERT INTO legal_provisions (id, document_id, provision_ref, section, title, content) VALUES
(1, 'doc-inforce', 's1', '1', '1. §', 'A személyes adat kezelése és elektronikus aláírás szabályai.'),
(2, 'doc-inforce', 's2', '2', '2. §', 'Kiberbiztonsági intézkedések és információs rendszer védelem.'),
(3, 'doc-amended', 's3', '3', '3. §', 'Üzleti titok és létfontosságú infrastruktúra védelme.');`
	if _, err := db.Exec(provisions); err != nil {
		t.Fatalf("seed provisions: %v", err)
	}

	return db
}

func TestRomanToArabic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		roman string
		want  int
	}{
		{"single I", "I", 1},
		{"single V", "V", 5},
		{"subtractive IX", "IX", 9},
		{"subtractive XIV", "XIV", 14},
		{"infotörvény CXII", "CXII", 112},
		{"mixed MCMXCIV", "MCMXCIV", 1994},
		{"large MMXXV", "MMXXV", 2025},
		{"lowercase input", "xiv", 14},
		{"empty string", "", 0},
		{"all unknown characters", "ZZ", 0},
		{"unknown chars count 0, C still worth 100", "ABC", 100},
		{"unknown char counts 0 mid-numeral (TS quirk)", "IZ", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := RomanToArabic(tt.roman); got != tt.want {
				t.Errorf("RomanToArabic(%q) = %d, want %d", tt.roman, got, tt.want)
			}
		})
	}
}

func TestParseHungarianReference(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"canonical", "2012. évi I. törvény", "hu-law-2012-1-00-00"},
		{"cxii", "2011. évi CXII. törvény", "hu-law-2011-112-00-00"},
		{"no space after year dot", "2012.évi X. törvény", "hu-law-2012-10-00-00"},
		{"case-insensitive", "2012. ÉVI III. TÖRVÉNY", "hu-law-2012-3-00-00"},
		{"unanchored match inside longer text", "lásd a 2012. évi I. törvény 5. §-át", "hu-law-2012-1-00-00"},
		{"törvény word is required", "2012. évi I. tv", ""},
		{"no match", "Ptk.", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ParseHungarianReference(tt.input); got != tt.want {
				t.Errorf("ParseHungarianReference(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveDocumentId(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"whitespace only is null", "   ", ""},
		{"direct id", "doc-inforce", "doc-inforce"},
		{"title_en match", "In Force Act EN", "doc-inforce"},
		{"short_name match", "IFA", "doc-inforce"},
		{"fuzzy title substring", "Force Act", "doc-inforce"},
		{"hungarian formal reference converts to id", "2012. évi I. törvény", "hu-law-2012-1-00-00"},
		{"hungarian reference without space after year dot", "2012.évi I. törvény", "hu-law-2012-1-00-00"},
		{"hu-law prefix strips trailing garbage", "hu-law-2012-1-00-00 suffix junk", "hu-law-2012-1-00-00"},
		{"amended doc by id", "doc-amended", "doc-amended"},
		{"percent matches literal percent in title", "%", "doc-percent"},
		{"escaped percent matches its document", "100%", "doc-percent"},
		{"underscore stays literal, not a single-char wildcard", "In_Force", ""},
		{"no match", "non-existent statute", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveDocumentId(context.Background(), db, tt.input)
			if err != nil {
				t.Fatalf("ResolveDocumentId(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ResolveDocumentId(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// Replicates the TS test: even with case_sensitive_like ON, the LOWER()
// fallback still folds ASCII case.
func TestResolveDocumentIdCaseInsensitiveTitleFallback(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := db.Exec(`PRAGMA case_sensitive_like = ON`); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if _, err := db.Exec(`UPDATE legal_documents SET title = 'MIXEDCASE ACT' WHERE id = 'doc-inforce'`); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := ResolveDocumentId(context.Background(), db, "mixedcase act")
	if err != nil {
		t.Fatalf("ResolveDocumentId error: %v", err)
	}
	if got != "doc-inforce" {
		t.Errorf("ResolveDocumentId(%q) = %q, want %q", "mixedcase act", got, "doc-inforce")
	}
}

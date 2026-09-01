package statute

import (
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
// "doc-percent" (a % in its title) exercises LIKE-wildcard escaping. Two
// ministry rendeletek share the year/number pair 1/2017 to prove the decree
// shorthand refuses ambiguous input; the Korm. rendelet carries an annex
// provision stored under the parser's "4. melléklet" label.
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
('doc-percent', '100% Guarantee Act', '100P', '100 Percent Act EN', 'in_force'),
('hu-law-2009-210-20-22', '210/2009. (IX. 29.) Korm. rendelet a kereskedelmi tevékenységek végzésének feltételeiről', 'Kertv. r.', NULL, 'in_force'),
('hu-law-2017-1-20-22', '1/2017. (II. 6.) FM rendelet a növényvédő szerek engedélyezéséről', NULL, NULL, 'in_force'),
('hu-law-2017-1-20-23', '1/2017. (II. 7.) EMMI rendelet egyes egészségügyi kérdésekről', NULL, NULL, 'in_force'),
('hu-law-2017-457-20-22', '457/2017. (XI. 8.) Korm. rendelet a kereskedelmi tevékenységek végzésének feltételeiről szóló 210/2009. (IX. 29.) Korm. rendelet módosításáról', NULL, NULL, 'in_force'),
('hu-law-2016-73-20-22', '73/2016. (III. 31.) Korm. rendelet az egyházi jogi személyek vagyongazdálkodásának szabályairól', NULL, NULL, 'in_force');`
	if _, err := db.Exec(docs); err != nil {
		t.Fatalf("seed docs: %v", err)
	}

	const provisions = `INSERT INTO legal_provisions (id, document_id, provision_ref, section, title, content) VALUES
(1, 'doc-inforce', 's1', '1', '1. §', 'A személyes adat kezelése és elektronikus aláírás szabályai.'),
(2, 'doc-inforce', 's2', '2', '2. §', 'Kiberbiztonsági intézkedések és információs rendszer védelem.'),
(3, 'doc-amended', 's3', '3', '3. §', 'Üzleti titok és létfontosságú infrastruktúra védelme.'),
(4, 'hu-law-2009-210-20-22', 's4mellklet', '4. melléklet', '4. melléklet', 'Vendéglátóhely üzlettípusok: Étterem, Büfé, Cukrászda.');`
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

func TestResolveDocumentID(t *testing.T) {
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
		// Decree identifier shorthand: corpus titles carry the promulgation
		// date between the year/number and the type, so the plain substring
		// pass misses and the decree pass resolves instead.
		{"decree shorthand without date", "210/2009. Korm. rendelet", "hu-law-2009-210-20-22"},
		{"decree shorthand with date", "210/2009. (IX. 29.) Korm. rendelet", "hu-law-2009-210-20-22"},
		{"ministry decree shorthand", "1/2017. FM rendelet", "hu-law-2017-1-20-22"},
		{"ministry decree shorthand with date", "1/2017. (II. 7.) EMMI rendelet", "hu-law-2017-1-20-23"},
		{"shared year/number without type is ambiguous", "1/2017. rendelet", ""},
		{"decree year/number not in corpus", "73/2016. (XII. 2.) Korm. rendelet", ""},
		// The year/number pair exists, but the typed promulgation date does
		// not match the title — the citation is factually wrong and must not
		// silently resolve to the same-number decree with another date.
		{"typed date mismatch does not resolve", "73/2016. (XII. 2.) Korm. rendelet", ""},
		{"decree shorthand mid-sentence does not resolve", "lásd a 210/2009. Korm. rendeletet", ""},
		// A decree that only CITES 210/2009 mid-title must not count as a
		// second hit: the identifier pass anchors at the title start.
		{"amendment decree citing the identifier is not a competing hit", "210/2009. Korm. rendelet", "hu-law-2009-210-20-22"},
		{"no match", "non-existent statute", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveDocumentID(t.Context(), db, tt.input)
			if err != nil {
				t.Fatalf("ResolveDocumentID(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ResolveDocumentID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// Replicates the TS test: even with case_sensitive_like ON, the LOWER()
// fallback still folds ASCII case.
func TestResolveDocumentIDCaseInsensitiveTitleFallback(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	if _, err := db.Exec(`PRAGMA case_sensitive_like = ON`); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if _, err := db.Exec(`UPDATE legal_documents SET title = 'MIXEDCASE ACT' WHERE id = 'doc-inforce'`); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := ResolveDocumentID(t.Context(), db, "mixedcase act")
	if err != nil {
		t.Fatalf("ResolveDocumentID error: %v", err)
	}
	if got != "doc-inforce" {
		t.Errorf("ResolveDocumentID(%q) = %q, want %q", "mixedcase act", got, "doc-inforce")
	}
}

func TestSectionRefCandidates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  []string
	}{
		{"3", []string{"3", "s3"}},
		{"3. §", []string{"3", "s3"}},
		{"3.§", []string{"3", "s3"}},
		{"  3.  § ", []string{"3", "s3"}},
		{"§ 3", []string{"3", "s3"}},
		{"3. § (2)", []string{"3", "s3"}},
		{"s13", []string{"13", "s13"}},
		{"116/A. §", []string{"116/A", "s116a"}},
		{"6:272. §", []string{"6:272", "s6272", "6272", "272", "s272"}},
		{"3:99/A. §", []string{"3:99/A", "s399a", "399/A", "99/A", "s99a"}},
		{"1-290", []string{"1–290", "s1290"}},
		{"1–290. §", []string{"1–290", "s1290"}},
		{"Ptk4. §", []string{"Ptk4", "sptk4"}},
		{"Ptk. 4:1. §", []string{"Ptk4:1", "sptk41", "Ptk41"}},
		// Grammar miss: typed provision_ref kept as exact candidates.
		{"s13a", []string{"13a", "s13a"}},
		{"zzz", []string{"zzz", "szzz"}},
		// Annex refs: the section label the parser stores plus the ref form
		// (lowercased ASCII alnum after the "s" marker).
		{"4. melléklet", []string{"4. melléklet", "s4mellklet"}},
		{"4 melléklet", []string{"4. melléklet", "s4mellklet"}},
		{"4.melléklet", []string{"4. melléklet", "s4mellklet"}},
		{"3/A. melléklet", []string{"3/A. melléklet", "s3amellklet"}},
		{"15. MELLÉKLET", []string{"15. melléklet", "s15mellklet"}},
		// No usable reference at all.
		{"", nil},
		{"§", nil},
		{"   ", nil},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := SectionRefCandidates(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("SectionRefCandidates(%q) = %q, want %q", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("SectionRefCandidates(%q) = %q, want %q", tt.input, got, tt.want)
				}
			}
		})
	}
}

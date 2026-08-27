package tools

// Tests for the search_eu_implementations tool — port of the
// searchEUImplementations describes in tests/tools/other-tools.test.ts.

import (
	"context"
	"strings"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store/storetest"
)

func TestSearchEUImplementationsEUDocumentsUnavailable(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	euDropTable(t, db, "eu_documents")

	results, meta, err := SearchEUImplementations(context.Background(), db, argsMap(t, `{"query":"GDPR"}`))
	if err != nil {
		t.Fatal(err)
	}
	resultsDecoded, metaMap := euEnvelope(t, MarshalResponse(results, meta))
	if rows := euRows(t, resultsDecoded); len(rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(rows))
	}
	// Note the different word: "documents", not "references".
	if metaMap["note"] != "EU documents not available in this database tier" {
		t.Fatalf("note = %v", metaMap["note"])
	}
}

func TestSearchEUImplementationsAllFilters(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	results, _, err := SearchEUImplementations(context.Background(), db, argsMap(t,
		`{"query":"GDPR","type":"regulation","year_from":2015,"year_to":2016,`+
			`"has_hungarian_implementation":true,"limit":500}`))
	if err != nil {
		t.Fatal(err)
	}
	rows := euRows(t, results)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row["eu_document_id"] != "regulation:2016/679" || row["type"] != "regulation" {
		t.Fatalf("id/type = %v/%v", row["eu_document_id"], row["type"])
	}
	if euNum(t, row, "year") != 2016 || euNum(t, row, "number") != 679 {
		t.Fatalf("year/number = %v/%v", row["year"], row["number"])
	}
	if row["title"] != "GDPR" || row["short_name"] != "GDPR" {
		t.Fatalf("title/short_name = %v/%v", row["title"], row["short_name"])
	}
	// Referenced by doc-inforce AND doc-amended.
	if euNum(t, row, "hungarian_statute_count") != 2 {
		t.Fatalf("hungarian_statute_count = %v", row["hungarian_statute_count"])
	}
}

func TestSearchEUImplementationsDefaultLimitOrder(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	results, _, err := SearchEUImplementations(context.Background(), db, argsMap(t, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	rows := euRows(t, results)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	// ORDER BY year DESC → NIS2 (2022) before GDPR (2016).
	if rows[0]["eu_document_id"] != "directive:2022/2555" || rows[1]["eu_document_id"] != "regulation:2016/679" {
		t.Fatalf("order = %v, %v", rows[0]["eu_document_id"], rows[1]["eu_document_id"])
	}
}

func TestSearchEUImplementationsLimitClamp(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	cases := []struct {
		name  string
		limit string
		want  int
	}{
		{"zero clamps up to 1", "0", 1},
		{"negative clamps up to 1", "-5", 1},
		{"one", "1", 1},
		{"two", "2", 2},
		{"above corpus keeps all", "500", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, _, err := SearchEUImplementations(context.Background(), db, argsMap(t,
				`{"limit":`+tc.limit+`}`))
			if err != nil {
				t.Fatal(err)
			}
			if rows := euRows(t, results); len(rows) != tc.want {
				t.Fatalf("limit %s → rows = %d, want %d", tc.limit, len(rows), tc.want)
			}
		})
	}

	// The clamped-minimum result is the newest document (year DESC).
	results, _, err := SearchEUImplementations(context.Background(), db, argsMap(t, `{"limit":0}`))
	if err != nil {
		t.Fatal(err)
	}
	if rows := euRows(t, results); rows[0]["eu_document_id"] != "directive:2022/2555" {
		t.Fatalf("first row = %v", rows[0]["eu_document_id"])
	}
}

func TestSearchEUImplementationsQueryNoMatch(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	results, meta, err := SearchEUImplementations(context.Background(), db, argsMap(t, `{"query":"no-such-eu-doc"}`))
	if err != nil {
		t.Fatal(err)
	}
	// Literal empty array (not null) on the wire, mirroring the TS [].
	if payload := MarshalResponse(results, meta); !strings.Contains(payload, `"results":[]`) {
		t.Fatalf("expected literal empty array in payload, got %s", payload)
	}
	if rows := euRows(t, results); len(rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(rows))
	}
}

func TestSearchEUImplementationsHasHungarianImplementationFilterAndNulls(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	// An EU document with no Hungarian references and a NULL title.
	if _, err := db.Exec(
		`INSERT INTO eu_documents (id, type, year, number, title, short_name, description)
		 VALUES ('decision:1999/1', 'decision', 1999, 1, NULL, NULL, 'Orphan decision')`); err != nil {
		t.Fatal(err)
	}

	// Default: included, with explicit nulls on the wire.
	results, meta, err := SearchEUImplementations(context.Background(), db, argsMap(t, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	payload := MarshalResponse(results, meta)
	if rows := euRows(t, results); len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if !strings.Contains(payload, `"title":null`) || !strings.Contains(payload, `"short_name":null`) {
		t.Fatalf("expected explicit nulls in %s", payload)
	}

	// has_hungarian_implementation=true (HAVING count > 0): excluded.
	results, _, err = SearchEUImplementations(context.Background(), db, argsMap(t,
		`{"has_hungarian_implementation":true}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range euRows(t, results) {
		if row["eu_document_id"] == "decision:1999/1" {
			t.Fatalf("unreferenced EU document must be filtered out: %v", row)
		}
	}
}

func TestSearchEUImplementationsYearZeroMeansNoFilter(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	// TS falsy check: year_from = 0 adds no filter at all.
	results, _, err := SearchEUImplementations(context.Background(), db, argsMap(t,
		`{"year_from":0,"year_to":0}`))
	if err != nil {
		t.Fatal(err)
	}
	if rows := euRows(t, results); len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (0 must not filter)", len(rows))
	}
}

func TestSearchEUImplementationsClosedDB(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	db.Close()

	// Probe-first tool: closed DB → degraded empty result + note, no error.
	results, meta, err := SearchEUImplementations(context.Background(), db, argsMap(t, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	resultsDecoded, metaMap := euEnvelope(t, MarshalResponse(results, meta))
	if rows := euRows(t, resultsDecoded); len(rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(rows))
	}
	if metaMap["note"] != "EU documents not available in this database tier" {
		t.Fatalf("note = %v", metaMap["note"])
	}
}

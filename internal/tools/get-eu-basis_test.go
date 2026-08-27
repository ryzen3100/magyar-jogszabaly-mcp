package tools_test

// Tests for the get_eu_basis tool — port of the getEUBasis describes in
// tests/tools/other-tools.test.ts. Shared wire-level helpers for the
// tools_test package live at the top of this file.

import (
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store/storetest"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/tools"
)

// --- helpers shared by the tools_test package -------------------------------

// euDropTable drops a table from the fixture db (the Go equivalent of
// createTestDb({withEuTables:false})).
func euDropTable(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	if _, err := db.Exec("DROP TABLE " + table); err != nil {
		t.Fatalf("drop %s: %v", table, err)
	}
}

// euEnvelope unmarshals a MarshalResponse payload into its results and
// _metadata parts.
func euEnvelope(t *testing.T, payload string) (results any, meta map[string]any) {
	t.Helper()
	var env struct {
		Results  any            `json:"results"`
		Metadata map[string]any `json:"_metadata"`
	}
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\npayload: %s", err, payload)
	}
	return env.Results, env.Metadata
}

// euRows asserts the results marshal to an array of objects and returns them.
// Accepts a raw handler result or an already-decoded envelope value — either
// way the assertion runs against the wire JSON.
func euRows(t *testing.T, results any) []map[string]any {
	t.Helper()
	b, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("marshal results: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatalf("results not an array of objects (%v): %s", err, b)
	}
	return rows
}

// euObject asserts the results marshal to a single object (singular-result
// tools). Accepts a raw handler result or a decoded envelope value.
func euObject(t *testing.T, results any) map[string]any {
	t.Helper()
	b, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("marshal results: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("results not an object (%v): %s", err, b)
	}
	return obj
}

func euNum(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("key %q not a number: %v (%T)", key, m[key], m[key])
	}
	return v
}

func euStr(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key].(string)
	if !ok {
		t.Fatalf("key %q not a string: %v (%T)", key, m[key], m[key])
	}
	return v
}

// euStrs asserts a value is an array of strings.
func euStrs(t *testing.T, v any) []string {
	t.Helper()
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("not a string array: %v (%T)", v, v)
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			t.Fatalf("array element not a string: %v (%T)", e, e)
		}
		out = append(out, s)
	}
	return out
}

// euWantErr asserts the handler returned exactly the given error message.
func euWantErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %q, got nil", want)
	}
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// --- get_eu_basis ------------------------------------------------------------

func TestGetEUBasisUnresolvedDocument(t *testing.T) {
	db := storetest.NewTestDb(t)

	results, meta, err := tools.GetEUBasis(db, json.RawMessage(`{"document_id":"missing-doc"}`))
	if err != nil {
		t.Fatal(err)
	}
	payload := tools.MarshalResponse(results, meta)
	if !strings.Contains(payload, `"results":[]`) {
		t.Fatalf("expected literal empty array in payload, got %s", payload)
	}
	resultsDecoded, metaMap := euEnvelope(t, payload)
	if rows := euRows(t, resultsDecoded); len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
	// Unresolved document → NO note.
	if _, has := metaMap["note"]; has {
		t.Fatalf("unexpected note for unresolved document: %v", metaMap["note"])
	}
	if metaMap["freshness"] != "2026-02-21T00:00:00Z" {
		t.Fatalf("freshness = %v", metaMap["freshness"])
	}
}

func TestGetEUBasisEUTablesUnavailable(t *testing.T) {
	db := storetest.NewTestDb(t)
	euDropTable(t, db, "eu_references")

	results, meta, err := tools.GetEUBasis(db, json.RawMessage(`{"document_id":"doc-inforce"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, metaMap := euEnvelope(t, tools.MarshalResponse(results, meta)); metaMap["note"] != "EU references not available in this database tier" {
		t.Fatalf("note = %v", metaMap["note"])
	}
}

func TestGetEUBasisFiltersAndArticleExpansion(t *testing.T) {
	db := storetest.NewTestDb(t)

	results, _, err := tools.GetEUBasis(db, json.RawMessage(
		`{"document_id":"doc-inforce","include_articles":true,"reference_types":["implements"]}`))
	if err != nil {
		t.Fatal(err)
	}
	rows := euRows(t, results)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row["eu_document_id"] != "regulation:2016/679" {
		t.Fatalf("eu_document_id = %v", row["eu_document_id"])
	}
	if row["eu_document_type"] != "regulation" || row["eu_document_title"] != "GDPR" {
		t.Fatalf("type/title = %v/%v", row["eu_document_type"], row["eu_document_title"])
	}
	if row["reference_type"] != "implements" || row["implementation_status"] != "complete" {
		t.Fatalf("type/status = %v/%v", row["reference_type"], row["implementation_status"])
	}
	if euNum(t, row, "reference_count") != 1 {
		t.Fatalf("reference_count = %v", row["reference_count"])
	}
	articles := euStrs(t, row["articles"])
	if len(articles) != 1 || articles[0] != "Article 6" {
		t.Fatalf("articles = %v, want [Article 6]", articles)
	}
}

func TestGetEUBasisArticlesOmittedUnlessRequested(t *testing.T) {
	db := storetest.NewTestDb(t)

	// Without include_articles the key must be absent from the wire.
	results, meta, err := tools.GetEUBasis(db, json.RawMessage(`{"document_id":"doc-inforce"}`))
	if err != nil {
		t.Fatal(err)
	}
	payload := tools.MarshalResponse(results, meta)
	if strings.Contains(payload, `"articles"`) {
		t.Fatalf("articles key must be omitted without include_articles: %s", payload)
	}
	if rows := euRows(t, results); len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}

	// With include_articles every row carries the key — [] when the document
	// has no non-NULL articles (doc-amended's two refs have NULL articles).
	results, meta, err = tools.GetEUBasis(db, json.RawMessage(
		`{"document_id":"doc-amended","include_articles":true}`))
	if err != nil {
		t.Fatal(err)
	}
	payload = tools.MarshalResponse(results, meta)
	if got := strings.Count(payload, `"articles":[]`); got != 2 {
		t.Fatalf(`"articles":[] occurrences = %d, want 2: %s`, got, payload)
	}
}

func TestGetEUBasisArticleExpansionSkipsNullsAndDedups(t *testing.T) {
	db := storetest.NewTestDb(t)
	// Extra articles for the same group; the NULL eu_article must be skipped.
	for _, article := range []any{"Article 9", nil} {
		if _, err := db.Exec(
			`INSERT INTO eu_references (document_id, provision_id, eu_document_id, eu_article, reference_type)
			 VALUES ('doc-inforce', 1, 'regulation:2016/679', ?, 'implements')`, article); err != nil {
			t.Fatal(err)
		}
	}

	results, _, err := tools.GetEUBasis(db, json.RawMessage(
		`{"document_id":"doc-inforce","include_articles":true}`))
	if err != nil {
		t.Fatal(err)
	}
	rows := euRows(t, results)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (grouped by eu_document_id+reference_type)", len(rows))
	}
	articles := euStrs(t, rows[0]["articles"])
	slices.Sort(articles)
	if !slices.Equal(articles, []string{"Article 6", "Article 9"}) {
		t.Fatalf("articles = %v, want [Article 6 Article 9]", articles)
	}
}

func TestGetEUBasisReferenceTypesFilter(t *testing.T) {
	db := storetest.NewTestDb(t)

	results, _, err := tools.GetEUBasis(db, json.RawMessage(
		`{"document_id":"doc-amended","reference_types":["references"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if rows := euRows(t, results); len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}

	results, _, err = tools.GetEUBasis(db, json.RawMessage(
		`{"document_id":"doc-amended","reference_types":["implements","references"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if rows := euRows(t, results); len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (IN filter)", len(rows))
	}

	results, _, err = tools.GetEUBasis(db, json.RawMessage(
		`{"document_id":"doc-amended","reference_types":["implements"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if rows := euRows(t, results); len(rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(rows))
	}
}

func TestGetEUBasisMissingArgument(t *testing.T) {
	db := storetest.NewTestDb(t)

	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{}`)} {
		_, _, err := tools.GetEUBasis(db, raw)
		euWantErr(t, err, `missing required argument "document_id"`)
	}
}

func TestGetEUBasisClosedDB(t *testing.T) {
	db := storetest.NewTestDb(t)
	db.Close() // resolve-first tool → the closed DB surfaces as an error

	if _, _, err := tools.GetEUBasis(db, json.RawMessage(`{"document_id":"doc-inforce"}`)); err == nil {
		t.Fatal("expected error on closed db")
	}
}

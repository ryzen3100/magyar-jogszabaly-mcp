package tools

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store/storetest"
)

// runSearchRaw invokes the exported SearchLegislation handler with raw JSON
// and returns the marshaled envelope (or the handler error).
func runSearchRaw(t *testing.T, db *sql.DB, rawArgs string) (string, error) {
	t.Helper()
	results, meta, err := SearchLegislation(db, json.RawMessage(rawArgs))
	if err != nil {
		return "", err
	}
	return MarshalResponse(results, meta), nil
}

func TestSearchLegislationEmptyQuery(t *testing.T) {
	db := storetest.NewTestDb(t)

	for _, query := range []string{``, `{"query": ""}`, `{"query": "   "}`, `{}`} {
		out, err := runSearchRaw(t, db, query)
		if err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		if !strings.Contains(out, `"results":[]`) {
			t.Errorf("query %q: expected empty results, got %s", query, out)
		}
	}
}

func TestSearchLegislationFindsProvisions(t *testing.T) {
	db := storetest.NewTestDb(t)

	out, err := runSearchRaw(t, db, `{"query": "személyes adat"}`)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Results []SearchLegislationResult `json:"results"`
		Meta    ResponseMetadata          `json:"_metadata"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(env.Results) < 1 {
		t.Fatalf("expected at least one hit, got %s", out)
	}
	top := env.Results[0]
	if top.DocumentID != "doc-inforce" {
		t.Errorf("document_id = %q, want doc-inforce", top.DocumentID)
	}
	if top.ProvisionRef != "s1" {
		t.Errorf("provision_ref = %q, want s1", top.ProvisionRef)
	}
	if !strings.Contains(top.Snippet, ">>>személyes adat<<<") {
		t.Errorf("snippet missing highlight markers: %q", top.Snippet)
	}
	if top.Relevance >= 0 {
		t.Errorf("bm25 relevance should be negative, got %v", top.Relevance)
	}
	if !strings.Contains(out, `"freshness":"2026-02-21T00:00:00Z"`) {
		t.Errorf("expected freshness from db_metadata, got %s", out)
	}
}

func TestSearchLegislationBroadenedStrategy(t *testing.T) {
	db := storetest.NewTestDb(t)

	// No single provision contains both terms, so only the OR variant hits.
	out, err := runSearchRaw(t, db, `{"query": "személyes kiberbiztonsági"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"query_strategy":"broadened"`) {
		t.Errorf("expected query_strategy broadened, got %s", out)
	}
	var env struct {
		Results []SearchLegislationResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Results) != 2 {
		t.Errorf("expected both matching provisions, got %d: %s", len(env.Results), out)
	}
}

func TestSearchLegislationLikeFallback(t *testing.T) {
	db := storetest.NewTestDb(t)

	// "zemélyes" is a substring of content but not an FTS token or prefix,
	// so FTS finds nothing and the LIKE tier takes over.
	out, err := runSearchRaw(t, db, `{"query": "zemélyes"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"query_strategy":"like_fallback"`) {
		t.Errorf("expected query_strategy like_fallback, got %s", out)
	}
	if !strings.Contains(out, `"relevance":0`) {
		t.Errorf("LIKE-tier rows must carry relevance 0, got %s", out)
	}
	var env struct {
		Results []SearchLegislationResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Results) != 1 || !strings.Contains(env.Results[0].Snippet, "személyes adat") {
		t.Errorf("unexpected LIKE results: %s", out)
	}
}

func TestSearchLegislationUnresolvableDocumentFilter(t *testing.T) {
	db := storetest.NewTestDb(t)

	results, meta, err := SearchLegislation(db, json.RawMessage(`{"query": "személyes", "document_id": "missing-doc"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := results.([]SearchLegislationResult); len(got) != 0 {
		t.Errorf("expected empty results, got %v", got)
	}
	want := `No document found matching "missing-doc"`
	if meta.Note != want {
		t.Errorf("note = %q, want %q", meta.Note, want)
	}
}

func TestSearchLegislationDocumentAndStatusFilters(t *testing.T) {
	db := storetest.NewTestDb(t)

	// "védelem" matches doc-inforce s2 and doc-amended s3.
	out, err := runSearchRaw(t, db, `{"query": "védelem", "document_id": "In Force Act"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, `"document_id":"doc-inforce"`) != 1 || strings.Contains(out, `"document_id":"doc-amended"`) {
		t.Errorf("document filter not applied: %s", out)
	}

	out, err = runSearchRaw(t, db, `{"query": "titok", "status": "amended"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"document_id":"doc-amended"`) {
		t.Errorf("status filter dropped amended hit: %s", out)
	}
	out, err = runSearchRaw(t, db, `{"query": "személyes", "status": "repealed"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"results":[]`) {
		t.Errorf("repealed filter should hide in_force hit, got %s", out)
	}
}

func TestSearchLegislationLimitAndDedup(t *testing.T) {
	db := storetest.NewTestDb(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("exec %q: %v", query, err)
		}
	}
	// Three extra matching provisions to exercise the limit cut.
	for i := 1; i <= 3; i++ {
		exec(`INSERT INTO legal_provisions (document_id, provision_ref, chapter, section, title, content)
			VALUES ('doc-inforce', ?, NULL, ?, NULL, ?)`,
			fmt.Sprintf("sx%d", i), fmt.Sprintf("%d", i), fmt.Sprintf("Tesztszabály %d rendelkezése.", i))
		exec(`INSERT INTO provisions_fts(rowid, content, title)
			VALUES ((SELECT id FROM legal_provisions WHERE provision_ref = ?), ?, NULL)`,
			fmt.Sprintf("sx%d", i), fmt.Sprintf("Tesztszabály %d rendelkezése.", i))
	}

	out, err := runSearchRaw(t, db, `{"query": "Tesztszabály", "limit": 2}`)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Results []SearchLegislationResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Results) != 2 {
		t.Errorf("limit=2: got %d results", len(env.Results))
	}

	// Dedup: two documents sharing title + provision_ref collapse to one hit.
	exec(`INSERT INTO legal_documents (id, type, title, status) VALUES ('doc-dup-a', 'statute', 'Dup Title Act', 'in_force')`)
	exec(`INSERT INTO legal_documents (id, type, title, status) VALUES ('doc-dup-b', 'statute', 'Dup Title Act', 'in_force')`)
	for _, id := range []string{"doc-dup-a", "doc-dup-b"} {
		exec(`INSERT INTO legal_provisions (document_id, provision_ref, section, content) VALUES (?, 's9', '9', 'Egyedi kulcsszószabály.')`, id)
		exec(`INSERT INTO provisions_fts(rowid, content) VALUES ((SELECT id FROM legal_provisions WHERE document_id = ? AND provision_ref = 's9'), 'Egyedi kulcsszószabály.')`, id)
	}

	out, err = runSearchRaw(t, db, `{"query": "kulcsszószabály"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, `"provision_ref":"s9"`) != 1 {
		t.Errorf("dedup by title::ref failed: %s", out)
	}
}

func TestSearchLegislationDegradedOnClosedDb(t *testing.T) {
	db := storetest.NewTestDb(t)
	db.Close() // every query now fails — handlers must degrade, not error

	results, meta, err := SearchLegislation(db, json.RawMessage(`{"query": "személyes"}`))
	if err != nil {
		t.Fatalf("closed db must not error: %v", err)
	}
	if got := results.([]SearchLegislationResult); len(got) != 0 {
		t.Errorf("expected empty results, got %v", got)
	}
	if meta.DataSource == "" {
		t.Error("metadata must still be populated")
	}
}

func TestSearchLegislationBadArgs(t *testing.T) {
	db := storetest.NewTestDb(t)

	if _, _, err := SearchLegislation(db, json.RawMessage(`{"query": 123}`)); err == nil {
		t.Error("expected error for non-string query")
	}
}

// Real-database suite — port of tests/tools/search-legislation.test.ts,
// guarded like the TS describeIfRealDb.
func TestSearchLegislationRealDb(t *testing.T) {
	if !storetest.RealDBAvailable() {
		t.Skip("real database not available")
	}
	db, err := store.OpenReadOnly(storetest.RealDBPath())
	if err != nil {
		t.Fatalf("open real db: %v", err)
	}
	defer db.Close()

	cases := []struct {
		name  string
		query string
	}{
		{"personal data", "személyes adat"},
		{"cybersecurity", "kiberbiztonsági"},
		{"critical infrastructure", "létfontosságú"},
		{"trade secret", "üzleti titok"},
		{"electronic signature", "elektronikus aláírás"},
		{"gdpr", "általános adatvédelmi rendelet"},
		{"information system", "információs rendszer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, _, err := SearchLegislation(db, json.RawMessage(`{"query": `+quoteJSON(tc.query)+`}`))
			if err != nil {
				t.Fatal(err)
			}
			if got := len(results.([]SearchLegislationResult)); got < 1 {
				t.Errorf("query %q: got %d results", tc.query, got)
			}
		})
	}

	t.Run("gibberish and empty queries return empty", func(t *testing.T) {
		for _, q := range []string{"xyzzyflurble99", ""} {
			results, _, err := SearchLegislation(db, json.RawMessage(`{"query": `+quoteJSON(q)+`}`))
			if err != nil {
				t.Fatal(err)
			}
			if got := results.([]SearchLegislationResult); len(got) != 0 {
				t.Errorf("query %q: expected 0 results, got %d", q, len(got))
			}
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		results, _, err := SearchLegislation(db, json.RawMessage(`{"query": "biztonsági", "limit": 3}`))
		if err != nil {
			t.Fatal(err)
		}
		if got := len(results.([]SearchLegislationResult)); got > 3 {
			t.Errorf("limit=3: got %d results", got)
		}
	})

	t.Run("filters by document_id", func(t *testing.T) {
		results, _, err := SearchLegislation(db, json.RawMessage(`{"query": "biztonsági", "document_id": "act-l-2013-electronic-info-security"}`))
		if err != nil {
			t.Fatal(err)
		}
		got := results.([]SearchLegislationResult)
		if len(got) < 1 {
			t.Fatal("expected at least one result")
		}
		for _, r := range got {
			if r.DocumentID != "act-l-2013-electronic-info-security" {
				t.Errorf("foreign document in filtered results: %s", r.DocumentID)
			}
		}
	})

	t.Run("filters by status", func(t *testing.T) {
		results, _, err := SearchLegislation(db, json.RawMessage(`{"query": "adat", "status": "in_force"}`))
		if err != nil {
			t.Fatal(err)
		}
		if got := len(results.([]SearchLegislationResult)); got < 1 {
			t.Error("expected at least one in_force result")
		}
	})
}

// quoteJSON encodes s as a JSON string literal (tests only).
func quoteJSON(s string) string {
	bs, _ := json.Marshal(s)
	return string(bs)
}

package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store/storetest"
)

func TestBuildLegalStanceEmptyQuery(t *testing.T) {
	db := storetest.NewTestDb(t)

	out, err := runBuildLegalStanceJSON(t, db, `{"query": ""}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"results":[]`) {
		t.Errorf("expected empty results, got %s", out)
	}
}

func TestBuildLegalStanceFindsMatches(t *testing.T) {
	db := storetest.NewTestDb(t)

	out, err := runBuildLegalStanceJSON(t, db, `{"query": "személyes adat", "limit": 100}`)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Results []LegalStanceResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Results) < 1 || len(env.Results) > 20 {
		t.Errorf("expected 1..20 results, got %d: %s", len(env.Results), out)
	}
}

func TestBuildLegalStanceLimitCap(t *testing.T) {
	db := storetest.NewTestDb(t)
	for i := 1; i <= 21; i++ {
		content := fmt.Sprintf("Korlátozó rendelkezés száma: %d.", i)
		if _, err := db.Exec(`INSERT INTO legal_provisions (document_id, provision_ref, section, content)
			VALUES ('doc-inforce', ?, ?, ?)`, fmt.Sprintf("scap%d", i), fmt.Sprintf("%d", i), content); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO provisions_fts(rowid, content)
			VALUES ((SELECT id FROM legal_provisions WHERE provision_ref = ?), ?)`,
			fmt.Sprintf("scap%d", i), content); err != nil {
			t.Fatal(err)
		}
	}

	// limit 100 clamps to 20 — the search core's cap of 50 must not apply.
	out, err := runBuildLegalStanceJSON(t, db, `{"query": "Korlátozó rendelkezés", "limit": 100}`)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Results) != 20 {
		t.Errorf("limit 100 should cap at 20, got %d", len(env.Results))
	}
}

func TestBuildLegalStanceDocumentFilter(t *testing.T) {
	db := storetest.NewTestDb(t)

	out, err := runBuildLegalStanceJSON(t, db, `{"query": "adat", "document_id": "doc-inforce", "limit": 2}`)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Results []LegalStanceResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Results) < 1 {
		t.Fatalf("expected results, got %s", out)
	}
	for _, row := range env.Results {
		if row.DocumentID != "doc-inforce" {
			t.Errorf("foreign document %s", row.DocumentID)
		}
	}
}

func TestBuildLegalStanceStripsChapter(t *testing.T) {
	db := storetest.NewTestDb(t)

	out, err := runBuildLegalStanceJSON(t, db, `{"query": "személyes"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "chapter") {
		t.Errorf("chapter key must be stripped: %s", out)
	}
	// The search tool keeps it — the two result types really differ.
	searchOut, err := runSearchRaw(t, db, `{"query": "személyes"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(searchOut, `"chapter"`) {
		t.Errorf("search results should keep chapter: %s", searchOut)
	}
}

func TestBuildLegalStanceDegradedOnClosedDb(t *testing.T) {
	db := storetest.NewTestDb(t)
	db.Close()

	out, err := runBuildLegalStanceJSON(t, db, `{"query": "adat"}`)
	if err != nil {
		t.Fatalf("closed db must degrade, not error: %v", err)
	}
	if !strings.Contains(out, `"results":[]`) {
		t.Errorf("expected empty results, got %s", out)
	}
}

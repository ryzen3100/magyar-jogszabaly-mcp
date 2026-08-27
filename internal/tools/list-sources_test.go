package tools_test

// Tests for the list_sources tool — port of the listSources describes in
// tests/tools/other-tools.test.ts.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store/storetest"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/tools"
)

func TestListSourcesPopulated(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	results, meta, err := tools.ListSources(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	root := euObject(t, results)

	sources := sourcesList(t, root["sources"])
	if len(sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(sources))
	}
	src := sources[0]
	// The four descriptive strings, verbatim from src/tools/list-sources.ts.
	if src["name"] != "Nemzeti Jogszabálytár (National Legislation Database)" {
		t.Fatalf("name = %v", src["name"])
	}
	if src["authority"] != "Magyar Közlöny (Hungarian Official Gazette)" {
		t.Fatalf("authority = %v", src["authority"])
	}
	if src["url"] != "https://njt.hu" {
		t.Fatalf("url = %v", src["url"])
	}
	if src["license"] != "Official legal text publication (see portal terms at njt.hu)" {
		t.Fatalf("license = %v", src["license"])
	}
	if src["coverage"] != "Curated set of key Hungarian statutes covering data protection, cybersecurity, "+
		"electronic commerce, telecommunications, public procurement, trade secrets, "+
		"trust services, and criminal cybercrime provisions" {
		t.Fatalf("coverage = %v", src["coverage"])
	}
	if got := euStrs(t, src["languages"]); len(got) != 2 || got[0] != "hu" || got[1] != "en" {
		t.Fatalf("languages = %v, want [hu en]", got)
	}

	dbBlock := euObject(t, root["database"])
	if dbBlock["tier"] != "free" || dbBlock["schema_version"] != "1.0" {
		t.Fatalf("tier/schema_version = %v/%v", dbBlock["tier"], dbBlock["schema_version"])
	}
	if dbBlock["built_at"] != "2026-02-21T00:00:00Z" {
		t.Fatalf("built_at = %v", dbBlock["built_at"])
	}
	if euNum(t, dbBlock, "document_count") != 4 {
		t.Fatalf("document_count = %v, want 4", dbBlock["document_count"])
	}
	if euNum(t, dbBlock, "provision_count") != 3 {
		t.Fatalf("provision_count = %v, want 3", dbBlock["provision_count"])
	}

	if meta.Freshness != "2026-02-21T00:00:00Z" {
		t.Fatalf("meta freshness = %q", meta.Freshness)
	}
}

// sourcesList asserts a value is an array of source objects.
func sourcesList(t *testing.T, v any) []map[string]any {
	t.Helper()
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("sources not an array: %T (%v)", v, v)
	}
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("source not an object: %T (%v)", e, e)
		}
		out = append(out, m)
	}
	return out
}

func TestListSourcesBuiltAtOmittedWhenAbsent(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	euDropTable(t, db, "db_metadata")

	results, meta, err := tools.ListSources(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	payload := tools.MarshalResponse(results, meta)
	// TS drops the undefined key: built_at must be omitted, not null.
	if strings.Contains(payload, `"built_at"`) {
		t.Fatalf("built_at must be omitted when absent: %s", payload)
	}
	root := euObject(t, results)
	dbBlock := euObject(t, root["database"])
	// Defaults apply when db_metadata is missing entirely.
	if dbBlock["tier"] != "free" || dbBlock["schema_version"] != "1.0" {
		t.Fatalf("tier/schema_version = %v/%v, want free/1.0", dbBlock["tier"], dbBlock["schema_version"])
	}
	if meta.Freshness != "" {
		t.Fatalf("meta freshness = %q, want empty", meta.Freshness)
	}
}

func TestListSourcesCountsZeroWhenTablesMissing(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	euDropTable(t, db, "legal_provisions")

	results, _, err := tools.ListSources(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	root := euObject(t, results)
	dbBlock := euObject(t, root["database"])
	// Counts degrade to 0 on error (safeCount semantics), never throw.
	if euNum(t, dbBlock, "provision_count") != 0 {
		t.Fatalf("provision_count = %v, want 0", dbBlock["provision_count"])
	}
	if euNum(t, dbBlock, "document_count") != 4 {
		t.Fatalf("document_count = %v, want 4", dbBlock["document_count"])
	}
}

func TestListSourcesResultsJSONKeyOrder(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	results, _, err := tools.ListSources(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	// Field order = TS insertion order, including built_at in third position.
	got, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"sources":[{"name":"Nemzeti Jogszabálytár (National Legislation Database)",` +
		`"authority":"Magyar Közlöny (Hungarian Official Gazette)","url":"https://njt.hu",` +
		`"license":"Official legal text publication (see portal terms at njt.hu)",` +
		`"coverage":"Curated set of key Hungarian statutes covering data protection, cybersecurity, ` +
		`electronic commerce, telecommunications, public procurement, trade secrets, ` +
		`trust services, and criminal cybercrime provisions","languages":["hu","en"]}],` +
		`"database":{"tier":"free","schema_version":"1.0","built_at":"2026-02-21T00:00:00Z",` +
		`"document_count":4,"provision_count":3}}`
	if string(got) != want {
		t.Fatalf("results JSON mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestListSourcesClosedDB(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	db.Close()

	// listSources never throws: every read degrades to defaults/zero.
	results, _, err := tools.ListSources(context.Background(), db)
	if err != nil {
		t.Fatalf("closed db must degrade, not error: %v", err)
	}
	root := euObject(t, results)
	dbBlock := euObject(t, root["database"])
	if euNum(t, dbBlock, "document_count") != 0 || euNum(t, dbBlock, "provision_count") != 0 {
		t.Fatalf("counts = %v/%v, want 0/0", dbBlock["document_count"], dbBlock["provision_count"])
	}
}

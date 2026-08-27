package tools_test

// Tests for the about tool — port of the getAbout describes in
// tests/tools/other-tools.test.ts.

import (
	"context"
	"strings"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store/storetest"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/tools"
)

func TestGetAboutPopulated(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	about := &tools.AboutContext{
		Version:     "1.2.3",
		Fingerprint: "abcdef123456",
		DbBuilt:     "2026-02-21T00:00:00Z",
	}

	results, meta, err := tools.GetAbout(context.Background(), db, about)
	if err != nil {
		t.Fatal(err)
	}
	root := euObject(t, results)

	if root["name"] != "Hungarian Law MCP" {
		t.Fatalf("name = %v", root["name"])
	}
	if root["version"] != "1.2.3" {
		t.Fatalf("version = %v", root["version"])
	}
	if root["jurisdiction"] != "HU" {
		t.Fatalf("jurisdiction = %v", root["jurisdiction"])
	}

	// Long Hungarian description, verbatim from about.ts:33-53.
	desc := euStr(t, root, "description")
	const descPrefix = "Magyar jogszabály-adatbázis a Nemzeti Jogszabálytár (njt.hu) hivatalos forrásából, Model Context Protocol (MCP) interfészen keresztül."
	if !strings.HasPrefix(desc, descPrefix) {
		t.Fatalf("description prefix mismatch: %q", desc[:min(len(desc), 120)])
	}
	const descSuffix = "Figyelmeztetés: ez kutatási eszköz, nem jogi tanácsadás — kritikus hivatkozásokat mindig ellenőrizze a hivatalos forráson (njt.hu)."
	if !strings.HasSuffix(desc, descSuffix) {
		t.Fatalf("description suffix mismatch: %q", desc[max(0, len(desc)-120):])
	}
	for _, fragment := range []string{
		"Az adatbázis több mint 4 300 hatályos és hatályon kívüli magyar jogszabályt tartalmaz, 130 000+ szakasz-szintű bekezdéssel és 5 000+ jogszabályi definícióval.",
		"a teljes Polgári Törvénykönyv (Ptk. — 2013. évi V. tv.), az információs önrendelkezési törvény (Infotv. — 2011. évi CXII. tv.), a Munka Törvénykönyve (Mt. — 2012. évi I. tv.), a Büntető Törvénykönyv (Btk. — 2012. évi C. tv.)",
		"(GDPR 2016/679, NIS2 2022/2555, e-Privacy, Kereskedelmi titkok irányelve 2016/943 stb.)",
		"A keresés BM25 rangsorolású teljes szöveges kereséssel (FTS5) működik",
	} {
		if !strings.Contains(desc, fragment) {
			t.Fatalf("description missing fragment: %q", fragment)
		}
	}

	stats := euObject(t, root["stats"])
	// Fixture: 4 documents, 3 provisions, 1 definition, 2 EU docs, 3 EU refs.
	if euNum(t, stats, "documents") != 4 {
		t.Fatalf("documents = %v", stats["documents"])
	}
	if euNum(t, stats, "provisions") != 3 {
		t.Fatalf("provisions = %v", stats["provisions"])
	}
	if euNum(t, stats, "definitions") != 1 {
		t.Fatalf("definitions = %v", stats["definitions"])
	}
	if euNum(t, stats, "eu_documents") != 2 {
		t.Fatalf("eu_documents = %v", stats["eu_documents"])
	}
	if euNum(t, stats, "eu_references") != 3 {
		t.Fatalf("eu_references = %v", stats["eu_references"])
	}

	sources := sourcesList(t, root["data_sources"])
	if len(sources) != 1 {
		t.Fatalf("data_sources = %d, want 1", len(sources))
	}
	// NOTE: no accents here — deliberately different from list_sources.
	if sources[0]["name"] != "Nemzeti Jogszabalytar (NJT)" ||
		sources[0]["url"] != "https://njt.hu" ||
		sources[0]["authority"] != "Ministry of Justice" {
		t.Fatalf("data_sources[0] = %v", sources[0])
	}

	freshness := euObject(t, root["freshness"])
	if freshness["database_built"] != "2026-02-21T00:00:00Z" {
		t.Fatalf("database_built = %v", freshness["database_built"])
	}
	if meta.Freshness != "2026-02-21T00:00:00Z" {
		t.Fatalf("meta freshness = %q", meta.Freshness)
	}

	if root["disclaimer"] != "This is a research tool, not legal advice. Verify critical citations against official sources." {
		t.Fatalf("disclaimer = %v", root["disclaimer"])
	}
	network := euObject(t, root["network"])
	if network["name"] != "Ansvar MCP Network" ||
		network["open_law"] != "https://ansvar.eu/open-law" ||
		network["directory"] != "https://ansvar.ai/mcp" {
		t.Fatalf("network = %v", network)
	}
}

func TestGetAboutStatsKeyOrder(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	about := &tools.AboutContext{Version: "1.0.0", Fingerprint: "fp", DbBuilt: "2026-02-21T00:00:00Z"}

	results, meta, err := tools.GetAbout(context.Background(), db, about)
	if err != nil {
		t.Fatal(err)
	}
	payload := tools.MarshalResponse(results, meta)
	// stats key order = TS insertion order: documents, provisions,
	// definitions, then the appended EU keys.
	documents := strings.Index(payload, `"documents":4`)
	provisions := strings.Index(payload, `"provisions":3`)
	definitions := strings.Index(payload, `"definitions":1`)
	euDocuments := strings.Index(payload, `"eu_documents":2`)
	euReferences := strings.Index(payload, `"eu_references":3`)
	if documents < 0 || provisions < 0 || definitions < 0 || euDocuments < 0 || euReferences < 0 {
		t.Fatalf("stats keys missing in payload: %s", payload)
	}
	if !(documents < provisions && provisions < definitions && definitions < euDocuments && euDocuments < euReferences) {
		t.Fatalf("stats key order wrong: documents@%d provisions@%d definitions@%d eu_documents@%d eu_references@%d",
			documents, provisions, definitions, euDocuments, euReferences)
	}
}

func TestGetAboutMissingOptionalTables(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	euDropTable(t, db, "definitions")
	euDropTable(t, db, "eu_references")
	about := &tools.AboutContext{Version: "1.0.0", Fingerprint: "fp", DbBuilt: "2026-02-21T00:00:00Z"}

	results, meta, err := tools.GetAbout(context.Background(), db, about)
	if err != nil {
		t.Fatal(err)
	}
	payload := tools.MarshalResponse(results, meta)
	root := euObject(t, results)

	stats := euObject(t, root["stats"])
	if euNum(t, stats, "definitions") != 0 {
		t.Fatalf("definitions = %v, want 0", stats["definitions"])
	}
	if euNum(t, stats, "documents") != 4 || euNum(t, stats, "provisions") != 3 {
		t.Fatalf("documents/provisions = %v/%v, want 4/3", stats["documents"], stats["provisions"])
	}
	// EU stats keys are absent (not null, not 0) when eu_references is empty.
	if strings.Contains(payload, `"eu_documents"`) || strings.Contains(payload, `"eu_references"`) {
		t.Fatalf("EU stats keys must be absent: %s", payload)
	}
	// Sanity: the other stats keys are still there.
	if !strings.Contains(payload, `"documents":4`) || !strings.Contains(payload, `"definitions":0`) {
		t.Fatalf("expected stats in payload: %s", payload)
	}
}

func TestGetAboutNilContext(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	if _, _, err := tools.GetAbout(context.Background(), db, nil); err == nil {
		t.Fatal("expected error for nil about context")
	}
}

func TestGetAboutClosedDB(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	db.Close()

	// CachedCount degrades to 0 on a closed db — never throws.
	about := &tools.AboutContext{Version: "1.0.0", Fingerprint: "fp", DbBuilt: "2026-02-21T00:00:00Z"}
	results, _, err := tools.GetAbout(context.Background(), db, about)
	if err != nil {
		t.Fatalf("closed db must degrade, not error: %v", err)
	}
	stats := euObject(t, euObject(t, results)["stats"])
	if euNum(t, stats, "documents") != 0 || euNum(t, stats, "provisions") != 0 || euNum(t, stats, "definitions") != 0 {
		t.Fatalf("stats = %v, want all zero", stats)
	}
}

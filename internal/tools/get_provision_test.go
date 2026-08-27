package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store/storetest"
)

func TestGetProvisionBySection(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	out, err := runHandlerJSON(t, GetProvision, db, `{"document_id": "doc-inforce", "section": "1"}`)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Results []ProvisionResult `json:"results"`
		Meta    ResponseMetadata  `json:"_metadata"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(env.Results) != 1 {
		t.Fatalf("expected exactly one provision, got %s", out)
	}
	p := env.Results[0]
	if p.ProvisionRef != "s1" || p.Section != "1" {
		t.Errorf("ref/section = %q/%q, want s1/1", p.ProvisionRef, p.Section)
	}
	if !strings.Contains(p.Content, "személyes adat") {
		t.Errorf("content = %q", p.Content)
	}
	if p.DocumentTitle != "In Force Act" {
		t.Errorf("document_title = %q", p.DocumentTitle)
	}
	if p.URL == nil || !strings.Contains(*p.URL, "njt.hu") {
		t.Errorf("url = %v, want njt.hu link", p.URL)
	}
	if p.Chapter == nil || *p.Chapter != "I. Fejezet" {
		t.Errorf("chapter = %v, want I. Fejezet", p.Chapter)
	}
}

func TestGetProvisionByDirectRef(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	out, err := runHandlerJSON(t, GetProvision, db, `{"document_id": "doc-inforce", "provision_ref": "s2"}`)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Results []ProvisionResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Results) != 1 || env.Results[0].Section != "2" {
		t.Errorf("expected s2 provision, got %s", out)
	}
}

func TestGetProvisionAllProvisions(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	out, err := runHandlerJSON(t, GetProvision, db, `{"document_id": "doc-inforce"}`)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Results []ProvisionResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Results) != 2 {
		t.Fatalf("expected both provisions of doc-inforce, got %d", len(env.Results))
	}
	if env.Results[0].ProvisionRef != "s1" || env.Results[1].ProvisionRef != "s2" {
		t.Errorf("provisions not in id order: %s", out)
	}
}

func TestGetProvisionNotFoundNotes(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	_, meta, err := GetProvision(context.Background(), db, testArgs(t, `{"document_id": "doc-inforce", "section": "999"}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := `Provision "999" not found in document "doc-inforce"`; meta.Note != want {
		t.Errorf("note = %q, want %q", meta.Note, want)
	}

	_, meta, err = GetProvision(context.Background(), db, testArgs(t, `{"document_id": "missing-doc", "section": "1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := `No document found matching "missing-doc"`; meta.Note != want {
		t.Errorf("note = %q, want %q", meta.Note, want)
	}
}

func TestGetProvisionLikeSectionFallback(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	// Section text "37/A" — querying "7/A" must find it via the LIKE arm.
	if _, err := db.Exec(`INSERT INTO legal_provisions (document_id, provision_ref, chapter, section, title, content)
		VALUES ('doc-inforce', 's37/A', 'I. Fejezet', '37/A', NULL, 'Különös rendelkezés.')`); err != nil {
		t.Fatal(err)
	}

	out, err := runHandlerJSON(t, GetProvision, db, `{"document_id": "doc-inforce", "section": "7/A"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"section":"37/A"`) {
		t.Errorf("LIKE fallback failed: %s", out)
	}
}

func TestGetProvisionNullURLOmitted(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	if _, err := db.Exec(`INSERT INTO legal_documents (id, type, title, status, url)
		VALUES ('doc-null-url', 'statute', 'Null URL Act', 'in_force', NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO legal_provisions (document_id, provision_ref, section, content)
		VALUES ('doc-null-url', 's1', '1', 'text')`); err != nil {
		t.Fatal(err)
	}

	single, err := runHandlerJSON(t, GetProvision, db, `{"document_id": "doc-null-url", "section": "1"}`)
	if err != nil {
		t.Fatal(err)
	}
	list, err := runHandlerJSON(t, GetProvision, db, `{"document_id": "doc-null-url"}`)
	if err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{"single": single, "list": list} {
		if !strings.Contains(out, `"results":[{`) || !strings.Contains(out, `"content":"text"`) {
			t.Errorf("%s: unexpected shape %s", name, out)
		}
		if strings.Contains(out, `"url"`) {
			t.Errorf("%s: null url must be omitted, got %s", name, out)
		}
	}
}

func TestGetProvisionMissingRequiredArg(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	_, _, err := GetProvision(context.Background(), db, testArgs(t, `{}`))
	if err == nil || err.Error() != `missing required argument "document_id"` {
		t.Errorf("err = %v, want missing required argument", err)
	}
}

func TestGetProvisionDegradedOnClosedDb(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	db.Close()

	// Closed database is infrastructure failure, like the TS throw into the
	// registry catch → error envelope.
	if _, _, err := GetProvision(context.Background(), db,
		testArgs(t, `{"document_id": "doc-inforce", "section": "1"}`)); err == nil {
		t.Error("expected error from closed db")
	}
}

func TestGetProvisionAllProvisionsCapped(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	for i := 1; i <= maxProvisionsPerDocument+1; i++ {
		if _, err := db.Exec(`INSERT INTO legal_provisions (document_id, provision_ref, section, content)
			VALUES ('doc-inforce', ?, ?, 'text')`, fmt.Sprintf("scap%d", i), fmt.Sprintf("%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	results, meta, err := GetProvision(context.Background(), db, testArgs(t, `{"document_id": "doc-inforce"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(results.([]ProvisionResult)); got != maxProvisionsPerDocument {
		t.Errorf("results = %d, want cap %d", got, maxProvisionsPerDocument)
	}
	if meta.Note == "" {
		t.Error("expected truncation note in _metadata")
	}
}

// Real-database suite — port of tests/tools/get-provision.test.ts.
func TestGetProvisionRealDb(t *testing.T) {
	t.Parallel()
	if !storetest.RealDBAvailable() {
		dbSkippedTests++
		t.Skip("real database not available")
	}
	db, err := store.OpenReadOnly(storetest.RealDBPath())
	if err != nil {
		t.Fatalf("open real db: %v", err)
	}
	defer db.Close()

	t.Run("Infotörvény § 1", func(t *testing.T) {
		out, err := runHandlerJSON(t, GetProvision, db,
			`{"document_id": "act-cxii-2011-info-self-determination", "section": "1"}`)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"magánszféráját", "adatok szabad áramlása"} {
			if !strings.Contains(out, want) {
				t.Errorf("content missing %q: %s", want, out)
			}
		}
	})

	t.Run("Ibtv. § 11 incident reporting", func(t *testing.T) {
		out, err := runHandlerJSON(t, GetProvision, db,
			`{"document_id": "act-l-2013-electronic-info-security", "section": "11"}`)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"szervezet vezetője köteles gondoskodni", "biztonsági osztály"} {
			if !strings.Contains(out, want) {
				t.Errorf("content missing %q", want)
			}
		}
	})

	t.Run("Criminal Code § 422", func(t *testing.T) {
		out, err := runHandlerJSON(t, GetProvision, db, `{"document_id": "criminal-code-cybercrime", "section": "422"}`)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"jogosulatlan megismerése", "információs rendszer"} {
			if !strings.Contains(out, want) {
				t.Errorf("content missing %q", want)
			}
		}
	})

	t.Run("direct provision_ref s423", func(t *testing.T) {
		out, err := runHandlerJSON(t, GetProvision, db,
			`{"document_id": "criminal-code-cybercrime", "provision_ref": "s423"}`)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, `"section":"423"`) {
			t.Errorf("expected section 423, got %s", out)
		}
	})

	t.Run("section column hit 37/A", func(t *testing.T) {
		out, err := runHandlerJSON(t, GetProvision, db,
			`{"document_id": "act-cxii-2011-info-self-determination", "section": "37/A"}`)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, `"section":"37/A"`) {
			t.Errorf("expected 37/A, got %s", out)
		}
	})

	t.Run("LIKE fallback 7/A → 37/A", func(t *testing.T) {
		out, err := runHandlerJSON(t, GetProvision, db,
			`{"document_id": "act-cxii-2011-info-self-determination", "section": "7/A"}`)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, `"section":"37/`) {
			t.Errorf("expected 37/* section, got %s", out)
		}
	})

	t.Run("title_en fuzzy resolution", func(t *testing.T) {
		out, err := runHandlerJSON(t, GetProvision, db, `{"document_id": "Informational Self-Determination", "section": "1"}`)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, `"document_id":"act-cxii-2011-info-self-determination"`) {
			t.Errorf("expected Infotörvény resolution, got %s", out)
		}
	})

	t.Run("short_name resolution", func(t *testing.T) {
		out, err := runHandlerJSON(t, GetProvision, db, `{"document_id": "Ibtv.", "section": "1"}`)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, `"document_id":"act-l-2013-electronic-info-security"`) {
			t.Errorf("expected Ibtv. resolution, got %s", out)
		}
	})

	t.Run("non-existent document and provision return empty", func(t *testing.T) {
		for _, args := range []string{
			`{"document_id": "2099-evi-MMMM-torveny", "section": "1"}`,
			`{"document_id": "act-cxii-2011-info-self-determination", "section": "999ZZZ"}`,
		} {
			out, err := runHandlerJSON(t, GetProvision, db, args)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, `"results":[]`) {
				t.Errorf("args %s: expected empty results, got %s", args, out)
			}
		}
	})
}

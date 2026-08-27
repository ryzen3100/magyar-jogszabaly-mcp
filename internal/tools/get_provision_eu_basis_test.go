package tools

// Tests for the get_provision_eu_basis tool — port of the
// getProvisionEUBasis describes in tests/tools/other-tools.test.ts.

import (
	"context"
	"strings"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store/storetest"
)

func TestGetProvisionEUBasisUnresolvedDocWinsOverProbe(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	// Document resolution runs BEFORE the EU probe: an unresolved document
	// yields empty results with NO note even when the EU table is missing.
	euDropTable(t, db, "eu_references")

	results, meta, err := GetProvisionEUBasis(context.Background(), db, argsMap(t,
		`{"document_id":"missing-doc","provision_ref":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	resultsDecoded, metaMap := euEnvelope(t, MarshalResponse(results, meta))
	if rows := euRows(t, resultsDecoded); len(rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(rows))
	}
	if _, has := metaMap["note"]; has {
		t.Fatalf("unresolved document must not carry a note: %v", metaMap["note"])
	}
}

func TestGetProvisionEUBasisEUTablesUnavailable(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	euDropTable(t, db, "eu_references")

	results, meta, err := GetProvisionEUBasis(context.Background(), db, argsMap(t,
		`{"document_id":"doc-inforce","provision_ref":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, metaMap := euEnvelope(t, MarshalResponse(results, meta))
	if metaMap["note"] != "EU references not available in this database tier" {
		t.Fatalf("note = %v", metaMap["note"])
	}
}

func TestGetProvisionEUBasisMissingProvision(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	results, meta, err := GetProvisionEUBasis(context.Background(), db, argsMap(t,
		`{"document_id":"doc-inforce","provision_ref":"999"}`))
	if err != nil {
		t.Fatal(err)
	}
	resultsDecoded, metaMap := euEnvelope(t, MarshalResponse(results, meta))
	if rows := euRows(t, resultsDecoded); len(rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(rows))
	}
	if _, has := metaMap["note"]; has {
		t.Fatalf("missing provision must not carry a note: %v", metaMap["note"])
	}
}

func TestGetProvisionEUBasisFound(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	results, _, err := GetProvisionEUBasis(context.Background(), db, argsMap(t,
		`{"document_id":"doc-inforce","provision_ref":"1"}`))
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
	if row["eu_article"] != "Article 6" || row["reference_type"] != "implements" {
		t.Fatalf("article/type = %v/%v", row["eu_article"], row["reference_type"])
	}
	if row["reference_context"] != "Implements GDPR requirements." {
		t.Fatalf("reference_context = %v", row["reference_context"])
	}
	if row["full_citation"] != "Regulation (EU) 2016/679" {
		t.Fatalf("full_citation = %v", row["full_citation"])
	}

	// provision_ref matches the s-prefixed form ('s1') and the section number
	// alike, and the input is trimmed.
	for _, ref := range []string{"s1", " 1 "} {
		results, _, err = GetProvisionEUBasis(context.Background(), db, argsMap(t,
			`{"document_id":"doc-inforce","provision_ref":"`+ref+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		if rows = euRows(t, results); len(rows) != 1 {
			t.Fatalf("ref %q → rows = %d, want 1", ref, len(rows))
		}
	}
}

func TestGetProvisionEUBasisNullArticleAndOrdering(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	results, meta, err := GetProvisionEUBasis(context.Background(), db, argsMap(t,
		`{"document_id":"doc-amended","provision_ref":"s3"}`))
	if err != nil {
		t.Fatal(err)
	}
	payload := MarshalResponse(results, meta)
	rows := euRows(t, results)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	// ORDER BY reference_type, eu_document_id — both rows are 'references',
	// so directive:2022/2555 sorts before regulation:2016/679.
	if rows[0]["eu_document_id"] != "directive:2022/2555" || rows[1]["eu_document_id"] != "regulation:2016/679" {
		t.Fatalf("order = %v, %v", rows[0]["eu_document_id"], rows[1]["eu_document_id"])
	}
	if !strings.Contains(payload, `"eu_article":null`) {
		t.Fatalf("expected explicit null eu_article in %s", payload)
	}
	for _, row := range rows {
		if row["eu_article"] != nil {
			t.Fatalf("eu_article = %v, want null", row["eu_article"])
		}
	}
}

func TestGetProvisionEUBasisMissingArguments(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	_, _, err := GetProvisionEUBasis(context.Background(), db, nil)
	euWantErr(t, err, `missing required argument "document_id"`)

	_, _, err = GetProvisionEUBasis(context.Background(), db, argsMap(t, `{"document_id":"doc-inforce"}`))
	euWantErr(t, err, `missing required argument "provision_ref"`)
}

func TestGetProvisionEUBasisClosedDB(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	db.Close() // resolve-first tool → the closed DB surfaces as an error

	if _, _, err := GetProvisionEUBasis(context.Background(), db, argsMap(t,
		`{"document_id":"doc-inforce","provision_ref":"1"}`)); err == nil {
		t.Fatal("expected error on closed db")
	}
}

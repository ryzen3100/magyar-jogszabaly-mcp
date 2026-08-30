package tools

// Tests for the get_hungarian_implementations tool — port of the
// getHungarianImplementations describes in tests/tools/other-tools.test.ts.

import (
	"strings"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/store/storetest"
)

func TestGetHungarianImplementationsEUProbeRunsFirst(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	euDropTable(t, db, "eu_references")

	// The EU probe precedes everything — even an unresolvable eu_document_id
	// yields the tier note (not the silent empty result).
	results, meta, err := GetHungarianImplementations(t.Context(), db, argsMap(t,
		`{"eu_document_id":"nonexistent:0000/0"}`))
	if err != nil {
		t.Fatal(err)
	}
	resultsDecoded, metaMap := euEnvelope(t, MarshalResponse(results, meta))
	if rows := euRows(t, resultsDecoded); len(rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(rows))
	}
	if metaMap["note"] != "EU references not available in this database tier" {
		t.Fatalf("note = %v", metaMap["note"])
	}

	// With tables present, an unknown EU document is a silent empty result.
	db2 := storetest.NewTestDb(t)
	results, meta, err = GetHungarianImplementations(t.Context(), db2, argsMap(t,
		`{"eu_document_id":"nonexistent:0000/0"}`))
	if err != nil {
		t.Fatal(err)
	}
	resultsDecoded, metaMap = euEnvelope(t, MarshalResponse(results, meta))
	if rows := euRows(t, resultsDecoded); len(rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(rows))
	}
	if _, has := metaMap["note"]; has {
		t.Fatalf("unexpected note: %v", metaMap["note"])
	}
}

func TestGetHungarianImplementationsOrderAndValues(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	results, _, err := GetHungarianImplementations(t.Context(), db, argsMap(t,
		`{"eu_document_id":"regulation:2016/679"}`))
	if err != nil {
		t.Fatal(err)
	}
	rows := euRows(t, results)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}

	// ORDER BY is_primary DESC, reference_count DESC → doc-inforce first.
	first, second := rows[0], rows[1]
	if first["document_id"] != "doc-inforce" || second["document_id"] != "doc-amended" {
		t.Fatalf("order = %v, %v", first["document_id"], second["document_id"])
	}
	if first["document_title"] != "In Force Act" || first["status"] != "in_force" {
		t.Fatalf("first row title/status = %v/%v", first["document_title"], first["status"])
	}
	if first["reference_type"] != "implements" || first["implementation_status"] != "complete" {
		t.Fatalf("first row type/status = %v/%v", first["reference_type"], first["implementation_status"])
	}
	if euNum(t, first, "is_primary") != 1 || euNum(t, first, "reference_count") != 1 {
		t.Fatalf("first row is_primary/count = %v/%v", first["is_primary"], first["reference_count"])
	}
	if euNum(t, second, "is_primary") != 0 {
		t.Fatalf("second row is_primary = %v", second["is_primary"])
	}
}

func TestGetHungarianImplementationsPrimaryOnly(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	results, meta, err := GetHungarianImplementations(t.Context(), db, argsMap(t,
		`{"eu_document_id":"regulation:2016/679","primary_only":true}`))
	if err != nil {
		t.Fatal(err)
	}
	payload := MarshalResponse(results, meta)
	rows := euRows(t, results)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0]["document_id"] != "doc-inforce" {
		t.Fatalf("document_id = %v", rows[0]["document_id"])
	}
	// is_primary is a NUMBER on the wire (MAX over the SQLite integer column).
	if !strings.Contains(payload, `"is_primary":1`) {
		t.Fatalf("expected numeric is_primary in %s", payload)
	}
}

func TestGetHungarianImplementationsInForceOnly(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	results, _, err := GetHungarianImplementations(t.Context(), db, argsMap(t,
		`{"eu_document_id":"regulation:2016/679","in_force_only":true}`))
	if err != nil {
		t.Fatal(err)
	}
	rows := euRows(t, results)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0]["status"] != "in_force" {
		t.Fatalf("status = %v", rows[0]["status"])
	}
}

func TestGetHungarianImplementationsMissingArgument(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	_, _, err := GetHungarianImplementations(t.Context(), db, nil)
	euWantErr(t, err, `missing required argument "eu_document_id"`)
}

func TestGetHungarianImplementationsClosedDB(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	db.Close()

	// Probe-first tool: a closed DB behaves like a missing EU table (the TS
	// euAvailable catch swallows the probe error) — empty results + note, not
	// an error.
	results, meta, err := GetHungarianImplementations(t.Context(), db, argsMap(t,
		`{"eu_document_id":"regulation:2016/679"}`))
	if err != nil {
		t.Fatal(err)
	}
	resultsDecoded, metaMap := euEnvelope(t, MarshalResponse(results, meta))
	if rows := euRows(t, resultsDecoded); len(rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(rows))
	}
	if metaMap["note"] != "EU references not available in this database tier" {
		t.Fatalf("note = %v", metaMap["note"])
	}
}

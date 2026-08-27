package tools

// Tests for the validate_eu_compliance tool — port of the
// validateEUCompliance describes in tests/tools/other-tools.test.ts,
// extended to cover the full compliant/partial/unclear/not_applicable matrix
// with ad-hoc seeded rows.

import (
	"context"
	"slices"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/store/storetest"
)

// complianceResult decodes the singular result object of the tool.
func complianceResult(t *testing.T, results any, meta ResponseMetadata, err error) map[string]any {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return euObject(t, results)
}

func TestValidateEUComplianceStatusMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		documentID   string
		euFilter     string // optional eu_document_id filter, "" = none
		wantStatus   string
		wantFound    float64
		wantTitle    string // resolved title ("Unknown" when unresolved)
		wantEchoID   string // document_id echoed back (input as typed)
		wantWarnings []string
		wantRecs     []string
	}{
		{
			name:         "unresolved document echoes input as typed",
			documentID:   "missing-doc",
			wantStatus:   "not_applicable",
			wantFound:    0,
			wantTitle:    "Unknown",
			wantEchoID:   "missing-doc",
			wantWarnings: []string{`Document not found: "missing-doc"`},
			wantRecs:     []string{},
		},
		{
			name:         "resolved statute without EU references",
			documentID:   "doc-future",
			wantStatus:   "not_applicable",
			wantFound:    0,
			wantTitle:    "Future Act",
			wantEchoID:   "doc-future",
			wantWarnings: []string{},
			wantRecs: []string{
				"No EU cross-references found for this Hungarian statute. " +
					"Hungary is an EU Member State; EU references indicate transposition obligations.",
			},
		},
		{
			name:         "all complete statuses",
			documentID:   "doc-inforce",
			wantStatus:   "compliant",
			wantFound:    1,
			wantTitle:    "In Force Act",
			wantEchoID:   "doc-inforce",
			wantWarnings: []string{},
			wantRecs:     []string{},
		},
		{
			name:         "filtered by matching eu_document_id stays compliant",
			documentID:   "doc-inforce",
			euFilter:     "regulation:2016/679",
			wantStatus:   "compliant",
			wantFound:    1,
			wantTitle:    "In Force Act",
			wantEchoID:   "doc-inforce",
			wantWarnings: []string{},
			wantRecs:     []string{},
		},
		{
			name:         "filtered by non-matching eu_document_id counts zero",
			documentID:   "doc-inforce",
			euFilter:     "directive:2022/2555",
			wantStatus:   "not_applicable",
			wantFound:    0,
			wantTitle:    "In Force Act",
			wantEchoID:   "doc-inforce",
			wantWarnings: []string{},
			wantRecs: []string{
				"No EU cross-references found for this Hungarian statute. " +
					"Hungary is an EU Member State; EU references indicate transposition obligations.",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := storetest.NewTestDb(t)
			raw := `{"document_id":"` + tc.documentID + `"`
			if tc.euFilter != "" {
				raw += `,"eu_document_id":"` + tc.euFilter + `"`
			}
			raw += `}`
			results, meta, err := ValidateEUCompliance(context.Background(), db, argsMap(t, raw))
			row := complianceResult(t, results, meta, err)

			if row["compliance_status"] != tc.wantStatus {
				t.Fatalf("compliance_status = %v, want %s", row["compliance_status"], tc.wantStatus)
			}
			if euNum(t, row, "eu_references_found") != tc.wantFound {
				t.Fatalf("eu_references_found = %v, want %v", row["eu_references_found"], tc.wantFound)
			}
			if row["document_title"] != tc.wantTitle {
				t.Fatalf("document_title = %v, want %s", row["document_title"], tc.wantTitle)
			}
			if row["document_id"] != tc.wantEchoID {
				t.Fatalf("document_id = %v, want %s", row["document_id"], tc.wantEchoID)
			}
			if got := euStrs(t, row["warnings"]); !slices.Equal(got, tc.wantWarnings) {
				t.Fatalf("warnings = %v, want %v", got, tc.wantWarnings)
			}
			if got := euStrs(t, row["recommendations"]); !slices.Equal(got, tc.wantRecs) {
				t.Fatalf("recommendations = %v, want %v", got, tc.wantRecs)
			}
		})
	}
}

func TestValidateEUCompliancePartialAndRepealed(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	if _, err := db.Exec(
		`INSERT INTO eu_references (
			document_id, provision_id, eu_document_id, eu_article, reference_type,
			reference_context, full_citation, implementation_status, is_primary_implementation
		) VALUES ('doc-repealed', 1, 'directive:2022/2555', NULL, 'references', 'ctx', 'cite', 'partial', 0)`); err != nil {
		t.Fatal(err)
	}

	results, meta, err := ValidateEUCompliance(context.Background(), db,
		argsMap(t, `{"document_id":"doc-repealed"}`))
	row := complianceResult(t, results, meta, err)

	if row["compliance_status"] != "partial" {
		t.Fatalf("compliance_status = %v, want partial", row["compliance_status"])
	}
	if euNum(t, row, "eu_references_found") != 1 {
		t.Fatalf("eu_references_found = %v", row["eu_references_found"])
	}
	warnings := euStrs(t, row["warnings"])
	wantWarnings := []string{"This statute has been repealed.", "1 EU reference(s) have partial alignment status."}
	if !slices.Equal(warnings, wantWarnings) {
		t.Fatalf("warnings = %v, want %v", warnings, wantWarnings)
	}
	recs := euStrs(t, row["recommendations"])
	if !slices.Equal(recs, []string{"Check for replacement legislation."}) {
		t.Fatalf("recommendations = %v", recs)
	}
}

func TestValidateEUComplianceUnclearUnknownStatus(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	if _, err := db.Exec(
		`INSERT INTO legal_documents (
			id, type, title, title_en, short_name, status, issued_date, in_force_date, url, description
		) VALUES ('doc-unclear', 'statute', 'Unclear Act', 'Unclear Act EN', 'UA', 'in_force',
			'2024-01-01', '2024-06-01', 'https://njt.hu/jogszabaly/unclear', 'unclear')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO eu_references (
			document_id, provision_id, eu_document_id, eu_article, reference_type,
			reference_context, full_citation, implementation_status, is_primary_implementation
		) VALUES ('doc-unclear', 1, 'directive:2022/2555', NULL, 'references', 'ctx', 'cite', 'unknown', 0)`); err != nil {
		t.Fatal(err)
	}

	results, meta, err := ValidateEUCompliance(context.Background(), db, argsMap(t, `{"document_id":"doc-unclear"}`))
	row := complianceResult(t, results, meta, err)

	if row["compliance_status"] != "unclear" {
		t.Fatalf("compliance_status = %v, want unclear", row["compliance_status"])
	}
	if got := euStrs(t, row["warnings"]); len(got) != 0 {
		t.Fatalf("warnings = %v, want empty", got)
	}
	wantRecs := []string{"1 EU reference(s) have unknown alignment status. Manual review recommended."}
	if got := euStrs(t, row["recommendations"]); !slices.Equal(got, wantRecs) {
		t.Fatalf("recommendations = %v, want %v", got, wantRecs)
	}
}

func TestValidateEUComplianceUnclearNullStatusNoRecommendation(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	// A NULL implementation_status is neither complete, partial, nor unknown:
	// → unclear with NO recommendation (TS only adds one when unknown > 0).
	if _, err := db.Exec(
		`INSERT INTO eu_references (
			document_id, provision_id, eu_document_id, eu_article, reference_type,
			reference_context, full_citation, implementation_status, is_primary_implementation
		) VALUES ('doc-future', NULL, 'directive:2022/2555', NULL, 'references', NULL, NULL, NULL, 0)`); err != nil {
		t.Fatal(err)
	}

	results, meta, err := ValidateEUCompliance(context.Background(), db, argsMap(t, `{"document_id":"doc-future"}`))
	row := complianceResult(t, results, meta, err)

	if row["compliance_status"] != "unclear" {
		t.Fatalf("compliance_status = %v, want unclear", row["compliance_status"])
	}
	if got := euStrs(t, row["recommendations"]); len(got) != 0 {
		t.Fatalf("recommendations = %v, want empty", got)
	}
	if euNum(t, row, "eu_references_found") != 1 {
		t.Fatalf("eu_references_found = %v", row["eu_references_found"])
	}
}

func TestValidateEUComplianceDistributionIgnoresEUDocumentFilter(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	// doc-inforce already has one 'complete' reference; add an 'unknown' one
	// against a different EU document. The count IS filtered by
	// eu_document_id, the status distribution is NOT (TS quirk).
	if _, err := db.Exec(
		`INSERT INTO eu_references (
			document_id, provision_id, eu_document_id, eu_article, reference_type,
			reference_context, full_citation, implementation_status, is_primary_implementation
		) VALUES ('doc-inforce', 1, 'directive:2022/2555', NULL, 'references', 'ctx', 'cite', 'unknown', 0)`); err != nil {
		t.Fatal(err)
	}

	results, meta, err := ValidateEUCompliance(context.Background(), db, argsMap(t,
		`{"document_id":"doc-inforce","eu_document_id":"regulation:2016/679"}`))
	row := complianceResult(t, results, meta, err)

	if row["compliance_status"] != "unclear" {
		t.Fatalf("compliance_status = %v, want unclear (unknown ref must taint the distribution)", row["compliance_status"])
	}
	if euNum(t, row, "eu_references_found") != 1 {
		t.Fatalf("eu_references_found = %v, want 1 (count IS filtered)", row["eu_references_found"])
	}
	wantRecs := []string{"1 EU reference(s) have unknown alignment status. Manual review recommended."}
	if got := euStrs(t, row["recommendations"]); !slices.Equal(got, wantRecs) {
		t.Fatalf("recommendations = %v, want %v", got, wantRecs)
	}
}

func TestValidateEUComplianceEUTablesUnavailable(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	euDropTable(t, db, "eu_references")

	results, meta, err := ValidateEUCompliance(context.Background(), db, argsMap(t, `{"document_id":"doc-inforce"}`))
	row := complianceResult(t, results, meta, err)

	if row["compliance_status"] != "not_applicable" {
		t.Fatalf("compliance_status = %v", row["compliance_status"])
	}
	if row["document_id"] != "doc-inforce" || row["document_title"] != "In Force Act" {
		t.Fatalf("id/title = %v/%v", row["document_id"], row["document_title"])
	}
	wantWarnings := []string{"EU references not available in this database tier"}
	if got := euStrs(t, row["warnings"]); !slices.Equal(got, wantWarnings) {
		t.Fatalf("warnings = %v, want %v", got, wantWarnings)
	}
	if got := euStrs(t, row["recommendations"]); len(got) != 0 {
		t.Fatalf("recommendations = %v, want empty", got)
	}
}

func TestValidateEUComplianceMissingArgument(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	_, _, err := ValidateEUCompliance(context.Background(), db, argsMap(t, `{}`))
	euWantErr(t, err, `missing required argument "document_id"`)
}

func TestValidateEUComplianceClosedDB(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	db.Close() // resolve-first tool → the closed DB surfaces as an error

	if _, _, err := ValidateEUCompliance(context.Background(), db,
		argsMap(t, `{"document_id":"doc-inforce"}`)); err == nil {
		t.Fatal("expected error on closed db")
	}
}

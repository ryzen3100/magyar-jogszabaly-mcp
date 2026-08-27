// validate_eu_compliance — Check EU alignment status for an Hungarian statute.
// Port of src/tools/validate-eu-compliance.ts.

package tools

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/statute"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store"
)

type validateEUComplianceArgs struct {
	DocumentID   *string `json:"document_id"`
	EuDocumentID *string `json:"eu_document_id"`
}

// euComplianceResult mirrors the TS EUComplianceResult (singular result
// object). warnings/recommendations are always present as arrays.
type euComplianceResult struct {
	DocumentID        string   `json:"document_id"`
	DocumentTitle     string   `json:"document_title"`
	ComplianceStatus  string   `json:"compliance_status"`
	EuReferencesFound int      `json:"eu_references_found"`
	Warnings          []string `json:"warnings"`
	Recommendations   []string `json:"recommendations"`
}

// ValidateEUCompliance implements the validate_eu_compliance MCP tool. The
// decision ladder reproduces the TS order exactly: unresolved doc → EU probe
// fail → zero references → repealed → status distribution. It always returns
// a singular result — never empty results — and _metadata.note stays unset.
func ValidateEUCompliance(ctx context.Context, db *sql.DB, args map[string]any) (any, ResponseMetadata, error) {
	var parsed validateEUComplianceArgs
	if err := decodeArgs(args, &parsed); err != nil {
		return nil, ResponseMetadata{}, err
	}
	if err := validateArgs(
		checkRequired("document_id", parsed.DocumentID),
		checkMaxLength("document_id", parsed.DocumentID, maxDocumentIDLength),
		checkMaxLength("eu_document_id", parsed.EuDocumentID, maxEuDocumentIDLength),
	); err != nil {
		return nil, ResponseMetadata{}, err
	}

	resolvedID, err := statute.ResolveDocumentId(ctx, db, *parsed.DocumentID)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("resolve document: %w", err)
	}
	if resolvedID == "" {
		// The input is echoed exactly as typed (untrimmed).
		return euComplianceResult{
			DocumentID:        *parsed.DocumentID,
			DocumentTitle:     "Unknown",
			ComplianceStatus:  "not_applicable",
			EuReferencesFound: 0,
			Warnings:          []string{fmt.Sprintf("Document not found: \"%s\"", *parsed.DocumentID)},
			Recommendations:   []string{},
		}, GenerateResponseMetadata(ctx, db), nil
	}

	var docTitle, docStatus string
	if err := db.QueryRowContext(ctx, "SELECT title, status FROM legal_documents WHERE id = ?", resolvedID).
		Scan(&docTitle, &docStatus); err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("query document: %w", err)
	}

	if !store.EUAvailable(ctx, db, "eu_references") {
		return euComplianceResult{
			DocumentID:        resolvedID,
			DocumentTitle:     docTitle,
			ComplianceStatus:  "not_applicable",
			EuReferencesFound: 0,
			Warnings:          []string{"EU references not available in this database tier"},
			Recommendations:   []string{},
		}, GenerateResponseMetadata(ctx, db), nil
	}

	countSQL := "SELECT COUNT(*) as count FROM eu_references WHERE document_id = ?"
	countParams := []any{resolvedID}
	if parsed.EuDocumentID != nil && *parsed.EuDocumentID != "" {
		countSQL += " AND eu_document_id = ?"
		countParams = append(countParams, *parsed.EuDocumentID)
	}
	var euRefCount int
	if err := db.QueryRowContext(ctx, countSQL, countParams...).Scan(&euRefCount); err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("count eu references: %w", err)
	}

	if euRefCount == 0 {
		return euComplianceResult{
			DocumentID:        resolvedID,
			DocumentTitle:     docTitle,
			ComplianceStatus:  "not_applicable",
			EuReferencesFound: 0,
			Warnings:          []string{},
			Recommendations: []string{
				"No EU cross-references found for this Hungarian statute. " +
					"Hungary is an EU Member State; EU references indicate transposition obligations.",
			},
		}, GenerateResponseMetadata(ctx, db), nil
	}

	warnings := []string{}
	recommendations := []string{}

	if docStatus == "repealed" {
		warnings = append(warnings, "This statute has been repealed.")
		recommendations = append(recommendations, "Check for replacement legislation.")
	}

	// The status distribution is intentionally NOT filtered by
	// eu_document_id — same quirk as the TS original: the count above is
	// filtered, this GROUP BY is not.
	statusRows, err := db.QueryContext(ctx,
		"SELECT implementation_status, COUNT(*) as count FROM eu_references "+
			"WHERE document_id = ? GROUP BY implementation_status",
		resolvedID)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("query status distribution: %w", err)
	}
	statusCounts := map[string]int{}
	for statusRows.Next() {
		var (
			status sql.Null[string]
			n      int
		)
		if err := statusRows.Scan(&status, &n); err != nil {
			statusRows.Close()
			return nil, ResponseMetadata{}, fmt.Errorf("scan status count: %w", err)
		}
		key := ""
		if status.Valid {
			key = status.V
		}
		statusCounts[key] = n
	}
	statusRows.Close()
	if err := statusRows.Err(); err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("scan status counts: %w", err)
	}

	completeCount := statusCounts["complete"]
	partialCount := statusCounts["partial"]
	unknownCount := statusCounts["unknown"]

	var complianceStatus string
	switch {
	case completeCount > 0 && partialCount == 0 && unknownCount == 0:
		complianceStatus = "compliant"
	case partialCount > 0:
		complianceStatus = "partial"
		warnings = append(warnings, fmt.Sprintf("%d EU reference(s) have partial alignment status.", partialCount))
	default:
		complianceStatus = "unclear"
		if unknownCount > 0 {
			recommendations = append(recommendations,
				fmt.Sprintf("%d EU reference(s) have unknown alignment status. Manual review recommended.", unknownCount))
		}
	}

	return euComplianceResult{
		DocumentID:        resolvedID,
		DocumentTitle:     docTitle,
		ComplianceStatus:  complianceStatus,
		EuReferencesFound: euRefCount,
		Warnings:          warnings,
		Recommendations:   recommendations,
	}, GenerateResponseMetadata(ctx, db), nil
}

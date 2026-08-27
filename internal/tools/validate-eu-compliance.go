// validate_eu_compliance — Check EU alignment status for an Hungarian statute.
// Port of src/tools/validate-eu-compliance.ts.

package tools

import (
	"database/sql"
	"encoding/json"
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
// fail → zero references → repealed → status distribution.
func ValidateEUCompliance(db *sql.DB, rawArgs json.RawMessage) (any, ResponseMetadata, error) {
	var args validateEUComplianceArgs
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return nil, ResponseMetadata{}, err
		}
	}
	if args.DocumentID == nil {
		return nil, ResponseMetadata{}, fmt.Errorf("missing required argument %q", "document_id")
	}

	resolvedID, err := statute.ResolveDocumentId(db, *args.DocumentID)
	if err != nil {
		return nil, ResponseMetadata{}, err
	}
	if resolvedID == "" {
		// The input is echoed exactly as typed (untrimmed).
		return euComplianceResult{
			DocumentID:        *args.DocumentID,
			DocumentTitle:     "Unknown",
			ComplianceStatus:  "not_applicable",
			EuReferencesFound: 0,
			Warnings:          []string{fmt.Sprintf("Document not found: \"%s\"", *args.DocumentID)},
			Recommendations:   []string{},
		}, GenerateResponseMetadata(db), nil
	}

	var docID, docTitle, docStatus string
	if err := db.QueryRow("SELECT id, title, status FROM legal_documents WHERE id = ?", resolvedID).
		Scan(&docID, &docTitle, &docStatus); err != nil {
		return nil, ResponseMetadata{}, err
	}

	if !store.EUAvailable(db, "eu_references") {
		return euComplianceResult{
			DocumentID:        resolvedID,
			DocumentTitle:     docTitle,
			ComplianceStatus:  "not_applicable",
			EuReferencesFound: 0,
			Warnings:          []string{"EU references not available in this database tier"},
			Recommendations:   []string{},
		}, GenerateResponseMetadata(db), nil
	}

	countSQL := "SELECT COUNT(*) as count FROM eu_references WHERE document_id = ?"
	countParams := []any{resolvedID}
	if args.EuDocumentID != nil && *args.EuDocumentID != "" {
		countSQL += " AND eu_document_id = ?"
		countParams = append(countParams, *args.EuDocumentID)
	}
	var euRefCount int
	if err := db.QueryRow(countSQL, countParams...).Scan(&euRefCount); err != nil {
		return nil, ResponseMetadata{}, err
	}

	if euRefCount == 0 {
		return euComplianceResult{
			DocumentID:        resolvedID,
			DocumentTitle:     docTitle,
			ComplianceStatus:  "not_applicable",
			EuReferencesFound: 0,
			Warnings:          []string{},
			Recommendations: []string{
				"No EU cross-references found for this Hungarian statute. Hungary is an EU Member State; EU references indicate transposition obligations.",
			},
		}, GenerateResponseMetadata(db), nil
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
	statusRows, err := db.Query(
		"SELECT implementation_status, COUNT(*) as count FROM eu_references WHERE document_id = ? GROUP BY implementation_status",
		resolvedID)
	if err != nil {
		return nil, ResponseMetadata{}, err
	}
	statusCounts := map[string]int{}
	for statusRows.Next() {
		var (
			status sql.NullString
			n      int
		)
		if err := statusRows.Scan(&status, &n); err != nil {
			statusRows.Close()
			return nil, ResponseMetadata{}, err
		}
		key := ""
		if status.Valid {
			key = status.String
		}
		statusCounts[key] = n
	}
	statusRows.Close()
	if err := statusRows.Err(); err != nil {
		return nil, ResponseMetadata{}, err
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
	}, GenerateResponseMetadata(db), nil
}

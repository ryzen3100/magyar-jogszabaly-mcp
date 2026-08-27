// check_currency — Check whether an Hungarian statute is currently in force.
// Port of src/tools/check-currency.ts.
package tools

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/statute"
)

// checkCurrencyArgs mirrors CheckCurrencyInput.
type checkCurrencyArgs struct {
	DocumentID *string `json:"document_id"`
}

// CheckCurrencyResult — singular object result; issued_date/in_force_date
// stay explicit nulls (no omitempty). Field order matches the TS interface.
type CheckCurrencyResult struct {
	DocumentID  string   `json:"document_id"`
	Title       string   `json:"title"`
	Status      string   `json:"status"`
	IssuedDate  *string  `json:"issued_date"`
	InForceDate *string  `json:"in_force_date"`
	Warnings    []string `json:"warnings"`
}

// CheckCurrency is the exported handler for check_currency.
func CheckCurrency(db *sql.DB, rawArgs json.RawMessage) (any, ResponseMetadata, error) {
	var args checkCurrencyArgs
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
		return CheckCurrencyResult{
			DocumentID: *args.DocumentID,
			Title:      "Unknown",
			Status:     "not_found",
			Warnings:   []string{fmt.Sprintf("Document not found: \"%s\"", *args.DocumentID)},
		}, GenerateResponseMetadata(db), nil
	}

	var (
		id, title, status       string
		issuedDate, inForceDate sql.NullString
	)
	err = db.QueryRow(
		"SELECT id, title, status, issued_date, in_force_date FROM legal_documents WHERE id = ?",
		resolvedID,
	).Scan(&id, &title, &status, &issuedDate, &inForceDate)
	if err != nil {
		return nil, ResponseMetadata{}, err
	}

	warnings := []string{}
	if status == "repealed" {
		warnings = append(warnings, "This statute has been repealed and is no longer in force.")
	} else if status == "not_yet_in_force" {
		warnings = append(warnings, "This statute has not yet entered into force.")
	}

	return CheckCurrencyResult{
		DocumentID:  id,
		Title:       title,
		Status:      status,
		IssuedDate:  nullStringPtr(issuedDate),
		InForceDate: nullStringPtr(inForceDate),
		Warnings:    warnings,
	}, GenerateResponseMetadata(db), nil
}

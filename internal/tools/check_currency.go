// check_currency — Check whether an Hungarian statute is currently in force.
// Port of src/tools/check-currency.ts.
package tools

import (
	"context"
	"database/sql"
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
func CheckCurrency(ctx context.Context, db *sql.DB, args map[string]any) (any, ResponseMetadata, error) {
	var parsed checkCurrencyArgs
	if err := decodeArgs(args, &parsed); err != nil {
		return nil, ResponseMetadata{}, err
	}
	if parsed.DocumentID == nil {
		return nil, ResponseMetadata{}, fmt.Errorf("missing required argument %q", "document_id")
	}
	if err := checkMaxLength("document_id", parsed.DocumentID, maxDocumentIDLength); err != nil {
		return nil, ResponseMetadata{}, err
	}

	resolvedID, err := statute.ResolveDocumentId(ctx, db, *parsed.DocumentID)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("resolve document: %w", err)
	}
	if resolvedID == "" {
		return CheckCurrencyResult{
			DocumentID: *parsed.DocumentID,
			Title:      "Unknown",
			Status:     "not_found",
			Warnings:   []string{fmt.Sprintf("Document not found: \"%s\"", *parsed.DocumentID)},
		}, GenerateResponseMetadata(ctx, db), nil
	}

	var (
		id, title, status       string
		issuedDate, inForceDate sql.NullString
	)
	err = db.QueryRowContext(
		ctx,
		"SELECT id, title, status, issued_date, in_force_date FROM legal_documents WHERE id = ?",
		resolvedID,
	).Scan(&id, &title, &status, &issuedDate, &inForceDate)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("query document: %w", err)
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
	}, GenerateResponseMetadata(ctx, db), nil
}

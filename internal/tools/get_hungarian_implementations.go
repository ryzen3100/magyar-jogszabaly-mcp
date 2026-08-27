// get_hungarian_implementations — Find Hungarian statutes that reference a
// specific EU directive/regulation. Port of src/tools/get-hungarian-implementations.ts.

package tools

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store"
)

type getHungarianImplementationsArgs struct {
	EUDocumentID *string `json:"eu_document_id"`
	PrimaryOnly  *bool   `json:"primary_only"`
	InForceOnly  *bool   `json:"in_force_only"`
}

// hungarianImplementationResult mirrors the TS HungarianImplementationResult.
// The TS interface declares is_primary as boolean, but MAX() over the SQLite
// integer column puts 0/1 on the wire — reproduced as a number.
type hungarianImplementationResult struct {
	DocumentID           string  `json:"document_id"`
	DocumentTitle        string  `json:"document_title"`
	Status               string  `json:"status"`
	ReferenceType        string  `json:"reference_type"`
	ImplementationStatus *string `json:"implementation_status"`
	IsPrimary            int     `json:"is_primary"`
	ReferenceCount       int     `json:"reference_count"`
}

// GetHungarianImplementations implements the get_hungarian_implementations
// MCP tool. The EU-tier probe runs first, so a missing EU tier adds its
// availability note (_metadata.note) even for an unresolvable id; zero
// matching statutes return empty results with no note.
func GetHungarianImplementations(ctx context.Context, db *sql.DB, args map[string]any) (any, ResponseMetadata, error) {
	var parsed getHungarianImplementationsArgs
	if err := decodeArgs(args, &parsed); err != nil {
		return nil, ResponseMetadata{}, err
	}
	if err := validateArgs(
		checkRequired("eu_document_id", parsed.EUDocumentID),
		checkMaxLength("eu_document_id", parsed.EUDocumentID, maxEUDocumentIDLength),
	); err != nil {
		return nil, ResponseMetadata{}, err
	}

	// Unlike get_eu_basis, the EU probe runs FIRST — before anything else —
	// so even an unresolvable eu_document_id yields the tier note, not an
	// empty-no-note result, when the table is missing.
	if !store.EUAvailable(ctx, db, "eu_references") {
		meta := GenerateResponseMetadata(ctx, db)
		meta.Note = store.EUUnavailableNote("eu_references")
		return []hungarianImplementationResult{}, meta, nil
	}

	query := `
		SELECT
		  ld.id as document_id,
		  ld.title as document_title,
		  ld.status,
		  er.reference_type,
		  MAX(er.implementation_status) as implementation_status,
		  MAX(er.is_primary_implementation) as is_primary,
		  COUNT(*) as reference_count
		FROM eu_references er
		JOIN legal_documents ld ON ld.id = er.document_id
		WHERE er.eu_document_id = ?`
	params := []any{*parsed.EUDocumentID}

	if parsed.PrimaryOnly != nil && *parsed.PrimaryOnly {
		query += " AND er.is_primary_implementation = 1"
	}

	if parsed.InForceOnly != nil && *parsed.InForceOnly {
		query += " AND ld.status = 'in_force'"
	}

	query += " GROUP BY ld.id, er.reference_type ORDER BY is_primary DESC, reference_count DESC"

	rows, err := db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("query implementations: %w", err)
	}
	defer rows.Close()

	results := []hungarianImplementationResult{}
	for rows.Next() {
		var (
			r          hungarianImplementationResult
			implStatus sql.Null[string]
		)
		if err := rows.Scan(&r.DocumentID, &r.DocumentTitle, &r.Status, &r.ReferenceType,
			&implStatus, &r.IsPrimary, &r.ReferenceCount); err != nil {
			return nil, ResponseMetadata{}, fmt.Errorf("scan implementation: %w", err)
		}
		r.ImplementationStatus = nullStringPtr(implStatus)
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("scan implementations: %w", err)
	}

	return results, GenerateResponseMetadata(ctx, db), nil
}

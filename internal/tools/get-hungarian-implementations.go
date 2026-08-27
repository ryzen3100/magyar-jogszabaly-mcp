// get_hungarian_implementations — Find Hungarian statutes that reference a
// specific EU directive/regulation. Port of src/tools/get-hungarian-implementations.ts.

package tools

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store"
)

type getHungarianImplementationsArgs struct {
	EuDocumentID *string `json:"eu_document_id"`
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
// MCP tool.
func GetHungarianImplementations(db *sql.DB, rawArgs json.RawMessage) (any, ResponseMetadata, error) {
	var args getHungarianImplementationsArgs
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return nil, ResponseMetadata{}, err
		}
	}
	if args.EuDocumentID == nil {
		return nil, ResponseMetadata{}, fmt.Errorf("missing required argument %q", "eu_document_id")
	}

	// Unlike get_eu_basis, the EU probe runs FIRST — before anything else —
	// so even an unresolvable eu_document_id yields the tier note, not an
	// empty-no-note result, when the table is missing.
	if !store.EUAvailable(db, "eu_references") {
		meta := GenerateResponseMetadata(db)
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
	params := []any{*args.EuDocumentID}

	if args.PrimaryOnly != nil && *args.PrimaryOnly {
		query += " AND er.is_primary_implementation = 1"
	}

	if args.InForceOnly != nil && *args.InForceOnly {
		query += " AND ld.status = 'in_force'"
	}

	query += " GROUP BY ld.id, er.reference_type ORDER BY is_primary DESC, reference_count DESC"

	rows, err := db.Query(query, params...)
	if err != nil {
		return nil, ResponseMetadata{}, err
	}
	defer rows.Close()

	results := []hungarianImplementationResult{}
	for rows.Next() {
		var (
			r          hungarianImplementationResult
			implStatus sql.NullString
		)
		if err := rows.Scan(&r.DocumentID, &r.DocumentTitle, &r.Status, &r.ReferenceType,
			&implStatus, &r.IsPrimary, &r.ReferenceCount); err != nil {
			return nil, ResponseMetadata{}, err
		}
		if implStatus.Valid {
			r.ImplementationStatus = &implStatus.String
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, ResponseMetadata{}, err
	}

	return results, GenerateResponseMetadata(db), nil
}

// get_provision_eu_basis — Get EU legal basis for a specific provision.
// Port of src/tools/get-provision-eu-basis.ts.

package tools

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/statute"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store"
)

type getProvisionEUBasisArgs struct {
	DocumentID   *string `json:"document_id"`
	ProvisionRef *string `json:"provision_ref"`
}

// provisionEuBasisResult mirrors the TS ProvisionEUBasisResult; every
// nullable column is a pointer WITHOUT omitempty (explicit nulls).
type provisionEuBasisResult struct {
	EuDocumentID     string  `json:"eu_document_id"`
	EuDocumentType   *string `json:"eu_document_type"`
	EuDocumentTitle  *string `json:"eu_document_title"`
	EuArticle        *string `json:"eu_article"`
	ReferenceType    string  `json:"reference_type"`
	ReferenceContext *string `json:"reference_context"`
	FullCitation     *string `json:"full_citation"`
}

// GetProvisionEUBasis implements the get_provision_eu_basis MCP tool.
func GetProvisionEUBasis(ctx context.Context, db *sql.DB, args map[string]any) (any, ResponseMetadata, error) {
	var parsed getProvisionEUBasisArgs
	if err := decodeArgs(args, &parsed); err != nil {
		return nil, ResponseMetadata{}, err
	}
	if parsed.DocumentID == nil {
		return nil, ResponseMetadata{}, fmt.Errorf("missing required argument %q", "document_id")
	}
	if parsed.ProvisionRef == nil {
		return nil, ResponseMetadata{}, fmt.Errorf("missing required argument %q", "provision_ref")
	}
	if err := validateArgs(
		checkMaxLength("document_id", parsed.DocumentID, maxDocumentIDLength),
		checkMaxLength("provision_ref", parsed.ProvisionRef, maxRefLength),
	); err != nil {
		return nil, ResponseMetadata{}, err
	}

	// Order of checks: resolve document → EU probe → provision lookup. Both
	// the unresolved-document and missing-provision paths yield empty results
	// with NO note; only a failed EU probe adds the tier note.
	resolvedID, err := statute.ResolveDocumentId(ctx, db, *parsed.DocumentID)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("resolve document: %w", err)
	}
	if resolvedID == "" {
		return []provisionEuBasisResult{}, GenerateResponseMetadata(ctx, db), nil
	}

	if !store.EUAvailable(ctx, db, "eu_references") {
		meta := GenerateResponseMetadata(ctx, db)
		meta.Note = store.EUUnavailableNote("eu_references")
		return []provisionEuBasisResult{}, meta, nil
	}

	ref := strings.TrimSpace(*parsed.ProvisionRef)
	var provisionID int64
	err = db.QueryRowContext(
		ctx,
		"SELECT id FROM legal_provisions WHERE document_id = ? AND (provision_ref = ? OR provision_ref = ? OR section = ?)",
		resolvedID, ref, "s"+ref, ref,
	).Scan(&provisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return []provisionEuBasisResult{}, GenerateResponseMetadata(ctx, db), nil
	}
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("query provision: %w", err)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
		  er.eu_document_id,
		  ed.type as eu_document_type,
		  COALESCE(ed.title, ed.short_name) as eu_document_title,
		  er.eu_article,
		  er.reference_type,
		  er.reference_context,
		  er.full_citation
		FROM eu_references er
		LEFT JOIN eu_documents ed ON ed.id = er.eu_document_id
		WHERE er.provision_id = ?
		ORDER BY er.reference_type, er.eu_document_id`, provisionID)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("query eu references: %w", err)
	}
	defer rows.Close()

	results := []provisionEuBasisResult{}
	for rows.Next() {
		var (
			r            provisionEuBasisResult
			docType      sql.NullString
			title        sql.NullString
			article      sql.NullString
			referenceCtx sql.NullString
			fullCitation sql.NullString
		)
		if err := rows.Scan(&r.EuDocumentID, &docType, &title, &article,
			&r.ReferenceType, &referenceCtx, &fullCitation); err != nil {
			return nil, ResponseMetadata{}, fmt.Errorf("scan eu reference: %w", err)
		}
		if docType.Valid {
			r.EuDocumentType = &docType.String
		}
		if title.Valid {
			r.EuDocumentTitle = &title.String
		}
		if article.Valid {
			r.EuArticle = &article.String
		}
		if referenceCtx.Valid {
			r.ReferenceContext = &referenceCtx.String
		}
		if fullCitation.Valid {
			r.FullCitation = &fullCitation.String
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("scan eu references: %w", err)
	}

	return results, GenerateResponseMetadata(ctx, db), nil
}

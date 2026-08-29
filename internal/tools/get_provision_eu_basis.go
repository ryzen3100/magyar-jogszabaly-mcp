// get_provision_eu_basis — Get the EU legal basis for a single provision
// (provision-level). Port of src/tools/get-provision-eu-basis.ts.

package tools

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/statute"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/store"
)

type getProvisionEUBasisArgs struct {
	DocumentID   *string `json:"document_id"`
	ProvisionRef *string `json:"provision_ref"`
}

// provisionEUBasisResult mirrors the TS ProvisionEUBasisResult; every
// nullable column is a pointer WITHOUT omitempty (explicit nulls).
type provisionEUBasisResult struct {
	EUDocumentID     string  `json:"eu_document_id"`
	EUDocumentType   *string `json:"eu_document_type"`
	EUDocumentTitle  *string `json:"eu_document_title"`
	EUArticle        *string `json:"eu_article"`
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
	if err := validateArgs(
		checkRequired("document_id", parsed.DocumentID),
		checkRequired("provision_ref", parsed.ProvisionRef),
		checkMaxLength("document_id", parsed.DocumentID, maxDocumentIDLength),
		checkMaxLength("provision_ref", parsed.ProvisionRef, maxRefLength),
	); err != nil {
		return nil, ResponseMetadata{}, err
	}

	// Order of checks: resolve document → EU probe → provision lookup. Both
	// the unresolved-document and missing-provision paths yield empty results
	// with NO note; only a failed EU probe adds the tier note.
	resolvedID, err := statute.ResolveDocumentID(ctx, db, *parsed.DocumentID)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("resolve document: %w", err)
	}
	if resolvedID == "" {
		return []provisionEUBasisResult{}, GenerateResponseMetadata(ctx, db), nil
	}

	if !store.EUAvailable(ctx, db, "eu_references") {
		meta := GenerateResponseMetadata(ctx, db)
		meta.Note = store.EUUnavailableNote("eu_references")
		return []provisionEUBasisResult{}, meta, nil
	}

	// Exact-match candidates from the typed ref (statute.SectionRefCandidates:
	// "3. §" → section "3" / provision_ref "s3"); no fuzzy tier.
	candidates := statute.SectionRefCandidates(*parsed.ProvisionRef)
	var provisionID int64
	if len(candidates) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(candidates)), ",")
		args := make([]any, 0, 2*len(candidates)+1)
		args = append(args, resolvedID)
		for _, c := range candidates {
			args = append(args, c)
		}
		for _, c := range candidates {
			args = append(args, c)
		}
		err = db.QueryRowContext(
			ctx,
			"SELECT id FROM legal_provisions WHERE document_id = ? AND "+
				"(provision_ref IN ("+placeholders+") OR section IN ("+placeholders+")) "+
				"ORDER BY id LIMIT 1",
			args...,
		).Scan(&provisionID)
	} else {
		err = sql.ErrNoRows
	}
	if errors.Is(err, sql.ErrNoRows) {
		return []provisionEUBasisResult{}, GenerateResponseMetadata(ctx, db), nil
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

	results := []provisionEUBasisResult{}
	for rows.Next() {
		var (
			r            provisionEUBasisResult
			docType      sql.Null[string]
			title        sql.Null[string]
			article      sql.Null[string]
			referenceCtx sql.Null[string]
			fullCitation sql.Null[string]
		)
		if err := rows.Scan(&r.EUDocumentID, &docType, &title, &article,
			&r.ReferenceType, &referenceCtx, &fullCitation); err != nil {
			return nil, ResponseMetadata{}, fmt.Errorf("scan eu reference: %w", err)
		}
		r.EUDocumentType = nullStringPtr(docType)
		r.EUDocumentTitle = nullStringPtr(title)
		r.EUArticle = nullStringPtr(article)
		r.ReferenceContext = nullStringPtr(referenceCtx)
		r.FullCitation = nullStringPtr(fullCitation)
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("scan eu references: %w", err)
	}

	return results, GenerateResponseMetadata(ctx, db), nil
}

// get_eu_basis — Get the EU legal basis for a statute as a whole
// (statute-level). Port of src/tools/get-eu-basis.ts.

package tools

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/statute"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store"
)

type getEUBasisArgs struct {
	DocumentID      *string  `json:"document_id"`
	IncludeArticles *bool    `json:"include_articles"`
	ReferenceTypes  []string `json:"reference_types"`
}

// euBasisResult mirrors the TS EUBasisResult row: field order = SELECT order,
// with `articles` appended last (TS assigns it after the query). Nullable
// columns are pointers WITHOUT omitempty (TS keeps explicit nulls); Articles
// is *[]string so the key is omitted unless include_articles ran on a
// non-empty result, and marshals as [] (not omitted) when a row has no
// articles.
type euBasisResult struct {
	EUDocumentID         string    `json:"eu_document_id"`
	EUDocumentType       *string   `json:"eu_document_type"`
	EUDocumentTitle      *string   `json:"eu_document_title"`
	ReferenceType        string    `json:"reference_type"`
	ReferenceCount       int       `json:"reference_count"`
	ImplementationStatus *string   `json:"implementation_status"`
	Articles             *[]string `json:"articles,omitempty"`
}

// GetEUBasis implements the get_eu_basis MCP tool.
func GetEUBasis(ctx context.Context, db *sql.DB, args map[string]any) (any, ResponseMetadata, error) {
	var parsed getEUBasisArgs
	if err := decodeArgs(args, &parsed); err != nil {
		return nil, ResponseMetadata{}, err
	}
	if err := validateArgs(
		checkRequired("document_id", parsed.DocumentID),
		checkMaxLength("document_id", parsed.DocumentID, maxDocumentIDLength),
		checkStringList("reference_types", parsed.ReferenceTypes, maxReferenceTypes, maxReferenceTypeLen),
	); err != nil {
		return nil, ResponseMetadata{}, err
	}

	// Order of checks matters (as in the TS original): document resolution
	// first — unresolved → empty results, no note — then the EU probe.
	resolvedID, err := statute.ResolveDocumentID(ctx, db, *parsed.DocumentID)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("resolve document: %w", err)
	}
	if resolvedID == "" {
		return []euBasisResult{}, GenerateResponseMetadata(ctx, db), nil
	}
	if !store.EUAvailable(ctx, db, "eu_references") {
		meta := GenerateResponseMetadata(ctx, db)
		meta.Note = store.EUUnavailableNote("eu_references")
		return []euBasisResult{}, meta, nil
	}

	query := `
		SELECT
		  er.eu_document_id,
		  ed.type as eu_document_type,
		  COALESCE(ed.title, ed.short_name) as eu_document_title,
		  er.reference_type,
		  COUNT(*) as reference_count,
		  MAX(er.implementation_status) as implementation_status
		FROM eu_references er
		LEFT JOIN eu_documents ed ON ed.id = er.eu_document_id
		WHERE er.document_id = ?`
	params := []any{resolvedID}

	if len(parsed.ReferenceTypes) > 0 {
		placeholders := make([]string, len(parsed.ReferenceTypes))
		for i := range placeholders {
			placeholders[i] = "?"
			params = append(params, parsed.ReferenceTypes[i])
		}
		query += " AND er.reference_type IN (" + strings.Join(placeholders, ", ") + ")"
	}

	query += " GROUP BY er.eu_document_id, er.reference_type ORDER BY reference_count DESC"

	rows, err := db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("query eu references: %w", err)
	}
	defer rows.Close()

	results := []euBasisResult{}
	for rows.Next() {
		var (
			r          euBasisResult
			docType    sql.Null[string]
			title      sql.Null[string]
			implStatus sql.Null[string]
		)
		if err := rows.Scan(&r.EUDocumentID, &docType, &title, &r.ReferenceType,
			&r.ReferenceCount, &implStatus); err != nil {
			return nil, ResponseMetadata{}, fmt.Errorf("scan eu reference: %w", err)
		}
		r.EUDocumentType = nullStringPtr(docType)
		r.EUDocumentTitle = nullStringPtr(title)
		r.ImplementationStatus = nullStringPtr(implStatus)
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("scan eu references: %w", err)
	}

	// Article expansion runs only when requested AND rows exist; every row
	// then gets an `articles` key ([] when the document has no articles;
	// NULL eu_article values are skipped).
	if parsed.IncludeArticles != nil && *parsed.IncludeArticles && len(results) > 0 {
		articleRows, err := db.QueryContext(ctx,
			"SELECT DISTINCT eu_document_id, eu_article FROM eu_references WHERE document_id = ?",
			resolvedID)
		if err != nil {
			return nil, ResponseMetadata{}, fmt.Errorf("query eu articles: %w", err)
		}
		articlesByDoc := map[string][]string{}
		for articleRows.Next() {
			var (
				docID   string
				article sql.Null[string]
			)
			if err := articleRows.Scan(&docID, &article); err != nil {
				articleRows.Close()
				return nil, ResponseMetadata{}, fmt.Errorf("scan eu article: %w", err)
			}
			if !article.Valid {
				continue
			}
			articlesByDoc[docID] = append(articlesByDoc[docID], article.V)
		}
		articleRows.Close()
		if err := articleRows.Err(); err != nil {
			return nil, ResponseMetadata{}, fmt.Errorf("scan eu articles: %w", err)
		}
		for i := range results {
			list := articlesByDoc[results[i].EUDocumentID]
			if list == nil {
				// TS `articlesByDoc.get(...) ?? []`: an empty (non-null) array.
				list = []string{}
			}
			results[i].Articles = &list
		}
	}

	return results, GenerateResponseMetadata(ctx, db), nil
}

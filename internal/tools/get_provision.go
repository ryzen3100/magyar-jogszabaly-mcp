// get_provision — Retrieve specific provision(s) from an Hungarian statute.
// Port of src/tools/get-provision.ts.
package tools

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/statute"
)

// maxProvisionsPerDocument caps the section-omitted full-document listing —
// the TS original streamed every row, which on a large act is megabytes of
// JSON in one tool result.
const maxProvisionsPerDocument = 500

// getProvisionArgs mirrors GetProvisionInput.
type getProvisionArgs struct {
	DocumentID   *string `json:"document_id"`
	Section      *string `json:"section"`
	ProvisionRef *string `json:"provision_ref"`
}

// ProvisionResult — url is omitted (not null) when the document has none,
// exactly like the TypeScript `url: docRow.url ?? undefined`. chapter/title
// stay explicit nulls. Field order matches the TS interface.
type ProvisionResult struct {
	DocumentID    string  `json:"document_id"`
	DocumentTitle string  `json:"document_title"`
	ProvisionRef  string  `json:"provision_ref"`
	Chapter       *string `json:"chapter"`
	Section       string  `json:"section"`
	Title         *string `json:"title"`
	Content       string  `json:"content"`
	URL           *string `json:"url,omitempty"`
}

// GetProvision is the exported handler for get_provision.
func GetProvision(ctx context.Context, db *sql.DB, args map[string]any) (any, ResponseMetadata, error) {
	var parsed getProvisionArgs
	if err := decodeArgs(args, &parsed); err != nil {
		return nil, ResponseMetadata{}, err
	}
	if parsed.DocumentID == nil {
		return nil, ResponseMetadata{}, fmt.Errorf("missing required argument %q", "document_id")
	}
	if err := validateArgs(
		checkMaxLength("document_id", parsed.DocumentID, maxDocumentIDLength),
		checkMaxLength("section", parsed.Section, maxRefLength),
		checkMaxLength("provision_ref", parsed.ProvisionRef, maxRefLength),
	); err != nil {
		return nil, ResponseMetadata{}, err
	}

	resolvedID, err := statute.ResolveDocumentId(ctx, db, *parsed.DocumentID)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("resolve document: %w", err)
	}
	if resolvedID == "" {
		meta := GenerateResponseMetadata(ctx, db)
		meta.Note = fmt.Sprintf("No document found matching \"%s\"", *parsed.DocumentID)
		return []ProvisionResult{}, meta, nil
	}

	var docID, docTitle string
	var docURL sql.NullString
	err = db.QueryRowContext(ctx, "SELECT id, title, url FROM legal_documents WHERE id = ?", resolvedID).Scan(&docID, &docTitle, &docURL)
	if errors.Is(err, sql.ErrNoRows) {
		return []ProvisionResult{}, GenerateResponseMetadata(ctx, db), nil
	}
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("query document: %w", err)
	}

	// Specific provision lookup — one OR-query covers exact, "s"-prefixed,
	// section-column, and fuzzy matches (same pattern as validate_citation).
	ref := parsed.ProvisionRef
	if ref == nil {
		ref = parsed.Section
	}
	if ref != nil && *ref != "" {
		refTrimmed := strings.TrimSpace(*ref)

		row := db.QueryRowContext(
			ctx,
			"SELECT * FROM legal_provisions WHERE document_id = ? AND (provision_ref = ? OR provision_ref = ? OR section = ? OR provision_ref LIKE ? OR section LIKE ?)",
			resolvedID, refTrimmed, "s"+refTrimmed, refTrimmed, "%"+refTrimmed+"%", "%"+refTrimmed+"%",
		)
		provision, err := scanProvision(row.Scan, resolvedID, docTitle, docURL)
		if err != nil {
			return nil, ResponseMetadata{}, fmt.Errorf("query provision: %w", err)
		}
		if provision != nil {
			return []ProvisionResult{*provision}, GenerateResponseMetadata(ctx, db), nil
		}

		meta := GenerateResponseMetadata(ctx, db)
		// Note carries the ref as typed (untrimmed), like the TS template.
		meta.Note = fmt.Sprintf("Provision \"%s\" not found in document \"%s\"", *ref, resolvedID)
		return []ProvisionResult{}, meta, nil
	}

	// Return all provisions for the document, capped — a full act does not
	// fit one tool result.
	rows, err := db.QueryContext(ctx, "SELECT * FROM legal_provisions WHERE document_id = ? ORDER BY id", resolvedID)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("query provisions: %w", err)
	}
	defer rows.Close()

	results := []ProvisionResult{}
	truncated := false
	for rows.Next() {
		if len(results) >= maxProvisionsPerDocument {
			truncated = true
			break
		}
		provision, err := scanProvision(rows.Scan, resolvedID, docTitle, docURL)
		if err != nil {
			return nil, ResponseMetadata{}, fmt.Errorf("scan provision: %w", err)
		}
		results = append(results, *provision)
	}
	// Release the connection before the metadata read below: the pool is
	// capped, and an early break leaves the rows (and their connection) open.
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("scan provisions: %w", err)
	}

	meta := GenerateResponseMetadata(ctx, db)
	if truncated {
		meta.Note = fmt.Sprintf("results truncated at %d provisions; pass section or provision_ref to narrow the query", maxProvisionsPerDocument)
	}
	return results, meta, nil
}

// scanProvision maps one legal_provisions row (SELECT * column order:
// id, document_id, provision_ref, chapter, section, title, content, metadata)
// to a ProvisionResult; nil (no error) when the row is empty.
func scanProvision(scan func(dest ...any) error, resolvedID, docTitle string, docURL sql.NullString) (*ProvisionResult, error) {
	var (
		id, documentID, provisionRef, section, content string
		chapter, title, metadata                       sql.NullString
	)
	if err := scan(&id, &documentID, &provisionRef, &chapter, &section, &title, &content, &metadata); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &ProvisionResult{
		DocumentID:    resolvedID,
		DocumentTitle: docTitle,
		ProvisionRef:  provisionRef,
		Chapter:       nullStringPtr(chapter),
		Section:       section,
		Title:         nullStringPtr(title),
		Content:       content,
		URL:           nullStringPtr(docURL),
	}, nil
}

// get_provision — Retrieve specific provision(s) from an Hungarian statute.
// Port of src/tools/get-provision.ts.

package tools

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/statute"
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

// GetProvision is the exported handler for get_provision. Empty results mean
// either an unresolved document_id ("No document found matching" note), a
// section/provision_ref that matched nothing (note says so), or a document
// with no provision rows at all (no note).
func GetProvision(ctx context.Context, db *sql.DB, args map[string]any) (any, ResponseMetadata, error) {
	var parsed getProvisionArgs
	if err := decodeArgs(args, &parsed); err != nil {
		return nil, ResponseMetadata{}, err
	}
	if err := validateArgs(
		checkRequired("document_id", parsed.DocumentID),
		checkMaxLength("document_id", parsed.DocumentID, maxDocumentIDLength),
		checkMaxLength("section", parsed.Section, maxRefLength),
		checkMaxLength("provision_ref", parsed.ProvisionRef, maxRefLength),
	); err != nil {
		return nil, ResponseMetadata{}, err
	}

	resolvedID, err := statute.ResolveDocumentID(ctx, db, *parsed.DocumentID)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("resolve document: %w", err)
	}
	if resolvedID == "" {
		meta := GenerateResponseMetadata(ctx, db)
		meta.Note = fmt.Sprintf("No document found matching \"%s\"", *parsed.DocumentID)
		return []ProvisionResult{}, meta, nil
	}

	var docID, docTitle string
	var docURL sql.Null[string]
	err = db.QueryRowContext(ctx, "SELECT id, title, url FROM legal_documents WHERE id = ?",
		resolvedID).Scan(&docID, &docTitle, &docURL)
	if errors.Is(err, sql.ErrNoRows) {
		return []ProvisionResult{}, GenerateResponseMetadata(ctx, db), nil
	}
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("query document: %w", err)
	}

	// Specific provision lookup — the typed ref is normalized into exact
	// match candidates (statute.SectionRefCandidates: "3. §" → section "3" /
	// provision_ref "s3"); no fuzzy LIKE tier, which could return a wrong
	// provision and break the zero-hallucination contract.
	ref := parsed.ProvisionRef
	if ref == nil {
		ref = parsed.Section
	}
	if ref != nil && *ref != "" {
		candidates := statute.SectionRefCandidates(*ref)
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
			row := db.QueryRowContext(ctx,
				"SELECT "+provisionColumns+" FROM legal_provisions WHERE document_id = ? AND "+
					"(provision_ref IN ("+placeholders+") OR section IN ("+placeholders+")) "+
					"ORDER BY id LIMIT 1",
				args...)
			provision, err := scanProvision(row.Scan, resolvedID, docTitle, docURL)
			if err != nil {
				return nil, ResponseMetadata{}, fmt.Errorf("query provision: %w", err)
			}
			if provision != nil {
				return []ProvisionResult{*provision}, GenerateResponseMetadata(ctx, db), nil
			}
		}

		meta := GenerateResponseMetadata(ctx, db)
		// Note carries the ref as typed (untrimmed), like the TS template.
		meta.Note = fmt.Sprintf("Provision \"%s\" not found in document \"%s\"", *ref, resolvedID)
		return []ProvisionResult{}, meta, nil
	}

	// Return all provisions for the document, capped — a full act does not
	// fit one tool result.
	rows, err := db.QueryContext(ctx,
		"SELECT "+provisionColumns+" FROM legal_provisions WHERE document_id = ? ORDER BY id", resolvedID)
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
		meta.Note = fmt.Sprintf("results truncated at %d provisions; "+
			"pass section or provision_ref to narrow the query", maxProvisionsPerDocument)
	}
	return results, meta, nil
}

// provisionColumns lists the legal_provisions columns get_provision reads,
// in scanProvision's scan order — an explicit list, not SELECT *, so a
// schema change cannot silently shift the positional Scan.
const provisionColumns = "id, document_id, provision_ref, chapter, section, title, content"

// scanProvision maps one legal_provisions row (provisionColumns order) to a
// ProvisionResult; nil (no error) when the row is empty.
func scanProvision(scan func(dest ...any) error, resolvedID, docTitle string,
	docURL sql.Null[string]) (*ProvisionResult, error) {
	var (
		id, documentID, provisionRef, section, content string
		chapter, title                                 sql.Null[string]
	)
	if err := scan(&id, &documentID, &provisionRef, &chapter, &section, &title, &content); err != nil {
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

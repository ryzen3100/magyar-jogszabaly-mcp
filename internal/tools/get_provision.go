// get_provision — Retrieve specific provision(s) from an Hungarian statute.
// Port of src/tools/get-provision.ts.
package tools

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/statute"
)

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
func GetProvision(db *sql.DB, rawArgs json.RawMessage) (any, ResponseMetadata, error) {
	var args getProvisionArgs
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
		meta := GenerateResponseMetadata(db)
		meta.Note = fmt.Sprintf("No document found matching \"%s\"", *args.DocumentID)
		return []ProvisionResult{}, meta, nil
	}

	var docID, docTitle string
	var docURL sql.NullString
	err = db.QueryRow("SELECT id, title, url FROM legal_documents WHERE id = ?", resolvedID).Scan(&docID, &docTitle, &docURL)
	if errors.Is(err, sql.ErrNoRows) {
		return []ProvisionResult{}, GenerateResponseMetadata(db), nil
	}
	if err != nil {
		return nil, ResponseMetadata{}, err
	}

	// Specific provision lookup — one OR-query covers exact, "s"-prefixed,
	// section-column, and fuzzy matches (same pattern as validate_citation).
	ref := args.ProvisionRef
	if ref == nil {
		ref = args.Section
	}
	if ref != nil && *ref != "" {
		refTrimmed := strings.TrimSpace(*ref)

		row := db.QueryRow(
			"SELECT * FROM legal_provisions WHERE document_id = ? AND (provision_ref = ? OR provision_ref = ? OR section = ? OR provision_ref LIKE ? OR section LIKE ?)",
			resolvedID, refTrimmed, "s"+refTrimmed, refTrimmed, "%"+refTrimmed+"%", "%"+refTrimmed+"%",
		)
		provision, err := scanProvision(row.Scan, resolvedID, docTitle, docURL)
		if err != nil {
			return nil, ResponseMetadata{}, err
		}
		if provision != nil {
			return []ProvisionResult{*provision}, GenerateResponseMetadata(db), nil
		}

		meta := GenerateResponseMetadata(db)
		// Note carries the ref as typed (untrimmed), like the TS template.
		meta.Note = fmt.Sprintf("Provision \"%s\" not found in document \"%s\"", *ref, resolvedID)
		return []ProvisionResult{}, meta, nil
	}

	// Return all provisions for the document
	rows, err := db.Query("SELECT * FROM legal_provisions WHERE document_id = ? ORDER BY id", resolvedID)
	if err != nil {
		return nil, ResponseMetadata{}, err
	}
	defer rows.Close()

	results := []ProvisionResult{}
	for rows.Next() {
		provision, err := scanProvision(rows.Scan, resolvedID, docTitle, docURL)
		if err != nil {
			return nil, ResponseMetadata{}, err
		}
		results = append(results, *provision)
	}
	if err := rows.Err(); err != nil {
		return nil, ResponseMetadata{}, err
	}

	return results, GenerateResponseMetadata(db), nil
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

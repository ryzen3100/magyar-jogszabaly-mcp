// format_citation — Format an Hungarian legal citation per standard
// conventions. Port of src/tools/format-citation.ts.
package tools

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/statute"
)

// formatCitationArgs mirrors FormatCitationInput.
type formatCitationArgs struct {
	Citation *string `json:"citation"`
	Format   *string `json:"format"`
}

// FormatCitationResult — singular object result.
type FormatCitationResult struct {
	Original  string `json:"original"`
	Formatted string `json:"formatted"`
	Format    string `json:"format"`
}

// FormatCitation is the exported handler for format_citation. It never
// returns empty results — an unparseable citation is echoed back verbatim as
// the act name — and never sets _metadata.note.
func FormatCitation(ctx context.Context, db *sql.DB, args map[string]any) (any, ResponseMetadata, error) {
	var parsed formatCitationArgs
	if err := decodeArgs(args, &parsed); err != nil {
		return nil, ResponseMetadata{}, err
	}
	if err := validateArgs(
		checkRequired("citation", parsed.Citation),
		checkMaxLength("citation", parsed.Citation, maxCitationLength),
		checkEnum("format", parsed.Format, formatEnumValues...),
	); err != nil {
		return nil, ResponseMetadata{}, err
	}

	format := "full"
	if parsed.Format != nil {
		format = *parsed.Format
	}
	trimmed := strings.TrimSpace(*parsed.Citation)

	parsedCitation := ParseCitation(trimmed)

	var section string
	var act string

	if parsedCitation != nil {
		// Structured references (Hungarian formal, database ID) additionally
		// get their full title resolved from the database.
		if parsedCitation.Structured {
			docID, err := statute.ResolveDocumentID(ctx, db, parsedCitation.DocumentRef)
			if err != nil {
				return nil, ResponseMetadata{}, fmt.Errorf("resolve document: %w", err)
			}
			if docID != "" {
				var title string
				err := db.QueryRowContext(ctx, "SELECT title FROM legal_documents WHERE id = ?", docID).Scan(&title)
				if errors.Is(err, sql.ErrNoRows) {
					act = parsedCitation.DocumentRef
				} else if err != nil {
					return nil, ResponseMetadata{}, fmt.Errorf("query document title: %w", err)
				} else {
					act = title
				}
			} else {
				act = parsedCitation.DocumentRef
			}
		} else {
			act = parsedCitation.DocumentRef
		}
		section = parsedCitation.SectionRef
	} else {
		act = trimmed
	}

	var formatted string
	switch {
	case section == "":
		formatted = act
	case format == "pinpoint":
		formatted = section + ". §"
	default:
		formatted = act + " " + section + ". §"
	}

	return FormatCitationResult{
		Original:  *parsed.Citation,
		Formatted: formatted,
		Format:    format,
	}, GenerateResponseMetadata(ctx, db), nil
}

// format_citation — Format an Hungarian legal citation per standard
// conventions. Port of src/tools/format-citation.ts.
package tools

import (
	"database/sql"
	"encoding/json"
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

// FormatCitation is the exported handler for format_citation.
func FormatCitation(db *sql.DB, rawArgs json.RawMessage) (any, ResponseMetadata, error) {
	var args formatCitationArgs
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return nil, ResponseMetadata{}, err
		}
	}
	if args.Citation == nil {
		return nil, ResponseMetadata{}, fmt.Errorf("missing required argument %q", "citation")
	}

	format := "full"
	if args.Format != nil {
		format = *args.Format
	}
	trimmed := strings.TrimSpace(*args.Citation)

	parsed := ParseCitation(trimmed)

	var section string
	var act string

	if parsed != nil {
		// Structured references (Hungarian formal, database ID) additionally
		// get their full title resolved from the database.
		if parsed.Structured {
			docID, err := statute.ResolveDocumentId(db, parsed.DocumentRef)
			if err != nil {
				return nil, ResponseMetadata{}, err
			}
			if docID != "" {
				var title string
				err := db.QueryRow("SELECT title FROM legal_documents WHERE id = ?", docID).Scan(&title)
				if errors.Is(err, sql.ErrNoRows) {
					act = parsed.DocumentRef
				} else if err != nil {
					return nil, ResponseMetadata{}, err
				} else {
					act = title
				}
			} else {
				act = parsed.DocumentRef
			}
		} else {
			act = parsed.DocumentRef
		}
		section = parsed.SectionRef
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
		Original:  *args.Citation,
		Formatted: formatted,
		Format:    format,
	}, GenerateResponseMetadata(db), nil
}

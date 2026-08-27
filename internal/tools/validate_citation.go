// validate_citation — Validate an Hungarian legal citation against the
// database. Port of src/tools/validate-citation.ts. ParseCitation is exported
// and reused by format_citation, exactly like in TypeScript.
package tools

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/statute"
)

// validateCitationArgs mirrors ValidateCitationInput.
type validateCitationArgs struct {
	Citation *string `json:"citation"`
}

// ValidateCitationResult — singular object result. normalized/document_id/
// document_title/provision_ref/status are omitted when unset (the TS
// interface's optional fields dropped by JSON.stringify). Field order matches
// the TS interface.
type ValidateCitationResult struct {
	Valid         bool     `json:"valid"`
	Citation      string   `json:"citation"`
	Normalized    string   `json:"normalized,omitempty"`
	DocumentID    string   `json:"document_id,omitempty"`
	DocumentTitle string   `json:"document_title,omitempty"`
	ProvisionRef  string   `json:"provision_ref,omitempty"`
	Status        string   `json:"status,omitempty"`
	Warnings      []string `json:"warnings"`
}

// ParsedCitation is the parse outcome; SectionRef is "" when the citation
// carries no section.
type ParsedCitation struct {
	DocumentRef string
	SectionRef  string
	// Structured is true when DocumentRef is a formal Hungarian reference or
	// a database ID.
	Structured bool
}

// Citation grammars, ported regex for regex from validate-citation.ts.
// All are RE2-safe; (?i) matches the JS /i flag.
var (
	hungarianFullRe = regexp.MustCompile(`(?i)^(\d{4}\.\s*évi\s+[IVXLCDM]+\.\s*törvény)\s+(\d+(?::\d+)?(?:\/[A-Za-z])?)\.\s*§`)
	hungarianDocRe  = regexp.MustCompile(`(?i)^(\d{4}\.\s*évi\s+[IVXLCDM]+\.\s*törvény)$`)
	dbIdWithSecRe   = regexp.MustCompile(`(?i)^(hu-law-\d{4}-\d+-\d{2}-\d{2})\s+s?(\d+(?::\d+)?(?:\/[A-Za-z])?)$`)
	dbIdOnlyRe      = regexp.MustCompile(`^(hu-law-\d{4}-\d+-\d{2}-\d{2})$`)
	sectionFirstRe  = regexp.MustCompile(`(?i)^Section\s+(\d+[A-Za-z]*(?:\(\d+\))?)\s*[,;]?\s+(.+)$`)
	sectionLastRe   = regexp.MustCompile(`(?i)^(.+?)\s*[,;]?\s+(?:s\.?\s+|Section\s+)(\d+[A-Za-z]*(?:\(\d+\))?)$`)
)

// ParseCitation parses an Hungarian legal citation. Returns nil only for an
// empty/whitespace string (the TS null).
func ParseCitation(citation string) *ParsedCitation {
	trimmed := strings.TrimSpace(citation)
	if trimmed == "" {
		return nil
	}

	// Hungarian formal: "2012. évi I. törvény 116. §" / "6:272. §" / "116/A. §"
	if m := hungarianFullRe.FindStringSubmatch(trimmed); m != nil {
		return &ParsedCitation{DocumentRef: strings.TrimSpace(m[1]), SectionRef: m[2], Structured: true}
	}

	// Hungarian document only: "2012. évi I. törvény" (no section)
	if m := hungarianDocRe.FindStringSubmatch(trimmed); m != nil {
		return &ParsedCitation{DocumentRef: strings.TrimSpace(m[1]), Structured: true}
	}

	// Database ID + section: "hu-law-2012-1-00-00 s116" / "… s6:272"
	if m := dbIdWithSecRe.FindStringSubmatch(trimmed); m != nil {
		return &ParsedCitation{DocumentRef: m[1], SectionRef: m[2], Structured: true}
	}

	// Database ID only: "hu-law-2012-1-00-00"
	if m := dbIdOnlyRe.FindStringSubmatch(trimmed); m != nil {
		return &ParsedCitation{DocumentRef: m[1], Structured: true}
	}

	// "Section N <Act>" or "Section N, <Act>"
	if m := sectionFirstRe.FindStringSubmatch(trimmed); m != nil {
		return &ParsedCitation{DocumentRef: strings.TrimSpace(m[2]), SectionRef: m[1]}
	}

	// "<Act> s N" / "<Act>, s N" / "<Act> s. N" / "<Act> Section N"
	if m := sectionLastRe.FindStringSubmatch(trimmed); m != nil {
		return &ParsedCitation{DocumentRef: strings.TrimSpace(m[1]), SectionRef: m[2]}
	}

	// Just a document reference (no section)
	return &ParsedCitation{DocumentRef: trimmed}
}

// ValidateCitation is the exported handler for validate_citation.
func ValidateCitation(db *sql.DB, rawArgs json.RawMessage) (any, ResponseMetadata, error) {
	var args validateCitationArgs
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return nil, ResponseMetadata{}, err
		}
	}
	if args.Citation == nil {
		return nil, ResponseMetadata{}, fmt.Errorf("missing required argument %q", "citation")
	}

	warnings := []string{}
	parsed := ParseCitation(*args.Citation)
	if parsed == nil {
		return ValidateCitationResult{
			Valid:    false,
			Citation: *args.Citation,
			Warnings: []string{"Could not parse citation format"},
		}, GenerateResponseMetadata(db), nil
	}

	docID, err := statute.ResolveDocumentId(db, parsed.DocumentRef)
	if err != nil {
		return nil, ResponseMetadata{}, err
	}
	if docID == "" {
		return ValidateCitationResult{
			Valid:    false,
			Citation: *args.Citation,
			Warnings: []string{fmt.Sprintf("Document not found: \"%s\"", parsed.DocumentRef)},
		}, GenerateResponseMetadata(db), nil
	}

	var id, title, status string
	err = db.QueryRow("SELECT id, title, status FROM legal_documents WHERE id = ?", docID).Scan(&id, &title, &status)
	if err != nil {
		return nil, ResponseMetadata{}, err
	}

	if status == "repealed" {
		warnings = append(warnings, "WARNING: This statute has been repealed.")
	} else if status == "amended" {
		warnings = append(warnings, "Note: This statute has been amended. Verify you are referencing the current version.")
	}

	if parsed.SectionRef != "" {
		// Normalize section ref: "6:272" → try "s6272", "s6:272", "6:272", "6272"
		// (TS replace(':', '') removes only the first occurrence).
		sectionClean := strings.Replace(parsed.SectionRef, ":", "", 1)
		var provisionRef string
		err := db.QueryRow(
			"SELECT provision_ref FROM legal_provisions WHERE document_id = ? AND (provision_ref = ? OR provision_ref = ? OR provision_ref = ? OR provision_ref = ? OR section = ? OR section = ?)",
			docID, parsed.SectionRef, "s"+parsed.SectionRef, "s"+sectionClean, sectionClean, parsed.SectionRef, sectionClean,
		).Scan(&provisionRef)
		if errors.Is(err, sql.ErrNoRows) {
			warnings = append(warnings, fmt.Sprintf("Provision \"%s. §\" not found in %s", parsed.SectionRef, title))
			return ValidateCitationResult{
				Valid:         false,
				Citation:      *args.Citation,
				DocumentID:    docID,
				DocumentTitle: title,
				Warnings:      warnings,
			}, GenerateResponseMetadata(db), nil
		}
		if err != nil {
			return nil, ResponseMetadata{}, err
		}

		return ValidateCitationResult{
			Valid:         true,
			Citation:      *args.Citation,
			Normalized:    fmt.Sprintf("%s %s. § (Section %s)", title, parsed.SectionRef, parsed.SectionRef),
			DocumentID:    docID,
			DocumentTitle: title,
			ProvisionRef:  provisionRef,
			Status:        status,
			Warnings:      warnings,
		}, GenerateResponseMetadata(db), nil
	}

	return ValidateCitationResult{
		Valid:         true,
		Citation:      *args.Citation,
		Normalized:    title,
		DocumentID:    docID,
		DocumentTitle: title,
		Status:        status,
		Warnings:      warnings,
	}, GenerateResponseMetadata(db), nil
}

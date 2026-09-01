// validate_citation — Validate an Hungarian legal citation against the
// database. Port of src/tools/validate-citation.ts. ParseCitation is exported
// and reused by format_citation, exactly like in TypeScript.

package tools

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/statute"
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
	hungarianFullRe = regexp.MustCompile(`(?i)^(\d{4}\.\s*évi\s+[IVXLCDM]+\.\s*törvény)\s+` +
		`(\d+(?::\d+)?(?:\/[A-Za-z])?)\.\s*§`)
	hungarianDocRe = regexp.MustCompile(`(?i)^(\d{4}\.\s*évi\s+[IVXLCDM]+\.\s*törvény)$`)
	// Decree + section: "210/2009. Korm. rendelet 1. §",
	// "210/2009. (IX. 29.) Korm. rendelet 1. §" and the full-title form
	// "…rendelet a kereskedelmi tevékenységek végzésének feltételeiről 1. §".
	// Greedy group 1 keeps everything up to the LAST trailing "N. §" (decree
	// titles are long and carry the promulgation date); the section grammar
	// matches hungarianFullRe.
	decreeFullRe   = regexp.MustCompile(`(?i)^(\d{1,4}/\d{4}\.?.*rendelet.*)\s+(\d+(?::\d+)?(?:\/[A-Za-z])?)\s*\.?\s*§$`)
	dbIDWithSecRe  = regexp.MustCompile(`(?i)^(hu-law-\d{4}-\d+-\d{2}-\d{2})\s+s?(\d+(?::\d+)?(?:\/[A-Za-z])?)$`)
	dbIDOnlyRe     = regexp.MustCompile(`^(hu-law-\d{4}-\d+-\d{2}-\d{2})$`)
	sectionFirstRe = regexp.MustCompile(`(?i)^Section\s+(\d+[A-Za-z]*(?:\(\d+\))?)\s*[,;]?\s+(.+)$`)
	sectionLastRe  = regexp.MustCompile(`(?i)^(.+?)\s*[,;]?\s+(?:s\.?\s+|Section\s+)(\d+[A-Za-z]*(?:\(\d+\))?)$`)
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

	// Decree + section: "210/2009. Korm. rendelet 1. §"
	if m := decreeFullRe.FindStringSubmatch(trimmed); m != nil {
		return &ParsedCitation{DocumentRef: strings.TrimSpace(m[1]), SectionRef: m[2], Structured: true}
	}

	// Database ID + section: "hu-law-2012-1-00-00 s116" / "… s6:272"
	if m := dbIDWithSecRe.FindStringSubmatch(trimmed); m != nil {
		return &ParsedCitation{DocumentRef: m[1], SectionRef: m[2], Structured: true}
	}

	// Database ID only: "hu-law-2012-1-00-00"
	if m := dbIDOnlyRe.FindStringSubmatch(trimmed); m != nil {
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

// ValidateCitation is the exported handler for validate_citation. It always
// returns a singular result — unparseable citations and unknown documents
// come back as valid:false plus warnings, not errors — and _metadata.note
// stays unset.
func ValidateCitation(ctx context.Context, db *sql.DB, args map[string]any) (any, ResponseMetadata, error) {
	var parsed validateCitationArgs
	if err := decodeArgs(args, &parsed); err != nil {
		return nil, ResponseMetadata{}, err
	}
	if err := validateArgs(
		checkRequired("citation", parsed.Citation),
		checkMaxLength("citation", parsed.Citation, maxCitationLength),
	); err != nil {
		return nil, ResponseMetadata{}, err
	}

	warnings := []string{}
	parsedCitation := ParseCitation(*parsed.Citation)
	if parsedCitation == nil {
		return ValidateCitationResult{
			Valid:    false,
			Citation: *parsed.Citation,
			Warnings: []string{"Could not parse citation format"},
		}, GenerateResponseMetadata(ctx, db), nil
	}

	docID, err := statute.ResolveDocumentID(ctx, db, parsedCitation.DocumentRef)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("resolve document: %w", err)
	}
	if docID == "" {
		return ValidateCitationResult{
			Valid:    false,
			Citation: *parsed.Citation,
			Warnings: []string{fmt.Sprintf("Document not found: \"%s\"", parsedCitation.DocumentRef)},
		}, GenerateResponseMetadata(ctx, db), nil
	}

	var title, status string
	err = db.QueryRowContext(ctx, "SELECT title, status FROM legal_documents WHERE id = ?",
		docID).Scan(&title, &status)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("query document: %w", err)
	}

	switch status {
	case "repealed":
		warnings = append(warnings, "WARNING: This statute has been repealed.")
	case "amended":
		warnings = append(warnings, "Note: This statute has been amended. Verify you are referencing the current version.")
	}

	if parsedCitation.SectionRef != "" {
		// Same normalized exact-match candidates as get_provision (shared
		// tolerance — audit E5): "6:272" → section "6:272"/"6272",
		// provision_ref "s6272".
		candidates := statute.SectionRefCandidates(parsedCitation.SectionRef)
		var provisionRef string
		err := sql.ErrNoRows
		if len(candidates) > 0 {
			where, args := provisionRefQuery(docID, candidates)
			err = db.QueryRowContext(ctx,
				"SELECT provision_ref FROM legal_provisions WHERE "+where+" ORDER BY id LIMIT 1",
				args...,
			).Scan(&provisionRef)
		}
		if errors.Is(err, sql.ErrNoRows) {
			warnings = append(warnings, fmt.Sprintf("Provision \"%s. §\" not found in %s", parsedCitation.SectionRef, title))
			return ValidateCitationResult{
				Valid:         false,
				Citation:      *parsed.Citation,
				DocumentID:    docID,
				DocumentTitle: title,
				Warnings:      warnings,
			}, GenerateResponseMetadata(ctx, db), nil
		}
		if err != nil {
			return nil, ResponseMetadata{}, fmt.Errorf("query provision: %w", err)
		}

		return ValidateCitationResult{
			Valid:         true,
			Citation:      *parsed.Citation,
			Normalized:    fmt.Sprintf("%s %s. § (Section %s)", title, parsedCitation.SectionRef, parsedCitation.SectionRef),
			DocumentID:    docID,
			DocumentTitle: title,
			ProvisionRef:  provisionRef,
			Status:        status,
			Warnings:      warnings,
		}, GenerateResponseMetadata(ctx, db), nil
	}

	return ValidateCitationResult{
		Valid:         true,
		Citation:      *parsed.Citation,
		Normalized:    title,
		DocumentID:    docID,
		DocumentTitle: title,
		Status:        status,
		Warnings:      warnings,
	}, GenerateResponseMetadata(ctx, db), nil
}

package tools

import (
	"strings"
)

// provisionRefQuery builds the parameterized exact-match WHERE fragment for a
// normalized provision-ref lookup: document_id plus provision_ref/section IN
// over the shared SectionRefCandidates. One construction point for all three
// ref-lookup tools (get_provision, get_provision_eu_basis, validate_citation).
func provisionRefQuery(documentID string, candidates []string) (string, []any) {
	placeholders := strings.TrimRight(strings.Repeat("?,", len(candidates)), ",")
	args := make([]any, 0, 2*len(candidates)+1)
	args = append(args, documentID)
	for _, c := range candidates {
		args = append(args, c)
	}
	for _, c := range candidates {
		args = append(args, c)
	}
	return "document_id = ? AND (provision_ref IN (" + placeholders + ") OR section IN (" + placeholders + "))", args
}

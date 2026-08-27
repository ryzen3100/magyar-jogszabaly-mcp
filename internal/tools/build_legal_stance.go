// build_legal_stance — Build a comprehensive set of citations for a legal
// question. Port of src/tools/build-legal-stance.ts: a thin wrapper over the
// search_legislation core with research-oriented defaults (lower result cap,
// no status filter) that strips chapter from every item.
package tools

import (
	"database/sql"
	"encoding/json"
)

// stanceArgs mirrors the Pick<SearchLegislationInput, …> input.
type stanceArgs struct {
	Query      *string  `json:"query"`
	DocumentID *string  `json:"document_id"`
	Limit      *float64 `json:"limit"`
}

// LegalStanceResult is SearchLegislationResult minus chapter — JSON field
// order preserved.
type LegalStanceResult struct {
	DocumentID    string  `json:"document_id"`
	DocumentTitle string  `json:"document_title"`
	ProvisionRef  string  `json:"provision_ref"`
	Section       string  `json:"section"`
	Title         *string `json:"title"`
	Snippet       string  `json:"snippet"`
	Relevance     float64 `json:"relevance"`
}

// BuildLegalStance is the exported handler for build_legal_stance.
func BuildLegalStance(db *sql.DB, rawArgs json.RawMessage) (any, ResponseMetadata, error) {
	var args stanceArgs
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return nil, ResponseMetadata{}, err
		}
	}

	// Math.min(Math.max(input.limit ?? 5, 1), 20) — the search core re-clamps
	// to its own [1,50], which is a no-op after this.
	limit := clampLimit(args.Limit, 5, 20)
	limitF := float64(limit)

	response, meta, err := runSearch(db, searchArgs{
		Query:      args.Query,
		DocumentID: args.DocumentID,
		Limit:      &limitF,
	})
	if err != nil {
		return nil, ResponseMetadata{}, err
	}

	searchResults, ok := response.([]SearchLegislationResult)
	if !ok {
		return []LegalStanceResult{}, meta, nil
	}
	results := make([]LegalStanceResult, 0, len(searchResults))
	for _, r := range searchResults {
		results = append(results, LegalStanceResult{
			DocumentID:    r.DocumentID,
			DocumentTitle: r.DocumentTitle,
			ProvisionRef:  r.ProvisionRef,
			Section:       r.Section,
			Title:         r.Title,
			Snippet:       r.Snippet,
			Relevance:     r.Relevance,
		})
	}
	return results, meta, nil
}

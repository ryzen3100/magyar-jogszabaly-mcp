// search_eu_implementations — Search EU directives/regulations referenced by
// Hungarian legislation. Port of src/tools/search-eu-implementations.ts.

package tools

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store"
)

type searchEUImplementationsArgs struct {
	Query                      *string  `json:"query"`
	Type                       *string  `json:"type"`
	YearFrom                   *float64 `json:"year_from"`
	YearTo                     *float64 `json:"year_to"`
	HasHungarianImplementation *bool    `json:"has_hungarian_implementation"`
	Limit                      *float64 `json:"limit"`
}

// euImplementationSearchResult mirrors the TS EUImplementationSearchResult.
// title/short_name are nullable columns — explicit nulls on the wire.
type euImplementationSearchResult struct {
	EUDocumentID          string  `json:"eu_document_id"`
	Type                  string  `json:"type"`
	Year                  int     `json:"year"`
	Number                int     `json:"number"`
	Title                 *string `json:"title"`
	ShortName             *string `json:"short_name"`
	HungarianStatuteCount int     `json:"hungarian_statute_count"`
}

// SearchEUImplementations implements the search_eu_implementations MCP tool.
// Empty results mean no matches (no note) — unless the database lacks
// eu_documents, in which case _metadata.note carries the availability note.
func SearchEUImplementations(ctx context.Context, db *sql.DB, args map[string]any) (any, ResponseMetadata, error) {
	var parsed searchEUImplementationsArgs
	if err := decodeArgs(args, &parsed); err != nil {
		return nil, ResponseMetadata{}, err
	}
	if err := validateArgs(
		checkMaxLength("query", parsed.Query, maxQueryLength),
		checkEnum("type", parsed.Type, euTypeEnumValues...),
	); err != nil {
		return nil, ResponseMetadata{}, err
	}

	// Probes eu_documents (not eu_references) — note the different word.
	if !store.EUAvailable(ctx, db, "eu_documents") {
		meta := GenerateResponseMetadata(ctx, db)
		meta.Note = store.EUUnavailableNote("eu_documents")
		return []euImplementationSearchResult{}, meta, nil
	}

	limit := 20.0
	if parsed.Limit != nil {
		limit = *parsed.Limit
	}
	clampedLimit := int(min(max(limit, 1), 100))

	query := `
		SELECT
		  ed.id as eu_document_id,
		  ed.type,
		  ed.year,
		  ed.number,
		  ed.title,
		  ed.short_name,
		  COUNT(DISTINCT er.document_id) as hungarian_statute_count
		FROM eu_documents ed
		LEFT JOIN eu_references er ON er.eu_document_id = ed.id
		WHERE 1=1`
	var params []any

	// Truthiness guards mirror the TS falsy checks: an empty query/type or a
	// 0 year bound adds no filter (year 0 is never a real year).
	if parsed.Query != nil && *parsed.Query != "" {
		// Plain LIKE, no LOWER — case behaviour follows the database. User
		// wildcards are escaped so the pattern matches literally.
		query += " AND (ed.title LIKE ? ESCAPE '\\' OR ed.short_name LIKE ? ESCAPE '\\' OR ed.description LIKE ? ESCAPE '\\')"
		pattern := "%" + escapeLike(*parsed.Query) + "%"
		params = append(params, pattern, pattern, pattern)
	}
	if parsed.Type != nil && *parsed.Type != "" {
		query += " AND ed.type = ?"
		params = append(params, *parsed.Type)
	}
	if parsed.YearFrom != nil && *parsed.YearFrom != 0 {
		query += " AND ed.year >= ?"
		params = append(params, *parsed.YearFrom)
	}
	if parsed.YearTo != nil && *parsed.YearTo != 0 {
		query += " AND ed.year <= ?"
		params = append(params, *parsed.YearTo)
	}

	query += " GROUP BY ed.id"

	if parsed.HasHungarianImplementation != nil && *parsed.HasHungarianImplementation {
		query += " HAVING hungarian_statute_count > 0"
	}

	query += " ORDER BY ed.year DESC, ed.number DESC LIMIT ?"
	params = append(params, clampedLimit)

	rows, err := db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("query eu documents: %w", err)
	}
	defer rows.Close()

	results := []euImplementationSearchResult{}
	for rows.Next() {
		var (
			r         euImplementationSearchResult
			title     sql.Null[string]
			shortName sql.Null[string]
		)
		if err := rows.Scan(&r.EUDocumentID, &r.Type, &r.Year, &r.Number,
			&title, &shortName, &r.HungarianStatuteCount); err != nil {
			return nil, ResponseMetadata{}, fmt.Errorf("scan eu document: %w", err)
		}
		r.Title = nullStringPtr(title)
		r.ShortName = nullStringPtr(shortName)
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("scan eu documents: %w", err)
	}

	return results, GenerateResponseMetadata(ctx, db), nil
}

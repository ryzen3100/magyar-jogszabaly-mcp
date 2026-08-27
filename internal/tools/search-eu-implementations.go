// search_eu_implementations — Search EU directives/regulations referenced by
// Hungarian legislation. Port of src/tools/search-eu-implementations.ts.

package tools

import (
	"database/sql"
	"encoding/json"
	"math"

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
	EuDocumentID          string  `json:"eu_document_id"`
	Type                  string  `json:"type"`
	Year                  int     `json:"year"`
	Number                int     `json:"number"`
	Title                 *string `json:"title"`
	ShortName             *string `json:"short_name"`
	HungarianStatuteCount int     `json:"hungarian_statute_count"`
}

// SearchEUImplementations implements the search_eu_implementations MCP tool.
func SearchEUImplementations(db *sql.DB, rawArgs json.RawMessage) (any, ResponseMetadata, error) {
	var args searchEUImplementationsArgs
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return nil, ResponseMetadata{}, err
		}
	}

	// Probes eu_documents (not eu_references) — note the different word.
	if !store.EUAvailable(db, "eu_documents") {
		meta := GenerateResponseMetadata(db)
		meta.Note = store.EUUnavailableNote("eu_documents")
		return []euImplementationSearchResult{}, meta, nil
	}

	limit := 20.0
	if args.Limit != nil {
		limit = *args.Limit
	}
	clampedLimit := int(math.Min(math.Max(limit, 1), 100))

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
	if args.Query != nil && *args.Query != "" {
		// Plain LIKE, no LOWER — case behaviour follows the database.
		query += " AND (ed.title LIKE ? OR ed.short_name LIKE ? OR ed.description LIKE ?)"
		pattern := "%" + *args.Query + "%"
		params = append(params, pattern, pattern, pattern)
	}
	if args.Type != nil && *args.Type != "" {
		query += " AND ed.type = ?"
		params = append(params, *args.Type)
	}
	if args.YearFrom != nil && *args.YearFrom != 0 {
		query += " AND ed.year >= ?"
		params = append(params, *args.YearFrom)
	}
	if args.YearTo != nil && *args.YearTo != 0 {
		query += " AND ed.year <= ?"
		params = append(params, *args.YearTo)
	}

	query += " GROUP BY ed.id"

	if args.HasHungarianImplementation != nil && *args.HasHungarianImplementation {
		query += " HAVING hungarian_statute_count > 0"
	}

	query += " ORDER BY ed.year DESC, ed.number DESC LIMIT ?"
	params = append(params, clampedLimit)

	rows, err := db.Query(query, params...)
	if err != nil {
		return nil, ResponseMetadata{}, err
	}
	defer rows.Close()

	results := []euImplementationSearchResult{}
	for rows.Next() {
		var (
			r         euImplementationSearchResult
			title     sql.NullString
			shortName sql.NullString
		)
		if err := rows.Scan(&r.EuDocumentID, &r.Type, &r.Year, &r.Number,
			&title, &shortName, &r.HungarianStatuteCount); err != nil {
			return nil, ResponseMetadata{}, err
		}
		if title.Valid {
			r.Title = &title.String
		}
		if shortName.Valid {
			r.ShortName = &shortName.String
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, ResponseMetadata{}, err
	}

	return results, GenerateResponseMetadata(db), nil
}

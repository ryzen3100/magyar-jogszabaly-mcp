// search_legislation — Full-text search across Hungarian statute provisions.
// Port of src/tools/search-legislation.ts: two-phase FTS5 search (BM25 rank
// query, then snippet re-MATCH restricted to the surviving rowids) with
// per-variant error degradation and a final LIKE tier.
package tools

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/fts"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/statute"
)

// SearchLegislationResult is one search hit — JSON field order matches the
// TypeScript SearchLegislationResult interface.
type SearchLegislationResult struct {
	DocumentID    string  `json:"document_id"`
	DocumentTitle string  `json:"document_title"`
	ProvisionRef  string  `json:"provision_ref"`
	Chapter       *string `json:"chapter"`
	Section       string  `json:"section"`
	Title         *string `json:"title"`
	Snippet       string  `json:"snippet"`
	Relevance     float64 `json:"relevance"`
}

// searchArgs mirrors SearchLegislationInput. Optionals are pointers; limit is
// a float so any JSON number unmarshals (TS Math.min/max accept floats too).
type searchArgs struct {
	Query      *string  `json:"query"`
	DocumentID *string  `json:"document_id"`
	Status     *string  `json:"status"`
	Limit      *float64 `json:"limit"`
}

const (
	defaultSearchLimit = 10
	maxSearchLimit     = 50

	searchRankSQL = `
      SELECT
        lp.id as provision_id,
        lp.document_id,
        ld.title as document_title,
        lp.provision_ref,
        lp.chapter,
        lp.section,
        lp.title,
        bm25(provisions_fts) as relevance
      FROM provisions_fts
      JOIN legal_provisions lp ON lp.id = provisions_fts.rowid
      JOIN legal_documents ld ON ld.id = lp.document_id
      WHERE provisions_fts MATCH ?
    `

	searchSnippetSQL = `
              SELECT rowid, snippet(provisions_fts, 0, '>>>', '<<<', '...', 32) as snippet
              FROM provisions_fts
              WHERE provisions_fts MATCH ? AND rowid IN (`

	searchLikeSQL = `
      SELECT
        lp.document_id,
        ld.title as document_title,
        lp.provision_ref,
        lp.chapter,
        lp.section,
        lp.title,
        substr(lp.content, 1, 200) as snippet,
        0 as relevance
      FROM legal_provisions lp
      JOIN legal_documents ld ON ld.id = lp.document_id
      WHERE lp.content LIKE ? ESCAPE '\'
    `
)

// SearchLegislation is the exported handler for search_legislation; it is
// also the entry point other tools (build_legal_stance) reuse. Blank queries
// and legitimate zero-hit searches return empty results with no note; an
// unresolved document_id and an all-tiers failure each add a _metadata.note
// (see runSearch).
func SearchLegislation(ctx context.Context, db *sql.DB, args map[string]any) (any, ResponseMetadata, error) {
	var parsed searchArgs
	if err := decodeArgs(args, &parsed); err != nil {
		return nil, ResponseMetadata{}, err
	}
	if err := validateArgs(
		checkMaxLength("query", parsed.Query, maxQueryLength),
		checkMaxLength("document_id", parsed.DocumentID, maxDocumentIDLength),
		checkEnum("status", parsed.Status, statusEnumValues...),
	); err != nil {
		return nil, ResponseMetadata{}, err
	}
	return runSearch(ctx, db, parsed)
}

// runSearch is the search core shared by search_legislation and
// build_legal_stance. Mirroring the TypeScript original, it never fails on
// query problems: FTS syntax errors move to the next variant, a LIKE failure
// degrades to empty results. Only infrastructure errors (document resolution,
// closed database) surface as errors — the TS equivalents throw into the
// registry catch. When every tier errors, the empty result carries a note
// instead of looking like a legitimate zero-hit search.
func runSearch(ctx context.Context, db *sql.DB, args searchArgs) (any, ResponseMetadata, error) {
	if args.Query == nil || strings.TrimSpace(*args.Query) == "" {
		return []SearchLegislationResult{}, GenerateResponseMetadata(ctx, db), nil
	}

	// Math.min(Math.max(limit ?? 10, 1), 50); fetch extra rows for dedup.
	limit := clampLimit(args.Limit, defaultSearchLimit, maxSearchLimit)
	fetchLimit := limit * 2
	variants := fts.BuildQueryVariants(fts.SanitizeInput(*args.Query))

	// Resolve document_id from title if provided (same resolution as get_provision)
	var resolvedDocID string
	if args.DocumentID != nil && *args.DocumentID != "" {
		resolved, err := statute.ResolveDocumentID(ctx, db, *args.DocumentID)
		if err != nil {
			return nil, ResponseMetadata{}, fmt.Errorf("resolve document: %w", err)
		}
		if resolved == "" {
			meta := GenerateResponseMetadata(ctx, db)
			meta.Note = fmt.Sprintf("No document found matching \"%s\"", *args.DocumentID)
			return []SearchLegislationResult{}, meta, nil
		}
		resolvedDocID = resolved
	}

	// lastErr + cleanTierRan separate "every tier failed" from "legitimately
	// empty": a tier that completes without error (even with zero hits)
	// proves the pipeline works, so the note only appears when no tier did.
	var lastErr error
	cleanTierRan := false

	// ponytail: rank rowids first without snippet(), then snippet() only the
	// final deduped rows — snippet over every match dominates high-fanout
	// queries. Phase B reuses the SAME MATCH expression (plain-rowid lookup
	// loses highlight context); never re-MATCH unbounded.
	for i, ftsQuery := range variants {
		query := searchRankSQL
		params := []any{ftsQuery}

		if resolvedDocID != "" {
			query += " AND lp.document_id = ?"
			params = append(params, resolvedDocID)
		}

		if args.Status != nil && *args.Status != "" {
			query += " AND ld.status = ?"
			params = append(params, *args.Status)
		}

		query += " ORDER BY relevance LIMIT ?"
		params = append(params, fetchLimit)

		ranked, err := queryRankedRows(ctx, db, query, params)
		if err != nil {
			lastErr = err
			continue // FTS query syntax error — try next variant
		}
		if len(ranked) == 0 {
			cleanTierRan = true
			continue
		}

		deduped := dedupeRanked(ranked, limit)
		snippets, err := fetchSnippets(ctx, db, ftsQuery, deduped)
		if err != nil {
			lastErr = err
			continue // same TS try block: a phase-B failure tries the next variant
		}

		results := make([]SearchLegislationResult, 0, len(deduped))
		for _, row := range deduped {
			res := toResult(row)
			if snippet, ok := snippets[row.provisionID]; ok {
				res.Snippet = snippet
			}
			results = append(results, res)
		}

		meta := GenerateResponseMetadata(ctx, db)
		if i > 0 { // winning variant is not variant 0 → 'broadened'
			meta.QueryStrategy = "broadened"
		}
		return results, meta, nil
	}

	// LIKE fallback — final tier when FTS5 returns no results
	results, meta, likeErr := runLikeFallback(ctx, db, args, resolvedDocID, limit, fetchLimit)
	if likeErr != nil {
		lastErr = likeErr
	} else {
		cleanTierRan = true
	}
	if likeErr == nil && results != nil {
		return results, meta, nil
	}

	meta = GenerateResponseMetadata(ctx, db)
	if lastErr != nil && !cleanTierRan {
		meta.Note = "search degraded: all query tiers failed"
	}
	return []SearchLegislationResult{}, meta, nil
}

// runLikeFallback is the final tier when FTS5 returns no results: a plain
// LIKE scan over provision content, query_strategy "like_fallback" on a hit.
// It returns (nil, empty, err) when the query fails — the caller records the
// failure for the degrade note — and (nil, empty, nil) on zero rows.
// args.Query must be non-nil (runSearch's guard).
func runLikeFallback(ctx context.Context, db *sql.DB, args searchArgs, resolvedDocID string, limit, fetchLimit int,
) ([]SearchLegislationResult, ResponseMetadata, error) {
	pattern := fts.BuildLikePattern(escapeLike(fts.SanitizeInput(*args.Query)))
	query := searchLikeSQL
	params := []any{pattern}

	if resolvedDocID != "" {
		query += " AND lp.document_id = ?"
		params = append(params, resolvedDocID)
	}
	if args.Status != nil && *args.Status != "" {
		query += " AND ld.status = ?"
		params = append(params, *args.Status)
	}
	query += " LIMIT ?"
	params = append(params, fetchLimit)

	rows, err := queryLikeRows(ctx, db, query, params)
	if err != nil {
		return nil, ResponseMetadata{}, err
	}
	if len(rows) == 0 {
		return nil, ResponseMetadata{}, nil
	}

	deduped := dedupeRanked(rows, limit)
	results := make([]SearchLegislationResult, 0, len(deduped))
	for _, row := range deduped {
		results = append(results, toResult(row))
	}
	meta := GenerateResponseMetadata(ctx, db)
	meta.QueryStrategy = "like_fallback"
	return results, meta, nil
}

// escapeLike escapes SQL LIKE wildcards so user input matches literally;
// pair it with `ESCAPE '\'` in the LIKE clause.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// rankedRow is a phase-A (or LIKE-tier) row before JSON shaping. Nullable
// columns go through sql.Null[string]; chapter/title stay null in JSON.
type rankedRow struct {
	provisionID   int64
	documentID    string
	documentTitle string
	provisionRef  string
	chapter       sql.Null[string]
	section       string
	title         sql.Null[string]
	snippet       string
	relevance     float64
}

func queryRankedRows(ctx context.Context, db *sql.DB, query string, params []any) ([]rankedRow, error) {
	rows, err := db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, fmt.Errorf("query ranked rows: %w", err)
	}
	defer rows.Close()

	var out []rankedRow
	for rows.Next() {
		var r rankedRow
		if err := rows.Scan(&r.provisionID, &r.documentID, &r.documentTitle,
			&r.provisionRef, &r.chapter, &r.section, &r.title, &r.relevance); err != nil {
			return nil, fmt.Errorf("scan ranked row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan ranked rows: %w", err)
	}
	return out, nil
}

func queryLikeRows(ctx context.Context, db *sql.DB, query string, params []any) ([]rankedRow, error) {
	rows, err := db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, fmt.Errorf("query like fallback: %w", err)
	}
	defer rows.Close()

	var out []rankedRow
	for rows.Next() {
		var r rankedRow
		var relevance int64
		if err := rows.Scan(&r.documentID, &r.documentTitle, &r.provisionRef,
			&r.chapter, &r.section, &r.title, &r.snippet, &relevance); err != nil {
			return nil, fmt.Errorf("scan like row: %w", err)
		}
		r.relevance = float64(relevance)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan like rows: %w", err)
	}
	return out, nil
}

// fetchSnippets runs phase B: snippet() over the same MATCH expression,
// restricted to the deduped rowids. Missing rows simply stay empty via the
// caller's map lookup.
func fetchSnippets(ctx context.Context, db *sql.DB, ftsQuery string, deduped []rankedRow) (map[int64]string, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(deduped)), ",")
	params := make([]any, 0, len(deduped)+1)
	params = append(params, ftsQuery)
	for _, row := range deduped {
		params = append(params, row.provisionID)
	}

	rows, err := db.QueryContext(ctx, searchSnippetSQL+placeholders+")", params...)
	if err != nil {
		return nil, fmt.Errorf("fetch snippets: %w", err)
	}
	defer rows.Close()

	snippets := map[int64]string{}
	for rows.Next() {
		var rowid int64
		var snippet string
		if err := rows.Scan(&rowid, &snippet); err != nil {
			return nil, fmt.Errorf("scan snippet: %w", err)
		}
		snippets[rowid] = snippet
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan snippets: %w", err)
	}
	return snippets, nil
}

// dedupeRanked deduplicates by document_title + provision_ref, keeping the
// first (highest-ranked) occurrence and cutting to limit — port of
// deduplicateResults. Duplicate document IDs (numeric vs slug) cause the same
// provision to appear twice.
func dedupeRanked(rows []rankedRow, limit int) []rankedRow {
	seen := make(map[string]bool, len(rows))
	deduped := make([]rankedRow, 0, min(len(rows), limit))
	for _, row := range rows {
		key := row.documentTitle + "::" + row.provisionRef
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, row)
		if len(deduped) >= limit {
			break
		}
	}
	return deduped
}

func toResult(r rankedRow) SearchLegislationResult {
	return SearchLegislationResult{
		DocumentID:    r.documentID,
		DocumentTitle: r.documentTitle,
		ProvisionRef:  r.provisionRef,
		Chapter:       nullStringPtr(r.chapter),
		Section:       r.section,
		Title:         nullStringPtr(r.title),
		Snippet:       r.snippet,
		Relevance:     r.relevance,
	}
}

func nullStringPtr(ns sql.Null[string]) *string {
	if !ns.Valid {
		return nil
	}
	s := ns.V
	return &s
}

// clampLimit ports Math.min(Math.max(limit ?? def, 1), max). JSON numbers
// are always finite (encoding/json rejects NaN/Infinity), so the float clamp
// cannot overflow the int conversion.
func clampLimit(v *float64, def, maxValue float64) int {
	f := def
	if v != nil {
		f = *v
	}
	return int(min(max(f, 1), maxValue))
}

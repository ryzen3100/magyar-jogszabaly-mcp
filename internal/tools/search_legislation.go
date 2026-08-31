// search_legislation — Full-text search across Hungarian statute provisions.
// Port of src/tools/search-legislation.ts: two-phase FTS5 search (BM25 rank
// query, then snippet re-MATCH restricted to the surviving rowids) with
// per-variant error degradation and a final LIKE tier.

package tools

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/fts"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/statute"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/store"
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

	// poolLimit caps each FTS tier's candidate fetch. Deliberately deep: the
	// per-tier ORDER BY already applies the doc-type/in-force boost (boostSQL),
	// so the cutoff keeps boosted candidates — regulating acts and decrees —
	// that raw bm25 buries under short provisions stuffed with one generic
	// token. The rank phase is a plain rowid scan (no snippet), so the extra
	// rows are cheap; snippets are still fetched only for the final rows.
	poolLimit = 500

	// Ranking weights of the final document-level re-rank. boostSQL mirrors
	// docType/noiseType/inForceBoost so the per-tier cutoff keeps the same
	// candidates the final ranking prefers.
	docTypeBoost   = 3.0  // törvény/rendelet titles
	noiseTypeBoost = -3.0 // utasítás/határozat/közlemény/helyesbítés titles
	inForceBoost   = 2.0  // status = in_force

	// boostSQL demotes noise document types (utasítás, határozat, közlemény,
	// helyesbítés) and promotes acts and decrees (törvény, rendelet) plus
	// in-force documents. Subtracted from bm25 (bm25 is negative, lower is
	// better), so a positive boost improves rank.
	boostSQL = `(
        CASE WHEN lower(ld.title) LIKE '%utasítás%' OR lower(ld.title) LIKE '%határozat%'
                  OR lower(ld.title) LIKE '%közlemény%' OR lower(ld.title) LIKE '%helyesbítés%'
             THEN -3.0
             WHEN lower(ld.title) LIKE '%törvény%' OR lower(ld.title) LIKE '%rendelet%'
             THEN 3.0
             ELSE 0 END
        + CASE WHEN ld.status = 'in_force' THEN 2.0 ELSE 0 END)`

	searchRankSQL = `
      SELECT
        lp.id as provision_id,
        lp.document_id,
        ld.title as document_title,
        ld.status,
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
        ld.status,
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

	// OR-tier filter. Broad OR tiers must not score bm25 over their whole
	// doclist, but the doclists of ultra-generic tokens ("Ha*", "és") also
	// bury the pool cut under provisions of noise documents. Pre-filtering
	// each OR tier to in-force, non-noise documents (the re-rank demotes
	// noise titles anyway; the AND tiers keep their candidate space) shrinks
	// the scored set ~5x. Combined with dropping ≤3-rune terms from OR
	// variants (fts.orTerms), this brings per-tier cost from ~5-10 s to
	// well under a second on the 72k corpus.
	searchPoolSQLFilter = ` AND ld.status = 'in_force'
        AND NOT (lower(ld.title) LIKE '%utasítás%' OR lower(ld.title) LIKE '%határozat%'
              OR lower(ld.title) LIKE '%közlemény%' OR lower(ld.title) LIKE '%helyesbítés%')`
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
	// loses highlight context) except for the broad OR tiers, whose MATCH
	// re-pays the full prefix-index setup and gets plain excerpts instead;
	// never re-MATCH unbounded.
	//
	// Tier merging: the first tier with any hit is NOT returned immediately —
	// on the 14.7k-document corpus the AND tiers match generic table-like
	// provisions (appendix lists full of numbers) that bury the regulating
	// provision plain bm25 ranks first. Instead every non-phrase tier feeds
	// a candidate pool, the pool is re-ranked by distinct query-term matches
	// (then tier specificity, then bm25), and only that final ranking is
	// returned. The exact-phrase tier keeps its early exit: a phrase hit is
	// the best possible precision.
	candidates := map[string]tierCandidate{} // key: documentTitle::provisionRef

	orFull := false // previous OR tier filled the pool
	for i, ftsQuery := range variants {
		// OR tiers pre-filter to in-force, non-noise documents (see
		// searchPoolSQLFilter); with resolvedDocID or a status filter the
		// match set is already tiny, so the extra filter is skipped.
		isOR := fts.HasORVariant(ftsQuery) && resolvedDocID == "" &&
			(args.Status == nil || *args.Status == "")
		// The stemmed-OR tier differs from the prefixed-OR tier only by stem
		// forms. When the prefixed tier already filled the pool, the stemmed
		// tier's unique contributions (a handful of documents, measured) don't
		// justify a second full scan — skip it.
		if isOR && orFull {
			continue
		}
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

		if isOR {
			query += searchPoolSQLFilter
		}
		query += " ORDER BY bm25(provisions_fts) - " + boostSQL + " LIMIT ?"
		params = append(params, poolLimit)

		ranked, err := queryRankedRows(ctx, db, query, params)
		if err != nil {
			lastErr = err
			continue // FTS query syntax error — try next variant
		}
		if len(ranked) == 0 {
			cleanTierRan = true
			continue
		}
		if i == 0 {
			return finishSearch(ctx, db, ftsQuery, dedupeRanked(ranked, limit), limit, "")
		}

		orFull = isOR && len(ranked) >= poolLimit

		for _, row := range ranked {
			key := row.documentTitle + "::" + row.provisionRef
			if c, ok := candidates[key]; !ok || row.relevance < c.row.relevance {
				candidates[key] = tierCandidate{row: row, tier: i, fts: ftsQuery}
			}
		}
	}

	if len(candidates) == 0 {
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

	pool := make([]tierCandidate, 0, len(candidates))
	for _, c := range candidates {
		pool = append(pool, c)
	}

	// Re-rank the merged pool at DOCUMENT level: idf-weighted distinct term
	// coverage of the whole document plus doc-type / in-force / title-match
	// boosts; per-provision bm25 only breaks ties. On the 72k-document corpus
	// per-provision match counts are dominated by generic tokens ("3", "fizet")
	// repeated across thousands of unrelated documents, while the regulating
	// act covers the rare query terms somewhere in its text.
	terms := fts.QueryTerms(fts.SanitizeInput(*args.Query))
	if err := rerankPool(ctx, db, pool, terms); err != nil {
		lastErr = err
	}

	bestTier := 0
	deduped := make([]rankedRow, 0, limit)
	seen := map[string]bool{}
	for _, c := range pool {
		key := c.row.documentTitle + "::" + c.row.provisionRef
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, c.row)
		if bestTier == 0 || c.tier < bestTier {
			bestTier = c.tier
		}
		if len(deduped) >= limit {
			break
		}
	}

	strategy := ""
	if bestTier > 0 {
		strategy = "broadened"
	}
	return finishSearch(ctx, db, pool[0].fts, deduped, limit, strategy)
}

// finishSearch shapes a final ranked row set: snippets (phase B, same MATCH
// expression), JSON mapping and response metadata. strategy is the
// query_strategy override ("" = unset, "broadened").
func finishSearch(ctx context.Context, db *sql.DB, ftsQuery string, deduped []rankedRow, limit int, strategy string,
) (any, ResponseMetadata, error) {
	if len(deduped) > limit {
		deduped = deduped[:limit]
	}
	snippets, err := fetchSnippets(ctx, db, ftsQuery, deduped)
	if err != nil {
		return nil, ResponseMetadata{}, fmt.Errorf("fetch snippets: %w", err)
	}
	results := make([]SearchLegislationResult, 0, min(len(deduped), limit))
	for _, row := range deduped {
		res := toResult(row)
		if snippet, ok := snippets[row.provisionID]; ok {
			res.Snippet = snippet
		}
		results = append(results, res)
	}
	meta := GenerateResponseMetadata(ctx, db)
	meta.QueryStrategy = strategy
	return results, meta, nil
}

// rerankPool scores every candidate by its document's relevance and sorts the
// pool best-first. Document score = sum of idf over the distinct query terms
// the document matches anywhere in its text (rare terms count more), plus the
// doc-type / in-force / title-match boosts. Ties break on per-provision bm25.
// Both the term-coverage scan and the document frequencies (fts.TermDocFreq,
// fts.DocTermHits) are restricted to the pool's document IDs: pool-restricted
// df preserves the idf ORDERING between terms — the only thing the re-rank
// consumes — while keeping every query bounded to the pool.
func rerankPool(ctx context.Context, db *sql.DB, pool []tierCandidate, terms []string) error {
	if len(pool) == 0 {
		return nil
	}
	docIDs := make([]string, 0, len(pool))
	for _, c := range pool {
		docIDs = append(docIDs, c.row.documentID)
	}
	slices.Sort(docIDs)
	docIDs = slices.Compact(docIDs)
	hits, err := fts.DocTermHits(ctx, db, terms, docIDs)
	if err != nil {
		return err
	}
	df, err := fts.TermDocFreq(ctx, db, terms, docIDs)
	if err != nil {
		return err
	}
	totalDocs := store.CachedCount(ctx, db, "SELECT COUNT(*) FROM legal_documents")
	idf := make(map[string]float64, len(terms))
	for _, term := range terms {
		if d := df[term]; d > 0 {
			idf[term] = math.Log(1 + float64(totalDocs)/float64(d))
		}
	}
	// Score first, then sort — the comparator must read the score that belongs
	// to the element it compares (a parallel slice indexed by pre-sort position
	// goes stale as soon as sort starts swapping).
	type scoredCandidate struct {
		cand tierCandidate
		s    float64
	}
	scoredPool := make([]scoredCandidate, len(pool))
	for i, c := range pool {
		s := typeBoost(c.row.documentTitle) + statusBoost(c.row.status)
		for _, term := range terms {
			if hits[term][c.row.documentID] {
				s += idf[term]
			}
		}
		if titleTermMatch(c.row.documentTitle, terms) {
			s += 1
		}
		scoredPool[i] = scoredCandidate{cand: c, s: s}
	}
	slices.SortStableFunc(scoredPool, func(a, b scoredCandidate) int {
		if c := cmp.Compare(b.s, a.s); c != 0 {
			return c
		}
		return cmp.Compare(a.cand.row.relevance, b.cand.row.relevance)
	})
	for i := range pool {
		pool[i] = scoredPool[i].cand
	}
	return nil
}

// typeBoost promotes acts and decrees over utasítás/határozat-style documents;
// statusBoost promotes in-force documents. Mirrors boostSQL.
func typeBoost(title string) float64 {
	t := strings.ToLower(title)
	switch {
	case strings.Contains(t, "utasítás"), strings.Contains(t, "határozat"),
		strings.Contains(t, "közlemény"), strings.Contains(t, "helyesbítés"):
		return noiseTypeBoost
	case strings.Contains(t, "törvény"), strings.Contains(t, "rendelet"):
		return docTypeBoost
	}
	return 0
}

func statusBoost(status string) float64 {
	if status == "in_force" {
		return inForceBoost
	}
	return 0
}

// titleTermMatch reports whether the document title shares a token-level
// prefix (≥4 runes, both directions) with any query term's stemmed form:
// "kávézókról" matches "kávézó", and "a munka törvénykönyvéről" matches
// "munkavállaló" via "munka". A flat +1: per-term title boosts would
// over-reward boilerplate titles enumerating amounts and dates
// ("20 000 forintos címletű bankjegy…").
func titleTermMatch(title string, terms []string) bool {
	tokens := strings.Fields(strings.ToLower(title))
	for _, term := range terms {
		stem := strings.ToLower(fts.StemTerm(strings.TrimSuffix(term, "*")))
		if stem == "" {
			continue
		}
		for _, tok := range tokens {
			if commonPrefixLen(tok, stem) >= 4 {
				return true
			}
		}
	}
	return false
}

// commonPrefixLen returns the length of the longest common prefix of two
// strings (rune-based).
func commonPrefixLen(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	n := min(len(ra), len(rb))
	for i := 0; i < n; i++ {
		if ra[i] != rb[i] {
			return i
		}
	}
	return n
}

// tierCandidate is one merged-tier candidate row: which variant produced it
// (tier 0 is the exact-phrase tier) and that variant's MATCH expression, kept
// for the phase-B snippet query.
type tierCandidate struct {
	row  rankedRow
	tier int
	fts  string
}

// poolRowIDs returns the provision rowids of a candidate pool, in pool order.
func poolRowIDs(pool []tierCandidate) []int64 {
	ids := make([]int64, 0, len(pool))
	for _, c := range pool {
		ids = append(ids, c.row.provisionID)
	}
	return ids
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
	status        string
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
			&r.status, &r.provisionRef, &r.chapter, &r.section, &r.title, &r.relevance); err != nil {
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
		if err := rows.Scan(&r.documentID, &r.documentTitle, &r.status, &r.provisionRef,
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
//
// The OR tiers are excluded from the re-MATCH: their broad MATCH expressions
// re-pay the full prefix-index setup (~1 s per broad term, measured) just to
// highlight rows we already have. Those tiers get the provision's opening
// text instead — no highlight markers, but no information lost.
func fetchSnippets(ctx context.Context, db *sql.DB, ftsQuery string, deduped []rankedRow) (map[int64]string, error) {
	if fts.HasORVariant(ftsQuery) {
		return fetchPlainExcerpts(ctx, db, deduped)
	}
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
			return nil, fmt.Errorf("fetch snippets: %w", err)
		}
		snippets[rowid] = snippet
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("fetch snippets: %w", err)
	}
	return snippets, nil
}

// fetchPlainExcerpts returns each provision's opening text, replacing the
// highlighted snippet for OR-tier results (see fetchSnippets).
func fetchPlainExcerpts(ctx context.Context, db *sql.DB, deduped []rankedRow) (map[int64]string, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(deduped)), ",")
	params := make([]any, 0, len(deduped))
	for _, row := range deduped {
		params = append(params, row.provisionID)
	}
	rows, err := db.QueryContext(ctx,
		"SELECT id, substr(content, 1, 200) FROM legal_provisions WHERE id IN ("+placeholders+")",
		params...)
	if err != nil {
		return nil, fmt.Errorf("fetch excerpts: %w", err)
	}
	defer rows.Close()

	excerpts := map[int64]string{}
	for rows.Next() {
		var rowid int64
		var excerpt string
		if err := rows.Scan(&rowid, &excerpt); err != nil {
			return nil, fmt.Errorf("fetch excerpts: %w", err)
		}
		excerpts[rowid] = excerpt
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("fetch excerpts: %w", err)
	}
	return excerpts, nil
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
	return new(ns.V)
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

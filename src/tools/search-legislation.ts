/**
 * search_legislation — Full-text search across Hungarian statute provisions.
 */

import type Database from '@ansvar/mcp-sqlite';
import { buildFtsQueryVariants, buildLikePattern, sanitizeFtsInput } from '../utils/fts-query.js';
import { resolveDocumentId } from '../utils/statute-id.js';
import { generateResponseMetadata, type ToolResponse } from '../utils/metadata.js';

export interface SearchLegislationInput {
  query: string;
  document_id?: string;
  status?: string;
  limit?: number;
}

export interface SearchLegislationResult {
  document_id: string;
  document_title: string;
  provision_ref: string;
  chapter: string | null;
  section: string;
  title: string | null;
  snippet: string;
  relevance: number;
}

/** Phase A ranking row; provision_id (= fts rowid) keys the phase B snippet fetch. */
type RankedRow = SearchLegislationResult & { provision_id: number };

const DEFAULT_LIMIT = 10;
const MAX_LIMIT = 50;

export async function searchLegislation(
  db: InstanceType<typeof Database>,
  input: SearchLegislationInput,
): Promise<ToolResponse<SearchLegislationResult[]>> {
  if (!input.query || input.query.trim().length === 0) {
    return { results: [], _metadata: generateResponseMetadata(db) };
  }

  const limit = Math.min(Math.max(input.limit ?? DEFAULT_LIMIT, 1), MAX_LIMIT);
  // Fetch extra rows to account for deduplication
  const fetchLimit = limit * 2;
  const queryVariants = buildFtsQueryVariants(sanitizeFtsInput(input.query));

  // Resolve document_id from title if provided (same resolution as get_provision)
  let resolvedDocId: string | undefined;
  if (input.document_id) {
    const resolved = resolveDocumentId(db, input.document_id);
    resolvedDocId = resolved ?? undefined;
    if (!resolved) {
      return {
        results: [],
        _metadata: {
          ...generateResponseMetadata(db),
          note: `No document found matching "${input.document_id}"`,
        },
      };
    }
  }

  let queryStrategy = 'none';
  // ponytail: rank rowids first without snippet(), then snippet() only the final deduped rows — snippet over every match dominates high-fanout queries. Phase B reuses the SAME MATCH expression (plain-rowid lookup loses highlight context); never re-MATCH unbounded.
  for (const ftsQuery of queryVariants) {
    let sql = `
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
    `;
    const params: (string | number)[] = [ftsQuery];

    if (resolvedDocId) {
      sql += ' AND lp.document_id = ?';
      params.push(resolvedDocId);
    }

    if (input.status) {
      sql += ' AND ld.status = ?';
      params.push(input.status);
    }

    sql += ' ORDER BY relevance LIMIT ?';
    params.push(fetchLimit);

    try {
      const ranked = db.prepare(sql).all(...params) as RankedRow[];
      if (ranked.length > 0) {
        queryStrategy = ftsQuery === queryVariants[0] ? 'exact' : 'fallback';
        const deduped = deduplicateResults(ranked, limit) as RankedRow[];
        const ids = deduped.map((row) => row.provision_id);
        const snippetRows = db
          .prepare(
            `
              SELECT rowid, snippet(provisions_fts, 0, '>>>', '<<<', '...', 32) as snippet
              FROM provisions_fts
              WHERE provisions_fts MATCH ? AND rowid IN (${ids.map(() => '?').join(',')})
            `,
          )
          .all(ftsQuery, ...ids) as { rowid: number; snippet: string }[];
        const snippets = new Map(snippetRows.map((row) => [row.rowid, row.snippet]));
        const results: SearchLegislationResult[] = deduped.map((row) => ({
          document_id: row.document_id,
          document_title: row.document_title,
          provision_ref: row.provision_ref,
          chapter: row.chapter,
          section: row.section,
          title: row.title,
          snippet: snippets.get(row.provision_id) ?? '',
          relevance: row.relevance,
        }));
        return {
          results,
          _metadata: {
            ...generateResponseMetadata(db),
            ...(queryStrategy === 'fallback' ? { query_strategy: 'broadened' } : {}),
          },
        };
      }
    } catch {
      // FTS query syntax error — try next variant
      continue;
    }
  }

  // LIKE fallback — final tier when FTS5 returns no results
  {
    const likePattern = buildLikePattern(sanitizeFtsInput(input.query));
    let likeSql = `
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
      WHERE lp.content LIKE ?
    `;
    const likeParams: (string | number)[] = [likePattern];

    if (resolvedDocId) {
      likeSql += ' AND lp.document_id = ?';
      likeParams.push(resolvedDocId);
    }

    if (input.status) {
      likeSql += ' AND ld.status = ?';
      likeParams.push(input.status);
    }

    likeSql += ' LIMIT ?';
    likeParams.push(fetchLimit);

    try {
      const rows = db.prepare(likeSql).all(...likeParams) as SearchLegislationResult[];
      if (rows.length > 0) {
        return {
          results: deduplicateResults(rows, limit),
          _metadata: {
            ...generateResponseMetadata(db),
            query_strategy: 'like_fallback',
          },
        };
      }
    } catch {
      // LIKE query failed
    }
  }

  return { results: [], _metadata: generateResponseMetadata(db) };
}

/**
 * Deduplicate search results by document_title + provision_ref.
 * Duplicate document IDs (numeric vs slug) cause the same provision to appear twice.
 * Keeps the first (highest-ranked) occurrence.
 */
function deduplicateResults(
  rows: SearchLegislationResult[],
  limit: number,
): SearchLegislationResult[] {
  const seen = new Set<string>();
  const deduped: SearchLegislationResult[] = [];
  for (const row of rows) {
    const key = `${row.document_title}::${row.provision_ref}`;
    if (seen.has(key)) continue;
    seen.add(key);
    deduped.push(row);
    if (deduped.length >= limit) break;
  }
  return deduped;
}

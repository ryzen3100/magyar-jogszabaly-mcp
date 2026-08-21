/**
 * build_legal_stance — Build a comprehensive set of citations for a legal question.
 *
 * Thin wrapper over search_legislation with research-oriented defaults
 * (lower result cap, no status filter).
 */

import type Database from '@ansvar/mcp-sqlite';
import { searchLegislation } from './search-legislation.js';
import type { ToolResponse } from '../utils/metadata.js';

export interface BuildLegalStanceInput {
  query: string;
  document_id?: string;
  limit?: number;
}

export interface LegalStanceResult {
  document_id: string;
  document_title: string;
  provision_ref: string;
  section: string;
  title: string | null;
  snippet: string;
  relevance: number;
}

export async function buildLegalStance(
  db: InstanceType<typeof Database>,
  input: BuildLegalStanceInput,
): Promise<ToolResponse<LegalStanceResult[]>> {
  const response = await searchLegislation(db, {
    query: input.query,
    document_id: input.document_id,
    limit: Math.min(Math.max(input.limit ?? 5, 1), 20),
  });
  return {
    results: response.results.map(({ chapter: _chapter, ...rest }) => rest),
    _metadata: response._metadata,
  };
}

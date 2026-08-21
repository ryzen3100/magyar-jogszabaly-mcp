/**
 * get_provision — Retrieve specific provision(s) from an Hungarian statute.
 */

import type Database from '@ansvar/mcp-sqlite';
import { resolveDocumentId } from '../utils/statute-id.js';
import { generateResponseMetadata, type ToolResponse } from '../utils/metadata.js';

export interface GetProvisionInput {
  document_id: string;
  section?: string;
  provision_ref?: string;
}

interface ProvisionResult {
  document_id: string;
  document_title: string;
  provision_ref: string;
  chapter: string | null;
  section: string;
  title: string | null;
  content: string;
  section_number?: string;
  url?: string;
}

export async function getProvision(
  db: InstanceType<typeof Database>,
  input: GetProvisionInput,
): Promise<ToolResponse<ProvisionResult[]>> {
  const resolvedId = resolveDocumentId(db, input.document_id);
  if (!resolvedId) {
    return {
      results: [],
      _metadata: {
        ...generateResponseMetadata(db),
        note: `No document found matching "${input.document_id}"`,
      },
    };
  }

  const docRow = db.prepare(
    'SELECT id, title, url FROM legal_documents WHERE id = ?'
  ).get(resolvedId) as { id: string; title: string; url: string | null } | undefined;
  if (!docRow) {
    return { results: [], _metadata: generateResponseMetadata(db) };
  }

  const toResult = (p: Record<string, unknown>) => ({
    document_id: resolvedId,
    document_title: docRow.title,
    provision_ref: String(p.provision_ref),
    chapter: p.chapter as string | null,
    section: String(p.section),
    title: p.title as string | null,
    content: String(p.content),
    section_number: String(p.provision_ref).replace(/^s/, ''),
    url: docRow.url ?? undefined,
  });

  // Specific provision lookup — one OR-query covers exact, "s"-prefixed,
  // section-column, and fuzzy matches (same pattern as validate_citation).
  const ref = input.provision_ref ?? input.section;
  if (ref) {
    const refTrimmed = ref.trim();

    const provision = db.prepare(
      "SELECT * FROM legal_provisions WHERE document_id = ? AND (provision_ref = ? OR provision_ref = ? OR section = ? OR provision_ref LIKE ? OR section LIKE ?)"
    ).get(resolvedId, refTrimmed, `s${refTrimmed}`, refTrimmed, `%${refTrimmed}%`, `%${refTrimmed}%`) as Record<string, unknown> | undefined;

    if (provision) {
      return {
        results: [toResult(provision)],
        _metadata: generateResponseMetadata(db),
      };
    }

    return {
      results: [],
      _metadata: {
        ...generateResponseMetadata(db),
        note: `Provision "${ref}" not found in document "${resolvedId}"`,
      },
    };
  }

  // Return all provisions for the document
  const provisions = db.prepare(
    'SELECT * FROM legal_provisions WHERE document_id = ? ORDER BY id'
  ).all(resolvedId) as Record<string, unknown>[];

  return {
    results: provisions.map(toResult),
    _metadata: generateResponseMetadata(db),
  };
}

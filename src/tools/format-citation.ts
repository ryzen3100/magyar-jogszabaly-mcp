/**
 * format_citation — Format an Hungarian legal citation per standard conventions.
 */

import { generateResponseMetadata, type ToolResponse } from '../utils/metadata.js';
import { parseCitation } from './validate-citation.js';
import { resolveDocumentId } from '../utils/statute-id.js';
import type Database from '@ansvar/mcp-sqlite';

export interface FormatCitationInput {
  citation: string;
  format?: 'full' | 'pinpoint';
}

interface FormatCitationResult {
  original: string;
  formatted: string;
  format: string;
}

export async function formatCitationTool(
  db: InstanceType<typeof Database>,
  input: FormatCitationInput,
): Promise<ToolResponse<FormatCitationResult>> {
  const format = input.format ?? 'full';
  const trimmed = input.citation.trim();

  const parsed = parseCitation(trimmed);

  let section: string | undefined;
  let act: string;

  if (parsed) {
    // Structured references (Hungarian formal, database ID) additionally get
    // their full title resolved from the database.
    const docId = parsed.structured ? resolveDocumentId(db, parsed.documentRef) : null;
    const doc = docId
      ? db.prepare('SELECT title FROM legal_documents WHERE id = ?').get(docId) as { title: string } | undefined
      : undefined;
    act = doc?.title ?? parsed.documentRef;
    section = parsed.sectionRef;
  } else {
    act = trimmed;
  }

  const formatted = !section ? act : format === 'pinpoint' ? `${section}. §` : `${act} ${section}. §`;

  return {
    results: { original: input.citation, formatted, format },
    _metadata: generateResponseMetadata(db),
  };
}

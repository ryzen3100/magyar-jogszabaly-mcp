/**
 * format_citation — Format an Hungarian legal citation per standard conventions.
 */

import { generateResponseMetadata, type ToolResponse } from '../utils/metadata.js';
import { resolveDocumentId } from '../utils/statute-id.js';
import type Database from '@ansvar/mcp-sqlite';

export interface FormatCitationInput {
  citation: string;
  format?: 'full' | 'short' | 'pinpoint';
}

export interface FormatCitationResult {
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

  // Parse Hungarian format: "YYYY. évi [ROMAN]. törvény NNN. §" or "6:272. §"
  const hungarianMatch = trimmed.match(/^(\d{4})\.\s*évi\s+([IVXLCDM]+)\.\s*törvény(?:\s+(\d+(?::\d+)?(?:\/[A-Za-z])?)\.\s*§)?/i);

  // Parse "document_id sNNN" format (e.g., "hu-law-2012-1-00-00 s116")
  const dbIdMatch = trimmed.match(/^(hu-law-\d{4}-\d+-\d{2}-\d{2})\s+s(\d+[A-Za-z]*(?:\/[A-Za-z])?)$/i);

  // Parse "Section N <Act>" or "Section N, <Act>"
  const sectionFirst = trimmed.match(/^Section\s+(\d+[A-Za-z]*(?:\(\d+\))?)\s*[,;]?\s+(.+)$/i);
  // Parse "<Act> s N" or "<Act>, s N" or "<Act> Section N"
  const sectionLast = trimmed.match(/^(.+?)\s*[,;]?\s+(?:s\.?\s+|Section\s+)(\d+[A-Za-z]*(?:\(\d+\))?)$/i);

  let section: string | undefined;
  let act: string;

  if (hungarianMatch) {
    // Look up full title from database
    const hunDocRef = `${hungarianMatch[1]}. évi ${hungarianMatch[2]}. törvény`;
    const hunDocId = resolveDocumentId(db, hunDocRef);
    if (hunDocId) {
      const doc = db.prepare('SELECT title FROM legal_documents WHERE id = ?').get(hunDocId) as { title: string } | undefined;
      act = doc?.title ?? hunDocRef;
    } else {
      act = hunDocRef;
    }
    section = hungarianMatch[3];
  } else if (dbIdMatch) {
    // Look up the title from the database
    const doc = db.prepare('SELECT title FROM legal_documents WHERE id = ?').get(dbIdMatch[1]) as { title: string } | undefined;
    act = doc?.title ?? dbIdMatch[1];
    section = dbIdMatch[2];
  } else {
    section = sectionFirst?.[1] ?? sectionLast?.[2];
    act = sectionFirst?.[2] ?? sectionLast?.[1] ?? trimmed;
  }

  let formatted: string;
  switch (format) {
    case 'short':
      formatted = section ? `${act} ${section}. §` : act;
      break;
    case 'pinpoint':
      formatted = section ? `${section}. §` : act;
      break;
    case 'full':
    default:
      formatted = section ? `${act} ${section}. §` : act;
      break;
  }

  return {
    results: { original: input.citation, formatted, format },
    _metadata: generateResponseMetadata(db),
  };
}

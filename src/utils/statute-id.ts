/**
 * Statute ID resolution for Hungarian Law MCP.
 *
 * Resolves fuzzy document references (titles, IDs) to database document IDs.
 * Hungarian legislation identifier resolution.
 * Supports:
 * - Database IDs: "hu-law-2012-1-00-00", "act-cxii-2011-info-self-determination"
 * - Hungarian formal: "2012. évi I. törvény"
 * - English titles: "Privacy Act 1988"
 * - Fuzzy title substring match
 */

import type Database from '@ansvar/mcp-sqlite';

/**
 * Convert a Roman numeral string to an Arabic number.
 */
function romanToArabic(roman: string): number {
  const values: Record<string, number> = { I: 1, V: 5, X: 10, L: 50, C: 100, D: 500, M: 1000 };
  let result = 0;
  const upper = roman.toUpperCase();
  for (let i = 0; i < upper.length; i++) {
    const current = values[upper[i]] ?? 0;
    const next = values[upper[i + 1]] ?? 0;
    result += current < next ? -current : current;
  }
  return result;
}

/**
 * Try to parse a Hungarian formal reference like "2012. évi I. törvény"
 * and convert it to the database ID format "hu-law-2012-1-00-00".
 */
function parseHungarianReference(input: string): string | null {
  const match = input.match(/(\d{4})\.\s*évi\s+([IVXLCDM]+)\.\s*törvény/i);
  if (!match) return null;
  const year = match[1];
  const lawNumber = romanToArabic(match[2]);
  return `hu-law-${year}-${lawNumber}-00-00`;
}

/**
 * Resolve a document identifier to a database document ID.
 * Supports:
 * - Direct ID match (e.g., "hu-law-2012-1-00-00")
 * - Hungarian formal format (e.g., "2012. évi I. törvény")
 * - Title match (e.g., "Privacy Act 1988", "Privacy Act")
 * - Short name/abbreviation match (e.g., "SOCI Act")
 * - Fuzzy title substring match
 */
export function resolveDocumentId(
  db: InstanceType<typeof Database>,
  input: string,
): string | null {
  const trimmed = input.trim();
  if (!trimmed) return null;

  // Direct ID match (exact)
  const directMatch = db.prepare(
    'SELECT id FROM legal_documents WHERE id = ?'
  ).get(trimmed) as { id: string } | undefined;
  if (directMatch) return directMatch.id;

  // Hungarian formal format: "2012. évi I. törvény" → "hu-law-2012-1-00-00"
  const hungarianId = parseHungarianReference(trimmed);
  if (hungarianId) {
    const hunMatch = db.prepare(
      'SELECT id FROM legal_documents WHERE id = ?'
    ).get(hungarianId) as { id: string } | undefined;
    if (hunMatch) return hunMatch.id;
  }

  // hu-law prefix match (strip trailing spaces/extra chars)
  if (trimmed.startsWith('hu-law-')) {
    const idMatch = trimmed.match(/^(hu-law-\d{4}-\d+-\d{2}-\d{2})/);
    if (idMatch) {
      const match = db.prepare(
        'SELECT id FROM legal_documents WHERE id = ?'
      ).get(idMatch[1]) as { id: string } | undefined;
      if (match) return match.id;
    }
  }

  // Title/short_name substring match
  const titleResult = db.prepare(
    "SELECT id FROM legal_documents WHERE title LIKE ? OR short_name LIKE ? OR title_en LIKE ? LIMIT 1"
  ).get(`%${trimmed}%`, `%${trimmed}%`, `%${trimmed}%`) as { id: string } | undefined;
  if (titleResult) return titleResult.id;

  // Case-insensitive fallback
  const lowerResult = db.prepare(
    "SELECT id FROM legal_documents WHERE LOWER(title) LIKE LOWER(?) OR LOWER(short_name) LIKE LOWER(?) OR LOWER(title_en) LIKE LOWER(?) LIMIT 1"
  ).get(`%${trimmed}%`, `%${trimmed}%`, `%${trimmed}%`) as { id: string } | undefined;
  if (lowerResult) return lowerResult.id;

  return null;
}

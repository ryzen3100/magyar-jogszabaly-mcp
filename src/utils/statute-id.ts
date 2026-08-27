/**
 * Statute ID resolution for Hungarian Law MCP.
 *
 * Resolves fuzzy document references (titles, IDs) to database document IDs.
 * Hungarian legislation identifier resolution.
 * Supports:
 * - Database IDs: "hu-law-2012-1-00-00", "act-cxii-2011-info-self-determination"
 * - Hungarian formal: "2012. évi I. törvény"
 * - English/foreign titles: e.g., "Data Protection Act 2011"
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
 * - Title match (e.g., "Infotörvény", "Data Protection Act")
 * - Short name/abbreviation match (e.g., "Ptk.", "Btk.")
 * - Fuzzy title substring match
 */
export function resolveDocumentId(
  db: InstanceType<typeof Database>,
  input: string,
): string | null {
  const trimmed = input.trim();
  if (!trimmed) return null;

  // Exact-ID candidates in priority order: trimmed input, Hungarian formal
  // reference ("2012. évi I. törvény" → "hu-law-2012-1-00-00"), hu-law
  // prefix with trailing extra characters stripped.
  const candidates = [trimmed];
  const hungarianId = parseHungarianReference(trimmed);
  if (hungarianId) candidates.push(hungarianId);
  const prefixId = trimmed.match(/^(hu-law-\d{4}-\d+-\d{2}-\d{2})/)?.[1];
  if (prefixId) candidates.push(prefixId);

  for (const candidate of candidates) {
    const hit = db.prepare(
      'SELECT id FROM legal_documents WHERE id = ?'
    ).get(candidate) as { id: string } | undefined;
    if (hit) return hit.id;
  }

  // Title/short_name substring match — single case-insensitive pass
  // (LOWER() is a superset of plain LIKE: it folds ASCII case like LIKE does)
  const titleResult = db.prepare(
    "SELECT id FROM legal_documents WHERE LOWER(title) LIKE LOWER(?) OR LOWER(short_name) LIKE LOWER(?) OR LOWER(title_en) LIKE LOWER(?) LIMIT 1"
  ).get(`%${trimmed}%`, `%${trimmed}%`, `%${trimmed}%`) as { id: string } | undefined;
  if (titleResult) return titleResult.id;

  return null;
}

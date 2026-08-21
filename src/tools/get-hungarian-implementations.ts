/**
 * get_hungarian_implementations — Find Hungarian statutes that reference a specific EU directive/regulation.
 */

import type Database from '@ansvar/mcp-sqlite';
import { euAvailable, euUnavailable } from '../capabilities.js';
import { generateResponseMetadata, type ToolResponse } from '../utils/metadata.js';

export interface GetHungarianImplementationsInput {
  eu_document_id: string;
  primary_only?: boolean;
  in_force_only?: boolean;
}

interface HungarianImplementationResult {
  document_id: string;
  document_title: string;
  status: string;
  reference_type: string;
  implementation_status: string | null;
  is_primary: boolean;
  reference_count: number;
}

export async function getHungarianImplementations(
  db: InstanceType<typeof Database>,
  input: GetHungarianImplementationsInput,
): Promise<ToolResponse<HungarianImplementationResult[]>> {
  if (!euAvailable(db)) return euUnavailable(db);

  let sql = `
    SELECT
      ld.id as document_id,
      ld.title as document_title,
      ld.status,
      er.reference_type,
      MAX(er.implementation_status) as implementation_status,
      MAX(er.is_primary_implementation) as is_primary,
      COUNT(*) as reference_count
    FROM eu_references er
    JOIN legal_documents ld ON ld.id = er.document_id
    WHERE er.eu_document_id = ?
  `;
  const params: (string | number)[] = [input.eu_document_id];

  if (input.primary_only) {
    sql += ' AND er.is_primary_implementation = 1';
  }

  if (input.in_force_only) {
    sql += " AND ld.status = 'in_force'";
  }

  sql += ' GROUP BY ld.id, er.reference_type ORDER BY is_primary DESC, reference_count DESC';

  const rows = db.prepare(sql).all(...params) as HungarianImplementationResult[];
  return { results: rows, _metadata: generateResponseMetadata(db) };
}

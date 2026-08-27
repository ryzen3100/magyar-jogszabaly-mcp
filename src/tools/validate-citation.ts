/**
 * validate_citation — Validate an Hungarian legal citation against the database.
 */

import type Database from '@ansvar/mcp-sqlite';
import { resolveDocumentId } from '../utils/statute-id.js';
import { generateResponseMetadata, type ToolResponse } from '../utils/metadata.js';

export interface ValidateCitationInput {
  citation: string;
}

interface ValidateCitationResult {
  valid: boolean;
  citation: string;
  normalized?: string;
  document_id?: string;
  document_title?: string;
  provision_ref?: string;
  status?: string;
  warnings: string[];
}

/**
 * Parse an Hungarian legal citation.
 * Supports:
 * - Hungarian formal: "2012. évi I. törvény 116. §"
 * - Database ID: "hu-law-2012-1-00-00 s116"
 * - "Section 3 Infotörvény" / "Section 3, Infotörvény"
 * - "Infotörvény s 3" / "Infotörvény, s 3"
 * - "[Act Title Year] s N"
 * - "s 13" (section only, no document)
 * - Plain document reference (e.g., "Infotörvény")
 */
const SECTION_FIRST_RE = /^Section\s+(\d+[A-Za-z]*(?:\(\d+\))?)\s*[,;]?\s+(.+)$/i;
const SECTION_LAST_RE = /^(.+?)\s*[,;]?\s+(?:s\.?\s+|Section\s+)(\d+[A-Za-z]*(?:\(\d+\))?)$/i;

export interface ParsedCitation {
  documentRef: string;
  sectionRef?: string;
  /** True when documentRef is a formal Hungarian reference or a database ID. */
  structured: boolean;
}

export function parseCitation(citation: string): ParsedCitation | null {
  const trimmed = citation.trim();
  if (!trimmed) return null;

  // Hungarian formal: "2012. évi I. törvény 116. §" or "2013. évi V. törvény 6:272. §" or "116/A. §"
  const hungarianFull = trimmed.match(
    /^(\d{4}\.\s*évi\s+[IVXLCDM]+\.\s*törvény)\s+(\d+(?::\d+)?(?:\/[A-Za-z])?)\.\s*§/i
  );
  if (hungarianFull) {
    return { documentRef: hungarianFull[1].trim(), sectionRef: hungarianFull[2], structured: true };
  }

  // Hungarian document only: "2012. évi I. törvény" (no section)
  const hungarianDoc = trimmed.match(
    /^(\d{4}\.\s*évi\s+[IVXLCDM]+\.\s*törvény)$/i
  );
  if (hungarianDoc) {
    return { documentRef: hungarianDoc[1].trim(), structured: true };
  }

  // Database ID + section: "hu-law-2012-1-00-00 s116" or "hu-law-2013-5-00-00 s6:272"
  const dbIdWithSection = trimmed.match(
    /^(hu-law-\d{4}-\d+-\d{2}-\d{2})\s+s?(\d+(?::\d+)?(?:\/[A-Za-z])?)$/i
  );
  if (dbIdWithSection) {
    return { documentRef: dbIdWithSection[1], sectionRef: dbIdWithSection[2], structured: true };
  }

  // Database ID only: "hu-law-2012-1-00-00"
  const dbIdOnly = trimmed.match(
    /^(hu-law-\d{4}-\d+-\d{2}-\d{2})$/
  );
  if (dbIdOnly) {
    return { documentRef: dbIdOnly[1], structured: true };
  }

  // "Section N <Act>" or "Section N, <Act>"
  const sectionFirst = trimmed.match(SECTION_FIRST_RE);
  if (sectionFirst) {
    return { documentRef: sectionFirst[2].trim(), sectionRef: sectionFirst[1], structured: false };
  }

  // "<Act> s N" / "<Act>, s N" / "<Act> s. N" or "<Act> Section N" / "<Act>, Section N"
  const sectionLast = trimmed.match(SECTION_LAST_RE);
  if (sectionLast) {
    return { documentRef: sectionLast[1].trim(), sectionRef: sectionLast[2], structured: false };
  }

  // Just a document reference (no section)
  return { documentRef: trimmed, structured: false };
}

export async function validateCitationTool(
  db: InstanceType<typeof Database>,
  input: ValidateCitationInput,
): Promise<ToolResponse<ValidateCitationResult>> {
  const warnings: string[] = [];
  const parsed = parseCitation(input.citation);

  if (!parsed) {
    return {
      results: {
        valid: false,
        citation: input.citation,
        warnings: ['Could not parse citation format'],
      },
      _metadata: generateResponseMetadata(db),
    };
  }

  const docId = resolveDocumentId(db, parsed.documentRef);
  if (!docId) {
    return {
      results: {
        valid: false,
        citation: input.citation,
        warnings: [`Document not found: "${parsed.documentRef}"`],
      },
      _metadata: generateResponseMetadata(db),
    };
  }

  const doc = db.prepare(
    'SELECT id, title, status FROM legal_documents WHERE id = ?'
  ).get(docId) as { id: string; title: string; status: string };

  if (doc.status === 'repealed') {
    warnings.push(`WARNING: This statute has been repealed.`);
  } else if (doc.status === 'amended') {
    warnings.push(`Note: This statute has been amended. Verify you are referencing the current version.`);
  }

  if (parsed.sectionRef) {
    // Normalize section ref: "6:272" → try "s6272", "s6:272", "6:272", "6272"
    const sectionClean = parsed.sectionRef.replace(':', '');
    const provision = db.prepare(
      "SELECT provision_ref FROM legal_provisions WHERE document_id = ? AND (provision_ref = ? OR provision_ref = ? OR provision_ref = ? OR provision_ref = ? OR section = ? OR section = ?)"
    ).get(docId, parsed.sectionRef, `s${parsed.sectionRef}`, `s${sectionClean}`, sectionClean, parsed.sectionRef, sectionClean) as { provision_ref: string } | undefined;

    if (!provision) {
      return {
        results: {
          valid: false,
          citation: input.citation,
          document_id: docId,
          document_title: doc.title,
          warnings: [...warnings, `Provision "${parsed.sectionRef}. §" not found in ${doc.title}`],
        },
        _metadata: generateResponseMetadata(db),
      };
    }

    return {
      results: {
        valid: true,
        citation: input.citation,
        normalized: `${doc.title} ${parsed.sectionRef}. § (Section ${parsed.sectionRef})`,
        document_id: docId,
        document_title: doc.title,
        provision_ref: provision.provision_ref,
        status: doc.status,
        warnings,
      },
      _metadata: generateResponseMetadata(db),
    };
  }

  return {
    results: {
      valid: true,
      citation: input.citation,
      normalized: doc.title,
      document_id: docId,
      document_title: doc.title,
      status: doc.status,
      warnings,
    },
    _metadata: generateResponseMetadata(db),
  };
}

/**
 * Golden contract tests for Hungarian Law MCP.
 * Validates DB integrity and tool behaviour against the full njt.hu ingestion.
 *
 * Skipped automatically when data/database.db is absent (e.g. CI without DB artefact).
 */

import { describe, it, expect, beforeAll, afterAll } from 'vitest';
import Database from '@ansvar/mcp-sqlite';
import * as path from 'path';
import * as fs from 'fs';
import { fileURLToPath } from 'url';
import { realDbExists } from '../helpers/test-db.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const DB_PATH = path.resolve(__dirname, '../../data/database.db');
const CENSUS_PATH = path.resolve(__dirname, '../../data/census.json');

const DB_EXISTS = realDbExists(DB_PATH);

const describeIf = DB_EXISTS ? describe : describe.skip;

let db: InstanceType<typeof Database>;

beforeAll(() => {
  if (!DB_EXISTS) return;
  db = new Database(DB_PATH, { readonly: true });
});

afterAll(() => {
  db?.close();
});

// ---------------------------------------------------------------------------
// Database integrity
// ---------------------------------------------------------------------------

describeIf('Database integrity', () => {
  it('should have a large legal-documents corpus', () => {
    const row = db.prepare('SELECT COUNT(*) as cnt FROM legal_documents').get() as { cnt: number };
    expect(row.cnt).toBeGreaterThanOrEqual(4000);
  });

  it('should have at least 100k provisions', () => {
    const row = db.prepare('SELECT COUNT(*) as cnt FROM legal_provisions').get() as { cnt: number };
    expect(row.cnt).toBeGreaterThanOrEqual(100_000);
  });

  it('should have extracted definitions', () => {
    const row = db.prepare('SELECT COUNT(*) as cnt FROM definitions').get() as { cnt: number };
    expect(row.cnt).toBeGreaterThanOrEqual(50);
  });

  it('should have FTS index rows for Hungarian terms', () => {
    const row = db.prepare(
      "SELECT COUNT(*) as cnt FROM provisions_fts WHERE provisions_fts MATCH 'adat'"
    ).get() as { cnt: number };
    expect(row.cnt).toBeGreaterThan(0);
  });

  it('should have EU cross-reference tables populated', () => {
    const docs = db.prepare('SELECT COUNT(*) as cnt FROM eu_documents').get() as { cnt: number };
    const refs = db.prepare('SELECT COUNT(*) as cnt FROM eu_references').get() as { cnt: number };
    expect(docs.cnt).toBeGreaterThan(0);
    expect(refs.cnt).toBeGreaterThan(0);
  });
});

// ---------------------------------------------------------------------------
// Census agreement
// ---------------------------------------------------------------------------

describeIf('Census agreement', () => {
  it('census.json exists and matches current DB counts', () => {
    expect(fs.existsSync(CENSUS_PATH)).toBe(true);
    const census = JSON.parse(fs.readFileSync(CENSUS_PATH, 'utf-8'));
    expect(census.jurisdiction).toBe('HU');

    const lawCount = (db.prepare('SELECT COUNT(*) as cnt FROM legal_documents').get() as { cnt: number }).cnt;
    const provCount = (db.prepare('SELECT COUNT(*) as cnt FROM legal_provisions').get() as { cnt: number }).cnt;

    expect(census.total_laws).toBe(lawCount);
    expect(census.total_provisions).toBe(provCount);
  });
});

// ---------------------------------------------------------------------------
// Article retrieval (hu-001 .. hu-004)
// ---------------------------------------------------------------------------

describeIf('Article retrieval', () => {
  it('hu-001: Infotörvény § 1 — data protection scope', () => {
    const row = db.prepare(
      "SELECT content FROM legal_provisions WHERE document_id = 'act-cxii-2011-info-self-determination' AND section = '1'"
    ).get() as { content: string } | undefined;
    expect(row).toBeDefined();
    expect(row!.content.length).toBeGreaterThan(50);
  });

  it('hu-002: Ibtv. § 11 — incident reporting', () => {
    const row = db.prepare(
      "SELECT content FROM legal_provisions WHERE document_id = 'act-l-2013-electronic-info-security' AND section = '11'"
    ).get() as { content: string } | undefined;
    expect(row).toBeDefined();
    expect(row!.content.length).toBeGreaterThan(50);
  });

  it('hu-003: Criminal Code § 422 — unauthorised access', () => {
    const row = db.prepare(
      "SELECT content FROM legal_provisions WHERE document_id = 'criminal-code-cybercrime' AND section = '422'"
    ).get() as { content: string } | undefined;
    expect(row).toBeDefined();
    expect(row!.content.length).toBeGreaterThan(50);
  });

  it('hu-004: Trade Secrets Act § 2 — definition', () => {
    const row = db.prepare(
      "SELECT content FROM legal_provisions WHERE document_id = 'act-liv-2018-trade-secrets' AND section = '2'"
    ).get() as { content: string } | undefined;
    expect(row).toBeDefined();
    expect(row!.content.length).toBeGreaterThan(50);
  });
});

// ---------------------------------------------------------------------------
// Search (hu-005 .. hu-007)
// ---------------------------------------------------------------------------

describeIf('Search', () => {
  it('hu-005: FTS search for "személyes adat" returns results', () => {
    const row = db.prepare(
      "SELECT COUNT(*) as cnt FROM provisions_fts WHERE provisions_fts MATCH 'személyes adat'"
    ).get() as { cnt: number };
    expect(row.cnt).toBeGreaterThan(0);
  });

  it('hu-006: FTS search for "kiberbiztonsági" returns results', () => {
    const row = db.prepare(
      "SELECT COUNT(*) as cnt FROM provisions_fts WHERE provisions_fts MATCH 'kiberbiztonsági'"
    ).get() as { cnt: number };
    expect(row.cnt).toBeGreaterThan(0);
  });

  it('hu-007: FTS search for "létfontosságú" returns results', () => {
    const row = db.prepare(
      "SELECT COUNT(*) as cnt FROM provisions_fts WHERE provisions_fts MATCH 'létfontosságú'"
    ).get() as { cnt: number };
    expect(row.cnt).toBeGreaterThan(0);
  });
});

// ---------------------------------------------------------------------------
// Citation URL pattern (hu-008 .. hu-009)
// ---------------------------------------------------------------------------

describeIf('Citation URL pattern', () => {
  it('hu-008: Infotörvény document has njt.hu URL', () => {
    const row = db.prepare(
      "SELECT url FROM legal_documents WHERE id = 'act-cxii-2011-info-self-determination'"
    ).get() as { url: string } | undefined;
    expect(row).toBeDefined();
    expect(row!.url).toMatch(/njt\.hu/);
  });

  it('hu-009: Criminal Code document has njt.hu URL', () => {
    const row = db.prepare(
      "SELECT url FROM legal_documents WHERE id = 'criminal-code-cybercrime'"
    ).get() as { url: string } | undefined;
    expect(row).toBeDefined();
    expect(row!.url).toMatch(/njt\.hu/);
  });
});

// ---------------------------------------------------------------------------
// EU cross-references (hu-010)
// ---------------------------------------------------------------------------

describeIf('EU cross-references', () => {
  it('hu-010: Infotörvény references GDPR (Regulation 2016/679)', () => {
    const row = db.prepare(
      "SELECT COUNT(*) as cnt FROM eu_references WHERE document_id = 'act-cxii-2011-info-self-determination' AND eu_document_id LIKE '%2016/679%'"
    ).get() as { cnt: number };
    expect(row.cnt).toBeGreaterThan(0);
  });
});

// ---------------------------------------------------------------------------
// Negative tests (hu-011 .. hu-012)
// ---------------------------------------------------------------------------

describeIf('Negative tests', () => {
  it('hu-011: non-existent document returns no provisions', () => {
    const row = db.prepare(
      "SELECT COUNT(*) as cnt FROM legal_provisions WHERE document_id = '2099-evi-MMMM-torveny-a-fikcio'"
    ).get() as { cnt: number };
    expect(row.cnt).toBe(0);
  });

  it('hu-012: invalid section returns no provisions', () => {
    const row = db.prepare(
      "SELECT COUNT(*) as cnt FROM legal_provisions WHERE document_id = 'act-cxii-2011-info-self-determination' AND section = '999ZZZ-INVALID'"
    ).get() as { cnt: number };
    expect(row.cnt).toBe(0);
  });
});

// ---------------------------------------------------------------------------
// Key law categories present
// ---------------------------------------------------------------------------

describeIf('Key law categories are present', () => {
  it('should contain törvény (statutes)', () => {
    const row = db.prepare(
      "SELECT COUNT(*) as cnt FROM legal_documents WHERE title LIKE '%törvény%'"
    ).get() as { cnt: number };
    expect(row.cnt).toBeGreaterThan(0);
  });

  it('should contain kormányrendelet (government decrees)', () => {
    const row = db.prepare(
      "SELECT COUNT(*) as cnt FROM legal_documents WHERE title LIKE '%kormányrendelet%'"
    ).get() as { cnt: number };
    expect(row.cnt).toBeGreaterThan(0);
  });
});

// ---------------------------------------------------------------------------
// Metadata compatibility
// ---------------------------------------------------------------------------

describeIf('Metadata compatibility', () => {
  it('should have db_metadata table with entries', () => {
    const row = db.prepare('SELECT COUNT(*) as cnt FROM db_metadata').get() as { cnt: number };
    expect(row.cnt).toBeGreaterThan(0);
  });

  it('should store HU jurisdiction metadata', () => {
    const row = db.prepare(
      "SELECT value FROM db_metadata WHERE key = 'jurisdiction'"
    ).get() as { value: string } | undefined;
    expect(row).toBeDefined();
    expect(row!.value).toBe('HU');
  });
});

/**
 * Golden contract tests for Hungarian Law MCP.
 * Validates DB integrity and tool behaviour against the full njt.hu ingestion.
 *
 * Skipped automatically when data/database.db is absent (e.g. CI without DB artefact).
 */

import { describe, it, expect, beforeAll, afterAll } from 'vitest';
import * as fs from 'fs';
import * as path from 'path';
import {
  REAL_DATA_DIR,
  REAL_DB_AVAILABLE,
  describeIfRealDb,
  openRealDb,
} from '../helpers/test-db.js';

const CENSUS_PATH = path.join(REAL_DATA_DIR, 'census.json');

let db: ReturnType<typeof openRealDb>;

beforeAll(() => {
  if (!REAL_DB_AVAILABLE) return;
  db = openRealDb();
});

afterAll(() => {
  db?.close();
});

// ---------------------------------------------------------------------------
// Database integrity
// ---------------------------------------------------------------------------

describeIfRealDb('Database integrity', () => {
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

describeIfRealDb('Census agreement', () => {
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

describeIfRealDb('Article retrieval', () => {
  it.each([
    { case: 'hu-001', docId: 'act-cxii-2011-info-self-determination', section: '1' },
    { case: 'hu-002', docId: 'act-l-2013-electronic-info-security', section: '11' },
    { case: 'hu-003', docId: 'criminal-code-cybercrime', section: '422' },
    { case: 'hu-004', docId: 'act-liv-2018-trade-secrets', section: '2' },
  ])('$case: $docId § $section returns provision content', ({ docId, section }) => {
    const row = db.prepare(
      'SELECT content FROM legal_provisions WHERE document_id = ? AND section = ?'
    ).get(docId, section) as { content: string } | undefined;
    expect(row).toBeDefined();
    expect(row!.content.length).toBeGreaterThan(50);
  });
});

// ---------------------------------------------------------------------------
// Search (hu-005 .. hu-007)
// ---------------------------------------------------------------------------

describeIfRealDb('Search', () => {
  it.each([
    { case: 'hu-005', term: 'személyes adat' },
    { case: 'hu-006', term: 'kiberbiztonsági' },
    { case: 'hu-007', term: 'létfontosságú' },
  ])('$case: FTS search for "$term" returns results', ({ term }) => {
    const row = db.prepare(
      'SELECT COUNT(*) as cnt FROM provisions_fts WHERE provisions_fts MATCH ?'
    ).get(term) as { cnt: number };
    expect(row.cnt).toBeGreaterThan(0);
  });
});

// ---------------------------------------------------------------------------
// Citation URL pattern (hu-008 .. hu-009)
// ---------------------------------------------------------------------------

describeIfRealDb('Citation URL pattern', () => {
  it.each([
    { case: 'hu-008', docId: 'act-cxii-2011-info-self-determination' },
    { case: 'hu-009', docId: 'criminal-code-cybercrime' },
  ])('$case: $docId document has njt.hu URL', ({ docId }) => {
    const row = db.prepare(
      'SELECT url FROM legal_documents WHERE id = ?'
    ).get(docId) as { url: string } | undefined;
    expect(row).toBeDefined();
    expect(row!.url).toMatch(/njt\.hu/);
  });
});

// ---------------------------------------------------------------------------
// EU cross-references (hu-010)
// ---------------------------------------------------------------------------

describeIfRealDb('EU cross-references', () => {
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

describeIfRealDb('Negative tests', () => {
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

describeIfRealDb('Key law categories are present', () => {
  it.each([
    { kind: 'törvény', label: 'törvény (statutes)' },
    { kind: 'kormányrendelet', label: 'kormányrendelet (government decrees)' },
  ])('should contain $label', ({ kind }) => {
    const row = db.prepare(
      'SELECT COUNT(*) as cnt FROM legal_documents WHERE title LIKE ?'
    ).get(`%${kind}%`) as { cnt: number };
    expect(row.cnt).toBeGreaterThan(0);
  });
});

// ---------------------------------------------------------------------------
// Metadata compatibility
// ---------------------------------------------------------------------------

describeIfRealDb('Metadata compatibility', () => {
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

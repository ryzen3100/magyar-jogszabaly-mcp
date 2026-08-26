import Database from '@ansvar/mcp-sqlite';
import * as fs from 'fs';
import * as path from 'path';
import { fileURLToPath } from 'url';
import { afterEach, describe } from 'vitest';

export interface TestDbOptions {
  withEuTables?: boolean;
  withDefinitionsTable?: boolean;
  withMetadataTable?: boolean;
}

/**
 * True when dbPath exists and is a usable law database (has legal_documents).
 * Shared guard so DB-backed tests skip cleanly on machines without a built DB.
 */
export function realDbExists(dbPath: string): boolean {
  if (!fs.existsSync(dbPath)) return false;
  try {
    const _db = new Database(dbPath, { readonly: true });
    const _row = _db.prepare("SELECT COUNT(*) as cnt FROM sqlite_master WHERE type='table' AND name='legal_documents'").get() as { cnt: number } | undefined;
    _db.close();
    return (_row?.cnt ?? 0) > 0;
  } catch { return false; }
}

export function createTestDb(options: TestDbOptions = {}): InstanceType<typeof Database> {
  const {
    withEuTables = true,
    withDefinitionsTable = true,
    withMetadataTable = true,
  } = options;

  const db = new Database(':memory:');

  db.exec(`
    CREATE TABLE legal_documents (
      id TEXT PRIMARY KEY,
      type TEXT,
      title TEXT NOT NULL,
      title_en TEXT,
      short_name TEXT,
      status TEXT NOT NULL,
      issued_date TEXT,
      in_force_date TEXT,
      url TEXT,
      description TEXT
    );

    CREATE TABLE legal_provisions (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      document_id TEXT NOT NULL,
      provision_ref TEXT NOT NULL,
      chapter TEXT,
      section TEXT NOT NULL,
      title TEXT,
      content TEXT NOT NULL,
      metadata TEXT
    );
  `);

  db.exec('CREATE VIRTUAL TABLE provisions_fts USING fts5(content, title);');

  if (withDefinitionsTable) {
    db.exec(`
      CREATE TABLE definitions (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        document_id TEXT NOT NULL,
        term TEXT NOT NULL,
        term_en TEXT,
        definition TEXT NOT NULL,
        source_provision TEXT
      );
    `);
  }

  if (withEuTables) {
    db.exec(`
      CREATE TABLE eu_documents (
        id TEXT PRIMARY KEY,
        type TEXT NOT NULL,
        year INTEGER NOT NULL,
        number INTEGER NOT NULL,
        title TEXT,
        short_name TEXT,
        description TEXT
      );

      CREATE TABLE eu_references (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        document_id TEXT NOT NULL,
        provision_id INTEGER,
        eu_document_id TEXT NOT NULL,
        eu_article TEXT,
        reference_type TEXT NOT NULL,
        reference_context TEXT,
        full_citation TEXT,
        implementation_status TEXT,
        is_primary_implementation INTEGER DEFAULT 0
      );
    `);
  }

  if (withMetadataTable) {
    db.exec('CREATE TABLE db_metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);');
  }

  seedCoreFixtures(db, { withDefinitionsTable, withEuTables, withMetadataTable });
  return db;
}

function seedCoreFixtures(
  db: InstanceType<typeof Database>,
  options: Required<TestDbOptions>
): void {
  const docStmt = db.prepare(`
    INSERT INTO legal_documents (
      id, type, title, title_en, short_name, status, issued_date, in_force_date, url, description
    ) VALUES (?, 'statute', ?, ?, ?, ?, ?, ?, ?, ?)
  `);

  docStmt.run(
    'doc-inforce',
    'In Force Act',
    'In Force Act EN',
    'IFA',
    'in_force',
    '2020-01-01',
    '2020-06-01',
    'https://njt.hu/jogszabaly/inforce',
    'In-force document'
  );

  docStmt.run(
    'doc-amended',
    'Amended Act',
    'Amended Act EN',
    'AA',
    'amended',
    '2018-01-01',
    '2018-06-01',
    'https://njt.hu/jogszabaly/amended',
    'Amended document'
  );

  docStmt.run(
    'doc-repealed',
    'Repealed Act',
    'Repealed Act EN',
    'RA',
    'repealed',
    '2010-01-01',
    '2010-06-01',
    'https://njt.hu/jogszabaly/repealed',
    'Repealed document'
  );

  docStmt.run(
    'doc-future',
    'Future Act',
    'Future Act EN',
    'FA',
    'not_yet_in_force',
    '2030-01-01',
    '2031-01-01',
    'https://njt.hu/jogszabaly/future',
    'Future document'
  );

  const provStmt = db.prepare(`
    INSERT INTO legal_provisions (document_id, provision_ref, chapter, section, title, content, metadata)
    VALUES (?, ?, ?, ?, ?, ?, ?)
  `);

  const p1 = provStmt.run(
    'doc-inforce',
    's1',
    'I. Fejezet',
    '1',
    '1. §',
    'A személyes adat kezelése és elektronikus aláírás szabályai.',
    null
  ).lastInsertRowid as number;

  const p2 = provStmt.run(
    'doc-inforce',
    's2',
    'I. Fejezet',
    '2',
    '2. §',
    'Kiberbiztonsági intézkedések és információs rendszer védelem.',
    null
  ).lastInsertRowid as number;

  const p3 = provStmt.run(
    'doc-amended',
    's3',
    'II. Fejezet',
    '3',
    '3. §',
    'Üzleti titok és létfontosságú infrastruktúra védelme.',
    null
  ).lastInsertRowid as number;

  const ftsStmt = db.prepare('INSERT INTO provisions_fts(rowid, content, title) VALUES (?, ?, ?)');
  ftsStmt.run(p1, 'A személyes adat kezelése és elektronikus aláírás szabályai.', '1. §');
  ftsStmt.run(p2, 'Kiberbiztonsági intézkedések és információs rendszer védelem.', '2. §');
  ftsStmt.run(p3, 'Üzleti titok és létfontosságú infrastruktúra védelme.', '3. §');

  if (options.withDefinitionsTable) {
    db.prepare(`
      INSERT INTO definitions (document_id, term, definition, source_provision)
      VALUES ('doc-inforce', 'személyes adat', 'Az érintettre vonatkozó adat.', 's1')
    `).run();
  }

  if (options.withEuTables) {
    db.prepare(`
      INSERT INTO eu_documents (id, type, year, number, title, short_name, description)
      VALUES ('regulation:2016/679', 'regulation', 2016, 679, 'GDPR', 'GDPR', 'General Data Protection Regulation')
    `).run();
    db.prepare(`
      INSERT INTO eu_documents (id, type, year, number, title, short_name, description)
      VALUES ('directive:2022/2555', 'directive', 2022, 2555, 'NIS2', 'NIS2', 'Network and Information Security')
    `).run();

    const euStmt = db.prepare(`
      INSERT INTO eu_references (
        document_id, provision_id, eu_document_id, eu_article, reference_type,
        reference_context, full_citation, implementation_status, is_primary_implementation
      ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    `);

    euStmt.run(
      'doc-inforce',
      p1,
      'regulation:2016/679',
      'Article 6',
      'implements',
      'Implements GDPR requirements.',
      'Regulation (EU) 2016/679',
      'complete',
      1
    );
    euStmt.run(
      'doc-amended',
      p3,
      'directive:2022/2555',
      null,
      'references',
      'References NIS2 baseline.',
      'Directive (EU) 2022/2555',
      'partial',
      0
    );
    euStmt.run(
      'doc-amended',
      p3,
      'regulation:2016/679',
      null,
      'references',
      'General GDPR reference.',
      'Regulation (EU) 2016/679',
      'unknown',
      0
    );
  }

  if (options.withMetadataTable) {
    const metaStmt = db.prepare('INSERT INTO db_metadata(key, value) VALUES (?, ?)');
    metaStmt.run('tier', 'free');
    metaStmt.run('schema_version', '1.0');
    metaStmt.run('built_at', '2026-02-21T00:00:00Z');
    metaStmt.run('builder', 'test-suite');
  }
}

// ---------------------------------------------------------------------------
// Shared lifecycle: auto-close tracked in-memory databases after each test
// ---------------------------------------------------------------------------

const trackedDbs: InstanceType<typeof Database>[] = [];

afterEach(() => {
  for (const db of trackedDbs.splice(0)) db.close();
});

/**
 * Register a test database for automatic close in the next afterEach.
 * For per-test connections only; a connection shared across tests via
 * beforeAll must be closed by its owner in afterAll instead.
 */
export function trackDb<T>(db: T): T {
  trackedDbs.push(db as unknown as InstanceType<typeof Database>);
  return db;
}

// ---------------------------------------------------------------------------
// Real-database (data/database.db) helpers shared by DB-backed suites
// ---------------------------------------------------------------------------

/** Directory holding the generated database, census, and seed data. */
export const REAL_DATA_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../data');

/** Absolute path of the generated production database. */
const REAL_DB_PATH = path.join(REAL_DATA_DIR, 'database.db');

/** True when the real database exists and is usable; DB-backed tests skip otherwise. */
export const REAL_DB_AVAILABLE = realDbExists(REAL_DB_PATH);

/** describe() when the real database is available, describe.skip otherwise. */
export const describeIfRealDb = REAL_DB_AVAILABLE ? describe : describe.skip;

/**
 * Open the real database read-only. Meant to be called once per suite in
 * beforeAll and closed by the caller in afterAll — deliberately not passed
 * through trackDb, whose afterEach drain would close a connection shared
 * across tests after the first test.
 */
export function openRealDb(): InstanceType<typeof Database> {
  return new Database(REAL_DB_PATH, { readonly: true, fileMustExist: true });
}

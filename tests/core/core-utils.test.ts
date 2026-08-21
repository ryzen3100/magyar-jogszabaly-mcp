import { afterEach, describe, expect, it } from 'vitest';
import Database from '@ansvar/mcp-sqlite';

import {
  DB_ENV_VAR,
  SERVER_NAME,
  SERVER_VERSION,
} from '../../src/constants.js';
import { normalizeAsOfDate } from '../../src/utils/as-of-date.js';
import { sanitizeFtsInput, buildFtsQueryVariants } from '../../src/utils/fts-query.js';
import { generateResponseMetadata } from '../../src/utils/metadata.js';
import { resolveDocumentId } from '../../src/utils/statute-id.js';
import { detectCapabilities, readDbMetadata, upgradeMessage } from '../../src/capabilities.js';
import { createTestDb } from '../helpers/test-db.js';

const opened: InstanceType<typeof Database>[] = [];

function trackDb(db: InstanceType<typeof Database>): InstanceType<typeof Database> {
  opened.push(db);
  return db;
}

afterEach(() => {
  while (opened.length > 0) {
    const db = opened.pop();
    db?.close();
  }
});

describe('constants', () => {
  it('exports expected server/package identifiers', () => {
    expect(SERVER_NAME).toBe('hungarian-law-mcp');
    expect(SERVER_VERSION).toBe('1.0.0');
    expect(DB_ENV_VAR).toBe('HUNGARIAN_LAW_DB_PATH');
  });
});

describe('normalizeAsOfDate', () => {
  it('normalizes and validates date input', () => {
    expect(normalizeAsOfDate()).toBeNull();
    expect(normalizeAsOfDate('')).toBeNull();
    expect(normalizeAsOfDate('   ')).toBeNull();
    expect(normalizeAsOfDate('2026-02-21')).toBe('2026-02-21');
    expect(normalizeAsOfDate('2026-02-21T12:34:56Z')).toBe('2026-02-21');
    expect(normalizeAsOfDate('not a date')).toBeNull();
  });
});

describe('fts-query helpers', () => {
  it('sanitizes FTS input', () => {
    expect(sanitizeFtsInput(` "GDPR" (Article) 6* `)).toBe('GDPR Article 6*');
  });

  it('preserves trailing wildcard for prefix search', () => {
    expect(sanitizeFtsInput('control*')).toBe('control*');
    expect(sanitizeFtsInput('a*b')).toBe('a b');
  });

  it('builds query variants', () => {
    expect(buildFtsQueryVariants('')).toEqual([]);
    expect(buildFtsQueryVariants('x')).toEqual(['x']);
    expect(buildFtsQueryVariants('adat')).toEqual(['adat', 'adat*']);
    expect(buildFtsQueryVariants('személyes adat')).toEqual([
      '"személyes adat"',
      'személyes AND adat',
      'személyes AND adat*',
      'személy* AND adat',
      'személyes OR adat',
    ]);
  });
});

describe('generateResponseMetadata', () => {
  it('includes freshness when db_metadata is available', () => {
    const db = trackDb(createTestDb({ withMetadataTable: true }));
    const metadata = generateResponseMetadata(db);
    expect(metadata.jurisdiction).toBe('HU');
    expect(metadata.data_source).toContain('njt.hu');
    expect(metadata.freshness).toBe('2026-02-21T00:00:00Z');
  });

  it('handles missing metadata table gracefully', () => {
    const db = trackDb(createTestDb({ withMetadataTable: false }));
    const metadata = generateResponseMetadata(db);
    expect(metadata.freshness).toBeUndefined();
  });
});

describe('resolveDocumentId', () => {
  it('resolves by direct id, title/short_name, lower fallback, and null cases', () => {
    const db = trackDb(createTestDb());

    expect(resolveDocumentId(db, '   ')).toBeNull();
    expect(resolveDocumentId(db, 'doc-inforce')).toBe('doc-inforce');
    expect(resolveDocumentId(db, 'In Force Act EN')).toBe('doc-inforce');
    expect(resolveDocumentId(db, 'IFA')).toBe('doc-inforce');

    db.pragma('case_sensitive_like = ON');
    db.prepare("UPDATE legal_documents SET title = 'MIXEDCASE ACT' WHERE id = 'doc-inforce'").run();
    expect(resolveDocumentId(db, 'mixedcase act')).toBe('doc-inforce');

    expect(resolveDocumentId(db, 'non-existent statute')).toBeNull();
  });
});

describe('capabilities', () => {
  it('detects available capabilities by table presence', () => {
    const db = trackDb(createTestDb({ withEuTables: true }));
    const caps = detectCapabilities(db);
    expect(caps.has('core_legislation')).toBe(true);
    expect(caps.has('eu_references')).toBe(true);
    expect(caps.size).toBe(2);
  });

  it('reads db metadata and falls back when metadata table is missing', () => {
    const withMeta = trackDb(createTestDb({ withMetadataTable: true }));
    const withMetaInfo = readDbMetadata(withMeta);
    expect(withMetaInfo.tier).toBe('free');
    expect(withMetaInfo.schema_version).toBe('1.0');
    expect(withMetaInfo.built_at).toBe('2026-02-21T00:00:00Z');
    expect(withMetaInfo.builder).toBe('test-suite');

    const withoutMeta = trackDb(createTestDb({ withMetadataTable: false }));
    const withoutMetaInfo = readDbMetadata(withoutMeta);
    expect(withoutMetaInfo.tier).toBe('free');
    expect(withoutMetaInfo.schema_version).toBe('1.0');
    expect(withoutMetaInfo.built_at).toBeUndefined();
    expect(withoutMetaInfo.builder).toBeUndefined();
  });

  it('formats upgrade messages', () => {
    expect(upgradeMessage('eu_references')).toContain('"eu_references"');
    expect(upgradeMessage('eu_references')).toContain('professional-tier');
  });
});

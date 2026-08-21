import { afterEach, describe, expect, it } from 'vitest';
import Database from '@ansvar/mcp-sqlite';

import { getAbout } from '../../src/tools/about.js';
import { listSources } from '../../src/tools/list-sources.js';
import { checkCurrency } from '../../src/tools/check-currency.js';
import { formatCitationTool } from '../../src/tools/format-citation.js';
import { validateCitationTool } from '../../src/tools/validate-citation.js';
import { buildLegalStance } from '../../src/tools/build-legal-stance.js';
import { getEUBasis } from '../../src/tools/get-eu-basis.js';
import { getHungarianImplementations } from '../../src/tools/get-hungarian-implementations.js';
import { searchEUImplementations } from '../../src/tools/search-eu-implementations.js';
import { getProvisionEUBasis } from '../../src/tools/get-provision-eu-basis.js';
import { validateEUCompliance } from '../../src/tools/validate-eu-compliance.js';
import { createTestDb } from '../helpers/test-db.js';

const opened: InstanceType<typeof Database>[] = [];

function trackDb(db: InstanceType<typeof Database>): InstanceType<typeof Database> {
  opened.push(db);
  return db;
}

afterEach(() => {
  while (opened.length > 0) {
    opened.pop()?.close();
  }
});

describe('about and sources tools', () => {
  it('returns populated about metadata and statistics', () => {
    const db = trackDb(createTestDb());
    const result = getAbout(db, {
      version: '1.2.3',
      fingerprint: 'abcdef123456',
      dbBuilt: '2026-02-21T00:00:00Z',
    });

    expect(result.name).toBe('Hungarian Law MCP');
    expect(result.version).toBe('1.2.3');
    expect(result.jurisdiction).toBe('HU');
    expect(result.stats.documents).toBeGreaterThan(0);
    expect(result.stats.provisions).toBeGreaterThan(0);
    expect(result.freshness.database_built).toBe('2026-02-21T00:00:00Z');
    expect(result.data_sources[0].url).toBe('https://njt.hu');
  });

  it('handles missing optional count tables in about/list_sources', async () => {
    const db = trackDb(createTestDb({ withEuTables: false, withDefinitionsTable: false }));
    const about = getAbout(db, {
      version: '1.0.0',
      fingerprint: 'fingerprint',
      dbBuilt: '2026-02-21T00:00:00Z',
    });
    expect(about.stats.definitions).toBe(0);
    expect(about.stats.eu_documents).toBeUndefined();
    expect(about.stats.eu_references).toBeUndefined();

    const sources = await listSources(db);
    expect(sources.results.database.document_count).toBeGreaterThan(0);
    expect(sources.results.database.provision_count).toBeGreaterThan(0);
    expect(sources.results.sources[0].url).toBe('https://njt.hu');
  });

  it('handles undefined counts and thrown count queries', async () => {
    const undefinedCountDb = {
      prepare(sql: string) {
        if (sql.includes('sqlite_master')) return { all: () => [] };
        if (sql.includes('db_metadata')) return { all: () => [] };
        if (sql.includes('COUNT(*)')) return { get: () => undefined };
        return { get: () => ({}) };
      },
    } as unknown as InstanceType<typeof Database>;

    const about = getAbout(undefinedCountDb, {
      version: '1.0.0',
      fingerprint: 'fp',
      dbBuilt: '2026-02-21T00:00:00Z',
    });
    expect(about.stats.documents).toBe(0);
    expect(about.stats.provisions).toBe(0);
    const undefinedSources = await listSources(undefinedCountDb);
    expect(undefinedSources.results.database.document_count).toBe(0);
    expect(undefinedSources.results.database.provision_count).toBe(0);

    const throwingDb = {
      prepare: () => ({
        get() {
          throw new Error('count failed');
        },
      }),
    } as unknown as InstanceType<typeof Database>;
    const sources = await listSources(throwingDb);
    expect(sources.results.database.document_count).toBe(0);
    expect(sources.results.database.provision_count).toBe(0);
  });
});

describe('checkCurrency', () => {
  it('returns not_found for unknown documents', async () => {
    const db = trackDb(createTestDb());
    const result = await checkCurrency(db, { document_id: 'missing-doc' });
    expect(result.results.status).toBe('not_found');
    expect(result.results.warnings[0]).toContain('Document not found');
  });

  it('returns status-specific warnings', async () => {
    const db = trackDb(createTestDb());

    const repealed = await checkCurrency(db, { document_id: 'doc-repealed' });
    expect(repealed.results.status).toBe('repealed');
    expect(repealed.results.warnings[0]).toContain('repealed');

    const future = await checkCurrency(db, { document_id: 'doc-future' });
    expect(future.results.status).toBe('not_yet_in_force');
    expect(future.results.warnings[0]).toContain('not yet entered into force');

    const inForce = await checkCurrency(db, { document_id: 'doc-inforce' });
    expect(inForce.results.status).toBe('in_force');
    expect(inForce.results.warnings).toHaveLength(0);
  });
});

describe('formatCitationTool', () => {
  it('formats citations in all supported styles', async () => {
    const db = trackDb(createTestDb());

    const full = await formatCitationTool(db, { citation: 'Section 3, In Force Act' });
    expect(full.results.formatted).toBe('In Force Act 3. §');

    const short = await formatCitationTool(db, { citation: 'In Force Act s 3', format: 'short' });
    expect(short.results.formatted).toBe('In Force Act 3. §');

    const pinpoint = await formatCitationTool(db, { citation: 'In Force Act Section 3', format: 'pinpoint' });
    expect(pinpoint.results.formatted).toBe('3. §');

    const noSection = await formatCitationTool(db, { citation: 'In Force Act' });
    expect(noSection.results.formatted).toBe('In Force Act');

    const shortNoSection = await formatCitationTool(db, { citation: 'In Force Act', format: 'short' });
    expect(shortNoSection.results.formatted).toBe('In Force Act');

    const pinpointNoSection = await formatCitationTool(db, { citation: 'In Force Act', format: 'pinpoint' });
    expect(pinpointNoSection.results.formatted).toBe('In Force Act');
  });
});

describe('validateCitationTool', () => {
  it('handles empty citation and unknown document', async () => {
    const db = trackDb(createTestDb());

    const empty = await validateCitationTool(db, { citation: '   ' });
    expect(empty.results.valid).toBe(false);
    expect(empty.results.warnings[0]).toContain('Could not parse');

    const missing = await validateCitationTool(db, { citation: 'Section 1 Missing Act' });
    expect(missing.results.valid).toBe(false);
    expect(missing.results.warnings[0]).toContain('Document not found');
  });

  it('validates existing sections and surfaces document status warnings', async () => {
    const db = trackDb(createTestDb());

    const ok = await validateCitationTool(db, { citation: 'In Force Act s 1' });
    expect(ok.results.valid).toBe(true);
    expect(ok.results.provision_ref).toBe('s1');
    expect(ok.results.normalized).toContain('Section 1');

    const badSection = await validateCitationTool(db, { citation: 'Section 999 In Force Act' });
    expect(badSection.results.valid).toBe(false);
    expect(badSection.results.warnings.join(' ')).toContain('not found');

    const amended = await validateCitationTool(db, { citation: 'Amended Act' });
    expect(amended.results.valid).toBe(true);
    expect(amended.results.warnings.join(' ')).toContain('amended');

    const repealed = await validateCitationTool(db, { citation: 'Repealed Act' });
    expect(repealed.results.valid).toBe(true);
    expect(repealed.results.warnings.join(' ')).toContain('repealed');

    const sectionWord = await validateCitationTool(db, { citation: 'In Force Act Section 1' });
    expect(sectionWord.results.valid).toBe(true);
    expect(sectionWord.results.provision_ref).toBe('s1');
  });
});

describe('buildLegalStance', () => {
  it('returns empty for empty query and finds matches for valid queries', async () => {
    const db = trackDb(createTestDb());
    const empty = await buildLegalStance(db, { query: '' });
    expect(empty.results).toHaveLength(0);

    const found = await buildLegalStance(db, { query: 'személyes adat', limit: 100 });
    expect(found.results.length).toBeGreaterThan(0);
    expect(found.results.length).toBeLessThanOrEqual(20);

    const filtered = await buildLegalStance(db, { query: 'adat', document_id: 'doc-inforce', limit: 2 });
    expect(filtered.results.length).toBeGreaterThan(0);
    filtered.results.forEach(row => expect(row.document_id).toBe('doc-inforce'));
  });

  it('handles FTS execution errors and returns empty result', async () => {
    const failingDb = {
      prepare: () => ({
        all: () => {
          throw new Error('boom');
        },
      }),
    } as unknown as InstanceType<typeof Database>;

    const result = await buildLegalStance(failingDb, { query: 'adat' });
    expect(result.results).toHaveLength(0);
  });
});

describe('EU basis and implementation tools', () => {
  it('returns empty when document cannot be resolved', async () => {
    const db = trackDb(createTestDb());
    const result = await getEUBasis(db, { document_id: 'missing-doc' });
    expect(result.results).toEqual([]);
  });

  it('returns tier note when EU tables are unavailable', async () => {
    const db = trackDb(createTestDb({ withEuTables: false }));
    const basis = await getEUBasis(db, { document_id: 'doc-inforce' });
    expect(basis.results).toEqual([]);
    expect(basis._metadata.note).toContain('EU references not available');

    const impl = await getHungarianImplementations(db, { eu_document_id: 'regulation:2016/679' });
    expect(impl.results).toEqual([]);
    expect(impl._metadata.note).toContain('EU references not available');

    const perProvision = await getProvisionEUBasis(db, { document_id: 'doc-inforce', provision_ref: '1' });
    expect(perProvision.results).toEqual([]);
    expect(perProvision._metadata.note).toContain('EU references not available');

    const search = await searchEUImplementations(db, { query: 'GDPR' });
    expect(search.results).toEqual([]);
    expect(search._metadata.note).toContain('EU documents not available');
  });

  it('retrieves EU basis with filters and article expansion', async () => {
    const db = trackDb(createTestDb());
    const result = await getEUBasis(db, {
      document_id: 'doc-inforce',
      include_articles: true,
      reference_types: ['implements'],
    });
    expect(result.results).toHaveLength(1);
    expect(result.results[0].eu_document_id).toBe('regulation:2016/679');
    expect(result.results[0].articles).toEqual(['Article 6']);
  });

  it('filters Hungarian implementations and searches EU docs', async () => {
    const db = trackDb(createTestDb());

    const primary = await getHungarianImplementations(db, {
      eu_document_id: 'regulation:2016/679',
      primary_only: true,
    });
    expect(primary.results).toHaveLength(1);
    expect(primary.results[0].document_id).toBe('doc-inforce');

    const inForceOnly = await getHungarianImplementations(db, {
      eu_document_id: 'regulation:2016/679',
      in_force_only: true,
    });
    expect(inForceOnly.results.length).toBeGreaterThan(0);
    inForceOnly.results.forEach(row => expect(row.status).toBe('in_force'));

    const search = await searchEUImplementations(db, {
      query: 'GDPR',
      type: 'regulation',
      year_from: 2015,
      year_to: 2016,
      has_hungarian_implementation: true,
      limit: 500,
    });
    expect(search.results).toHaveLength(1);
    expect(search.results[0].eu_document_id).toBe('regulation:2016/679');

    const defaultLimit = await searchEUImplementations(db, {});
    expect(defaultLimit.results.length).toBeGreaterThan(0);

    const minLimit = await searchEUImplementations(db, { limit: 0 });
    expect(minLimit.results.length).toBeGreaterThan(0);
  });

  it('retrieves EU basis for a specific provision and handles missing provision', async () => {
    const db = trackDb(createTestDb());

    const unresolved = await getProvisionEUBasis(db, {
      document_id: 'missing-doc',
      provision_ref: '1',
    });
    expect(unresolved.results).toEqual([]);

    const missingProvision = await getProvisionEUBasis(db, {
      document_id: 'doc-inforce',
      provision_ref: '999',
    });
    expect(missingProvision.results).toEqual([]);

    const found = await getProvisionEUBasis(db, {
      document_id: 'doc-inforce',
      provision_ref: '1',
    });
    expect(found.results).toHaveLength(1);
    expect(found.results[0].eu_document_id).toBe('regulation:2016/679');
  });
});

describe('validateEUCompliance', () => {
  it('handles unresolved documents and missing EU datasets', async () => {
    const db = trackDb(createTestDb());
    const unresolved = await validateEUCompliance(db, { document_id: 'missing-doc' });
    expect(unresolved.results.compliance_status).toBe('not_applicable');
    expect(unresolved.results.warnings.join(' ')).toContain('Document not found');

    const noEuDb = trackDb(createTestDb({ withEuTables: false }));
    const missingEu = await validateEUCompliance(noEuDb, { document_id: 'doc-inforce' });
    expect(missingEu.results.compliance_status).toBe('not_applicable');
    expect(missingEu.results.warnings.join(' ')).toContain('EU references not available');
  });

  it('returns not_applicable when there are no EU refs for a resolved statute', async () => {
    const db = trackDb(createTestDb());
    const result = await validateEUCompliance(db, { document_id: 'doc-future' });
    expect(result.results.compliance_status).toBe('not_applicable');
    expect(result.results.eu_references_found).toBe(0);
    expect(result.results.recommendations.join(' ')).toContain('No EU cross-references');
  });

  it('returns compliant, partial, and unclear statuses', async () => {
    const db = trackDb(createTestDb());

    const compliant = await validateEUCompliance(db, { document_id: 'doc-inforce' });
    expect(compliant.results.compliance_status).toBe('compliant');

    const compliantFiltered = await validateEUCompliance(db, {
      document_id: 'doc-inforce',
      eu_document_id: 'regulation:2016/679',
    });
    expect(compliantFiltered.results.compliance_status).toBe('compliant');

    db.prepare(`
      INSERT INTO eu_references (
        document_id, provision_id, eu_document_id, eu_article, reference_type,
        reference_context, full_citation, implementation_status, is_primary_implementation
      ) VALUES ('doc-repealed', 1, 'directive:2022/2555', NULL, 'references', 'ctx', 'cite', 'partial', 0)
    `).run();
    const partial = await validateEUCompliance(db, { document_id: 'doc-repealed' });
    expect(partial.results.compliance_status).toBe('partial');
    expect(partial.results.warnings.join(' ')).toContain('repealed');
    expect(partial.results.warnings.join(' ')).toContain('partial alignment');
    expect(partial.results.recommendations.join(' ')).toContain('replacement legislation');

    db.prepare(`
      INSERT INTO legal_documents (
        id, type, title, title_en, short_name, status, issued_date, in_force_date, url, description
      ) VALUES ('doc-unclear', 'statute', 'Unclear Act', 'Unclear Act EN', 'UA', 'in_force', '2024-01-01', '2024-06-01', 'https://njt.hu/jogszabaly/unclear', 'unclear')
    `).run();
    db.prepare(`
      INSERT INTO eu_references (
        document_id, provision_id, eu_document_id, eu_article, reference_type,
        reference_context, full_citation, implementation_status, is_primary_implementation
      ) VALUES ('doc-unclear', 1, 'directive:2022/2555', NULL, 'references', 'ctx', 'cite', 'unknown', 0)
    `).run();

    const unclear = await validateEUCompliance(db, { document_id: 'doc-unclear' });
    expect(unclear.results.compliance_status).toBe('unclear');
    expect(unclear.results.recommendations.join(' ')).toContain('unknown alignment status');
  });
});

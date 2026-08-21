import { describe, it, expect, beforeAll, afterAll } from 'vitest';
import Database from '@ansvar/mcp-sqlite';
import * as path from 'path';
import { fileURLToPath } from 'url';
import { realDbExists } from '../helpers/test-db.js';
import { searchLegislation } from '../../src/tools/search-legislation.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const DB_PATH = path.resolve(__dirname, '../../data/database.db');

const DB_EXISTS = realDbExists(DB_PATH);

const describeIf = DB_EXISTS ? describe : describe.skip;

describeIf('searchLegislation', () => {
  let db: InstanceType<typeof Database>;

  beforeAll(() => {
    db = new Database(DB_PATH, { readonly: true, fileMustExist: true });
  });
  afterAll(() => {
    db.close();
  });

  it('should find provisions about personal data', async () => {
    const result = await searchLegislation(db, { query: 'személyes adat' });
    expect(result.results.length).toBeGreaterThanOrEqual(1);
    const allSnippets = result.results.map(r => r.snippet.toLowerCase()).join(' ');
    expect(allSnippets).toContain('adat');
  });

  it('should find provisions about cybersecurity', async () => {
    const result = await searchLegislation(db, { query: 'kiberbiztonsági' });
    expect(result.results.length).toBeGreaterThanOrEqual(1);
  });

  it('should find provisions about critical infrastructure', async () => {
    const result = await searchLegislation(db, { query: 'létfontosságú' });
    expect(result.results.length).toBeGreaterThanOrEqual(1);
  });

  it('should find provisions about trade secret', async () => {
    const result = await searchLegislation(db, { query: 'üzleti titok' });
    expect(result.results.length).toBeGreaterThanOrEqual(1);
  });

  it('should find provisions about electronic signature', async () => {
    const result = await searchLegislation(db, { query: 'elektronikus aláírás' });
    expect(result.results.length).toBeGreaterThanOrEqual(1);
  });

  it('should find provisions about GDPR', async () => {
    const result = await searchLegislation(db, { query: 'általános adatvédelmi rendelet' });
    expect(result.results.length).toBeGreaterThanOrEqual(1);
  });

  it('should find provisions about information system fraud', async () => {
    const result = await searchLegislation(db, { query: 'információs rendszer' });
    expect(result.results.length).toBeGreaterThanOrEqual(1);
  });

  it('should return empty for gibberish query', async () => {
    const result = await searchLegislation(db, { query: 'xyzzyflurble99' });
    expect(result.results).toHaveLength(0);
  });

  it('should return empty for empty query', async () => {
    const result = await searchLegislation(db, { query: '' });
    expect(result.results).toHaveLength(0);
  });

  it('should respect limit parameter', async () => {
    const result = await searchLegislation(db, { query: 'biztonsági', limit: 3 });
    expect(result.results.length).toBeLessThanOrEqual(3);
  });

  it('should filter by document_id', async () => {
    const result = await searchLegislation(db, { query: 'biztonsági', document_id: 'act-l-2013-electronic-info-security' });
    expect(result.results.length).toBeGreaterThanOrEqual(1);
    result.results.forEach(r => {
      expect(r.document_id).toBe('act-l-2013-electronic-info-security');
    });
  });

  it('should filter by status', async () => {
    const result = await searchLegislation(db, { query: 'adat', status: 'in_force' });
    expect(result.results.length).toBeGreaterThanOrEqual(1);
  });

  it('should include metadata in response', async () => {
    const result = await searchLegislation(db, { query: 'adat' });
    expect(result._metadata).toBeDefined();
  });

  it('should handle database query errors by returning empty results', async () => {
    const fakeDb = {
      prepare() {
        return {
          all() {
            throw new Error('fts failure');
          },
          get() {
            throw new Error('meta failure');
          },
        };
      },
    } as unknown as InstanceType<typeof Database>;

    const result = await searchLegislation(fakeDb, { query: 'adat' });
    expect(result.results).toHaveLength(0);
  });
});

import { describe, it, expect, beforeAll, afterAll } from 'vitest';
import {
  createTestDb,
  describeIfRealDb,
  openRealDb,
  trackDb,
} from '../helpers/test-db.js';
import { getProvision } from '../../src/tools/get-provision.js';

describeIfRealDb('getProvision', () => {
  let db: ReturnType<typeof openRealDb>;

  beforeAll(() => {
    db = openRealDb();
  });
  afterAll(() => {
    db.close();
  });

  it('should retrieve Infotörvény § 1 (data protection scope)', async () => {
    const result = await getProvision(db, { document_id: 'act-cxii-2011-info-self-determination', section: '1' });
    expect(result.results).toHaveLength(1);
    expect(result.results[0].content).toContain('magánszféráját');
    expect(result.results[0].content).toContain('adatok szabad áramlása');
  });

  it('should retrieve Ibtv. § 11 (incident reporting)', async () => {
    const result = await getProvision(db, { document_id: 'act-l-2013-electronic-info-security', section: '11' });
    expect(result.results).toHaveLength(1);
    expect(result.results[0].content).toContain('szervezet vezetője köteles gondoskodni');
    expect(result.results[0].content).toContain('biztonsági osztály');
  });

  it('should retrieve Criminal Code § 422 (unauthorised access)', async () => {
    const result = await getProvision(db, { document_id: 'criminal-code-cybercrime', section: '422' });
    expect(result.results).toHaveLength(1);
    expect(result.results[0].content).toContain('jogosulatlan megismerése');
    expect(result.results[0].content).toContain('információs rendszer');
  });

  it('should retrieve provision by direct provision_ref', async () => {
    const result = await getProvision(db, { document_id: 'criminal-code-cybercrime', provision_ref: 's423' });
    expect(result.results).toHaveLength(1);
    expect(result.results[0].section).toBe('423');
  });

  it('should retrieve Trade Secrets Act § 2 (definition)', async () => {
    const result = await getProvision(db, { document_id: 'act-liv-2018-trade-secrets', section: '2' });
    expect(result.results).toHaveLength(1);
    expect(result.results[0].content).toContain('üzleti titok');
    expect(result.results[0].content).toContain('jogszerű ellenőrzést gyakorló személy');
  });

  it('should retrieve eIDAS Act § 10 (electronic signatures)', async () => {
    const result = await getProvision(db, { document_id: 'act-ccxxii-2015-trust-services', section: '10' });
    expect(result.results).toHaveLength(1);
    expect(result.results[0].content).toContain('elektronikus ügyintézéshez szükséges nyilatkozatokat');
    expect(result.results[0].content).toContain('egysége');
  });

  it('should retrieve all provisions for a document when no ref given', async () => {
    const result = await getProvision(db, { document_id: 'act-l-2013-electronic-info-security' });
    expect(result.results.length).toBeGreaterThanOrEqual(15);
    expect(result.results[0].document_id).toBe('act-l-2013-electronic-info-security');
  });

  it('should return empty for non-existent document', async () => {
    const result = await getProvision(db, { document_id: '2099-evi-MMMM-torveny', section: '1' });
    expect(result.results).toHaveLength(0);
  });

  it('should return empty for non-existent provision', async () => {
    const result = await getProvision(db, { document_id: 'act-cxii-2011-info-self-determination', section: '999ZZZ' });
    expect(result.results).toHaveLength(0);
  });

  it('should resolve by exact section column when s-prefix lookup does not match', async () => {
    const result = await getProvision(db, { document_id: 'act-cxii-2011-info-self-determination', section: '37/A' });
    expect(result.results).toHaveLength(1);
    expect(result.results[0].section).toBe('37/A');
  });

  it('should resolve via LIKE fallback for flexible section input', async () => {
    const result = await getProvision(db, { document_id: 'act-cxii-2011-info-self-determination', section: '7/A' });
    expect(result.results).toHaveLength(1);
    expect(result.results[0].section).toContain('37/');
  });

  it('should resolve document by title_en via fuzzy match', async () => {
    const result = await getProvision(db, { document_id: 'Informational Self-Determination', section: '1' });
    expect(result.results).toHaveLength(1);
    expect(result.results[0].document_id).toBe('act-cxii-2011-info-self-determination');
  });

  it('should resolve document by short_name', async () => {
    const result = await getProvision(db, { document_id: 'Ibtv.', section: '1' });
    expect(result.results).toHaveLength(1);
    expect(result.results[0].document_id).toBe('act-l-2013-electronic-info-security');
  });

  it('should include URL from njt.hu', async () => {
    const result = await getProvision(db, { document_id: 'act-cxii-2011-info-self-determination', section: '1' });
    expect(result.results).toHaveLength(1);
    expect(result.results[0].url).toContain('njt.hu');
  });

  it('should include metadata in response', async () => {
    const result = await getProvision(db, { document_id: 'act-cxii-2011-info-self-determination', section: '1' });
    expect(result._metadata).toBeDefined();
  });

  it('should map null URL to undefined for single and list responses', async () => {
    const db = trackDb(createTestDb());
    db.prepare(
      "INSERT INTO legal_documents (id, type, title, status, url) VALUES ('doc-null-url', 'statute', 'Null URL Act', 'in_force', NULL)"
    ).run();
    db.prepare(
      "INSERT INTO legal_provisions (document_id, provision_ref, section, content) VALUES ('doc-null-url', 's1', '1', 'text')"
    ).run();

    const single = await getProvision(db, { document_id: 'doc-null-url', section: '1' });
    expect(single.results).toHaveLength(1);
    expect(single.results[0].url).toBeUndefined();

    const list = await getProvision(db, { document_id: 'doc-null-url' });
    expect(list.results).toHaveLength(1);
    expect(list.results[0].url).toBeUndefined();
  });
});

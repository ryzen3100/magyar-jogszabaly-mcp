import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Server } from '@modelcontextprotocol/sdk/server/index.js';
import { CallToolRequestSchema, ListToolsRequestSchema } from '@modelcontextprotocol/sdk/types.js';
import Database from '@ansvar/mcp-sqlite';

import { createTestDb } from '../helpers/test-db.js';

vi.mock('../../src/tools/search-legislation.js', () => ({ searchLegislation: vi.fn() }));
vi.mock('../../src/tools/get-provision.js', () => ({ getProvision: vi.fn() }));
vi.mock('../../src/tools/validate-citation.js', () => ({ validateCitationTool: vi.fn() }));
vi.mock('../../src/tools/build-legal-stance.js', () => ({ buildLegalStance: vi.fn() }));
vi.mock('../../src/tools/format-citation.js', () => ({ formatCitationTool: vi.fn() }));
vi.mock('../../src/tools/check-currency.js', () => ({ checkCurrency: vi.fn() }));
vi.mock('../../src/tools/get-eu-basis.js', () => ({ getEUBasis: vi.fn() }));
vi.mock('../../src/tools/get-hungarian-implementations.js', () => ({ getHungarianImplementations: vi.fn() }));
vi.mock('../../src/tools/search-eu-implementations.js', () => ({ searchEUImplementations: vi.fn() }));
vi.mock('../../src/tools/get-provision-eu-basis.js', () => ({ getProvisionEUBasis: vi.fn() }));
vi.mock('../../src/tools/validate-eu-compliance.js', () => ({ validateEUCompliance: vi.fn() }));
vi.mock('../../src/tools/list-sources.js', () => ({ listSources: vi.fn() }));
vi.mock('../../src/tools/about.js', () => ({ getAbout: vi.fn() }));

import { searchLegislation } from '../../src/tools/search-legislation.js';
import { getProvision } from '../../src/tools/get-provision.js';
import { validateCitationTool } from '../../src/tools/validate-citation.js';
import { buildLegalStance } from '../../src/tools/build-legal-stance.js';
import { formatCitationTool } from '../../src/tools/format-citation.js';
import { checkCurrency } from '../../src/tools/check-currency.js';
import { getEUBasis } from '../../src/tools/get-eu-basis.js';
import { getHungarianImplementations } from '../../src/tools/get-hungarian-implementations.js';
import { searchEUImplementations } from '../../src/tools/search-eu-implementations.js';
import { getProvisionEUBasis } from '../../src/tools/get-provision-eu-basis.js';
import { validateEUCompliance } from '../../src/tools/validate-eu-compliance.js';
import { listSources } from '../../src/tools/list-sources.js';
import { getAbout } from '../../src/tools/about.js';
import { buildTools, registerTools } from '../../src/tools/registry.js';

type Handler = (request?: unknown) => Promise<unknown>;

function mockServer() {
  const handlers = new Map<unknown, Handler>();
  const server = {
    setRequestHandler(schema: unknown, handler: Handler) {
      handlers.set(schema, handler);
    },
  } as unknown as Server;

  return { server, handlers };
}

const opened: InstanceType<typeof Database>[] = [];

function trackDb(db: InstanceType<typeof Database>): InstanceType<typeof Database> {
  opened.push(db);
  return db;
}

afterEach(() => {
  while (opened.length > 0) opened.pop()?.close();
});

beforeEach(() => {
  vi.clearAllMocks();

  (searchLegislation as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({ tool: 'search_legislation' });
  (getProvision as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({ tool: 'get_provision' });
  (validateCitationTool as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({ tool: 'validate_citation' });
  (buildLegalStance as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({ tool: 'build_legal_stance' });
  (formatCitationTool as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({ tool: 'format_citation' });
  (checkCurrency as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({ tool: 'check_currency' });
  (getEUBasis as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({ tool: 'get_eu_basis' });
  (getHungarianImplementations as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({ tool: 'get_hungarian_implementations' });
  (searchEUImplementations as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({ tool: 'search_eu_implementations' });
  (getProvisionEUBasis as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({ tool: 'get_provision_eu_basis' });
  (validateEUCompliance as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({ tool: 'validate_eu_compliance' });
  (listSources as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({ tool: 'list_sources' });
  (getAbout as unknown as ReturnType<typeof vi.fn>).mockReturnValue({ tool: 'about' });
});

describe('buildTools', () => {
  it('returns base tool list without about when context is absent', () => {
    const tools = buildTools();
    const names = tools.map(t => t.name);
    expect(names).toContain('search_legislation');
    expect(names).toContain('list_sources');
    expect(names).not.toContain('about');
  });

  it('adds about when context is provided', () => {
    const tools = buildTools({
      version: '1.0.0',
      fingerprint: 'abc',
      dbBuilt: '2026-02-21T00:00:00Z',
    });
    expect(tools.map(t => t.name)).toContain('about');
  });
});

describe('registerTools', () => {
  it('registers list and call handlers and dispatches each tool', async () => {
    const db = trackDb(createTestDb());
    const { server, handlers } = mockServer();

    registerTools(server, db, {
      version: '1.0.0',
      fingerprint: 'abc',
      dbBuilt: '2026-02-21T00:00:00Z',
    });

    const listHandler = handlers.get(ListToolsRequestSchema);
    const callHandler = handlers.get(CallToolRequestSchema);
    expect(listHandler).toBeDefined();
    expect(callHandler).toBeDefined();

    const listed = await (listHandler as Handler)();
    const listedNames = (listed as { tools: Array<{ name: string }> }).tools.map(t => t.name);
    expect(listedNames).toContain('about');
    expect(listedNames).toContain('list_sources');

    const calls: Array<{ name: string; args: Record<string, unknown>; fn: ReturnType<typeof vi.fn> }> = [
      { name: 'search_legislation', args: { query: 'x' }, fn: searchLegislation as unknown as ReturnType<typeof vi.fn> },
      { name: 'get_provision', args: { document_id: 'x' }, fn: getProvision as unknown as ReturnType<typeof vi.fn> },
      { name: 'validate_citation', args: { citation: 'x' }, fn: validateCitationTool as unknown as ReturnType<typeof vi.fn> },
      { name: 'build_legal_stance', args: { query: 'x' }, fn: buildLegalStance as unknown as ReturnType<typeof vi.fn> },
      { name: 'format_citation', args: { citation: 'x' }, fn: formatCitationTool as unknown as ReturnType<typeof vi.fn> },
      { name: 'check_currency', args: { document_id: 'x' }, fn: checkCurrency as unknown as ReturnType<typeof vi.fn> },
      { name: 'get_eu_basis', args: { document_id: 'x' }, fn: getEUBasis as unknown as ReturnType<typeof vi.fn> },
      { name: 'get_hungarian_implementations', args: { eu_document_id: 'x' }, fn: getHungarianImplementations as unknown as ReturnType<typeof vi.fn> },
      { name: 'search_eu_implementations', args: { query: 'x' }, fn: searchEUImplementations as unknown as ReturnType<typeof vi.fn> },
      { name: 'get_provision_eu_basis', args: { document_id: 'x', provision_ref: '1' }, fn: getProvisionEUBasis as unknown as ReturnType<typeof vi.fn> },
      { name: 'validate_eu_compliance', args: { document_id: 'x' }, fn: validateEUCompliance as unknown as ReturnType<typeof vi.fn> },
      { name: 'list_sources', args: {}, fn: listSources as unknown as ReturnType<typeof vi.fn> },
      { name: 'about', args: {}, fn: getAbout as unknown as ReturnType<typeof vi.fn> },
    ];

    for (const entry of calls) {
      const response = await (callHandler as Handler)({
        params: { name: entry.name, arguments: entry.args },
      });
      expect(entry.fn).toHaveBeenCalled();
      const text = (response as { content: Array<{ text: string }> }).content[0].text;
      expect(JSON.parse(text).tool).toBe(entry.name);
    }
  });

  it('returns explicit error for about when context is missing', async () => {
    const db = trackDb(createTestDb());
    const { server, handlers } = mockServer();
    registerTools(server, db);

    const callHandler = handlers.get(CallToolRequestSchema) as Handler;
    const response = await callHandler({ params: { name: 'about', arguments: {} } });
    expect((response as { isError: boolean }).isError).toBe(true);
    expect((response as { content: Array<{ text: string }> }).content[0].text).toContain('About tool not configured');
  });

  it('returns explicit error for unknown tools', async () => {
    const db = trackDb(createTestDb());
    const { server, handlers } = mockServer();
    registerTools(server, db);

    const callHandler = handlers.get(CallToolRequestSchema) as Handler;
    const response = await callHandler({ params: { name: 'missing_tool', arguments: {} } });
    expect((response as { isError: boolean }).isError).toBe(true);
    expect((response as { content: Array<{ text: string }> }).content[0].text).toContain('Unknown tool');
  });

  it('returns structured error when a tool throws', async () => {
    const db = trackDb(createTestDb());
    const { server, handlers } = mockServer();
    registerTools(server, db);

    (searchLegislation as unknown as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('forced failure'));
    const callHandler = handlers.get(CallToolRequestSchema) as Handler;
    const response = await callHandler({
      params: { name: 'search_legislation', arguments: { query: 'x' } },
    });
    expect((response as { isError: boolean }).isError).toBe(true);
    expect((response as { content: Array<{ text: string }> }).content[0].text).toContain('forced failure');
  });

  it('stringifies non-Error throw values', async () => {
    const db = trackDb(createTestDb());
    const { server, handlers } = mockServer();
    registerTools(server, db);

    (searchLegislation as unknown as ReturnType<typeof vi.fn>).mockRejectedValueOnce('raw-failure');
    const callHandler = handlers.get(CallToolRequestSchema) as Handler;
    const response = await callHandler({
      params: { name: 'search_legislation', arguments: { query: 'x' } },
    });
    expect((response as { isError: boolean }).isError).toBe(true);
    expect((response as { content: Array<{ text: string }> }).content[0].text).toContain('raw-failure');
  });
});

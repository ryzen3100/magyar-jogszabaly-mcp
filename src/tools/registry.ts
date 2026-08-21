/**
 * Tool registry for Hungarian Law MCP Server.
 * Shared between stdio (index.ts) and HTTP entry points.
 */

import { Server } from '@modelcontextprotocol/sdk/server/index.js';
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
  Tool,
} from '@modelcontextprotocol/sdk/types.js';
import type Database from '@ansvar/mcp-sqlite';

import { searchLegislation } from './search-legislation.js';
import { getProvision } from './get-provision.js';
import { validateCitationTool } from './validate-citation.js';
import { buildLegalStance } from './build-legal-stance.js';
import { formatCitationTool } from './format-citation.js';
import { checkCurrency } from './check-currency.js';
import { getEUBasis } from './get-eu-basis.js';
import { getHungarianImplementations } from './get-hungarian-implementations.js';
import { searchEUImplementations } from './search-eu-implementations.js';
import { getProvisionEUBasis } from './get-provision-eu-basis.js';
import { validateEUCompliance } from './validate-eu-compliance.js';
import { listSources } from './list-sources.js';
import { getAbout, type AboutContext } from './about.js';
export type { AboutContext } from './about.js';

const ABOUT_TOOL: Tool = {
  name: 'about',
  description:
    'Server metadata, dataset statistics, freshness, and provenance. ' +
    'Call this to verify data coverage, currency, and content basis before relying on results.',
  inputSchema: { type: 'object', properties: {}, additionalProperties: false },
  annotations: { title: 'About this server', readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
};

const LIST_SOURCES_TOOL: Tool = {
  name: 'list_sources',
  description:
    'Returns detailed provenance metadata for all data sources used by this server, ' +
    'including the Nemzeti Jogszabálytár (National Legislation Database) (Magyar Közlöny (Hungarian Official Gazette)). ' +
    'Use this to understand what data is available, its authority, coverage scope, and known limitations. ' +
    'Also returns dataset statistics (document counts, provision counts) and database build timestamp. ' +
    'Call this FIRST when you need to understand what Hungarian legal data this server covers.',
  inputSchema: { type: 'object', properties: {}, additionalProperties: false },
  annotations: { title: 'List data sources', readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
};

export const TOOLS: Tool[] = [
  {
    name: 'search_legislation',
    description:
      'Search Hungarian statutes and regulations by keyword using full-text search (FTS5 with BM25 ranking). ' +
      'Returns matching provisions with document context, snippets with >>> <<< markers around matched terms, and relevance scores. ' +
      'Supports FTS5 syntax: quoted phrases ("exact match"), boolean operators (AND, OR, NOT), and prefix wildcards (term*). ' +
      'Results are in English. Default limit is 10 results. For broad topics, increase the limit. ' +
      'Do NOT use this for retrieving a known provision — use get_provision instead.',
    inputSchema: {
      type: 'object',
      properties: {
        query: {
          type: 'string',
          description:
            'Search query in English. Supports FTS5 syntax: ' +
            '"personal information" for exact phrase, privacy* for prefix.',
        },
        document_id: {
          type: 'string',
          description: 'Optional: filter results to a specific statute by its document ID.',
        },
        status: {
          type: 'string',
          enum: ['in_force', 'amended', 'repealed'],
          description: 'Optional: filter by legislative status.',
        },
        limit: {
          type: 'number',
          description: 'Maximum results to return (default: 10, max: 50).',
          default: 10,
        },
      },
      required: ['query'],
    },
    annotations: { title: 'Search legislation', readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  {
    name: 'get_provision',
    description:
      'Retrieve the full text of a specific provision (section) from an Hungarian statute. ' +
      'Specify a document_id (Act title, abbreviation, or internal ID) and optionally a section or provision_ref. ' +
      'Omit section/provision_ref to get ALL provisions in the statute (use sparingly — can be large). ' +
      'Returns provision text, chapter, section number, and metadata. ' +
      'Supports Act title references (e.g., "Privacy Act 1988"), abbreviations, and full titles. ' +
      'Use this when you know WHICH provision you want. For discovery, use search_legislation instead.',
    inputSchema: {
      type: 'object',
      properties: {
        document_id: {
          type: 'string',
          description:
            'Statute identifier: Act title (e.g., "Privacy Act 1988"), abbreviation, ' +
            'or internal document ID (e.g., "privacy-act-1988").',
        },
        section: {
          type: 'string',
          description: 'Section number (e.g., "13", "8"). Omit to get all provisions.',
        },
        provision_ref: {
          type: 'string',
          description: 'Direct provision reference (e.g., "s13"). Alternative to section parameter.',
        },
      },
      required: ['document_id'],
    },
    annotations: { title: 'Get provision text', readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  {
    name: 'validate_citation',
    description:
      'Validate an Hungarian legal citation against the database — zero-hallucination check. ' +
      'Parses the citation, checks that the document and provision exist, and returns warnings about status ' +
      '(repealed, amended). Use this to verify any citation BEFORE including it in a legal analysis. ' +
      'Supports formats: "Section 13 Privacy Act 1988", "Privacy Act 1988 s 13", "s 13".',
    inputSchema: {
      type: 'object',
      properties: {
        citation: {
          type: 'string',
          description: 'Citation string to validate. Examples: "Section 13 Privacy Act 1988", "Privacy Act 1988 s 13".',
        },
      },
      required: ['citation'],
    },
    annotations: { title: 'Validate citation', readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  {
    name: 'build_legal_stance',
    description:
      'Build a comprehensive set of citations for a legal question by searching across all Hungarian statutes simultaneously. ' +
      'Returns aggregated results from multiple relevant provisions, useful for legal research on a topic. ' +
      'Use this for broad legal questions like "What are the penalties for data breaches in Hungary?" ' +
      'rather than looking up a specific known provision.',
    inputSchema: {
      type: 'object',
      properties: {
        query: {
          type: 'string',
          description: 'Legal question or topic to research (e.g., "personal information", "critical infrastructure").',
        },
        document_id: {
          type: 'string',
          description: 'Optional: limit search to one statute by document ID.',
        },
        limit: {
          type: 'number',
          description: 'Max results per category (default: 5, max: 20).',
          default: 5,
        },
      },
      required: ['query'],
    },
    annotations: { title: 'Build legal stance', readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  {
    name: 'format_citation',
    description:
      'Format an Hungarian legal citation per standard conventions. ' +
      'Three formats: "full" (formal, e.g., "Section 13, Privacy Act 1988"), ' +
      '"short" (abbreviated, e.g., "Privacy Act 1988 s 13"), "pinpoint" (section reference only, e.g., "s 13").',
    inputSchema: {
      type: 'object',
      properties: {
        citation: { type: 'string', description: 'Citation string to format.' },
        format: {
          type: 'string',
          enum: ['full', 'short', 'pinpoint'],
          description: 'Output format (default: "full").',
          default: 'full',
        },
      },
      required: ['citation'],
    },
    annotations: { title: 'Format citation', readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  {
    name: 'check_currency',
    description:
      'Check whether an Hungarian statute or provision is currently in force, amended, repealed, or not yet in force. ' +
      'Returns the document status, issued date, in-force date, and warnings. ' +
      'Essential before citing any provision — always verify currency.',
    inputSchema: {
      type: 'object',
      properties: {
        document_id: {
          type: 'string',
          description: 'Statute identifier (Act title, abbreviation, or ID).',
        },
        provision_ref: {
          type: 'string',
          description: 'Optional: provision reference to check a specific section.',
        },
      },
      required: ['document_id'],
    },
    annotations: { title: 'Check currency status', readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  {
    name: 'get_eu_basis',
    description:
      'Get the EU legal basis that an Hungarian statute references or aligns with. ' +
      'As an EU Member State, Hungary transposes EU directives and implements EU regulations ' +
      '(e.g., Privacy Act references GDPR concepts, SOCI Act aligns with NIS2 patterns). ' +
      'Returns EU document identifiers, reference types, and alignment status.',
    inputSchema: {
      type: 'object',
      properties: {
        document_id: { type: 'string', description: 'Hungarian statute identifier.' },
        include_articles: {
          type: 'boolean',
          description: 'Include specific EU article references (default: false).',
          default: false,
        },
        reference_types: {
          type: 'array',
          items: { type: 'string' },
          description: 'Optional: filter by reference type (e.g., "implements", "transposes").',
        },
      },
      required: ['document_id'],
    },
    annotations: { title: 'Get EU legal basis', readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  {
    name: 'get_hungarian_implementations',
    description:
      'Find all Hungarian statutes that reference or align with a specific EU directive or regulation. ' +
      'Given an EU document ID (e.g., "regulation:2016/679" for GDPR), returns matching Hungarian statutes. ' +
      'Note: Hungary is an EU Member State and transposes EU directives into national law.',
    inputSchema: {
      type: 'object',
      properties: {
        eu_document_id: {
          type: 'string',
          description: 'EU document ID (e.g., "regulation:2016/679" for GDPR, "directive:2022/2555" for NIS2).',
        },
        primary_only: {
          type: 'boolean',
          description: 'Return only primary referencing statutes (default: false).',
          default: false,
        },
        in_force_only: {
          type: 'boolean',
          description: 'Return only currently in-force statutes (default: false).',
          default: false,
        },
      },
      required: ['eu_document_id'],
    },
    annotations: { title: 'Find Hungarian implementations', readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  {
    name: 'search_eu_implementations',
    description:
      'Search for EU directives and regulations that are referenced by Hungarian legislation. ' +
      'Search by keyword, type (directive/regulation), or year range.',
    inputSchema: {
      type: 'object',
      properties: {
        query: { type: 'string', description: 'Keyword search across EU document titles.' },
        type: { type: 'string', enum: ['directive', 'regulation'], description: 'Filter by EU document type.' },
        year_from: { type: 'number', description: 'Filter by year (from).' },
        year_to: { type: 'number', description: 'Filter by year (to).' },
        has_hungarian_implementation: {
          type: 'boolean',
          description: 'If true, only return EU documents referenced by Hungarian legislation.',
        },
        limit: { type: 'number', description: 'Max results (default: 20, max: 100).', default: 20 },
      },
    },
    annotations: { title: 'Search EU implementations', readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  {
    name: 'get_provision_eu_basis',
    description:
      'Get the EU legal basis for a SPECIFIC provision within an Hungarian statute. ' +
      'More granular than get_eu_basis (which operates at the statute level). ' +
      'Use this for pinpoint EU alignment checks at the provision level.',
    inputSchema: {
      type: 'object',
      properties: {
        document_id: { type: 'string', description: 'Hungarian statute identifier.' },
        provision_ref: { type: 'string', description: 'Provision reference (e.g., "s13" or "13").' },
      },
      required: ['document_id', 'provision_ref'],
    },
    annotations: { title: 'Get provision EU basis', readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  {
    name: 'validate_eu_compliance',
    description:
      'Check EU alignment status for an Hungarian statute or provision. ' +
      'Detects references to EU directives, alignment status, and cross-references. ' +
      'Returns compliance status (compliant, partial, unclear, not_applicable) with warnings. ' +
      'Note: As an EU Member State, Hungary is bound by EU law. This checks transposition and compliance status.',
    inputSchema: {
      type: 'object',
      properties: {
        document_id: { type: 'string', description: 'Hungarian statute identifier.' },
        eu_document_id: { type: 'string', description: 'Optional: check against a specific EU document.' },
      },
      required: ['document_id'],
    },
    annotations: { title: 'Validate EU compliance', readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
];

/** Tool name → handler. Each handler declares its own input type; raw MCP
 * args flow straight through (`never` makes every concrete input assignable). */
const HANDLERS: Record<string, (db: InstanceType<typeof Database>, args: never) => Promise<unknown>> = {
  search_legislation: searchLegislation,
  get_provision: getProvision,
  validate_citation: validateCitationTool,
  build_legal_stance: buildLegalStance,
  format_citation: formatCitationTool,
  check_currency: checkCurrency,
  get_eu_basis: getEUBasis,
  get_hungarian_implementations: getHungarianImplementations,
  search_eu_implementations: searchEUImplementations,
  get_provision_eu_basis: getProvisionEUBasis,
  validate_eu_compliance: validateEUCompliance,
  list_sources: (db) => listSources(db),
};

export function buildTools(context?: AboutContext): Tool[] {
  const tools = [...TOOLS, LIST_SOURCES_TOOL];

  if (context) {
    tools.push(ABOUT_TOOL);
  }

  return tools;
}

export function registerTools(
  server: Server,
  db: InstanceType<typeof Database>,
  context?: AboutContext,
): void {
  const allTools = buildTools(context);

  server.setRequestHandler(ListToolsRequestSchema, async () => {
    return { tools: allTools };
  });

  server.setRequestHandler(CallToolRequestSchema, async (request) => {
    const { name, arguments: args } = request.params;

    try {
      let result: unknown;

      if (name === 'about') {
        if (!context) {
          return {
            content: [{ type: 'text' as const, text: 'About tool not configured.' }],
            isError: true,
          };
        }
        result = getAbout(db, context);
      } else {
        const handler = HANDLERS[name];
        if (!handler) {
          return {
            content: [{ type: 'text' as const, text: `Error: Unknown tool "${name}".` }],
            isError: true,
          };
        }
        result = await handler(db, args as never);
      }

      return {
        content: [{ type: 'text' as const, text: JSON.stringify(result, null, 2) }],
      };
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      return {
        content: [{ type: 'text' as const, text: `Error: ${message}` }],
        isError: true,
      };
    }
  });
}

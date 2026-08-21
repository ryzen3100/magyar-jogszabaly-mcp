#!/usr/bin/env node

/**
 * HTTP entry point for Hungarian Law MCP Server (Docker proxy transport).
 *
 * Endpoints:
 *   GET  /health  → { status, server, version, uptime_seconds }
 *   POST /mcp     → MCP Streamable HTTP transport (new + existing sessions)
 *   GET  /mcp     → SSE stream (existing session) or metadata (no session)
 *   DELETE /mcp   → session termination
 *   OPTIONS *     → CORS preflight
 */

import { Server } from '@modelcontextprotocol/sdk/server/index.js';
import { StreamableHTTPServerTransport } from '@modelcontextprotocol/sdk/server/streamableHttp.js';
import {
  ListPromptsRequestSchema,
  GetPromptRequestSchema,
  ListResourcesRequestSchema,
  ReadResourceRequestSchema,
} from '@modelcontextprotocol/sdk/types.js';
import { createServer as createHttpServer, IncomingMessage, ServerResponse } from 'node:http';
import { randomUUID } from 'crypto';
import { existsSync, readFileSync } from 'fs';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';
import Database from '@ansvar/mcp-sqlite';

import { registerTools } from './tools/registry.js';
import { listSources as listSourcesFn } from './tools/list-sources.js';
import { getAbout as getAboutFn, type AboutContext } from './tools/about.js';
import { detectCapabilities, readDbMetadata } from './capabilities.js';
import { SERVER_NAME, SERVER_VERSION } from './constants.js';
import { computeDbFingerprint, resolveDbPath } from './db-info.js';

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const PORT = parseInt(process.env.PORT || '3000', 10);

// ---------------------------------------------------------------------------
// Session management
// ---------------------------------------------------------------------------

/** UUID v4 pattern — prevents injection via session ID header. */
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function validSessionId(raw: string | undefined): string | undefined {
  if (!raw || !UUID_RE.test(raw)) return undefined;
  return raw;
}

const sessions = new Map<string, StreamableHTTPServerTransport>();

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

async function main() {
  const dbPath = resolveDbPath();
  const db = new Database(dbPath, { readonly: true });
  db.pragma('foreign_keys = ON');

  const caps = detectCapabilities(db);
  const meta = readDbMetadata(db);
  console.error(`[${SERVER_NAME}] Database: ${dbPath}`);
  console.error(`[${SERVER_NAME}] Tier: ${meta.tier}, Capabilities: ${[...caps].join(', ')}`);

  // About context for the about tool — sampled hash avoids loading the entire
  // DB into memory (some are 200MB+); db_metadata built_at overrides mtime.
  const { fingerprint, dbBuilt: mtimeFallback } = computeDbFingerprint(dbPath);
  const aboutContext: AboutContext = {
    version: SERVER_VERSION,
    fingerprint,
    dbBuilt: meta.built_at ?? mtimeFallback,
  };

  /** Create a fresh MCP server instance (one per session). */
  function createMCPServer(): Server {
    const server = new Server(
      { name: SERVER_NAME, version: SERVER_VERSION },
      { capabilities: { tools: {}, prompts: {}, resources: {} } },
    );
    registerTools(server, db, aboutContext);

    // Prompts
    server.setRequestHandler(ListPromptsRequestSchema, async () => ({
      prompts: [
        {
          name: 'legal_review',
          description: 'Review a Hungarian legal document, contract, or policy for compliance issues, risks, and missing elements. Returns structured findings with risk levels and specific legal references.',
          arguments: [
            { name: 'document_text', description: 'The full text of the document to review', required: true },
            { name: 'focus_area', description: 'Optional focus: gdpr, contract, employment, consumer, corporate', required: false },
          ],
        },
        {
          name: 'legal_research',
          description: 'Research a Hungarian legal question across all statutes. Returns relevant provisions, EU cross-references, and practical guidance for SMEs.',
          arguments: [
            { name: 'question', description: 'The legal question in plain language (Hungarian or English)', required: true },
          ],
        },
      ],
    }));

    server.setRequestHandler(GetPromptRequestSchema, async (request) => {
      const { name, arguments: args } = request.params;
      if (name === 'legal_review') {
        return {
          messages: [{
            role: 'user',
            content: { type: 'text', text: `Review the following Hungarian legal document for compliance issues, risks, and missing elements.\n\nFocus area: ${args?.focus_area || 'all'}\n\nDocument:\n${args?.document_text || '(no document provided)'}` },
          }],
        };
      }
      if (name === 'legal_research') {
        return {
          messages: [{
            role: 'user',
            content: { type: 'text', text: `Research this Hungarian legal question using the legislation database. Cite specific provisions with section numbers.\n\nQuestion: ${args?.question || '(no question provided)'}` },
          }],
        };
      }
      throw new Error(`Unknown prompt: ${name}`);
    });

    // Resources
    server.setRequestHandler(ListResourcesRequestSchema, async () => ({
      resources: [
        {
          uri: 'hungarian-law://sources',
          name: 'Data Sources & Provenance',
          description: 'Authoritative legal data sources, coverage scope, and database freshness metadata',
          mimeType: 'application/json',
        },
        {
          uri: 'hungarian-law://stats',
          name: 'Database Statistics',
          description: 'Document counts, provision counts, definition counts, and EU reference coverage',
          mimeType: 'application/json',
        },
      ],
    }));

    server.setRequestHandler(ReadResourceRequestSchema, async (request) => {
      const { uri } = request.params;
      if (uri === 'hungarian-law://sources') {
        const sources = await listSourcesFn(db);
        return { contents: [{ uri, mimeType: 'application/json', text: JSON.stringify(sources, null, 2) }] };
      }
      if (uri === 'hungarian-law://stats') {
        const about = getAboutFn(db, aboutContext);
        return { contents: [{ uri, mimeType: 'application/json', text: JSON.stringify(about, null, 2) }] };
      }
      throw new Error(`Unknown resource: ${uri}`);
    });

    return server;
  }

  // -------------------------------------------------------------------------
  // HTTP server
  // -------------------------------------------------------------------------

  const httpServer = createHttpServer(async (req: IncomingMessage, res: ServerResponse) => {
    const url = new URL(req.url || '/', `http://localhost:${PORT}`);

    // CORS
    res.setHeader('Access-Control-Allow-Origin', '*');
    res.setHeader('Access-Control-Allow-Methods', 'GET, POST, DELETE, OPTIONS');
    res.setHeader('Access-Control-Allow-Headers', 'Content-Type, mcp-session-id, Authorization');
    res.setHeader('Access-Control-Expose-Headers', 'mcp-session-id');

    try {
      // OPTIONS — preflight
      if (req.method === 'OPTIONS') {
        res.writeHead(204);
        res.end();
        return;
      }

      // GET /health
      if (url.pathname === '/health' && (req.method === 'GET' || req.method === 'HEAD')) {
        let dbOk = false;
        try {
          if (caps.has('core_legislation')) {
            const counts = db.prepare(`
              SELECT
                (SELECT COUNT(*) FROM legal_documents) AS documents,
                (SELECT COUNT(*) FROM legal_provisions) AS provisions
            `).get() as { documents: number; provisions: number };
            dbOk = Number(counts.documents) > 0 && Number(counts.provisions) > 0;
          }
        } catch { /* DB not healthy */ }

        res.writeHead(dbOk ? 200 : 503, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({
          status: dbOk ? 'ok' : 'degraded',
          server: SERVER_NAME,
          version: SERVER_VERSION,
          uptime_seconds: Math.floor(process.uptime()),
        }));
        return;
      }

      // /mcp — MCP Streamable HTTP transport
      if (url.pathname === '/mcp') {
        const sessionId = validSessionId(req.headers['mcp-session-id'] as string | undefined);

        // Existing session — delegate
        if (sessionId && sessions.has(sessionId)) {
          await sessions.get(sessionId)!.handleRequest(req, res);
          return;
        }

        // DELETE — session termination (no existing session found)
        if (req.method === 'DELETE') {
          res.writeHead(404, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ error: 'Session not found' }));
          return;
        }

        // POST — new session (initialize)
        if (req.method === 'POST') {
          // Pre-generate sessionId so we can store it before handleRequest.
          // This eliminates a race where the client sends a follow-up request
          // between handleRequest completing and sessions.set() executing.
          const newSessionId = randomUUID();
          const transport = new StreamableHTTPServerTransport({
            sessionIdGenerator: () => newSessionId,
          });

          sessions.set(newSessionId, transport);

          transport.onclose = () => {
            sessions.delete(newSessionId);
          };

          const server = createMCPServer();
          await server.connect(transport);
          await transport.handleRequest(req, res);
          return;
        }

        // GET/HEAD without session — metadata
        if (req.method === 'GET' || req.method === 'HEAD') {
          res.writeHead(200, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({
            name: SERVER_NAME,
            version: SERVER_VERSION,
            description: 'Full-text search across 4,300+ Hungarian statutes and 130,000+ provisions from Nemzeti Jogszabálytár (njt.hu). Database freshness is checked daily; new data is shipped with new container images.',
            protocol: 'mcp',
            transport: 'streamable-http',
          }));
          return;
        }

        res.writeHead(400, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: 'Bad request — missing or invalid session' }));
        return;
      }

      // GET /icon.png — server icon
      if (url.pathname === '/icon.png' && (req.method === 'GET' || req.method === 'HEAD')) {
        try {
          // dist/icon.png (packaged/Docker layout) with repo-root fallback for plain local runs
          const iconPath = existsSync(join(__dirname, '..', 'icon.png'))
            ? join(__dirname, '..', 'icon.png')
            : join(__dirname, '..', '..', 'icon.png');
          const iconData = readFileSync(iconPath);
          res.writeHead(200, { 'Content-Type': 'image/png', 'Cache-Control': 'public, max-age=86400', 'Content-Length': iconData.length.toString() });
          if (req.method !== 'HEAD') res.end(iconData);
          else res.end();
        } catch {
          res.writeHead(404);
          res.end();
        }
        return;
      }

      // GET /.well-known/mcp/server-card.json — MCP server card for registries
      if (url.pathname === '/.well-known/mcp/server-card.json' && req.method === 'GET') {
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({
          serverInfo: {
            name: SERVER_NAME,
            version: SERVER_VERSION,
            displayName: 'Hungarian Law MCP',
            description: 'Full-text search across 4,300+ Hungarian statutes and 130,000+ provisions. Covers the full corpus from Nemzeti Jogszabálytár (njt.hu) including Ptk., Infotv., Mt., Btk., and EU cross-references. Database freshness is checked daily; new data is shipped with new container images.',
            homepage: 'https://github.com/Ansvar-Systems/Hungarian-law-mcp',
            keywords: ['hungarian-law', 'legislation', 'legal', 'mcp', 'gdpr', 'data-protection', 'cybersecurity', 'compliance', 'ptk', 'infotv'],
            author: 'Ansvar Systems / AVIAN Care Kft.',
            license: 'Apache-2.0',
          },
          capabilities: {
            tools: true,
            prompts: true,
            resources: true,
          },
          transport: {
            type: 'streamable-http',
            url: '/mcp',
          },
        }));
        return;
      }

      // 404
      res.writeHead(404, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: 'Not found' }));
    } catch (error) {
      console.error(`[${SERVER_NAME}] Unhandled error:`, error);
      if (!res.headersSent) {
        res.writeHead(500, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: 'Internal server error' }));
      }
    }
  });

  httpServer.listen(PORT, () => {
    console.error(`${SERVER_NAME} v${SERVER_VERSION} HTTP server listening on port ${PORT}`);
  });

  // -------------------------------------------------------------------------
  // Graceful shutdown
  // -------------------------------------------------------------------------

  const shutdown = (signal: string) => {
    console.error(`[${SERVER_NAME}] Shutting down (${signal})...`);
    for (const [, t] of sessions) t.close().catch(() => {});
    sessions.clear();
    try { db.close(); } catch { /* ignore */ }
    httpServer.close(() => process.exit(0));
    setTimeout(() => process.exit(1), 5000);
  };

  process.on('SIGTERM', () => shutdown('SIGTERM'));
  process.on('SIGINT', () => shutdown('SIGINT'));
}

main().catch((err) => {
  console.error('Fatal:', err);
  process.exit(1);
});

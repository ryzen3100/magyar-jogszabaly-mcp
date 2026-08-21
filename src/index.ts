#!/usr/bin/env node

/**
 * Hungarian Law MCP Server — stdio entry point.
 *
 * Provides Hungarian legislation search via Model Context Protocol.
 */

import { Server } from '@modelcontextprotocol/sdk/server/index.js';
import { StdioServerTransport } from '@modelcontextprotocol/sdk/server/stdio.js';
import Database from '@ansvar/mcp-sqlite';

import { registerTools, type AboutContext } from './tools/registry.js';
import { detectCapabilities, readDbMetadata } from './capabilities.js';
import { SERVER_NAME, SERVER_VERSION } from './constants.js';
import { computeDbFingerprint, resolveDbPath } from './db-info.js';

let db: InstanceType<typeof Database> | null = null;

function getDb(): InstanceType<typeof Database> {
  if (!db) {
    const dbPath = resolveDbPath();
    db = new Database(dbPath, { readonly: true });
    db.pragma('foreign_keys = ON');

    const caps = detectCapabilities(db);
    const meta = readDbMetadata(db);
    console.error(`[${SERVER_NAME}] DB opened: tier=${meta.tier}, caps=[${[...caps].join(',')}]`);
  }
  return db;
}

function computeAboutContext(): AboutContext {
  const { fingerprint, dbBuilt: fileFallback } = computeDbFingerprint(resolveDbPath());
  const dbBuilt = readDbMetadata(getDb()).built_at ?? fileFallback;
  return { version: SERVER_VERSION, fingerprint, dbBuilt };
}

async function main() {
  const database = getDb();
  const aboutContext = computeAboutContext();

  const server = new Server(
    { name: SERVER_NAME, version: SERVER_VERSION },
    { capabilities: { tools: {} } }
  );

  registerTools(server, database, aboutContext);

  const transport = new StdioServerTransport();
  await server.connect(transport);
  console.error(`[${SERVER_NAME}] Server running on stdio`);

  const cleanup = () => {
    if (db) {
      db.close();
      db = null;
    }
    process.exit(0);
  };

  process.on('SIGINT', cleanup);
  process.on('SIGTERM', cleanup);
}

main().catch((err) => {
  console.error(`[${SERVER_NAME}] Fatal error:`, err);
  process.exit(1);
});

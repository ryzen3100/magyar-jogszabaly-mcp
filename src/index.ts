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
import { readDbMetadata } from './capabilities.js';
import { SERVER_NAME, SERVER_VERSION } from './constants.js';
import { buildAboutContext, resolveDbPath } from './db-info.js';

let db: InstanceType<typeof Database> | null = null;

function getDb(): InstanceType<typeof Database> {
  if (!db) {
    db = new Database(resolveDbPath(), { readonly: true });

    const meta = readDbMetadata(db);
    console.error(`[${SERVER_NAME}] DB opened: tier=${meta.tier}`);
  }
  return db;
}

async function main() {
  const database = getDb();
  const aboutContext: AboutContext = {
    version: SERVER_VERSION,
    ...buildAboutContext(resolveDbPath(), database),
  };

  const server = new Server(
    { name: SERVER_NAME, version: SERVER_VERSION },
    { capabilities: { tools: {} } }
  );

  registerTools(server, database, aboutContext);

  const transport = new StdioServerTransport();
  await server.connect(transport);
  console.error(`[${SERVER_NAME}] Server running on stdio`);

  const cleanup = () => {
    if (db) db.close();
    process.exit(0);
  };

  process.on('SIGINT', cleanup);
  process.on('SIGTERM', cleanup);
}

main().catch((err) => {
  console.error(`[${SERVER_NAME}] Fatal error:`, err);
  process.exit(1);
});

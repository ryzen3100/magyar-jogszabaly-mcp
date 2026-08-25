/**
 * Runtime capability detection for Hungarian Law MCP.
 * Detects which database tables are available to enable/disable features.
 */

import type Database from '@ansvar/mcp-sqlite';
import { generateResponseMetadata, type ToolResponse } from './utils/metadata.js';

type Capability = 'core_legislation';

const CORE_TABLES = ['legal_documents', 'legal_provisions', 'provisions_fts'];

export function detectCapabilities(db: InstanceType<typeof Database>): Set<Capability> {
  const caps = new Set<Capability>();
  const tables = new Set(
    (db.prepare("SELECT name FROM sqlite_master WHERE type='table'").all() as { name: string }[])
      .map(r => r.name)
  );

  if (CORE_TABLES.every(t => tables.has(t))) {
    caps.add('core_legislation');
  }

  return caps;
}

interface DbMetadata {
  tier: string;
  schema_version: string;
  built_at?: string;
}

// DB is opened readonly, so metadata never changes per connection.
const metadataCache = new WeakMap<InstanceType<typeof Database>, DbMetadata>();

export function readDbMetadata(db: InstanceType<typeof Database>): DbMetadata {
  let cached = metadataCache.get(db);
  if (!cached) {
    const meta: Record<string, string> = {};
    try {
      const rows = db.prepare('SELECT key, value FROM db_metadata').all() as { key: string; value: string }[];
      for (const row of rows) {
        meta[row.key] = row.value;
      }
    } catch {
      // db_metadata table may not exist
    }
    cached = Object.freeze({
      tier: meta.tier ?? 'free',
      schema_version: meta.schema_version ?? '1.0',
      built_at: meta.built_at,
    });
    metadataCache.set(db, cached);
  }
  return cached;
}

// Readonly DB → table presence never changes per connection.
const euAvailabilityCache = new WeakMap<InstanceType<typeof Database>, Map<string, boolean>>();

/** Probe whether an EU table exists. Callers pass compile-time table names only. */
export function euAvailable(db: InstanceType<typeof Database>, table = 'eu_references'): boolean {
  let byTable = euAvailabilityCache.get(db);
  if (!byTable) {
    byTable = new Map();
    euAvailabilityCache.set(db, byTable);
  }
  let available = byTable.get(table);
  if (available === undefined) {
    try {
      db.prepare(`SELECT 1 FROM ${table} LIMIT 1`).get();
      available = true;
    } catch {
      available = false;
    }
    byTable.set(table, available);
  }
  return available;
}

export function euUnavailable(
  db: InstanceType<typeof Database>,
  table = 'eu_references',
): ToolResponse<never[]> {
  return {
    results: [],
    _metadata: {
      ...generateResponseMetadata(db),
      note: `EU ${table.slice(3)} not available in this database tier`,
    },
  };
}

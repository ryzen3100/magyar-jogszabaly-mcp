/**
 * Response metadata utilities for Hungarian Law MCP.
 */

import type Database from '@ansvar/mcp-sqlite';
import { readDbMetadata } from '../capabilities.js';

interface ResponseMetadata {
  data_source: string;
  jurisdiction: string;
  disclaimer: string;
  freshness?: string;
  note?: string;
  query_strategy?: string;
}

export interface ToolResponse<T> {
  results: T;
  _metadata: ResponseMetadata;
}

export function safeCount(db: InstanceType<typeof Database>, sql: string): number {
  try {
    const row = db.prepare(sql).get() as { count: number } | undefined;
    return row ? Number(row.count) : 0;
  } catch {
    return 0;
  }
}

// Readonly DB → counts never change per connection.
const countCache = new WeakMap<InstanceType<typeof Database>, Map<string, number>>();

/** cachedCount: like safeCount, but memoized per db+sql (immutable facts). */
export function cachedCount(db: InstanceType<typeof Database>, sql: string): number {
  let bySql = countCache.get(db);
  if (!bySql) {
    bySql = new Map();
    countCache.set(db, bySql);
  }
  let count = bySql.get(sql);
  if (count === undefined) {
    count = safeCount(db, sql);
    bySql.set(sql, count);
  }
  return count;
}

export function generateResponseMetadata(
  db: InstanceType<typeof Database>,
): ResponseMetadata {
  return {
    data_source: 'Nemzeti Jogszabálytár (National Legislation Database) (njt.hu) — Magyar Közlöny (Hungarian Official Gazette)',
    jurisdiction: 'HU',
    disclaimer:
      'This data is sourced from the Nemzeti Jogszabálytár (National Legislation Database) under public domain. ' +
      'The authoritative versions are maintained by Magyar Közlöny (Hungarian Official Gazette). ' +
      'Always verify with the official Nemzeti Jogszabálytár (National Legislation Database) portal (njt.hu).',
    freshness: readDbMetadata(db).built_at,
  };
}

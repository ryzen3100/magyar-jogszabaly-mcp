/**
 * Shared database location/fingerprint helpers for the stdio and HTTP entry points.
 */

import { createHash } from 'crypto';
import { closeSync, existsSync, openSync, readSync, statSync } from 'fs';
import { dirname, join } from 'path';
import { fileURLToPath } from 'url';

import { DB_ENV_VAR } from './constants.js';

const HERE = dirname(fileURLToPath(import.meta.url));

/**
 * Resolve the law database path: env override first, then standard data/
 * locations relative to the build output.
 */
export function resolveDbPath(): string {
  const envPath = process.env[DB_ENV_VAR];
  if (envPath) return envPath;

  for (const candidate of [
    join(HERE, '..', 'data', 'database.db'),
    join(HERE, '..', '..', 'data', 'database.db'),
  ]) {
    if (existsSync(candidate)) return candidate;
  }

  throw new Error(
    `Database not found. Set ${DB_ENV_VAR} or place database.db in data/`,
  );
}

/**
 * Sampled sha256 fingerprint + mtime fallback for the about tool.
 * Partial hash avoids loading the entire DB into memory (some are 200MB+).
 */
export function computeDbFingerprint(dbPath: string): { fingerprint: string; dbBuilt: string } {
  let fingerprint = 'unknown';
  let dbBuilt = new Date().toISOString();

  try {
    const SAMPLE = 64 * 1024;
    const fd = openSync(dbPath, 'r');
    const buf = Buffer.alloc(SAMPLE);
    readSync(fd, buf, 0, SAMPLE, 0);
    closeSync(fd);
    fingerprint = createHash('sha256').update(buf).digest('hex').slice(0, 12);
    dbBuilt = statSync(dbPath).mtime.toISOString();
  } catch { /* non-fatal */ }

  return { fingerprint, dbBuilt };
}

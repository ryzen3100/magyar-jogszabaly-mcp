#!/usr/bin/env tsx
/**
 * Hungarian Law MCP — Data Freshness Checker
 *
 * Checks whether the local database is stale or missing expected legislation.
 *
 * Detection strategy:
 * 1. Database age — flags if built_at > MAX_AGE days old
 * 2. Document count — compares DB rows against census.json expected count
 * 3. Source portal — verifies the official legal portal is reachable
 *
 * Exit codes:
 *   0 = database is fresh, no updates detected
 *   1 = updates detected (stale DB, missing documents, or new content upstream)
 *   2 = check failed (DB missing, portal unreachable, unexpected error)
 */

import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const DB_PATH = resolve(__dirname, '../data/database.db');
const CENSUS_PATH = resolve(__dirname, '../data/census.json');

const MAX_DB_AGE_DAYS = Number(process.env['MAX_DB_AGE_DAYS'] ?? '90');
const PORTAL_URL = 'https://njt.hu';
const PORTAL_NAME = 'Nemzeti Jogszabalytár (National Legislation Database)';

interface CensusData {
  total_laws?: number;
  total_provisions?: number;
}

function daysSince(isoDate: string): number | null {
  const dt = new Date(isoDate);
  if (Number.isNaN(dt.getTime())) return null;
  return Math.floor((Date.now() - dt.getTime()) / (1000 * 60 * 60 * 24));
}

async function checkPortal(url: string): Promise<boolean> {
  try {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 15_000);
    const res = await fetch(url, {
      method: 'HEAD',
      signal: controller.signal,
      headers: { 'User-Agent': '@ansvar/hungarian-law-mcp/1.0 (data-freshness-check)' },
    });
    clearTimeout(timeout);
    return res.ok || res.status === 301 || res.status === 302 || res.status === 403;
  } catch {
    return false;
  }
}

async function main(): Promise<void> {
  console.log('Hungarian Law MCP — Data Freshness Check');
  console.log(`Portal: ${PORTAL_NAME} (${PORTAL_URL})`);
  console.log('');

  // --- 1. Database existence ---
  if (!existsSync(DB_PATH)) {
    console.error('ERROR: Database not found at', DB_PATH);
    console.error('Run "npm run build:db" first.');
    process.exit(2);
  }

  // --- 2. Database age check ---
  let updatesNeeded = false;
  let checkError = false;
  const { default: Database } = await import('@ansvar/mcp-sqlite');
  const db = new Database(DB_PATH, { readonly: true });

  let builtAt: string | null = null;
  try {
    const row = db.prepare("SELECT value FROM db_metadata WHERE key = 'built_at'").get() as { value: string } | undefined;
    builtAt = row?.value ?? null;
  } catch {
    checkError = true;
    console.log('ERROR: db_metadata table is missing');
  }

  if (builtAt) {
    const age = daysSince(builtAt);
    if (age === null) {
      checkError = true;
      console.log('ERROR: Database built_at metadata is invalid');
    } else if (age > MAX_DB_AGE_DAYS) {
      console.log(`STALE: Database is ${age} days old (threshold: ${MAX_DB_AGE_DAYS} days)`);
      updatesNeeded = true;
    } else {
      console.log(`OK: Database is ${age} days old (threshold: ${MAX_DB_AGE_DAYS} days)`);
    }
  } else {
    checkError = true;
    console.log('ERROR: No built_at in db_metadata — cannot assess age');
  }

  // --- 3. Document and provision count check ---
  let dbDocCount = 0;
  let dbProvCount = 0;
  try {
    const docRow = db.prepare("SELECT COUNT(*) as count FROM legal_documents").get() as { count: number };
    dbDocCount = Number(docRow.count);
    console.log(`DB documents: ${dbDocCount}`);
  } catch {
    checkError = true;
    console.log('ERROR: Cannot count legal_documents');
  }

  try {
    const provRow = db.prepare("SELECT COUNT(*) as count FROM legal_provisions").get() as { count: number };
    dbProvCount = Number(provRow.count);
    console.log(`DB provisions: ${dbProvCount}`);
  } catch {
    checkError = true;
    console.log('ERROR: Cannot count legal_provisions');
  }

  if (dbDocCount < 1 || dbProvCount < 1) {
    checkError = true;
    console.log('ERROR: Database contains no legal data');
  }

  // Compare against census if available
  if (existsSync(CENSUS_PATH)) {
    try {
      const census = JSON.parse(readFileSync(CENSUS_PATH, 'utf-8')) as CensusData;
      const expectedDocuments = census.total_laws;
      const expectedProvisions = census.total_provisions;

      if (expectedDocuments === undefined) {
        checkError = true;
        console.log('ERROR: census.json has no expected document count');
      } else if (dbDocCount < expectedDocuments) {
        console.log(`MISSING: DB has ${dbDocCount} documents but census expects ${expectedDocuments}`);
        updatesNeeded = true;
      } else {
        console.log(`OK: DB documents (${dbDocCount}) >= census expected (${expectedDocuments})`);
      }

      if (expectedProvisions !== undefined && dbProvCount < expectedProvisions) {
        console.log(`MISSING: DB has ${dbProvCount} provisions but census expects ${expectedProvisions}`);
        updatesNeeded = true;
      } else if (expectedProvisions !== undefined) {
        console.log(`OK: DB provisions (${dbProvCount}) >= census expected (${expectedProvisions})`);
      }
    } catch {
      checkError = true;
      console.log('ERROR: Could not parse census.json');
    }
  } else {
    checkError = true;
    console.log('ERROR: census.json is missing');
  }

  db.close();

  // --- 4. Source portal reachability ---
  console.log('');
  console.log(`Checking portal: ${PORTAL_URL}`);
  const portalOk = await checkPortal(PORTAL_URL);
  if (portalOk) {
    console.log(`OK: ${PORTAL_NAME} is reachable`);
  } else {
    checkError = true;
    console.log(`ERROR: ${PORTAL_NAME} is unreachable`);
  }

  // --- Result ---
  console.log('');
  if (checkError) {
    console.log('RESULT: Freshness check failed');
    process.exit(2);
  } else if (updatesNeeded) {
    console.log('RESULT: Updates detected — re-ingestion recommended');
    process.exit(1);
  } else {
    console.log('RESULT: Database appears current — no updates needed');
    process.exit(0);
  }
}

main().catch((err) => {
  console.error('Unexpected error:', err);
  process.exit(2);
});

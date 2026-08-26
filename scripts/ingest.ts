#!/usr/bin/env tsx
/**
 * Hungarian Law MCP -- Ingestion Pipeline
 *
 * Fetches Hungarian legislation from the official Nemzeti Jogszabalytar portal
 * (https://njt.hu), parses section-level provisions, and writes seed JSON files.
 *
 * Usage:
 *   npm run ingest                                  # Curated corpus (10 laws)
 *   npm run ingest -- --full                        # Discover and ingest full corpus
 *   npm run ingest -- --full --in-force-only        # Full discovery for in-force laws only
 *   npm run ingest -- --full --discover-only        # Discover all laws metadata only
 *   npm run ingest -- --full --resume               # Skip already-generated seed files
 *   npm run ingest -- --skip-fetch                  # Reuse locally cached HTML where available
 */

import * as fs from 'fs';
import { parseArgs as parseCliArgs } from 'util';
import * as path from 'path';
import { fileURLToPath } from 'url';
import { requestWithRateLimit } from './lib/fetcher.js';
import { parseHungarianHtml, KEY_HUNGARIAN_ACTS, htmlToText, type ActIndexEntry, type ParsedAct } from './lib/parser.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const SOURCE_DIR = path.resolve(__dirname, '../data/source');
const SEED_DIR = path.resolve(__dirname, '../data/seed');
const BLOCK_ENDPOINT = 'https://njt.hu/ajax/njtGetBlock.json';
const SEARCH_URL_ENDPOINT = 'https://njt.hu/ajax/get_search_url.json';

const DISCOVERY_PAGE_SIZE = 50;

interface CliArgs {
  skipFetch: boolean;
  full: boolean;
  inForceOnly: boolean;
  discoverOnly: boolean;
  refreshDiscovery: boolean;
  resume: boolean;
}

interface DiscoverySeed {
  inForceOnly: boolean;
  pageSize: number;
  laws: DiscoveredLaw[];
}

interface DiscoveredLaw {
  documentId: string;
  title: string;
  titleEn?: string;
  description?: string;
  status: ActIndexEntry['status'];
  issuedDate?: string;
  inForceDate?: string;
  url: string;
}

interface IngestionRow {
  act: string;
  provisions: number;
  definitions: number;
  status: string;
}

function toMetadataOnlyAct(act: ActIndexEntry): ParsedAct {
  return {
    id: act.id,
    type: 'statute',
    title: act.title,
    title_en: act.titleEn,
    short_name: act.shortName,
    status: act.status,
    issued_date: act.issuedDate,
    in_force_date: act.inForceDate,
    url: act.url,
    description:
      act.description ??
      'Metadata-only entry: section-level text could not be extracted from public njt.hu HTML for this statute.',
    provisions: [],
    definitions: [],
  };
}

function parseArgs(): CliArgs {
  const { values } = parseCliArgs({
    options: {
      'skip-fetch': { type: 'boolean', default: false },
      full: { type: 'boolean', default: false },
      'in-force-only': { type: 'boolean', default: false },
      'discover-only': { type: 'boolean', default: false },
      'refresh-discovery': { type: 'boolean', default: false },
      resume: { type: 'boolean', default: false },
    },
  });

  return {
    skipFetch: values['skip-fetch'] ?? false,
    full: values.full ?? false,
    inForceOnly: values['in-force-only'] ?? false,
    discoverOnly: values['discover-only'] ?? false,
    refreshDiscovery: values['refresh-discovery'] ?? false,
    resume: values.resume ?? false,
  };
}

function extractNjtDocumentId(url: string): string | null {
  const match = url.match(/\/jogszabaly\/([^/?#]+)/);
  return match ? match[1] : null;
}

function extractDeferredBlockStarts(html: string): number[] {
  return [...html.matchAll(/class=\"pH borderStart\"data-show-order=\"(\d+)\"/g)]
    .map(match => Number.parseInt(match[1], 10))
    .filter(n => Number.isFinite(n))
    .sort((a, b) => a - b);
}

function extractTotalPages(html: string): number {
  const match = html.match(/id=\"page-count\">\s*\/\s*(\d+)\s*</i);
  if (!match) return 1;

  const parsed = Number.parseInt(match[1], 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 1;
}

function parseSearchResultPage(html: string): DiscoveredLaw[] {
  const chunks = html.split('<div class="resultItemWrapper">').slice(1);
  const laws: DiscoveredLaw[] = [];

  for (const chunk of chunks) {
    const mainLinkMatch = chunk.match(
      /(<a[^>]*href="jogszabaly\/([0-9]{4}-[0-9A-Z]+-00-00)"[^>]*>)([\s\S]*?)<\/a>/i
    );
    if (!mainLinkMatch) continue;

    const linkTag = mainLinkMatch[1];
    const documentId = mainLinkMatch[2];
    const shortTitle = htmlToText(mainLinkMatch[3]);
    const linkClasses = (linkTag.match(/class="([^"]*)"/i)?.[1] ?? '').toLowerCase();

    const description = htmlToText(chunk.match(/<p>([\s\S]*?)<\/p>/i)?.[1] ?? '');
    const fullTitle = description.length > 0 ? `${shortTitle} ${description}` : shortTitle;

    const titleEnRaw = chunk.match(/class=\"resultItem translation\"[^>]*title=\"([^\"]+)\"/i)?.[1];
    const titleEn = titleEnRaw ? htmlToText(titleEnRaw) : undefined;

    const dateSpan = htmlToText(chunk.match(/<span class=\"resultDate\"[^>]*>([\s\S]*?)<\/span>/i)?.[1] ?? '');
    const dateMatch = dateSpan.match(/(\d{4})\.\s*(\d{2})\.\s*(\d{2})\./);
    const inForceDate = dateMatch ? `${dateMatch[1]}-${dateMatch[2]}-${dateMatch[3]}` : undefined;

    let status: ActIndexEntry['status'] = 'amended';
    if (linkClasses.includes('now')) status = 'in_force';
    else if (linkClasses.includes('future')) status = 'not_yet_in_force';
    else if (linkClasses.includes('past')) status = 'repealed';

    laws.push({
      documentId,
      title: fullTitle,
      titleEn,
      description: description.length > 0 ? description : undefined,
      status,
      issuedDate: undefined,
      inForceDate,
      url: `https://njt.hu/jogszabaly/${documentId}`,
    });
  }

  return laws;
}

async function fetchSearchPathForLaws(inForceOnly: boolean): Promise<string> {
  const payload = {
    evszam: '',
    sorszam: '',
    author_type: '0000',
    szokereso: '',
    csak_hatalyos: inForceOnly,
    pontos_szora: false,
    csak_cimben: false,
    targyszo: false,
    gazette_state: false,
  };

  const response = await requestWithRateLimit(SEARCH_URL_ENDPOINT, {
    method: 'POST',
    headers: {
      'Accept': 'text/html,application/json,*/*',
      'Content-Type': 'application/json; charset=utf-8',
    },
    body: JSON.stringify(payload),
  });
  if (response.status !== 200) {
    throw new Error(`Search URL request failed (HTTP ${response.status})`);
  }

  let parsed: { success?: boolean; url?: string };
  try {
    parsed = JSON.parse(response.body) as { success?: boolean; url?: string };
  } catch (error) {
    throw new Error(`Search URL response was not JSON: ${String(error)}`);
  }

  if (!parsed.success || !parsed.url) {
    throw new Error('Search URL request did not return a valid path');
  }

  return parsed.url;
}

function discoveryCachePath(inForceOnly: boolean): string {
  const suffix = inForceOnly ? 'in-force' : 'all';
  return path.join(SOURCE_DIR, `law-discovery-${suffix}.json`);
}

function readDiscoveryCache(inForceOnly: boolean): DiscoveredLaw[] | null {
  const cacheFile = discoveryCachePath(inForceOnly);
  if (!fs.existsSync(cacheFile)) return null;

  try {
    const parsed = JSON.parse(fs.readFileSync(cacheFile, 'utf-8')) as DiscoverySeed;
    if (!Array.isArray(parsed.laws) || parsed.laws.length === 0) return null;
    if (parsed.inForceOnly !== inForceOnly) return null;
    if (parsed.pageSize !== DISCOVERY_PAGE_SIZE) return null;
    return parsed.laws;
  } catch {
    return null;
  }
}

async function discoverLaws(inForceOnly: boolean): Promise<DiscoveredLaw[]> {
  fs.mkdirSync(SOURCE_DIR, { recursive: true });

  const searchPath = await fetchSearchPathForLaws(inForceOnly);
  const firstUrl = `https://njt.hu/search/${searchPath}/1/${DISCOVERY_PAGE_SIZE}`;

  const firstPageResponse = await requestWithRateLimit(firstUrl);
  if (firstPageResponse.status !== 200) {
    throw new Error(`Discovery page fetch failed (HTTP ${firstPageResponse.status})`);
  }

  const totalPages = extractTotalPages(firstPageResponse.body);
  const discoveredMap = new Map<string, DiscoveredLaw>();

  for (const law of parseSearchResultPage(firstPageResponse.body)) {
    discoveredMap.set(law.documentId, law);
  }

  for (let page = 2; page <= totalPages; page++) {
    const url = `https://njt.hu/search/${searchPath}/${page}/${DISCOVERY_PAGE_SIZE}`;
    const response = await requestWithRateLimit(url);
    if (response.status !== 200) {
      throw new Error(`Discovery page ${page} failed (HTTP ${response.status})`);
    }

    for (const law of parseSearchResultPage(response.body)) {
      if (!discoveredMap.has(law.documentId)) {
        discoveredMap.set(law.documentId, law);
      }
    }

    if (page % 10 === 0 || page === totalPages) {
      console.log(`  Discovery progress: page ${page}/${totalPages} (${discoveredMap.size} laws)`);
    }
  }

  const laws = Array.from(discoveredMap.values()).sort((a, b) => a.documentId.localeCompare(b.documentId));

  const cache: DiscoverySeed = {
    inForceOnly,
    pageSize: DISCOVERY_PAGE_SIZE,
    laws,
  };

  fs.writeFileSync(discoveryCachePath(inForceOnly), `${JSON.stringify(cache, null, 2)}\n`, 'utf-8');
  return laws;
}

function buildFullCorpusActList(discovered: DiscoveredLaw[]): ActIndexEntry[] {
  const subsetOnlyIds = new Set<string>([
    'act-cxii-2011-public-data',
    'criminal-code-cybercrime',
  ]);

  // First non-subset curated act per njt doc ID wins, in KEY_HUNGARIAN_ACTS order.
  const curatedByDocId = new Map<string, ActIndexEntry>();
  for (const act of KEY_HUNGARIAN_ACTS) {
    if (subsetOnlyIds.has(act.id)) continue;
    const docId = extractNjtDocumentId(act.url);
    if (!docId || curatedByDocId.has(docId)) continue;
    curatedByDocId.set(docId, act);
  }

  const result: ActIndexEntry[] = [];

  for (const law of discovered) {
    const curatedFull = curatedByDocId.get(law.documentId);
    if (curatedFull) {
      result.push({
        ...curatedFull,
        url: law.url,
        status: law.status,
        inForceDate: law.inForceDate ?? curatedFull.inForceDate,
      });
      continue;
    }

    result.push({
      id: `hu-law-${law.documentId.toLowerCase()}`,
      title: law.title,
      titleEn: law.titleEn,
      shortName: undefined,
      status: law.status,
      issuedDate: law.issuedDate,
      inForceDate: law.inForceDate,
      url: law.url,
      description: law.description ?? 'Official Hungarian statute text from Nemzeti Jogszabalytar (njt.hu).',
    });
  }

  // Preserve curated subset aliases for compatibility with existing document IDs/tools.
  for (const act of KEY_HUNGARIAN_ACTS.filter(a => subsetOnlyIds.has(a.id))) {
    result.push(act);
  }

  return result;
}

function loadExistingSeedCounts(seedFile: string): { provisions: number; definitions: number } {
  const existing = JSON.parse(fs.readFileSync(seedFile, 'utf-8')) as ParsedAct;
  return {
    provisions: existing.provisions?.length ?? 0,
    definitions: existing.definitions?.length ?? 0,
  };
}

async function hydrateDeferredBlocks(html: string, act: ActIndexEntry, logHydration: boolean): Promise<string> {
  const starts = extractDeferredBlockStarts(html);
  if (starts.length === 0) return html;

  const documentId = extractNjtDocumentId(act.url);
  if (!documentId) return html;

  const blockRanges = starts.map((start, index) => ({
    start,
    last: index + 1 < starts.length ? starts[index + 1] : null,
  }));

  const chunkSize = 20;
  let appended = '';

  for (let i = 0; i < blockRanges.length; i += chunkSize) {
    const chunk = blockRanges.slice(i, i + chunkSize).map(range =>
      range.last === null ? { start: range.start } : { start: range.start, last: range.last }
    );

    const response = await requestWithRateLimit(BLOCK_ENDPOINT, {
      method: 'POST',
      headers: {
        'Accept': 'text/html,application/json,*/*',
        'Content-Type': 'application/json; charset=utf-8',
      },
      body: JSON.stringify({ documentId, data: chunk }),
    });

    if (response.status !== 200) {
      throw new Error(`Deferred block fetch failed for ${act.id} (HTTP ${response.status})`);
    }

    appended += `\n${response.body}`;
  }

  if (logHydration && blockRanges.length > 0) {
    console.log(`    -> hydrated ${blockRanges.length} deferred block ranges`);
  }

  return `${html}\n${appended}`;
}

function parseSourceCacheKey(act: ActIndexEntry): string {
  const documentId = extractNjtDocumentId(act.url);
  if (documentId) return documentId;
  return act.id;
}

async function fetchAndParseActs(acts: ActIndexEntry[], skipFetch: boolean, resume: boolean): Promise<void> {
  console.log(`\nProcessing ${acts.length} Hungarian statutes from njt.hu...\n`);

  fs.mkdirSync(SOURCE_DIR, { recursive: true });
  fs.mkdirSync(SEED_DIR, { recursive: true });

  let processed = 0;
  let cached = 0;
  let failed = 0;
  let totalProvisions = 0;
  let totalDefinitions = 0;
  let success = 0;

  const results: IngestionRow[] = [];
  const verbosePerAct = acts.length <= 20;
  const progress = () => `[${processed + 1}/${acts.length}]`;
  // One line per event; compact mode prefixes progress and trims detail.
  const log = (verboseMsg: string, compactMsg: string) => console.log(verbosePerAct ? verboseMsg : compactMsg);

  for (const act of acts) {
    const sourceFile = path.join(SOURCE_DIR, `${parseSourceCacheKey(act)}.html`);
    const seedFile = path.join(SEED_DIR, `${act.id}.json`);

    if (resume && fs.existsSync(seedFile)) {
      const counts = loadExistingSeedCounts(seedFile);
      totalProvisions += counts.provisions;
      totalDefinitions += counts.definitions;
      results.push({ act: act.shortName ?? act.id, provisions: counts.provisions, definitions: counts.definitions, status: 'cached' });
      cached++;
      processed++;
      continue;
    }

    try {
      let html: string;

      if (skipFetch && fs.existsSync(sourceFile)) {
        html = fs.readFileSync(sourceFile, 'utf-8');
        log(`  Using cached HTML for ${act.shortName ?? act.id}`, `  ${progress()} ${act.shortName ?? act.id} -> cached`);
      } else {
        log(`  Fetching ${act.shortName ?? act.id} (${act.url})...`, `  ${progress()} ${act.shortName ?? act.id} ...`);
        const result = await requestWithRateLimit(act.url);

        if (result.status !== 200) {
          log(` HTTP ${result.status}`, `  ${progress()} ${act.shortName ?? act.id} -> HTTP ${result.status}`);
          results.push({
            act: act.shortName ?? act.id,
            provisions: 0,
            definitions: 0,
            status: `HTTP ${result.status}`,
          });
          failed++;
          processed++;
          continue;
        }

        html = result.body;
        fs.writeFileSync(sourceFile, html);

        if (!html.includes('jogszabalyMainTitle') || !html.includes('class="jhId"')) {
          const metadataOnly = toMetadataOnlyAct(act);
          fs.writeFileSync(seedFile, `${JSON.stringify(metadataOnly, null, 2)}\n`);

          log(' NO_SECTION_CONTENT -> METADATA_ONLY', `  ${progress()} ${act.shortName ?? act.id} -> METADATA_ONLY (NO_SECTION_CONTENT)`);
          results.push({
            act: act.shortName ?? act.id,
            provisions: 0,
            definitions: 0,
            status: 'METADATA_ONLY',
          });
          processed++;
          continue;
        }

        log(` OK (${(html.length / 1024).toFixed(0)} KB)`, '');
      }

      const hydratedHtml = await hydrateDeferredBlocks(html, act, verbosePerAct);
      const parsed = parseHungarianHtml(hydratedHtml, act);

      if (parsed.provisions.length === 0) {
        const metadataOnly = toMetadataOnlyAct({
          ...act,
          title: parsed.title,
        });
        fs.writeFileSync(seedFile, `${JSON.stringify(metadataOnly, null, 2)}\n`);

        log('    -> 0 provisions extracted, stored as METADATA_ONLY', `  ${progress()} ${act.shortName ?? act.id} -> METADATA_ONLY (NO_SECTION_CONTENT)`);

        results.push({
          act: act.shortName ?? act.id,
          provisions: 0,
          definitions: 0,
          status: 'METADATA_ONLY',
        });
        processed++;
        continue;
      }

      fs.writeFileSync(seedFile, `${JSON.stringify(parsed, null, 2)}\n`);

      totalProvisions += parsed.provisions.length;
      totalDefinitions += parsed.definitions.length;
      success++;

      results.push({
        act: act.shortName ?? act.id,
        provisions: parsed.provisions.length,
        definitions: parsed.definitions.length,
        status: 'OK',
      });

      if (verbosePerAct || (processed + 1) % 25 === 0) {
        log(
          `    -> ${parsed.provisions.length} provisions, ${parsed.definitions.length} definitions extracted`,
          `  ${progress()} ok=${success} failed=${failed} cached=${cached} provisions=${totalProvisions} defs=${totalDefinitions}`
        );
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      log(`  ERROR ${act.shortName ?? act.id}: ${message}`, `  ${progress()} ${act.shortName ?? act.id} -> ERROR: ${message.substring(0, 120)}`);
      results.push({
        act: act.shortName ?? act.id,
        provisions: 0,
        definitions: 0,
        status: `ERROR: ${message.substring(0, 80)}`,
      });
      failed++;
    }

    processed++;
  }

  console.log(`\n${'='.repeat(72)}`);
  console.log('Ingestion Report');
  console.log('='.repeat(72));
  console.log('\n  Source:       https://njt.hu');
  console.log('  Authority:    Nemzeti Jogszabalytar / Magyar Kozlony');
  console.log(`  Processed:    ${processed}`);
  console.log(`  Cached:       ${cached}`);
  console.log(`  Failed:       ${failed}`);
  console.log(`  Provisions:   ${totalProvisions}`);
  console.log(`  Definitions:  ${totalDefinitions}`);

  if (results.length <= 20) {
    console.log('\n  Per-Act breakdown:');
    console.log(`  ${'Act'.padEnd(32)} ${'Provisions'.padStart(12)} ${'Definitions'.padStart(13)} ${'Status'.padStart(16)}`);
    console.log(`  ${'-'.repeat(32)} ${'-'.repeat(12)} ${'-'.repeat(13)} ${'-'.repeat(16)}`);

    for (const result of results) {
      console.log(
        `  ${result.act.padEnd(32)} ${String(result.provisions).padStart(12)} ${String(result.definitions).padStart(13)} ${result.status.padStart(16)}`
      );
    }
  } else {
    const metadataOnlyRows = results.filter(r => r.status === 'METADATA_ONLY');
    const errorRows = results.filter(r => r.status !== 'OK' && r.status !== 'cached' && r.status !== 'METADATA_ONLY');
    console.log(`  Window summary: ${success} OK, ${cached} cached, ${metadataOnlyRows.length} metadata-only, ${failed} failed/skipped`);
    if (metadataOnlyRows.length > 0) {
      console.log(`  Metadata-only entries in this window: ${metadataOnlyRows.length}`);
    }
    if (errorRows.length > 0) {
      console.log('  Non-OK entries in this window:');
      for (const row of errorRows.slice(0, 10)) {
        console.log(`    - ${row.act}: ${row.status}`);
      }
      if (errorRows.length > 10) {
        console.log(`    ... and ${errorRows.length - 10} more`);
      }
    }
  }
  console.log('');
}

async function main(): Promise<void> {
  const args = parseArgs();

  console.log('Hungarian Law MCP -- Ingestion Pipeline');
  console.log('======================================\n');
  console.log('  Source: https://njt.hu (official Hungarian legal portal)');
  console.log('  Parse target: section-level text (szakasz, "§")');
  console.log('  Rate limit: 1200ms/request');
  console.log(`  Mode: ${args.full ? 'full corpus discovery' : 'curated corpus'}`);

  if (args.full) {
    console.log(`  In-force only: ${args.inForceOnly ? 'yes' : 'no'}`);
  }

  if (args.skipFetch) console.log('  --skip-fetch');
  if (args.resume) console.log('  --resume');
  if (args.discoverOnly) console.log('  --discover-only');
  if (args.refreshDiscovery) console.log('  --refresh-discovery');

  let acts: ActIndexEntry[];

  if (args.full) {
    let discovered = !args.refreshDiscovery
      ? readDiscoveryCache(args.inForceOnly)
      : null;

    if (!discovered) {
      console.log('\nDiscovering laws from njt.hu search index...');
      discovered = await discoverLaws(args.inForceOnly);
    } else {
      console.log(`\nLoaded discovery cache (${discovered.length} laws): ${discoveryCachePath(args.inForceOnly)}`);
    }

    acts = buildFullCorpusActList(discovered);

    console.log(`  Discovered laws: ${discovered.length}`);
    console.log(`  Ingestion act list: ${acts.length} (includes compatibility aliases where needed)`);
    console.log(`  Discovery cache: ${discoveryCachePath(args.inForceOnly)}`);
  } else {
    acts = [...KEY_HUNGARIAN_ACTS];
  }

  if (args.discoverOnly) {
    console.log(`\nDiscovery-only run completed. Selected acts for ingestion would be: ${acts.length}`);
    return;
  }

  await fetchAndParseActs(acts, args.skipFetch, args.resume);
}

main().catch(error => {
  console.error('Fatal ingestion error:', error);
  process.exit(1);
});

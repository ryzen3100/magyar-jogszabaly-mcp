#!/usr/bin/env node
/**
 * Parity harness — proves the Go port answers identically to the TypeScript
 * server over stdio MCP, against the same data/database.db.
 *
 * Spawns both servers, speaks newline-delimited JSON-RPC (initialize →
 * notifications/initialized → tools/list → one tools/call per case in
 * cases.json), extracts result.content[0].text from each response and diffs
 * TS vs Go per case.
 *
 * Usage:
 *   node tools/parity/parity.mjs --go-cmd /tmp/hlm-go
 *   node tools/parity/parity.mjs --go-cmd "go run ./cmd/hungarian-law-mcp" --filter citation --show-diff
 *
 * Flags:
 *   --ts-cmd <cmd>     TS server command (default: "node --import tsx src/index.ts",
 *                      run with cwd = repo root)
 *   --go-cmd <cmd>     Go server command or prebuilt binary path (required)
 *   --filter <substr>  only run cases whose name contains <substr>
 *   --show-diff        print the differing payloads for failing cases
 *   --no-normalize-freshness  keep `freshness` keys when comparing (default ON:
 *                      freshness keys are dropped, since they are wall-clock
 *                      derived when db_metadata lacks built_at)
 *   --timeout-ms <n>   per-request timeout (default 120000)
 *
 * Both servers run with HUNGARIAN_LAW_DB_PATH pinned to <repo>/data/database.db
 * so `about` (fingerprint + built_at) is byte-comparable.
 *
 * Comparison: payload texts are parsed as JSON and compared recursively —
 * numbers with an absolute tolerance of 1e-9 (bm25 float formatting may
 * differ in the last digits), everything else exactly. Texts that do not
 * parse as JSON (error strings) are compared exactly. `isError` is compared
 * too. Cases marked "xfail" in cases.json document known acceptable
 * differences: failing them reports XFAIL instead of failing the run.
 */

import { spawn } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = join(HERE, '..', '..');
const PROTOCOL_VERSION = '2025-06-18';
const NUMERIC_TOLERANCE = 1e-9;

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------

function parseArgs(argv) {
  const opts = {
    tsCmd: ['node', '--import', 'tsx', 'src/index.ts'],
    goCmd: null,
    filter: null,
    showDiff: false,
    normalizeFreshness: true,
    timeoutMs: 120_000,
  };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    const next = () => {
      if (i + 1 >= argv.length) throw new Error(`missing value for ${arg}`);
      return argv[++i];
    };
    switch (arg) {
      case '--ts-cmd': opts.tsCmd = next().split(/\s+/); break;
      case '--go-cmd': opts.goCmd = next().split(/\s+/); break;
      case '--filter': opts.filter = next(); break;
      case '--show-diff': opts.showDiff = true; break;
      case '--normalize-freshness': opts.normalizeFreshness = true; break;
      case '--no-normalize-freshness': opts.normalizeFreshness = false; break;
      case '--timeout-ms': opts.timeoutMs = Number(next()); break;
      case '--help': case '-h':
        console.log('see the header comment of tools/parity/parity.mjs');
        process.exit(0);
      default:
        throw new Error(`unknown flag: ${arg}`);
    }
  }
  if (!opts.goCmd) throw new Error('--go-cmd is required (command string or prebuilt binary path)');
  return opts;
}

// ---------------------------------------------------------------------------
// Minimal MCP stdio client
// ---------------------------------------------------------------------------

class McpClient {
  constructor(label, cmd, cwd, env, timeoutMs) {
    this.label = label;
    this.cmd = cmd;
    this.cwd = cwd;
    this.env = env;
    this.timeoutMs = timeoutMs;
    this.child = null;
    this.nextId = 1;
    this.pending = new Map(); // id -> {resolve, reject, timer}
    this.lineBuf = '';
    this.exited = null; // rejection for requests racing process exit
  }

  start() {
    this.child = spawn(this.cmd[0], this.cmd.slice(1), {
      cwd: this.cwd,
      env: { ...process.env, ...this.env },
      stdio: ['pipe', 'pipe', 'inherit'],
    });
    this.child.stdout.setEncoding('utf8');
    this.child.stdout.on('data', (chunk) => this.onData(chunk));
    this.child.on('exit', (code, signal) => {
      this.exited = new Error(
        `${this.label} server exited (code=${code} signal=${signal})`,
      );
      for (const p of this.pending.values()) p.reject(this.exited);
      this.pending.clear();
    });
    this.child.on('error', (err) => {
      const e = new Error(`${this.label} server spawn failed: ${err.message}`);
      for (const p of this.pending.values()) p.reject(e);
      this.pending.clear();
    });
  }

  onData(chunk) {
    this.lineBuf += chunk;
    let idx;
    while ((idx = this.lineBuf.indexOf('\n')) >= 0) {
      const line = this.lineBuf.slice(0, idx).trim();
      this.lineBuf = this.lineBuf.slice(idx + 1);
      if (!line) continue;
      let msg;
      try {
        msg = JSON.parse(line);
      } catch {
        continue; // not JSON-RPC; ignore
      }
      if (msg && msg.id !== undefined && msg.id !== null && this.pending.has(msg.id)) {
        const p = this.pending.get(msg.id);
        this.pending.delete(msg.id);
        clearTimeout(p.timer);
        p.resolve(msg);
      }
      // notifications and responses to unknown ids are ignored
    }
  }

  send(msg) {
    this.child.stdin.write(JSON.stringify(msg) + '\n');
  }

  request(method, params) {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      if (this.exited) return reject(this.exited);
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`${this.label} timeout after ${this.timeoutMs}ms on ${method}`));
      }, this.timeoutMs);
      this.pending.set(id, { resolve, reject, timer });
      this.send({ jsonrpc: '2.0', id, method, params });
    });
  }

  notify(method, params) {
    this.send({ jsonrpc: '2.0', method, params });
  }

  async stop() {
    if (!this.child || this.child.exitCode !== null) return;
    this.child.stdin.end(); // closing stdin ends the stdio session cleanly
    await new Promise((resolve) => {
      const t = setTimeout(() => {
        this.child.kill('SIGKILL');
        resolve();
      }, 3000);
      this.child.once('exit', () => { clearTimeout(t); resolve(); });
    });
  }
}

// ---------------------------------------------------------------------------
// Response capture and comparison
// ---------------------------------------------------------------------------

/** Reduce a JSON-RPC response to the comparable shape. */
function capture(msg) {
  if (msg.error !== undefined) {
    return {
      isError: true,
      text: `__jsonrpc_error__ ${msg.error.code}: ${msg.error.message}`,
    };
  }
  const result = msg.result ?? {};
  const block = Array.isArray(result.content) ? result.content[0] : undefined;
  return { isError: result.isError === true, text: block?.text ?? null };
}

function dropFreshness(node) {
  if (Array.isArray(node)) {
    for (const item of node) dropFreshness(item);
  } else if (node && typeof node === 'object') {
    delete node.freshness;
    for (const value of Object.values(node)) dropFreshness(value);
  }
}

function tryParseJson(text) {
  if (typeof text !== 'string') return undefined;
  try {
    return JSON.parse(text);
  } catch {
    return undefined; // not JSON — caller falls back to exact string compare
  }
}

function deepEqual(a, b) {
  if (typeof a === 'number' && typeof b === 'number') {
    return Math.abs(a - b) <= NUMERIC_TOLERANCE;
  }
  if (a === null || b === null || typeof a !== 'object' || typeof b !== 'object') {
    return a === b;
  }
  if (Array.isArray(a) || Array.isArray(b)) {
    return (
      Array.isArray(a) === Array.isArray(b) &&
      a.length === b.length &&
      a.every((v, i) => deepEqual(v, b[i]))
    );
  }
  const ka = Object.keys(a);
  const kb = Object.keys(b);
  if (ka.length !== kb.length) return false;
  return ka.every((k) => Object.hasOwn(b, k) && deepEqual(a[k], b[k]));
}

/** First differing JSON path between two values, for diagnostics. */
function firstDiffPath(a, b, path = '$') {
  if (deepEqual(a, b)) return null;
  if (
    a && b && typeof a === 'object' && typeof b === 'object' &&
    !Array.isArray(a) && !Array.isArray(b)
  ) {
    for (const k of new Set([...Object.keys(a), ...Object.keys(b)])) {
      const hit = firstDiffPath(a?.[k], b?.[k], `${path}.${k}`);
      if (hit) return hit;
    }
  }
  if (Array.isArray(a) && Array.isArray(b)) {
    for (let i = 0; i < Math.max(a.length, b.length); i++) {
      const hit = firstDiffPath(a[i], b[i], `${path}[${i}]`);
      if (hit) return hit;
    }
  }
  return path;
}

function truncate(str, max = 2500) {
  return str.length <= max ? str : str.slice(0, max) + ` … [+${str.length - max} chars]`;
}

/**
 * Compare two captured payloads. Returns null when equal, else a
 * human-readable diff description.
 */
function comparePayloads(ts, go, normalizeFreshness) {
  if (ts.isError !== go.isError) {
    return `isError differs: ts=${ts.isError} go=${go.isError}`;
  }
  if (ts.text === go.text) return null;

  const tsJson = tryParseJson(ts.text);
  const goJson = tryParseJson(go.text);
  if (tsJson === undefined || goJson === undefined) {
    return textsDiffer(ts.text, go.text, 'texts differ (non-JSON)');
  }
  if (normalizeFreshness) {
    dropFreshness(tsJson);
    dropFreshness(goJson);
  }
  if (deepEqual(tsJson, goJson)) {
    return null;
  }
  const path = firstDiffPath(tsJson, goJson);
  return textsDiffer(ts.text, go.text, `JSON differs at ${path}`);
}

function textsDiffer(tsText, goText, reason) {
  return (
    `${reason}\n` +
    `  TS: ${truncate(tsText ?? 'null')}\n` +
    `  GO: ${truncate(goText ?? 'null')}`
  );
}

// ---------------------------------------------------------------------------
// Driver
// ---------------------------------------------------------------------------

async function runServer(label, cmd, cases, opts) {
  const dbPath = join(REPO_ROOT, 'data', 'database.db');
  const env = existsSync(dbPath) ? { HUNGARIAN_LAW_DB_PATH: dbPath } : {};
  const client = new McpClient(label, cmd, REPO_ROOT, env, opts.timeoutMs);
  client.start();
  try {
    await client.request('initialize', {
      protocolVersion: PROTOCOL_VERSION,
      capabilities: {},
      clientInfo: { name: 'parity-harness', version: '1.0.0' },
    });
    client.notify('notifications/initialized', {});

    const listed = await client.request('tools/list', {});
    const toolNames = (listed.result?.tools ?? []).map((t) => t.name);

    const results = [];
    for (const c of cases) {
      const response = await client.request('tools/call', {
        name: c.tool,
        arguments: c.arguments,
      });
      results.push({ name: c.name, captured: capture(response) });
    }
    return { toolNames, results };
  } finally {
    await client.stop();
  }
}

async function main() {
  const opts = parseArgs(process.argv.slice(2));
  const allCases = JSON.parse(readFileSync(join(HERE, 'cases.json'), 'utf8'));
  const cases = opts.filter
    ? allCases.filter((c) => c.name.includes(opts.filter))
    : allCases;
  if (cases.length === 0) {
    console.error(`no cases match filter: ${opts.filter}`);
    process.exit(2);
  }

  console.log(`Parity harness: ${cases.length} case(s)`);
  console.log(`  TS: ${opts.tsCmd.join(' ')}`);
  console.log(`  GO: ${opts.goCmd.join(' ')}`);

  console.log('\nRunning TS server…');
  const ts = await runServer('TS', opts.tsCmd, cases, opts);
  console.log(`  tools/list: ${ts.toolNames.length} tools`);

  console.log('Running Go server…');
  const go = await runServer('GO', opts.goCmd, cases, opts);
  console.log(`  tools/list: ${go.toolNames.length} tools`);

  // tools/list parity — compared as a SET: the Go SDK returns tools sorted
  // by name (mcp.featureSet.all() always sorts), while the TS server uses
  // registration order. Order carries no MCP semantics; membership does.
  const tsSet = [...ts.toolNames].sort();
  const goSet = [...go.toolNames].sort();
  const listOk = JSON.stringify(tsSet) === JSON.stringify(goSet);
  const orderOk = JSON.stringify(ts.toolNames) === JSON.stringify(go.toolNames);
  if (!listOk) {
    const onlyTs = ts.toolNames.filter((n) => !go.toolNames.includes(n));
    const onlyGo = go.toolNames.filter((n) => !ts.toolNames.includes(n));
    console.error(
      `\nFAIL tools/list: TS=${JSON.stringify(ts.toolNames)}\n` +
      `                GO=${JSON.stringify(go.toolNames)}` +
      (onlyTs.length ? `\n  only in TS: ${onlyTs.join(', ')}` : '') +
      (onlyGo.length ? `\n  only in GO: ${onlyGo.join(', ')}` : ''),
    );
  } else if (!orderOk) {
    console.log(
      '\nINFO tools/list: same 13 tools in both servers; order differs ' +
      '(TS: registration order, Go SDK: sorted by name) — not a semantic diff',
    );
  }

  console.log('\nPer-case results:');
  let pass = 0;
  let fail = 0;
  let xfail = 0;
  for (let i = 0; i < cases.length; i++) {
    const c = cases[i];
    const tsR = ts.results[i]?.captured;
    const goR = go.results[i]?.captured;
    if (!tsR || !goR) {
      fail++;
      console.log(`  FAIL ${c.name} (missing response: ts=${!!tsR} go=${!!goR})`);
      continue;
    }
    const diff = comparePayloads(tsR, goR, opts.normalizeFreshness);
    if (diff === null) {
      pass++;
      console.log(`  PASS ${c.name}`);
    } else if (c.xfail) {
      xfail++;
      console.log(`  XFAIL ${c.name} — ${c.xfail}`);
      if (opts.showDiff) console.log(indent(diff, '    '));
    } else {
      fail++;
      console.log(`  FAIL ${c.name}`);
      if (opts.showDiff) console.log(indent(diff, '    '));
    }
  }

  console.log(
    `\nSummary: ${pass} pass, ${fail} fail, ${xfail} xfail ` +
    `(tools/list ${listOk ? 'OK' : 'DIFFERS'}), ` +
    `${cases.length} case(s), freshness normalization ${opts.normalizeFreshness ? 'ON' : 'OFF'}`,
  );
  process.exit(fail > 0 || !listOk ? 1 : 0);
}

function indent(text, pad) {
  return text.split('\n').map((l) => pad + l).join('\n');
}

main().catch((err) => {
  console.error('parity harness error:', err);
  process.exit(2);
});

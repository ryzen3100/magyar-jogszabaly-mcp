# Agent instructions

## Project shape

- This is a strict TypeScript ESM MCP server (Node `>=18`); `.js` extensions in TypeScript imports are intentional for NodeNext.
- Shared MCP tool behavior lives in `src/tools/registry.ts`; `src/index.ts` is the stdio entrypoint and `src/http-server.ts` is the Streamable HTTP/Docker entrypoint.
- `data/seed/*.json` and `data/eu-mappings.json` are the database inputs. `npm run build:db` deletes and recreates `data/database.db`; treat the database as generated, not hand-edited.

## Commands

- Install with `npm ci`.
- `npm run lint` is `tsc --noEmit` (there is no separate ESLint step).
- Run all unit tests with `npm test`; focus a file/test with `npm test -- tests/tools/registry.test.ts -t "test name"`.
- `npm run test:contract` runs the golden contract suite. DB-backed tests skip when `data/database.db` is missing or lacks the required schema, so a green run may only cover in-memory tests; build a usable DB before relying on those results.
- `npm run validate` runs lint, unit tests, and contract tests. CI then runs `npm run build`; use `npm run validate && npm run build` for the local equivalent. `npm run test:coverage` enforces 100% statements, branches, functions, and lines.
- For stdio development use `npm run dev`; after building use `node dist/src/index.js`. `npm start` and the package bin both point at `dist/src/index.js`.
- For HTTP development use `node --import tsx src/http-server.ts`, or `node dist/src/http-server.js` after building. `PORT` defaults to `3000`; override the database with `HUNGARIAN_LAW_DB_PATH`.
- `docker compose up` pulls the published GHCR image; it does not run the local source. Use the `Dockerfile` when testing local Docker changes.

## Data updates

- `scripts/ingest.ts` is a networked, rate-limited scraper for official `njt.hu` data. `--full` discovers the corpus, `--resume` reuses existing seed files, `--refresh-discovery` refreshes the discovery cache, and `--skip-fetch` reuses `data/source` HTML.
- Run ingestion only on a data-update branch, inspect `data/seed`, `data/census.json`, and `sources.yml`, then run `npm run build:db`, lint, unit tests, contract tests, and `npm run check-updates`. Ingestion does not update the census or source metadata automatically; never fabricate text when the source is metadata-only.
- `npm run check-updates` needs a usable local database and network access; exit `0` means current, `1` means updates detected, and `2` means the check failed. Do not run ingestion from the production container or through MCP tools.

## Workflow conventions

- Follow `CONTRIBUTING.md`: branch from `dev`, do not push directly to `main`, and target PRs at `dev`.
- Keep SQL parameterized. Reuse the existing FTS/query and document-ID helpers rather than constructing ad-hoc search or identifier parsing.

# Ponytail, lazy senior dev mode

You are a lazy senior developer. Lazy means efficient, not careless. The best code is the code never written.

Before writing any code, stop at the first rung that holds:

1. Does this need to be built at all? (YAGNI)
2. Does it already exist in this codebase? Reuse the helper, util, or pattern that's already here, don't re-write it.
3. Does the standard library already do this? Use it.
4. Does a native platform feature cover it? Use it.
5. Does an already-installed dependency solve it? Use it.
6. Can this be one line? Make it one line.
7. Only then: write the minimum code that works.

The ladder runs after you understand the problem, not instead of it: read the task and the code it touches, trace the real flow end to end, then climb.

Bug fix = root cause, not symptom: a report names a symptom. Grep every caller of the function you touch and fix the shared function once — one guard there is a smaller diff than one per caller, and patching only the path the ticket names leaves a sibling caller still broken.

Rules:

- No abstractions that weren't explicitly requested.
- No new dependency if it can be avoided.
- No boilerplate nobody asked for.
- Deletion over addition. Boring over clever. Fewest files possible.
- Shortest working diff wins, but only once you understand the problem. The smallest change in the wrong place isn't lazy, it's a second bug.
- Question complex requests: "Do you actually need X, or does Y cover it?"
- Pick the edge-case-correct option when two stdlib approaches are the same size, lazy means less code, not the flimsier algorithm.
- Mark deliberate simplifications that cut a real corner with a known ceiling (global lock, O(n²) scan, naive heuristic) with a `ponytail:` comment naming the ceiling and upgrade path.

Not lazy about: understanding the problem (read it fully and trace the real flow before picking a rung, a small diff you don't understand is just laziness dressed up as efficiency), input validation at trust boundaries, error handling that prevents data loss, security, accessibility, the calibration real hardware needs (the platform is never the spec ideal, a clock drifts, a sensor reads off), anything explicitly requested. Lazy code without its check is unfinished: non-trivial logic leaves ONE runnable check behind, the smallest thing that fails if the logic breaks (an assert-based demo/self-check or one small test file; no frameworks, no fixtures). Trivial one-liners need no test.

(Yes, this file also applies to agents working on the ponytail repo itself. Especially to them.)

<!-- codebase-memory-mcp:start -->
# Codebase Memory

## Codebase Knowledge Graph (codebase-memory-mcp)

This project uses codebase-memory-mcp to maintain a knowledge graph of the codebase.
ALWAYS prefer MCP graph tools over grep/glob/file-search for code discovery.

### Priority Order
1. `search_graph` — find functions, classes, routes, variables by pattern
2. `trace_path` — trace who calls a function or what it calls
3. `get_code_snippet` — read specific function/class source code
4. `check_index_coverage` — validate candidate paths and missed ranges before claims
5. `query_graph` — run Cypher queries for complex patterns
6. `get_architecture` — high-level project summary

### Evidence tiers
- **Scout (Tier 1):** quick positive lookup with few calls and targeted source checks. Mark it provisional; do not make negative or exhaustive claims.
- **Verify (Tier 2, default):** task-directed graph evidence, relevant trace directions, exact snippets for material claims, and relevant pagination.
- **Auditor (Tier 3):** bounded-scope full verification with current generation, complete relevant pagination, both call directions and broader relationships when material, and every limitation disclosed.
- After candidate paths are known in any tier, call `check_index_coverage` once with every evidence path. Add relevant scopes for negative or exhaustive claims. A clean result means no recorded gap, not proof of completeness. For partial, skipped, excluded, stale, pending, or unknown coverage, read/grep the reported ranges or scope before relying on graph results.

### When to fall back to grep/glob
- Searching for string literals, error messages, config values
- Searching non-code files (Dockerfiles, shell scripts, configs)
- When MCP tools return insufficient results

### Examples
- Find a handler: `search_graph(name_pattern=".*OrderHandler.*")`
- Who calls it: `trace_path(function_name="OrderHandler", direction="inbound")`
- Read source: `get_code_snippet(qualified_name="pkg/orders.OrderHandler")`

### Session resets and subagents
- At session start or after compaction, confirm the nearest graph project and generation with `list_projects` or `index_status`, then choose Scout, Verify, or Auditor.
- Before spawning a subagent, query the graph and coverage in the parent. Pass the tier, project, generation/freshness, bounded scope, queries and pagination state, qualified symbols, paths, call-chain findings, coverage evidence with ranges/reasons, source fallback already performed, and unresolved questions in the delegated task context.
- Do not assume subagents inherit MCP access or the parent conversation. If a child lacks MCP tools, it must not call or claim MCP access. It should use the supplied evidence and read/grep exact source, especially every reported missed-coverage range.
<!-- codebase-memory-mcp:end -->

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
- For stdio development use `npm run dev`; after building use `node dist/src/index.js`. The current `npm start`/package bin points at `dist/index.js`, which `tsc` does not emit.
- For HTTP development use `node --import tsx src/http-server.ts`, or `node dist/src/http-server.js` after building. `PORT` defaults to `3000`; override the database with `HUNGARIAN_LAW_DB_PATH`.
- `docker compose up` pulls the published GHCR image; it does not run the local source. Use the `Dockerfile` when testing local Docker changes.

## Data updates

- `scripts/ingest.ts` is a networked, rate-limited scraper for official `njt.hu` data. `--full` discovers the corpus, `--resume` reuses existing seed files, `--refresh-discovery` refreshes the discovery cache, and `--skip-fetch` reuses `data/source` HTML.
- Run ingestion only on a data-update branch, inspect `data/seed`, `data/census.json`, and `sources.yml`, then run `npm run build:db`, lint, unit tests, contract tests, and `npm run check-updates`. Ingestion does not update the census or source metadata automatically; never fabricate text when the source is metadata-only.
- `npm run check-updates` needs a usable local database and network access; exit `0` means current, `1` means updates detected, and `2` means the check failed. Do not run ingestion from the production container or through MCP tools.

## Workflow conventions

- Follow `CONTRIBUTING.md`: branch from `dev`, do not push directly to `main`, and target PRs at `dev`.
- Keep SQL parameterized. Reuse the existing FTS/query and document-ID helpers rather than constructing ad-hoc search or identifier parsing.

# Agent instructions

## Project overview

MCP (Model Context Protocol) server exposing Hungarian legislation — 4,300+ statutes
and 130k+ provisions from the official `njt.hu` / Magyar Közlöny corpus — to AI clients
(Claude, Cursor, etc.). Go port (v2) of an Ansvar Systems TypeScript server; the fork
adds a Hungarian citation parser (`"2012. évi I. törvény 116. §"`, `6:272. §` style),
normalized Hungarian output, and EU directive/regulation cross-reference data.

- Module: `github.com/ryzen3100/magyar-jogszabaly-mcp/v2`, Go 1.26, Apache-2.0.
- Pure Go build (`CGO_ENABLED=0`); SQLite via `modernc.org/sqlite` (no cgo).
- Key deps: `github.com/modelcontextprotocol/go-sdk` (MCP SDK),
  `github.com/google/jsonschema-go` (tool input schemas), FTS5 for full-text search.
- `README.md` is written in Hungarian; code comments, `TOOLS.md`, and `CONTRIBUTING.md`
  are in English. Write new code comments and commit messages in English.

## Commands

- Build: `go build ./...` — vet: `go vet ./...`, tests: `go test ./...` (stdlib `testing`).
  All three must pass before committing (`CONTRIBUTING.md` requirement). CI also runs
  `govulncheck ./...`.
- Focus a package/test: `go test ./internal/tools -run TestName`.
- DB-backed tests skip when `data/database.db` is missing or lacks the required schema,
  so a green run may only cover in-memory tests; run `go run ./cmd/build-db` first if you
  need those results to be meaningful. (`data/database.db` is gitignored and generated.)
- Server: `go run ./cmd/hungarian-law-mcp` (stdio MCP) or
  `go run ./cmd/hungarian-law-mcp serve` (Streamable HTTP). `PORT` defaults to `3000`,
  `HOST` to `127.0.0.1`. The DB path defaults to `data/database.db` resolved relative to
  the executable dir — since `go run` compiles into a temp dir, set
  `HUNGARIAN_LAW_DB_PATH=data/database.db` for dev runs.
- Lint GitHub Actions workflows with `actionlint` (install once:
  `go install github.com/rhysd/actionlint/cmd/actionlint@latest`); run it after editing
  anything in `.github/workflows/`.
- TS↔Go parity harness (historical, for the port): `node tools/parity/parity.mjs`
  (TS side runs from the pre-port tree on the `dev` branch) and
  `python3 tools/parity/compare_db.py` for DB logical parity.
- HTTP smoke test: `python3 scripts/http-smoke.py http://127.0.0.1:<port>`.

## Architecture

Single binary, four commands under `cmd/`:

- `hungarian-law-mcp` — the server; `serve` subcommand switches stdio → Streamable HTTP.
- `build-db` — rebuilds `data/database.db` from `data/seed/*.json` + `data/eu-mappings.json`.
  Writes a temp file and atomically renames over the DB on success. Treat the database as
  generated, never hand-edited.
- `ingest` — networked, rate-limited scraper for `njt.hu` (see Data updates below).
- `check-updates` — freshness check; needs a local DB and network. Exit codes:
  `0` current, `1` updates detected, `2` check failed.

Shared logic in `internal/` (no `src/`):

- `server` — stdio and Streamable HTTP entrypoints, prompts, resources. Session header
  validated against a UUIDv4 regex before use.
- `tools` — one file per MCP tool plus `registry.go` (tool registry shared by both
  transports). 13 tools: 8 core (`search_legislation`, `get_provision`, `list_sources`,
  `validate_citation`, `build_legal_stance`, `format_citation`, `check_currency`, `about`)
  and 5 EU-related (`get_eu_basis`, `get_hungarian_implementations`,
  `search_eu_implementations`, `get_provision_eu_basis`, `validate_eu_compliance`).
  Parameter contracts are documented in `TOOLS.md`.
- `store` — SQLite access layer, metadata, capabilities; `storetest` holds test helpers.
- `fts` — FTS5 query construction; `statute` — Hungarian statute/section reference parsing.
- `builddb` — DB schema and build pipeline incl. EU reference extraction (`eurefs.go`);
  `ingest` — njt.hu discovery/fetch/parse; `seed` — seed-file access.

Data inputs: `data/seed/*.json` (one JSON per statute, from njt.hu HTML),
`data/census.json` (corpus census), `data/eu-mappings.json` (EU cross-references),
`sources.yml` (provenance metadata served by `list_sources`).

## Data updates

- `go run ./cmd/ingest` scrapes official njt.hu data. Flags: `-full` (discover the whole
  corpus instead of curated acts), `-resume` (reuse existing seed files),
  `-refresh-discovery` (bypass the discovery cache), `-skip-fetch` (reuse cached HTML in
  `data/source`), `-discover-only`, `-in-force-only`, `-base-url`, `-data-dir`.
- Run ingestion only on a dedicated data-update branch (e.g. `data/update-YYYY-MM-DD`),
  inspect `data/seed`, `data/census.json`, and `sources.yml`, then run
  `go run ./cmd/build-db`, `go vet ./...`, `go test ./...`, and `go run ./cmd/check-updates`.
  Ingestion does not update the census or source metadata automatically — review and edit
  them by hand. Never fabricate statute text when the source is metadata-only (a few
  legacy acts are stored as metadata-only records by design).
- Do not run ingestion from the production container or through MCP tools.

## Build, CI/CD, and deployment

- CI (`.github/workflows/ci.yml`, on `main`/`dev`/`go-port`): vet → test → govulncheck → build.
- `docker-publish.yml` (pushes to `main` and `v*.*.*` tags): builds the DB from committed
  seed data first (the DB is gitignored), builds the Docker image, runs a container smoke
  test (`/health`, `/mcp`, `scripts/http-smoke.py`, plus a DB sanity check via python3
  sqlite3), then pushes to `ghcr.io/ryzen3100/magyar-jogszabaly-mcp`.
- `check-updates.yml`: daily cron builds a DB from seed data, runs `check-updates`, and
  opens/updates/closes a `data-update` issue as needed.
- Other workflows: `publish.yml` (tag builds, gates release), `scorecard.yml`, `semgrep.yml`,
  `trivy.yml` (security scanning). Dependabot is enabled.
- Deployment target is a LAN-only host via `docker compose up` (compose pulls the published
  GHCR image — it does not run local source; use the `Dockerfile` for local Docker testing).
  The image bundles the DB and an entrypoint that installs it into the `/data` volume with
  checksum verification. The server has no in-process auth; exposure is bounded to the LAN.

## Workflow conventions

- Branch from `dev`, never push directly to `main`; PRs target `dev`. `main` receives
  merges from `dev` only. CI also runs on the `go-port` branch (legacy of the port).
- Code style: `gofmt`-clean and `go vet`-clean; modern Go 1.26 idioms. Every MCP tool
  declares a JSON-Schema `inputSchema` with field descriptions. Keep SQL parameterized;
  reuse the existing FTS/query and document-ID helpers in `internal/fts` and `internal/statute`
  rather than constructing ad-hoc search or identifier parsing.
- Audit/review docs live at repo root (`AUDIT_GOLANG_SKILLS.md`); check it before
  re-doing a known review.

## Security and data-integrity considerations

- All SQL must use parameterized statements (FTS input is escaped through `internal/fts`
  helpers, never string-concatenated).
- Legal text comes only from official sources (`njt.hu`, Magyar Közlöny); EU metadata from
  EUR-Lex is metadata-only. Never invent or approximate statute wording.
- Citations for professional use must be verifiable against the DB (`validate_citation` is
  a zero-hallucination check) — preserve that property in any new tool.
- The HTTP server is unauthenticated by design and LAN-only; do not add public exposure
  assumptions (e.g. router port forwarding) to configs or docs.

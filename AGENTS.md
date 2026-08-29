# Agent instructions

## Project shape

- This is a Go MCP server (Go `>= 1.26`, module `github.com/ryzen3100/magyar-jogszabaly-mcp/v2`); it builds with `CGO_ENABLED=0` — the SQLite driver (`modernc.org/sqlite`) is pure Go.
- Binaries live in `cmd/`: `hungarian-law-mcp` (default stdio MCP; the `serve` subcommand runs Streamable HTTP), plus `build-db`, `check-updates`, and `ingest`.
- Shared server logic lives under `internal/` (`server`, `tools`, `store`, `statute`, `fts`, `builddb`, `ingest`, `seed`); there is no `src/`.
- `data/seed/*.json` and `data/eu-mappings.json` are the database inputs. `go run ./cmd/build-db` writes a temporary file and atomically renames it over `data/database.db` on success; treat the database as generated, not hand-edited.

## Commands

- Build with `go build ./...`; static checks with `go vet ./...`.
- Lint GitHub Actions workflows with `actionlint` (install once: `go install github.com/rhysd/actionlint/cmd/actionlint@latest`); run it after editing anything in `.github/workflows/`.
- Run all tests with `go test ./...` (stdlib `testing`); focus a package/test with `go test ./internal/tools -run TestName`.
- DB-backed tests skip when `data/database.db` is missing or lacks the required schema, so a green run may only cover in-memory tests; build a usable DB before relying on those results.
- For stdio development use `go run ./cmd/hungarian-law-mcp`; for HTTP use `go run ./cmd/hungarian-law-mcp serve`. `PORT` defaults to `3000` and `HOST` to `127.0.0.1`; override the database with `HUNGARIAN_LAW_DB_PATH`.
- TS↔Go parity harness: `node tools/parity/parity.mjs` (the TS side runs from the pre-port tree kept on the `dev` branch) and `python3 tools/parity/compare_db.py` for database logical parity.
- `docker compose up` pulls the published GHCR image; it does not run the local source. Use the `Dockerfile` when testing local Docker changes.

## Data updates

- `go run ./cmd/ingest` is a networked, rate-limited scraper for official `njt.hu` data. `-full` discovers the corpus, `-resume` reuses existing seed files, `-refresh-discovery` refreshes the discovery cache, and `-skip-fetch` reuses `data/source` HTML; `-base-url` and `-data-dir` override the `njt.hu` origin and the data root.
- Run ingestion only on a data-update branch, inspect `data/seed`, `data/census.json`, and `sources.yml`, then run `go run ./cmd/build-db`, `go vet ./...`, `go test ./...`, and `go run ./cmd/check-updates`. Ingestion does not update the census or source metadata automatically; never fabricate text when the source is metadata-only.
- `go run ./cmd/check-updates` needs a usable local database and network access; exit `0` means current, `1` means updates detected, and `2` means the check failed. Do not run ingestion from the production container or through MCP tools.

## Workflow conventions

- Follow `CONTRIBUTING.md`: branch from `dev`, do not push directly to `main`, and target PRs at `dev`.
- Keep SQL parameterized. Reuse the existing FTS/query and document-ID helpers rather than constructing ad-hoc search or identifier parsing.


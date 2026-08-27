# Changelog

All notable changes to this project will be documented in this file.

## [2.0.0] - 2026-08-27
### Changed
- Complete TypeScript → Go rewrite; the server no longer requires Node.js
- Same 13 MCP tools, the same HTTP prompts/resources, and the same health/version routes as 1.x
- Same database artifact (`data/database.db`) and seed inputs — no data migration needed
- Dependencies: official Go MCP SDK (modelcontextprotocol/go-sdk v1.7.0), modernc.org/sqlite v1.57.0 (pure Go, `CGO_ENABLED=0`), google/uuid

### Behavioral changes
- Missing-required-argument tool errors now return `Error: missing required argument "x"` instead of a leaked TypeError message
- Unknown tools/prompts/resources are rejected by the SDK as JSON-RPC errors instead of TS's in-band text envelopes
- HTTP session cap/TTL is now SDK-managed (the TS 500-session cap with a 30-minute sweep is gone)
- Rebuilt databases iterate seed files in sorted order, so provision rowids differ from TS-built databases (content identical; logical parity verified 14/14 with `tools/parity/compare_db.py`)
- Go-built databases contain all 109 EU references; the stock TS builder silently dropped ~17 to a driver statement-reset bug (TS with its own workaround also yields 109)
- `cmd/ingest` adds `-base-url` and `-data-dir` flags

## [1.0.0] - 2026-02-21
### Added
- Initial release of Hungarian Law MCP
- `search_legislation` tool for full-text search across Hungarian legislation
- `get_provision` tool for retrieving specific articles
- `validate_citation` tool for citation validation
- `check_currency` tool for checking if legislation is in force
- `get_eu_basis` tool for EU cross-references
- `get_hungarian_implementations` tool for finding national EU implementations
- `search_eu_implementations` tool for searching EU documents
- `validate_eu_compliance` tool for EU compliance checking
- `build_legal_stance` tool for comprehensive legal research
- `format_citation` tool for citation formatting
- `get_provision_eu_basis` tool for provision-level EU references
- `list_sources` tool for data provenance
- `about` tool for server metadata
- Contract tests with 12 golden test cases
- Health and version endpoints
- Vercel deployment (Strategy A, bundled DB)
- npm package with stdio transport

[1.0.0]: https://github.com/Ansvar-Systems/Hungarian-law-mcp/releases/tag/v1.0.0
[2.0.0]: https://github.com/ryzen3100/magyar-jogszabaly-mcp/releases/tag/v2.0.0

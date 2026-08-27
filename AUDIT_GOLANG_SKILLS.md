# Go Skills Audit — magyar-jogszabaly-mcp

Date: 2026-08-27 · Method: 27 read-only sub-agents, one per skill-concern split, each applying its skill's audit/full-scan checklist to a distinct scope. Full-file reads + grep; only the test-execution agent ran the suite (`go test`, `-race`, `-cover`), only the tooling agents ran `gofmt`/`go vet`/`staticcheck`/`govulncheck`/the `modernize` analyzer. No files were modified.

- ~12.1k lines of Go (8 `internal/` packages, 4 `cmd/` binaries), 27 test files, 67 Go files total.
- ~340 raw findings across 11 skills → deduplicated to ~120 unique findings below (cross-skill duplicates like context propagation, `dependabot`, `cacheMu`, entrypoint stdout merged once).
- Severity schemes are the skills' own where defined (DREAD for security; MUST/SHOULD for style; RFC-2119 for naming; standard Critical/High/Medium/Low where a skill defines none — disclosed per section).

## Verified positives (auditor-confirmed)

- **No SQL/FTS injection.** Every value, filter, IN-list and the rowid placeholder generator is `?`-parameterized; no `fmt.Sprintf`-built SQL exists (internal/store, internal/fts, all 13 tool handlers).
- **govulncheck v1.7.0: clean** — no vulnerabilities at called-symbol or module level.
- **`go test ./...` all pass; `go test -race ./...` no races; gofmt clean; `go vet` clean.**
- **No test mutates `data/database.db`** — all real-DB access is `mode=ro&_pragma=query_only(1)`; fixtures are in-memory; builddb/e2e write only under `t.TempDir()`.
- **Stdio protocol channel is clean in Go code** — every server-mode write goes to stderr; SDK v1.7.0 discards its logger by default; `internal/ingest` (the only stdout writer) is unreachable from the server binary.
- No committed secrets; no goroutine leaks (only one `go func`, correctly owned); no defer-in-loop; no nil-map writes; no unchecked production type assertions; no loopvar hazards (go 1.26 semantics).
- Core coverage: fts 96.6%, statute 94.7%, store 90.2%, tools 88.3%, ingest 82.8%, builddb 82.3%.
- go.mod/go.sum are exactly tidy (`go mod tidy -diff` empty); module path matches the repo; all 3 direct deps current and maintained.
- Comment quality and port-provenance notes (incl. `ponytail:` ceilings) are a stated strength of the codebase.

---

# Merged priority report (CLAUDE.md / GOLANG-AI-DRIVEN-REVIEW.md tiers)

## Tier 1 — Blocking-first

### Security

| # | Sev | Location | Finding | Fix sketch |
|---|-----|----------|---------|------------|
| S1 | High | internal/server/http.go:219-278, 283 | No authn/authz on `/mcp`/`/health`; any network peer can query and use/DELETE any session by UUID; combined with `Access-Control-Allow-Origin: *` any website can drive the API via a visitor's browser | Reverse proxy or shared-token middleware (`crypto/subtle`); restrict ACAO or add `http.NewCrossOriginProtection` |
| S2 | High | internal/server/http.go:253-277 | No request body size limit; SDK does unbounded `io.ReadAll` (go-sdk streamable.go:478,1418) → one multi-GB POST OOMs the single-process server | `http.MaxBytesReader` wrapper |
| S3 | Med | internal/server/http.go:108 | `http.Server` has no ReadHeaderTimeout/Read/Write/IdleTimeout → slowloris connection exhaustion | Set explicit timeouts |
| S4 | Med | internal/server/http.go:221-225 | No rate limiting; TS 500-session hard cap never ported (acknowledged `ponytail:` comment); 30-min sessions accumulate | Counting wrapper + per-IP limiter |
| S5 | Med | internal/server/http.go:110, docker-compose.yml:9 | Binds `:PORT` (all interfaces); compose publishes `0.0.0.0:3000` — unauthenticated service reachable from the LAN | HOST env defaulting to loopback; pin compose publish IP |
| S6 | Med | internal/server/http.go:243-249 | Degraded `/health` runs `CoreTablesReady` + two full-table `COUNT(*)` per unauthenticated request — cheap CPU amplification | Cache failure state with short TTL |
| S7 | Med | internal/ingest/fetcher.go:143 (also ingest.go:322-323) | Scraper reads response bodies with unbounded `io.ReadAll` — hostile/compromised origin OOMs the pipeline | `io.LimitReader` with max size |
| S8 | Med | internal/ingest/fetcher.go:71, cmd/ingest/main.go:45 | Production fetcher falls back to `http.DefaultClient` (no Timeout) with `context.Background()` — a stalled njt.hu connection hangs ingestion forever | `Client.Timeout` or per-request deadline |
| S9 | Med | internal/builddb/build.go:68-74 | `build-db` deletes the existing 296 MB `database.db` up front, then builds in place — a failed build destroys the last-good artifact | Build to `outPath.tmp`, `os.Rename` on success |
| S10 | Med | scripts/download-db.sh:8-12 | CI downloads the release DB via `curl \| gunzip` with no checksum, from a different org's repo (Ansvar-Systems) — supply-chain integrity of the artifact check-updates opens | Pin + verify a committed SHA256 |
| S11 | Low | internal/statute/statute.go:93; internal/fts/fts.go:13,40-47; internal/store/store.go:31, capabilities.go:57; internal/tools/schemas.go:13-31, registry.go:243, get_provision.go:96, search_legislation.go:144-147 | Hardening cluster: `%`/`_` unescaped in LIKE (full-scan lever); `HasBooleanOperators` is case-sensitive so lowercase `not` silently pushes every such search to the LIKE tier; DSN built by raw concat; table identifier interpolated (all call sites currently literal); raw `err.Error()` (driver text incl. SQL fragments) returned in-band; no `maxLength`/`maxItems`/enum enforcement in schemas; `get_provision` without section returns unbounded rows | ESCAPE clause + `(?i)`; escape DSN path; allowlist identifiers; schema limits enforced in handlers; generic in-band message + server-side detail; result cap |

### Code safety

| # | Sev | Location | Finding | Fix sketch |
|---|-----|----------|---------|------------|
| C1 | Med | internal/server/http.go:355-368 | `statusWriter` forwards neither `http.Flusher` nor `Unwrap()` → SDK's `http.NewResponseController(w).Flush()` silently no-ops; SSE keepalive and session GET streams buffer behind proxies | Add `Flush()` delegating to the underlying `http.Flusher` |
| C2 | High* | docker-entrypoint.sh:22,54 | `echo` to stdout before `exec "$@"` — in containerized **stdio** mode these lines land on the JSON-RPC protocol channel before the handshake (only stdout corruption found anywhere; the Go code itself is clean) | `>&2` on both echoes |
| C3 | Med | internal/ingest/discovery.go:249 | `ExtractTotalPages` parses an unbounded int from first-page HTML; `discoverLaws` loops `page <= totalPages` with a fetch per page — a corrupt/hostile page-count yields an effectively unbounded crawl | Clamp (e.g. ≤10000) |

### Error handling

| # | Sev | Location | Finding | Fix sketch |
|---|-----|----------|---------|------------|
| E1 | High | internal/builddb/build.go:229 | Every `insertEuReference` seed-phase error is silently dropped (not just UNIQUE — FK/CHECK too): rows vanish from the shipped `database.db` with no count or warning | Count failures + warn, or tolerate only UNIQUE |
| E2 | High | internal/tools/get_provision.go:65 (+~30 sites across all 13 handlers; statute.go:87,103) | Zero `%w` wrapping in the whole tools/store surface — clients see bare `sql: no rows in result set`; diverges from builddb's own wrapping convention | Wrap with operation context at each site |
| E3 | Med | internal/store/capabilities.go:23, metadata.go:34 | `rows.Scan` errors ignored, `rows.Err()` never checked; metadata.go then **caches the possibly-degraded result forever** — transient failure pins wrong tier/built_at for process lifetime | Check `rows.Err()`; cache only fully-read maps |
| E4 | Med | internal/tools/search_legislation.go:154, 202 | Every FTS-variant and LIKE-tier error swallowed via `continue` / `err == nil && len(rows) > 0` — infrastructure failure (closed DB, SQLITE_BUSY) indistinguishable from zero hits | Keep degradation, log last error or set `_metadata.note` |
| E5 | Med | internal/ingest/ingest.go:554, cmd/ingest/main.go:53-56 | `FetchAndParseActs` always returns nil → `cmd/ingest` exits 0 even when every act fails; batch failures invisible to CI/cron | Track failed>0 → summary error or `-strict` |
| E6 | Med | internal/ingest/ingest.go:400 | One corrupt/unreadable cached seed JSON aborts the entire `--resume` run instead of re-fetching that act | Log + fall through to normal fetch |
| E7 | Med | internal/ingest/discovery.go:296, ingest.go:437,442,462,471 | Seeds, HTML cache and discovery cache written non-atomically — interrupt leaves truncated files; truncated seed then blocks `--resume` (E6); truncated HTML silently re-parses to METADATA_ONLY under `--skip-fetch` | Write temp file + rename in same dir |
| E8 | Med | internal/builddb/build.go:308 | Missing `eu-mappings.json` silently tolerated — build proceeds without manual mappings, nothing logged | Warn in the `fs.ErrNotExist` branch |
| E9 | Med | internal/ingest/fetcher.go:134,138,149,152 | Retry loop retries `context.Canceled`/`DeadlineExceeded` (burns 2+4+8s backoff on a dead ctx); sleeps ignore ctx; 429 `Retry-After` ignored | `errors.Is` cancel check; ctx-aware sleep; honor Retry-After capped |
| E10 | Low | internal/store/dbinfo.go:40, internal/tools/about.go:83, internal/server/prompts.go:53 | Capitalized/period-terminated error strings ×3 (staticcheck ST1005; deliberate TS-parity wire text in two cases); no sentinels anywhere → tests string-match exact messages (get_provision_test.go:156 et al.); duplicated "About tool not configured." literal in two paths | Sentinels + `errors.Is` in tests; parity comments where the text is contractual |

### Concurrency

| # | Sev | Location | Finding | Fix sketch |
|---|-----|----------|---------|------------|
| K1 | Med | internal/tools/registry.go:29,222 | `Handler` signature has no `ctx`; dispatch discards the request context; zero `QueryContext` in tools — client disconnect/timeout never cancels in-flight FTS/LIKE corpus scans; abandoned queries accumulate on the shared `*sql.DB` | Thread ctx through `Handler`; `QueryContext`/`QueryRowContext` |
| K2 | Med | internal/store/store.go:31 | Shared read-only `*sql.DB` with no `SetMaxOpenConns` and no `busy_timeout` pragma — unbounded pool serving all HTTP sessions | Cap the pool; add `busy_timeout(5000)` |
| K3 | Med | internal/store/store.go:76, metadata.go:31, capabilities.go:57; internal/server/http.go:234 | Package-global `cacheMu` (and `healthMu`) held **across DB I/O** — one cold `COUNT(*)` serializes every tool call and `/health` probe process-wide instead of coalescing | Query outside the lock, publish under it (or `singleflight`) |
| K4 | Med | internal/tools/registry.go:222 (+ internal/server/stdio.go:45) | No panic isolation on the stdio path — go-sdk v1.7.0 contains zero `recover()` (verified in module source), so a handler panic kills the whole stdio server, client sees silent EOF; HTTP mode recovers but drops the stack (http.go:293) | One `recover()` → `errorResult` + `debug.Stack()` in `dispatch` covers both transports |
| K5 | Med | cmd/ingest/main.go:45 | No `signal.NotifyContext` — SIGINT default-kills mid-write instead of unwinding the ctx already threaded through Run→Fetch | Wrap with `signal.NotifyContext` |
| K6 | Low | internal/server/http.go:132-134, 220; internal/ingest/e2e_test.go:42 | `Shutdown(context.Background())` with 5s `time.AfterFunc(os.Exit(1))` watchdog bypasses deferred `db.Close()` and reports failure to supervisors (TS parity, commented); `getServer` rebuilds the full `mcp.Server` (13 tools + prompts + resources) on every request; e2e test leaks the child process on timeout | `Shutdown` with `context.WithTimeout`; hoist one `sessionServer`; `exec.CommandContext` |

## Tier 2 — Important

### Tests

| # | Sev | Location | Finding | Fix sketch |
|---|-----|----------|---------|------------|
| T1 | High | internal/server/ (no test files) | Entire serving layer 0.0% coverage: HTTP routing, CORS, OPTIONS, panic recovery, `/health` degrade+cache, UUID session validation, both prompts and both resource readers — the file with the most recent fix (30-min idle timeout) is untested | `httptest` handler tests + `goleak.VerifyTestMain` |
| T2 | High | cmd/check-updates/main_test.go | Package at 9.2% coverage despite a test file; exit-code 0/1/2 classification paths largely unexercised | Table-drive the classification paths |
| T3 | High | internal/ingest/parser_test.go:75,97,131,146; discovery_test.go:14,29; ingest_test.go:33,48; builddb/eurefs_test.go:132 | 9 table-driven loops without `t.Run` subtests (skill MUST #1) — failures name only the test, not the case | Add named subtests |
| T4 | Med | internal/tools/registry.go:200,246-251; schemas.go | `Register` (the SDK wiring path incl. the `about==nil` skip) untested; dispatch "about" success branch untested; all 13 JSON input schemas never validated against what handlers enforce | In-memory `mcp.NewServer`+`Register` test; schema-vs-handler consistency test |
| T5 | Med | internal/ingest/ingest.go:84 | `Pipeline.Run` untested at unit level — cache-hit / `-discover-only` / `-refresh-discovery` branches only via the self-skipping binary e2e | Table-driven `Run` with fake server |
| T6 | Med | store_test.go:277, get_provision_test.go:175, search_legislation_test.go:243 | DB-backed integration tests gate on runtime `t.Skip`, not a build tag (skill BP #2) — a green run silently covers zero DB tests when the generated DB is absent (matches the AGENTS.md caveat) | Build tag, or loud aggregate skip summary in TestMain |
| T7 | Low | 25/27 test files; ingest_test.go:76 et al.; fetcher_test.go:122-147 | `t.Parallel()` only in fts/statute; fake servers read bodies with single `r.Body.Read` (under-read risk) → `io.ReadFull`; real 100ms sleeps with 10ms epsilon in the rate-limit test (CI flake risk); no fuzz targets despite pure parsers (`ParseCitation`, `ExtractEuReferences`, `SanitizeFtsInput`); `strings.Replace` self-replacing no-op at ingest_test.go:222 | Mechanical; fuzz targets are cheap wins |

Execution facts: all packages pass; no races; per-package coverage listed under Verified positives; server/seed/thin cmds at 0.0%.

### Dependencies

| # | Sev | Location | Finding | Fix sketch |
|---|-----|----------|---------|------------|
| D1 | High | .github/dependabot.yml:3 | Dependabot watches `npm` (dead since the Go port; npm package unpublished) and has no `gomod` entry — zero automated Go dependency updates | Replace with `gomod` (+ `docker`) |
| D2 | High | .github/workflows/ci.yml:4-7 | vet/tests/trivy/semgrep trigger on `main` only, but CONTRIBUTING.md mandates PRs target `dev` — checks never run on the branch work actually lands on | Add `dev` to push/pull_request lists |
| D3 | High | .github/workflows/ (all 7) | No govulncheck anywhere in CI/release (skill rule: before every release); only Trivy's module-level scan covers Go deps | govulncheck step in ci.yml (+publish.yml); consider `go get -tool` pinning |
| D4 | Med | semgrep.yml:29, docker-publish.yml:107-115, ci.yml:22, semgrep.yml:20 | SAST runs `p/typescript` (+`p/secrets`) on a Go codebase — zero Go SAST coverage; published GHCR image never scanned (trivy.yml is fs-only); all 11 action refs pinned to mutable tags, not SHAs (with `issues: write` held by check-updates.yml); semgrep image is a 2024-era 1.79 | `p/golang`; image scan; SHA-pin with version comments; bump |
| D5 | Low | go.mod:20,17,21; Dockerfile:15,4 | `x/oauth2` v0.35→v0.36 (one minor behind, token path); `x/sync` v0.21→v0.22; `segmentio/asm` pinned by encoding; `alpine:3.22` two stable releases behind (supported to ~May 2027); `golang:1.26-alpine` one minor behind 1.27 (floating tag = auto patch pickup, intentional) | Batch bumps at next dependency touch |

Facts: govulncheck clean (see positives); modernc libc/memory ride the sqlite tag — do not bump independently; no `vendor/` (optional hermetic-build hardening); scorecard/trivy/semgrep workflows exist and run weekly.

## Tier 3 — Suggestion-first

### Code style (MUST/SHOULD/nit; gofmt & vet clean)

- MUST ~45 lines >120 cols (search_legislation.go:241,260; validate_citation.go:152; get_provision.go:60,78; ingest.go:406,445,466,489,522,538; parser.go:42; discovery.go:60; cmd/ingest/main.go:34 …) and 4+-arg calls not one-per-line (http.go:107,117; build.go:158,173,229,274,292; storetest.go:130-181; dbinfo.go:55-57 3-operand condition; statute.go:95-98).
- SHOULD: `Build` ~295 lines / `FetchAndParseActs` ~140 / `ParseHungarianHTML` ~165 — extract EU-mappings, metadata, per-act, section-accumulation blocks; 9 copies of the nil-guard `missing required argument` → one `missingArg(name)` helper; 11 hand-rolled `sql.NullString`→pointer blocks duplicating `nullStringPtr`; `math.Min/Max` clamp reimplementing `clampLimit` (search-eu-implementations.go:51-55); `%q` instead of `\"%s\"` ×7; if/else chains → `switch` (check_currency.go:67-71, validate_citation.go:140-144, parser.go:363); `SELECT *` + positional Scan fragility (get_provision.go:78); LIKE fallback inline in ~110-line `runSearch` → extract; blank sqlite driver import in library code (store.go:16) → move to mains; `p.printf("")` no-op (ingest.go:553); typo "stay ” via" (search_legislation.go:270); 5 identical 7-line test wrappers → one `runHandlerJSON`.
- nit: store/statute/fts helpers take no `context.Context` (see K1); comment quality otherwise strong.

### Naming

- MUST/High: acronym casing split — `Eu*` fields (`EuDocumentID`, get-eu-basis.go:29 + 5 sibling files, ~25 identifiers) vs `GetEUBasis`/`EURef`/`EUDocumentID`; `Db*` store API (`DbMetadata`, `ResolveDbPath`, `AboutContext.DbBuilt`) vs `storetest.RealDBPath`'s `DB`; `ResolveDocumentId` (statute.go:63); `fts.SanitizeFtsInput`/`BuildFtsQueryVariants` stutter the package → `fts.SanitizeInput`/`fts.BuildQueryVariants`; file-name split inside internal/tools: 6 hyphenated (`get-eu-basis.go`) vs 5 underscored (`search_legislation.go`) — skill mandates underscores.
- SHOULD/Low: `SCHEMA` all-caps const (builddb/schema.go:11; deliberate parity); test-package split 8×`package tools` vs 7×`tools_test`; `dataSourceConst`-style suffixes; regex suffix drift `Patt`/`Pattern`/`Re`; `isoLayout` vs `isoMillis`; `…OnClosedDb` vs `…ClosedDB` test names; `ToolResponse` stutters; `dbIdWithSecRe` → `dbIDWithSecRe`; `seed.DocumentSeed` stutter (deliberate parity); get-eu-basis.go vs get-provision-eu-basis.go confusion risk.
- Clean: all 10 package names, receivers (19 methods, consistent per type), no GetX getters, no snake_case, no interfaces violated.

### Documentation

Code (MUST-level): `internal/server` has **no** `// Package server` doc and 5 file headers attached to the package clause; `RunHTTP` (http.go:84) undocumented vs documented `RunStdio`; `seed.DocumentSeed/ProvisionSeed/DefinitionSeed` undocumented; 8 internal/tools files have port-note headers attached to the package clause (godoc concatenates 9 package comments into one); no `doc.go` anywhere despite 3-16-file packages; 12 tool-handler docs are formulaic restatements (empty-result/`_metadata.note` semantics live only in inline comments); `DbBuiltOrMtime` has no production caller (server.go:47 re-implements it) and its doc misleads; `Pipeline` fields mostly undocumented.

Project (High): CHANGELOG.md:8 claims a `/version` route that never existed; DISCLAIMER.md:78 promises "staleness warnings when data is >30 days old" — no such feature exists. (Medium): version drift — CHANGELOG says 2.0.0 but `serverVersion="1.0.0"` (server.go:17), server.json/PRIVACY/SECURITY say 1.0.x; module path lacks `/v2` while README:198/PRIVACY:18 instruct `go install ...@latest` (uninstallable v2 tag); README:85 "109 uniós kereszthivatkozás" vs 92 in the shipped DB (TS-built, drops ~17); NOTICE lists only deleted TS files; PRIVACY presents a Vercel deployment the Go port doesn't ship; about.go:32 tells Hungarian users "az adatbázis naponta frissül" contradicting http.go:32/DISCLAIMER. (Low): registry.go:238 dead "Unknown tool" branch contradicts CHANGELOG:14; `go run` + exe-relative `data/` caveat (README:208); upstream Ansvar-Systems links in PRIVACY/DISCLAIMER/SECURITY vs CONTRIBUTING's fork routing; `kozlony.gov.hu` vs `magyarkozlony.hu`; CHANGELOG lists `google/uuid` which is only indirect.

### Observability

- Zero structured logging anywhere: ~100 sites, all ad-hoc `fmt` writes; no `log.*`, no `slog.*`, no levels (severity conveyed by "ERROR:"/"⚠" text prefixes). Single choke point exists (`logf` → stderr) — migrating it to `slog.NewJSONHandler(os.Stderr, …)` is the one-line upgrade path.
- SDK loggers never set: `ServerOptions.Logger` and `StreamableHTTPOptions.Logger` nil in both modes → keepalive-failure/notification/AddTool diagnostics silently discarded (SDK substitutes `slog.DiscardHandler`).
- Serve mode has no per-request access log (method/path/status/duration) — abuse of the unauthenticated surface is invisible; recovered panics log without `debug.Stack()`; tool-call errors are never logged server-side (in-band only) — a broken DB is invisible to the operator.
- Mixed stream discipline in `cmd/check-updates`: "ERROR:" lines on stdout (main.go:78-200) while siblings use stderr; ingest's `Fetcher.Logf` defaults to stdout `fmt.Printf`, bypassing the injectable `Pipeline.Stdout` (latent hazard if ever linked into the server binary).
- Build pipeline emits no heartbeat for minutes at a time (builddb seed loop).
- Gap analysis verdict (skill stance): worth adding = access-log line, dispatch error log, env-gated pprof on a localhost listener in `RunHTTP`, `busy_timeout`, the dispatch `recover()`. Explicitly NOT worth adding: Prometheus/OpenTelemetry/trace IDs/RUM/continuous profiling/session gauges — single-container read-only lookup server.

### Modernize (full-scan; analyzer: golang.org/x/tools modernize, 5 diagnostics, all corroborated)

- Medium: if-based clamps → `min`/`max` builtins (eurefs.go:152,156; ingest.go:298; search-eu-implementations.go:55; clampLimit itself at search_legislation.go:343-355 — `max` param shadows the builtin); `boolPtr(false)` → `new(false)` (registry.go:187-193); `sort.Ints` → `slices.Sort` (ingest.go:259); `sort.Slice` ×2 → `slices.SortFunc` (discovery.go:274, parser.go:447); map→slice loops → `slices.SortedFunc(maps.Values(…))` (parser.go:443-446, discovery.go:270-274); `os.IsNotExist` → `errors.Is(…, fs.ErrNotExist)` (check-updates/main.go:106); hand-rolled `orDefault` → `cmp.Or` (prompts.go:60-65; http.go:86).
- Lower: `for i := 0; i < n; i++` → `for i := range n` (fetcher_test.go:129, search_legislation_test.go:175, build_legal_stance_test.go:44); `slices.Clone` (ingest.go:137), `slices.Concat` (fts.go:81); `strings.CutPrefix` (parser.go:135-138); `t.Context()` in 18 test sites; `sql.Null[string]` (9 sites); 8-line hand-rolled UUID-v4 → `github.com/google/uuid` is already in the module graph (borderline; current code correct).
- Infra/testing patterns: `actions/setup-go@v5` → @v7; docker action majors stale (buildx v3→v4.3, login v3→v4.6, metadata v5→v6.2, build-push v6→v7.3); trivy-action 0.35→0.36; scorecard v2.4.0→v2.4.4; `go-version: '1.26'` → `go-version-file: go.mod`; no `.golangci.yml` (modernize linter unwired); no `tool` directive for govulncheck; go 1.26.6 → 1.27.0 optional; raw `mcp.ToolHandler` + hand-maintained schemas vs SDK's generic `AddTool[In,Out]` (repo comment cites deliberate TS payload parity — switch only if the parity harness covers schema shape). Already clean: no deprecated stdlib APIs, `any` everywhere, modern SDK options, `t.Setenv` in use, CGO_ENABLED=0 + `-trimpath -ldflags="-s -w"` correct.

---

# Per-skill consolidated reports

Raw counts per skill (pre-dedup): security 35 · concurrency 22 · error-handling 49 · testing 28 · observability 31 · code-style 52 · naming 28 · safety 19 · dependencies 23 · documentation 27 · modernize 33.

Notes on fidelity: agent-6 (error-handling/pipeline) could not locate golang-error-handling/SKILL.md on disk (it exists at /home/laci/.agents/skills/golang-error-handling/SKILL.md) and applied the standard Go checklist with Critical/High/Medium/Low; all other agents either applied their skill's scheme or disclosed that none is defined. Skills with no named audit mode (naming, safety, dependency-management, documentation) were scanned by concern-split sub-agents per the same principle.

## 1. golang-security (DREAD) — 3 agents: injection/input-validation · HTTP surface · scraper/filesystem/secrets

Critical: none. High: S1 (no auth + wildcard CORS), S2 (no body limit → OOM). Medium: S3–S10. Low: S11 cluster. Explicitly clean: path traversal (exact string route matches), SQL/command/template injection, XSS, crypto misuse (session UUIDs from crypto/rand), committed secrets (grep'd; SECURITY-SETUP.md: GITHUB_TOKEN only), ReDoS (RE2 only). DREAD rationale for the S1/S2 Highs: remote, unauthenticated, low effort, single-process victim.

## 2. golang-concurrency (Audit mode) — 2 agents: server/stdio · scrape→seed→build pipeline

High: none (no goroutine leaks, no races on shared state, no shutdown deadlocks found statically; pipeline is deliberately single-threaded — no goroutines/channels/WaitGroups exist in production pipeline code). Medium: K1–K5. Low: K6, rate-limiter sleep held under mutex (documented, intentional), retry bypasses `rateLimit()` on attempt 2+ (moot today: backoff > MinDelay), test-only shared-state without locks (ingest_test.go:75-79 — safe only under the current single thread), build.go signal-handling gap. Clean: channel ownership, `time.After` loops, select misuse, `realDBOnce`, `Fetcher.mu` correct; `rand.Read` error-ignore is per the Go ≥1.24 contract and commented.

## 3. golang-error-handling (Audit mode) — 3 agents: store/fts/statute/tools · ingest/builddb/seed/cmd · server/cmd

High: E1 (silent EU-reference loss), E2 (zero %w wrapping, ~30 sites). Medium: E3–E9 (+ discovery multi-page has no checkpoint — failure on page N of ~90 discards all in-memory progress; body-read errors not retried while network errors are; check-updates misdiagnoses: any `os.Stat` error → "Database not found", any non-ErrNoRows `readBuiltAt` error → "db_metadata table is missing"; `checkPortal` discards the cause). Low: E10 (+ `check_currency.go:62` missing the `ErrNoRows` branch its siblings have; `SafeCount` swallows all errors → 0; discarded `f.Close()`/marshal fallbacks; UNIQUE classification via `strings.Contains(err.Error())`; `totalEuDocuments` overcounts ignored duplicates; `%v` breaking the chain at discovery.go:189). Explicitly clean: no single-handling violations (the scope contains zero logging — the root of the swallow findings), no panic/recover misuse, all 9 `errors.Is` uses correct, no `%v`-wrapping, exit-code conventions consistent with the documented 0/1/2 contract.

## 4. golang-testing (Audit mode) — 2 agents: inventory/quality · execution

Execution: all pass, no races, no DB mutation; skip sites enumerated (ingest e2e under `-short`; three DB-backed suites on `RealDBAvailable()` — none skipped in this run since the DB exists). High: T1–T3. Medium: T4–T6 (+ mixed white-box/black-box test packages in internal/tools; `TestResolveDbPathError` passes only because the test binary sits in the build cache — would flip in a repo-shaped layout). Low: T7 (+ store_test.go:166 POSIX `/tmp` path; e2e shells out to `go build` making ingest the slowest package). Strengths recorded by the auditor: error-path assertion quality, injected clocks/backoffs, a differential TS-lookahead-equivalence test, read-only + skip-guarded real-DB suites.

## 5. golang-dependency-management — 2 agents: hygiene · vulnerabilities

Facts: 3 direct + 15 indirect (not 16); tidy-diff clean; module path verified to match the live repo; `go 1.26.6` patch-pin is the reproducible-toolchain convention (CI's `'1.26'` auto-downloads via GOTOOLCHAIN); single pseudo-version (bigfft) is upstream-dormant but stable and pinned by the modernc stack; direct deps all current and maintained. High: D1–D3. Medium: D4. Low: D5 (+ libc/memory ride sqlite — do not bump independently; vendoring optional). govulncheck v1.7.0: **no vulnerabilities** (called-symbol and module level). CI facts: dependabot's github-actions ecosystem correctly weekly; no renovate; go.sum committed.

## 6. golang-safety — 3 agents: nil/assertions/zero-values · aliasing/numerics · lifecycle/init

High (reachable-panic class): none. Medium: C1 (statusWriter Flusher), C3 (unbounded page crawl). Low: `int(n)` without bounds check (store.go:44); `Pipeline` zero value unsafe (constructor-only by verified convention); 7 bare type assertions in search_legislation_test.go (panic-on-drift instead of clean failure); `1<<uint(attempt+1)` overflows for MaxRetries ≥ 63 (prod value 3); float64→int out-of-range branch unreachable but unchecked; ignored `strconv.Atoi` in parser.go:251 silently drops sections from the public-data subset filter; exported mutable `KeyHungarianActs` slice (in-repo consumers copy; external importers could mutate); EU result rows share a backing array (safe only under immediate marshal). Explicitly clean: no nil derefs on reachable paths, no nil-map writes, all production assertions comma-ok/switch, no typed-nil-in-interface, no defer-in-loop, no `init()`, loopvar safe, no narrow int truncations, no float equality, no division by zero, regexp group indices cannot be −1. Lifecycle: `metadata.go` rows Close/Err hygiene (E3), double `db.Close()` in builddb (idempotent but misleading), last `Close` before success report unchecked, caches keyed by raw `*sql.DB` pointer with a now-false "can never collide" comment (ABA after close+GC), `openDB` never pings so servers start "successfully" against a missing/corrupt DB reporting tier=free (only `/health` notices, lazily). Sync/time lifecycle clean.

## 7. golang-code-style (Parallelizing reviews) — 3 agents by package group + repo-wide tooling

MUST: line-length and one-arg-per-line list above; staticcheck (run via `go run honnef.co/go/tools/cmd/staticcheck@latest` — the installed binary is built for go1.23 and refuses the go1.26 module): exactly 3 findings, both ST1005 capitalized-error sites already listed as E10. `gofmt -l .` clean, `go vet ./...` clean. SHOULD/nit: list above. The scope agent confirmed nearly every flagged deviation carries an explicit TS-parity comment — fixes must respect the parity harnesses.

## 8. golang-naming — 2 agents: packages/files/receivers/acronyms · identifiers

MUST/High: acronym split (`Eu`/`EU`, `Db`/`DB`, `Id`/`ID`), `fts` stutter, hyphen-vs-underscore file split. Medium/Low: list above. Clean: package names, receivers, no getters, no snake_case, no ALL_CAPS locals, constructor naming correct, `f`-suffix log helpers correct, option structs correctly `Options`/`RequestOptions`. Note: renames touching wire-adjacent names (`Eu*` appears in result JSON via field tags? — no, tags are separate) are mechanical but wide (~40 identifiers); defer to gopls rename per skill.

## 9. golang-documentation — 2 agents: code docs · project docs accuracy

Code MUST/SHOULD + project High/Medium/Low: lists above. Anti-patterns (fabricated/obsolete docs to delete on sight): none in code; project-level drift is concentrated in CHANGELOG/DISCLAIMER/NOTICE/PRIVACY (TS-era leftovers and version skew). Godoc readability of the complex functions (`runSearch`, `ExtractEuReferences`, `extractDefinitions` lookahead proof, `dispatch`) explicitly praised above the skill's bar. cmd/ mains follow the correct `// Command x …` convention.

## 10. golang-observability (Audit mode) — 3 agents: current-state inventory · gap analysis · dedicated stdio/stdout check

Dedicated stdio/stdout verdict: **PASS for Go code** — every app write in the server path goes to stderr (server.go:22-26 documents the invariant); SDK v1.7.0 writes only JSON-RPC to stdout and discards its logger by default (verified in module source: server.go:208-209 → logging.go:105-110, transport.go:118); the only stdout writers in the repo (`Fetcher.logf`, subprocess `Stdout: os.Stdout`) are unreachable from the server binary. The single corruption risk found anywhere is the entrypoint echo (C2). Inventory/gaps: lists above; positives — no sensitive data in logs, no duplicate log-and-return (each error printed exactly once at its entrypoint), scraper progress/resume visibility and the `/health`+HEALTHCHECK wiring judged appropriate as-is.

## 11. golang-modernize (Full-scan mode) — 2 agents: language/stdlib · deprecated APIs/tooling/testing

Analyzer + manual scan: Medium/Lower lists above. High: none — no loopvar hazards, no `math/rand`/`rand.Seed`, no deprecated crypto/reflect/httputil APIs, no `interface{}`. Clean categories: errors.Join (no multi-error accumulation exists), generics (no compelling site), time patterns (`now.Sub` + `math.Floor` is deliberate and test-pinned), SDK options modern, compose has no obsolete `version:` key, Docker build flags correct.

---

# Coverage & NOT CHECKED (aggregate disclosure)

Every non-test Go file was read in full by at least one agent; 27 test files were read or pattern-swept per assignment. Recurring unverified items, disclosed by the agents:

- Static-only: no live server was run; SSE-with-statusWriter behavior (C1) is reasoned from go-sdk v1.7.0 source in the module cache, not executed; stdio corruption (C2) not reproduced in a container.
- `go test -race` for 6 small packages was served from the cache of the killed combined run (same flags); `-count=1` uncached re-run not performed.
- govulncheck ran `./...` from source; no `-mode=binary` scan of the shipped container image; published GHCR digest not inspected; README's "109" claim checked against the local working-tree DB only.
- TS-parity (dev branch `src/**`) taken from in-repo parity comments, not diffed against the `dev` tree; the parity harness itself (tools/parity/*.mjs, compare_db.py) not run.
- modernc.org/sqlite driver DSN parsing (S11 DSN finding) reasoned from URI conventions, not executed.
- Whether the MCP SDK enforces JSON Schema (`maxLength`/enums) before invoking handlers was assumed advisory — if enforced, two Low security findings weaken further.
- Long test bodies (parser_test.go:121-682 et al.) spot-read + grep-verified rather than line-by-line.

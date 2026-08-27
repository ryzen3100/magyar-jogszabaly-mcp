# Whole-Repository Ponytail Audit — `magyar-jogszabaly-mcp`

## Method & verification basis

Five parallel read-only audits (runtime `src/`, scripts/data pipeline, tests, CI/deploy/Docker, docs/config), each required to show its search evidence. Every material claim was then independently re-verified against the working tree before being accepted. Empirically executed during this audit (all non-destructive, read-only):

- `npm test` → **6 files, 85 tests, all green**
- `npx vitest run tests/contract/golden.test.ts --reporter=verbose` → **22 tests green, genuinely exercising the real 282 MB DB** (census agreement passes)
- `npx vitest run --coverage` → **FAILS its own 100% thresholds: 74.44% statements/lines, 87.5% branches** (observed, twice)
- SQLite read-only queries: `legal_documents` = 4,326; `eu_references` = 92; documents matching `%Privacy Act%` = **0**
- Confirmed byte-level: 27 unresolved `<<<<<<< HEAD … >>>>>>> origin/dev` markers across `PRIVACY.md` (11) and `DISCLAIMER.md` (16); `grep -ri oauth` footprint; workflow trigger blocks; `.dockerignore` contents; ingest CLI flag definitions; export-consumer greps for all "dead API" claims.

No tracked file was modified.

---

## FINDINGS

### A. Committed artifacts that are objectively broken (highest value)

**A1. Unresolved merge conflicts committed in PRIVACY.md**
- Category: `other` → shrink (broken deliverable)
- Location: `PRIVACY.md:14,49,60,99,162,178,187,196,235,260,274` — 11 hunks, 33 marker lines (`<<<<<<< HEAD` … `>>>>>>> origin/dev`)
- What: resolve each hunk; the file currently renders raw conflict markers to every reader
- Why safe: docs-only; content choice between the two sides needs no code knowledge. Verified with grep, not inferred.
- Est. reduction: ~40–70 lines (toward the leaner `origin/dev` side)
- Impact: none (docs)
- Confidence: **high**

**A2. Same defect in DISCLAIMER.md**
- Category: same as A1
- Location: `DISCLAIMER.md:11,25,41,65,74,91,102,115,127,139,159,182,229,240,253,285` — 16 hunks, 48 marker lines
- Same fields as A1. Est. ~50–80 lines. Confidence: **high**

### B. Dead documentation / dead configuration (stale pointers)

**B1. Issue template describes Polish law in a Hungarian-law repo**
- Category: `delete` (or rewrite)
- Location: `.github/ISSUE_TEMPLATE/data-error.md:13,14,15,16,20,24,34`
- What: "Ustawa/Kodeks", "Artykul", "Dziennik Ustaw", "**Jurisdiction:** PL", "isap.sejm.gov.pl", "Polish characters". Rewriting for njt.hu/Magyar Közlöny or deleting both work; the template is unusable as-is.
- Why safe: no code references issue templates; purely user-facing text. Verified by reading the whole file.
- Est. reduction: 41 (delete) or ~0 net (rewrite)
- Impact: none
- Confidence: **high**

**B2. SECURITY.md names scanners that do not exist**
- Category: `dead-documentation`
- Location: `SECURITY.md:26` ("Trivy, npm audit, and **Socket**" — zero Socket references anywhere; npm audit exists only as a can't-fail step, see B8), `:27` ("**Gitleaks** scans all commits" — no gitleaks workflow exists; workflows are exactly ci/publish/docker-publish/check-updates/scorecard/semgrep/trivy), `:28` ("Semgrep + **CodeQL** scan on every push" — no CodeQL; Semgrep yes; and nothing scans "every push", see B7)
- Est. reduction: ~3 rewritten
- Impact: security-posture accuracy only
- Confidence: **high**

**B3. SECURITY-SETUP.md describes a publishing/scanning pipeline that isn't there**
- Category: `dead-documentation`
- Location: `.github/SECURITY-SETUP.md:22–27` (`mcp-publisher login dns azure-key-vault`, vault `kv-ansvar-dev` — publish.yml actually does plain `npm publish --provenance`; zero `mcp-publisher` hits elsewhere), `:38–45` ("All 6 scanners are configured in ci.yml" — ci.yml contains lint/test/build only; listed CodeQL/Gitleaks/Socket don't exist; real scanners live in separate workflow files; status checks "ci"/"contract-tests" match no job name)
- What: delete the Key Vault section, rewrite the scanner list (or retire the internal file entirely — 46 lines, nothing consumes it)
- Est. reduction: ~20–46
- Impact: none
- Confidence: **high**

**B4. PERFORMANCE.md is an executed plan still presented as an open checklist**
- Category: `delete` (or archive)
- Location: `PERFORMANCE.md` entire 39-line file; all five `- [ ]` Tier-1 items and the Tier-2 item are verifiably implemented in current code: pretty-print gone (`registry.ts:377` — `JSON.stringify(result)`, no indent arg), session cap/TTL sweep present (`http-server.ts:44–46,107–116`), startup caches present (icon read once `http-server.ts:48–56`, pre-stringified card `:66`, cached counts), combined article query in `get-eu-basis.ts:61–75`, merged LOWER scan in `statute-id.ts:74–79` (its own comment says so), search restructure shipped (`search-legislation.ts:107` `MATCH ? AND rowid IN (...)`), composite index moot since `build-db.ts` already defines `UNIQUE(document_id, provision_ref)`
- Why safe: pure notes; nothing links to the file (verified repo-wide)
- Est. reduction: up to 39 (or replace with one "completed" line)
- Impact: none
- Confidence: **high**

**B5. REGISTRY.md contradicts the actual corpus and package metadata**
- Category: `deduplicate` (with factual staleness)
- Location: `REGISTRY.md:6,22–23` (says **4314 laws / 130124 provisions**; census.json and the live DB both say **4326 / 130220** — verified by query), `:16` (Homepage `ansvar.eu/open-law` vs `package.json:12` homepage `https://ansvar.eu`)
- What: it duplicates `package.json` + `server.json` content and nothing consumes it programmatically — delete, or sync before any registry submission
- Est. reduction: 30 (delete) or ~8 (sync)
- Impact: none
- Confidence: **high**

**B6. Commented OAuth/BASE_URL compose config advertising nonexistent capability**
- Category: `delete`
- Location: `docker-compose.yml:13–15`
- What: `# OAUTH_ENABLED`, `# BASE_URL` — repo-wide grep shows no code ever reads `OAUTH_*`/`BASE_URL` (the only env reads in the entire repo are `PORT` at `src/http-server.ts:42` and `HUNGARIAN_LAW_DB_PATH` at `src/db-info.ts:22`). Enabling these would do literally nothing.
- Est. reduction: 3
- Impact: deployment docs only
- Confidence: **high**

**B7. CI gates never fire on the repo's own documented workflow**
- Category: `other` (config/doc mismatch)
- Location: `ci.yml:4–7`, `semgrep.yml:6–7`, `trivy.yml:6–7` all use `branches: [main]`; `CONTRIBUTING.md` mandates PRs targeting `dev`
- Consequence: feature→dev PRs run **zero** lint/test/scan; everything lands ungated until dev merges to main. Fix either side (+3 lines to add `dev` to pull_request, or change docs). Which side is intended is a maintainer call.
- Est. reduction: 0 net (+3)
- Impact: deployment/tests assurance
- Confidence: **high** (facts; intent flagged separately)

**B8. publish.yml audit step cannot fail**
- Category: `shrink`
- Location: `publish.yml:31` — `npm audit --omit dev || true`
- What: gates nothing; pure release-log noise. Drop the step, or drop `|| true` and let it actually gate (which would need triage first).
- Est. reduction: 1
- Impact: workflow output only
- Confidence: **high** (mechanism), owner intent unknown

### C. Residue of an abandoned/never-shipped OAuth layer (cluster)

**C1. `--oauth` branch in http-smoke.py tests endpoints that don't exist**
- Category: `delete`
- Location: `scripts/http-smoke.py:2,55,61–104` plus the `NoRedirect`/`OPENER` machinery at `:17–22` whose sole purpose is capturing the OAuth 302
- Evidence: case-insensitive `oauth` grep over `src/` → zero matches; server routes are exactly `/health`, `/mcp`, `/icon.png`, `/.well-known/mcp/server-card.json`; sole caller `docker-publish.yml:88` invokes **without** `--oauth`, so the branch is never exercised by CI; running it today would hit `/oauth/*` → 404 and fail unconditionally. Nothing that works is being deleted. The non-OAuth half (including its intentional "OAuth metadata returns 404" regression check at `:106–107`) stays.
- Est. reduction: ~55 (file drops from 162 to ~105)
- Impact: none (smoke tooling only; workflow keeps passing)
- Confidence: **high**

**C2. Server card still points at the archived upstream repo**
- Category: `other` (stale public endpoint payload; missed in the fork's URL-consistency sweep)
- Location: `src/http-server.ts:72` — `homepage: 'https://github.com/Ansvar-Systems/Hungarian-law-mcp'`
- Evidence: every other identity string in the repo (package.json repo/homepage, compose image, REGISTRY.md repository) refers to this fork / ansvar.eu; this one literal serves the archived upstream to every HTTP client. Runtime string constant; no logic depends on its value.
- Est. reduction: rewrite 1 line
- Impact: runtime behavior of one informational response body only
- Confidence: **high**

*(C-cluster also covered by B6; `Access-Control-Allow-Headers: Authorization` at `http-server.ts:233` is related but intentionally listed under KEPT as defensible CORS practice.)*

### D. MCP-schema accuracy (public API copy, not removable code)

**D1. Schema descriptions use example identifiers that resolve to nothing in this corpus**
- Category: `other` (public-API copy correctness)
- Location: `src/tools/registry.ts:94,102–103` (`get_provision`: `"Privacy Act 1988"`, `"privacy-act-1988"`), `:124,130` (`validate_citation`: `"Section 13 Privacy Act 1988"` ×2), `:209` (`get_eu_basis`: *"Privacy Act references GDPR concepts, SOCI Act…"*), echoed in `validate-citation.ts:29–30`
- Evidence: SQL over `legal_documents` (`title`, `title_en`, `short_name`) → **0 rows** for Privacy Act; no SOCI statute exists (seed grep hits for "SOCI" are substrings of "ASSOCIATED"). A client pasting the schema's own example gets `Document not found`.
- What: replace examples with resolving IDs, e.g. `"2011. évi CXII. törvény"` / `"act-cxii-2011-info-self-determination"`. Do **not** remove the English-style citation parsers themselves — they're live, tested functionality.
- Est. reduction: ~6 lines reworded
- Impact: MCP schema (descriptions clients read)
- Confidence: **high**

**D2. TOOLS.md parameter tables drifted from the actual input schemas**
- Category: `dead-documentation` (drift fix)
- Location/evidence: heading `TOOLS.md:127` is literally `## 10. get_{jurisdiction}_implementations` (template placeholder; real name `get_hungarian_implementations`); missing params: `document_id` (search_legislation `TOOLS.md:15–17` vs `registry.ts:68–71`), `provision_ref` (get_provision `:29–32` vs `registry.ts:109–112`), `document_id` (build_legal_stance `:66–69` vs `registry.ts:151–154`), `reference_types` (get_eu_basis `:117–121` vs `registry.ts:220–224`), `primary_only`+`in_force_only` (get_hungarian_implementations `:133–135` vs `registry.ts:243–252`), `year_from/year_to/has_hungarian_implementation/limit` (search_eu_implementations `:147–150` vs `registry.ts:268–274`). Related nit: `README.md:103` claims format_citation supports "teljes, rövid és pinpoint" — code has exactly `'full' | 'pinpoint'` (`format-citation.ts:12,46`). All 13 tool names themselves match; nothing invented.
- Est. reduction: ~0 net (corrections across ~20 rows + 1 heading)
- Impact: docs describing public API
- Confidence: **high**

**D3. CONTRIBUTING.md mandates Zod, which the project doesn't use**
- Category: `shrink` (fact fix)
- Location: `CONTRIBUTING.md:27` — "All tools must have Zod schema…"
- Evidence: zero zod occurrences in `package.json`, `src/`, `scripts/`; tools declare plain JSON-Schema objects.
- Est.: 1 line reworded. Impact: none. Confidence: **high**

### E. Code cleanup in `src/` (small, verified)

**E1. Identical annotations literal repeated 13×**
- Category: `deduplicate`
- Location: `registry.ts:85,116,135(±),163,184,202,228,256,277,293,310,…` — all `{ readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false }` differing only by `title` (spot-verified at :85/:116/:202)
- What: hoist one `READ_ONLY` const; spread + per-tool title. Mechanical, SDK-typed.
- Est.: ~0 net lines, removes ~500 chars of duplication
- Impact: none. Confidence: **high**

**E2. Misleading foreign-jurisdiction examples** — see D1 (listed here so the src area isn't understated; it's the one src change users actually feel).

**E3. Dead-state variable in search-legislation**
- Category: `shrink`
- Location: `src/tools/search-legislation.ts:63,99,126` — `queryStrategy` values `'none'`/`'exact'` are never observable; only `=== 'fallback'` matters
- What: inline the fallback condition, drop the variable (~4 lines). Behavior-identical; existing tests assert `query_strategy: 'broadened'` and stay valid.
- Impact: none. Confidence: **medium** (sub-agent traced control flow; not independently executed)

**E4. Duplicated multi-term branch in FTS variant builder**
- Category: `deduplicate`
- Location: `src/utils/fts-query.ts:54–72` — OR-fallback repeats the `terms.length > 1` condition that the first block guards
- What: fold push into first block (~3 lines); ordering locked by `tests/core/core-utils.test.ts:23–27`
- Impact: none. Confidence: **medium**

**E5. Provision-ref lookup logic triplicated with inconsistent tolerances**
- Category: `deduplicate` (with a correctness edge — see correctness section)
- Location: `validate-citation.ts:139–142` accepts `"6:272"`-style refs via three candidate forms; `get-provision-eu-basis.ts:37–40` doesn't (so same ref validates but then fails retrieval); `get-provision.ts:65–67` adds an unconditional `%ref%` LIKE fallback able to return `s61` when asked for `s6`
- What: extract one `findProvisionRef(db, docId, ref)` helper near `statute-id.ts`; choose tolerance explicitly per tool initially. Also collapses six copies of the 4-line "unknown document" envelope guard. Needs a semantics decision first — not blind mechanical dedup.
- Est.: ~15 net
- Impact: runtime edge cases (if tolerances unified), tests
- Confidence: **medium**

**E6. Non-published-beyond-dist exports with zero in-repo consumers**
- Category: `dead-API` *(surface hygiene, with caveat)*
- Location: `registry.ts:50` `export const TOOLS` (internal use at :332 only; tests import `buildTools`/`registerTools`), `validate-citation.ts:38` `ParsedCitation`, `get-provision.ts:9` `GetProvisionInput`, `db-info.ts:41` `computeDbFingerprint` (used at :66 same file)
- Caveat: the npm package ships `dist/` with a `main`, so these form an informal library surface; dropping `export` changes nothing at runtime but slightly narrows importability. Only worth doing if you consider `dist` internals private.
- Est.: 0 net. Impact: marginal package surface. Confidence: **high** on consumer-absence (greps), removal itself optional.

### F. Tests & test config

**F1. Coverage gate demonstrably red on the current tree**
- Category: `other` (config correctness; inverse of an unused option)
- Location: `vitest.config.ts:14–15` — `include: ['src/**/*.ts']`, `exclude: ['src/index.ts']`
- Observed: `vitest run --coverage` fails thresholds at 74.44%/87.5%. Drivers: `src/http-server.ts` (an entrypoint that starts a listener on import; imported by no test) pulled in by the glob at 0%; additionally `constants.ts`/`db-info.ts` report 0% statements despite `about.ts` at 100% importing `db-info` — i.e., the v8/tsx attribution looks partially unreliable, so treat "just exclude http-server" as necessary-but-not-proven-sufficient. No CI step runs `test:coverage`, so this only bites locally today.
- What: add `src/http-server.ts` (+probe constants/db-info attribution) to exclude, then re-measure before restoring faith in the gate. Note AGENTS.md/PERFORMANCE-era assumption "100% enforced" currently doesn't hold on this machine.
- Est.: ±1 line. Impact: tests. Confidence: **high** on the failure (observed), **medium** on minimal fix sufficing.

**F2. Unused fixture rows in the shared test DB**
- Category: `delete`
- Location: `tests/helpers/test-db.ts:185–193,207` (provision `doc-inforce`/`s2`, asserted nowhere), `:256–266` (third `eu_references` row `doc-amended`→GDPR, referenced by no assertion — `validateEUCompliance('doc-amended')` is never called; partial/unclear paths use self-inserted rows in `other-tools.test.ts:337,354`)
- Rows confirmed present by direct read. Guard: re-run `npm test` (green expectation) and confirm the 100%-branch objective still exercisable after removal.
- Est.: ~22. Impact: tests only. Confidence: **medium**

**F3. Export keywords on helper symbols nobody imports**
- Category: `dead-API` (trim)
- Location: `tests/helpers/test-db.ts:7` `TestDbOptions`, `:17` `realDbExists` — grep across all suites finds importers of `createTestDb/trackDb/describeIfRealDb/openRealDb/REAL_DATA_DIR` etc., but neither of these two names
- Est.: 0 net. Impact: none. Confidence: **high**

### G. Scripts / data pipeline

**G1. Seed-shape types declared twice and already drifting**
- Category: `deduplicate`
- Location: `scripts/build-db.ts:23–51` vs `scripts/lib/parser.ts:20–47` (e.g. `title` required in parser, optional in build-db)
- What: single source for seed types (parser writes them, build-db reads them); type-only refactor, no `src/` involvement (verified: nothing in `src/` imports from `scripts/`).
- Est.: ~15–25 net. Impact: none at runtime. Confidence: **high**

**G2. Hand-rolled fetch timeout where stdlib exists**
- Category: `stdlib`
- Location: `scripts/check-updates.ts:43–50` — `AbortController` + `setTimeout` + `clearTimeout` → `AbortSignal.timeout(15_000)` (Node ≥18 guaranteed by engines)
- Est.: ~3. Impact: none. Confidence: **high**

**G3. Internal-only exports in parser lib**
- Category: `dead-API`
- Location: `scripts/lib/parser.ts:64` `decodeHtmlEntities`, `:20` `ParsedProvision`, `:28` `ParsedDefinition` — sole importer `ingest.ts` pulls only `{parseHungarianHtml, KEY_HUNGARIAN_ACTS, htmlToText, ActIndexEntry, ParsedAct}`
- Caveat: interacts with G1's direction (types may become deliberately shared). Whichever refactor lands first wins.
- Est.: 0 net. Confidence: **high**

**G4. census.json is internally inconsistent and 99% machine-unread**
- Category: `shrink` — flagged, **not recommended blindly**; recommendation is regenerate, not delete
- Location: `data/census.json` `laws[]` (~1.36 MB). Readers exhaustively traced: `check-updates.ts:129–131` and `golden.test.ts:73–80` read only the top-level totals/jurisdiction.
- Evidence of drift: `laws[]` lists 4,314 ids; 4,326 seed files exist; `total_laws` says 4,326 (verified counts all three ways). So the review surface silently lags the corpus — the known "ingestion doesn't auto-update census" gap materialized.
- Why keep the file: AGENTS.md/README route humans through `git diff data/census.json`; deleting `laws[]` would make such slips invisible.
- Est.: ~25,700 only if overruled — excluded from totals below. Impact: persisted-data review workflow. Confidence: **high** on facts, deletion itself rejected.

**G5. download-db.sh is pinned to the archived upstream's releases**
- Category: `other` (deployment-design fragility)
- Location: `scripts/download-db.sh:4` (`REPO="Ansvar-Systems/Hungarian-law-mcp"`) + `.github/workflows/check-updates.yml:28` (sole caller)
- Facts verified in-repo: script alive (also referenced NOTICE:23); nothing in this repo builds/uploads such an asset. Sub-agent verified via GitHub API that upstream is archived with a single v1.0.0 release — mark that external fact as sub-verified, not independently re-checked. Consequences: works today only because versions coincide; any future version bump points the URL at a nonexistent tag and the daily freshness job fails forever; the check also benchmarks the Mar-2026 snapshot, not this fork's fresher corpus.
- What: owner decision — repoint at this repo's own releases (needs a publish step that doesn't exist), tag a baseline, or pause the schedule.
- Est.: ~0–4. Impact: deployment/persisted-data workflow. Confidence: **high** mechanics / medium design direction.

### H. Deployment/build

**H1. Build context ships the 282 MB database that build:db immediately deletes**
- Category: `shrink`
- Location: `.dockerignore` (no `data/database.db` entry) + `Dockerfile` COPY of `./data` followed by `npm run build:db`, which recreates the DB from `data/seed/` alone
- Bonus: `.dockerignore:11` `__tests__` matches nothing in this repo (tests live in `tests/`) — naming convention copied from another project layout. Line verified by reading the file.
- Est.: −1 +2 lines; ~283 MB out of every build context/cache invalidation. Impact: deployment builds only. Confidence: **high**

**H2. Dependabot covers npm+actions but no docker ecosystem; semgrep engine pin ~2 years old**
- Category: `other` (gap observation)
- Location: `.github/dependabot.yml`; `Dockerfile` (`node:20-alpine`), `semgrep.yml:20` (`semgrep/semgrep:1.79`)
- What: +7 lines for a docker ecosystem block (covers Dockerfile only; workflow `container:` pins need manual bumping regardless).
- Impact: deployment freshness. Confidence: **medium-high**

**H3. Server version lives in 3 unsynced places with no assertion**
- Category: `other` (process risk, not cleanup)
- Location: `package.json:3`, `server.json:10,16`, plus `download-db.sh` deriving its URL from it. Currently synced at 1.0.0 (checked). One `npm version` bump desyncs all three and simultaneously trips G5.
- What: smallest durable fix is a 5-line equality assert in publish.yml.
- Impact: release process. Confidence: **high** on risk shape, low urgency.

---

## CORRECTNESS ISSUES OBSERVED (not cleanup items)

Kept out of the removable tally on principle:

1. **`get_provision` bare-section fuzzy fallback can return the wrong provision** (`get-provision.ts:65–67`): asking for `section:"6"` where `s6` doesn't exist falls back to `%6%`-LIKE and can return `s61`/any containing row. Partly mitigated by status/document scoping; fixing overlaps with E5's shared-helper work. Marked `other — correctness`, needs its own ticket.
2. **Ref-format tolerance differs between validate and retrieve paths** (E5 detail): a citation like `"Törvény 6:272. §"` can validate successfully yet fail to retrieve via `get_provision_eu_basis`. Inconsistency, not necessarily bug; owner call.
3. **Coverage gate red** (F1) is presented as config-vs-reality drift; whether any residual gap hides behind the instrumented-attribution oddity (`constants/db-info` at 0%) should be probed once before trusting future green runs.
4. Daily-check fragility (G5) will turn into a permanently failing cron or false-positive issue the moment versions diverge — functional breakage waiting on a version bump, not undesirable code.

---

## CHECKED BUT KEPT

Investigated, looked suspicious, decided they stay:

- **icon.png** — served live at `/icon.png` (`http-server.ts` icon handler; Dockerfile copies it to `dist/` precisely to hit the fast path). Deleting 404s an advertised endpoint.
- **The 13 MCP tools, 2 prompts, 2 resources** — public contract, each documented and tested in some form. Their handling seam in `registry.ts` (incl. optional `AboutContext`) is prod-superset-of-tests, cheap, and plausibly wanted by library embedders (package exposes a `main`).
- **English-style citation parsers** (`parseHungarianReference`, roman numerals, `"Privacy Act s13"` grammar) — live and tested; only their *usage examples* are wrong (D1). Removing the parsers would remove real functionality.
- **`UUID_RE` hand-rolled instead of crypto.randomUUID validation** — deliberately pins the acceptable UUID variant; commented as an injection guard. Edge-case-correct beats shorter.
- **English/Japanese-style-schema acceptance in a Hungarian DB** — supports the tools' bilingual promise (`hu-law-*` ↔ `"évi N. törvény"` round-trips verified in statute-id + seeds). Intentional design.
- **sources.yml** — parsed by zero code (verified: only a display string `'See sources.yml'` in build-db + doc mentions), duplicating `list-sources.ts` content. Kept anyway: it's the documented human provenance-review artifact in the data-update flow; wiring YAML parsing would *add* a dependency for no gain. Worth a one-line "not parsed; mirrored in list-sources.ts" comment to prevent future accidental coupling.
- **census.json `laws[]` bulk** — 99% unread by code (G4) but it *is* the diff payload for the human data-review ritual; and it just proved its worth by exposing 12 seeds added without census refresh.
- **scripts/download-db.sh & http-smoke.py mainlines** — both referenced by workflows (check-updates.yml:28; docker-publish.yml:88) and NOTICE.
- **compose restating PORT/HUNGARIAN_LAW_DB_PATH/NODE_ENV identically to image ENV** — redundant but self-documenting, values verified equal; `pull_policy: always` + `latest` matches the publish tagging strategy; the LAN port-forward warning encodes real ops guidance.
- **docker-publish's build→smoke→retag dance** — genuinely refuses to ship an unhealthy image rather than trusting build-time tags.
- **semgrep/trivy/scorecard coexistence** — three distinct scanner classes (SAST / CVE / posture); overlap limited to SARIF sink. Whether to maintain all three is org-intent, not redundancy provable from the repo.
- **CI matrix node 18/22** — engines promises ≥18; testing the floor matches the claim even though 18 is EOL upstream (that's an engines-doc decision, not dead config).
- **vitest `pool:'forks'/singleFork/timeout:30_000`** — determinism and amortized startup for suites opening a 280 MB SQLite file; `describeIfRealDb` gating is infra-awareness, not skip-debt (repo has zero xit/skip debt otherwise).
- **Coverage thresholds & `exclude: ['src/index.ts']` concept** — right idea per AGENTS.md; the gap is missing entries, not the mechanism (F1).
- **`tsconfig.build.json` `rootDir:"."` + `include:["src"]`** — load-bearing: produces `dist/src/index.js`, hardcoded in `bin`, `main`, Docker CMD. "Simplifying" flattens the publish layout.
- **`tsconfig.json` strictness options** — one truly dead flag found (`forceConsistentCasingInFileNames`, default-on since TS 5.0) and a belt-and-braces `exclude` shadowing `include`; trimming is cosmetic (~4 lines) and folded into the estimates loosely. Base config otherwise correct for NodeNext ESM.
- **test helper option flags** (`withEuTables/withDefinitionsTable/withMetadataTable`) — each passed `false` somewhere; no dead options.
- **`mockReturnValue` (not Resolved) for about in registry tests** — load-bearing: production deliberately doesn't await; a resolved mock would mask that.
- **Per-`it` `trackDb(createTestDb())` repetition** — style; hoisting obscures per-test option variants and saves nothing.
- **other-tools.test.ts monolith** — dispatch/round-trip/error coverage exists centrally (`registry.test.ts` TOOL_CALLS table) with behavior suites for the heavy hitters; splitting = churn, deletes nothing.
- **Ansvar branding, npm scope, `mcpName`, email contacts in CoC/SECURITY/NOTICE** — consistent fork-of-record identity policy across the repo; changing it is a product decision.
- **`Access-Control-Allow-Headers: Authorization`** (`http-server.ts:233`) with no auth feature — conventional CORS forward-compat; borderline, left in place knowingly.

---

## CHANGED-SINCE-PRIOR-AUDIT RESIDUE

Evidence-only inference from current tree state (no reliance on prior reports):

| Item | Location | Evidence in current repo | Verdict |
|---|---|---|---|
| OAuth scaffolding with no OAuth feature | `http-smoke.py` `--oauth` branch + opener class; `docker-compose.yml:13–15` commented envs; `http-server.ts:233` Authorization CORS header | Server implements no `/oauth/*` route and reads no `OAUTH_*` env; the flag is unpassable, unwired in CI, and the compose block suggests a capability that cannot activate. Classic leftover of a layer designed, scaffold-tested, then removed — or of sibling-project template reuse. | **Definitely stale** (branch/comments); header **suspected** forward-compat |
| Phantom security-scanner prose | `SECURITY.md:26–28`, `SECURITY-SETUP.md:38–45` | References to Gitleaks/CodeQL/Socket/"6 scanners in ci.yml" while those workflows simply do not exist in `.github/workflows/` (complete enumeration above) — text survived whatever removed/never-created those workflows | **Definitely stale** |
| Template-placeholder tool heading | `TOOLS.md:127` `get_{jurisdiction}_implementations` | Naming idiom matches no tool; siblings renamed (`get_hungarian_implementations` in code since the same section's params reference it) | **Definitely stale** |
| Upstream-repo URLs amid a fork-consistent identity | `http-server.ts:72`, `CHANGELOG.md:26`, `PRIVACY.md:272`(conflicted hunk), `download-db.sh:4`, NOTICE links | Every surrounding identity literal points at ryzen3100/ansvar.eu; these point at Ansvar-Systems upstream. Whether deliberate attribution or missed sweep varies per file (NOTICE/attribution = deliberate; server-card + changelog link = **suspected missed**) |
| Polish-law issue template | `data-error.md` throughout | Language/jurisdiction/source-portal all PL in HU repo | **Definitely stale** (copy residue) |
| Pre-refresh numeric snapshots | `REGISTRY.md` 4314/130124; `README.md:85` "109"; `sources.yml` "weekly"/2026-02-21 | Census regenerated 2026-08-21 says 4326/130220; DB says 92 EU refs; CI cron is daily — docs froze at an older data state and never caught up | **Definitely stale** numbers |
| census.json `laws[]` lagging seeds by 12 | `data/census.json` | File self-inconsistent (laws[] 4314 < total_laws 4326 == 4,326 seeds on disk) — residue of ingestion rounds applied without the documented manual census refresh | **Definitely stale**, fix by regeneration |
| `__tests__` in .dockerignore | `.dockerignore:11` | Repo uses `tests/`; no path matches | **Suspected stale** (foreign-layout copy) |
| Coverage exclude list missing an entrypoint | `vitest.config.ts:15` | Excludes only `index.ts` while `http-server.ts` (second entrypoint) sails in at 0% and reds the gate — baseline predates or ignored the second entrypoint | **Suspected stale** (gate is empirically red) |

---

## SUMMARY

**A. Total findings:** 33 (of which 4 are explicitly owner-decision observations rather than mechanical cleanups, and the correctness section holds 4 more excluded from the count)

**B. Estimated total lines removable/tightenable:** **≈330–400** (docs conflict-marker resolution ~90–150, dead docs/templates ~170, OAuth script residue ~58, test fixtures ~22, misc code/config ~25) — *excluding* the deliberately-not-recommended census.json `laws[]` truncation (~25.7k)

**C. High-confidence findings:** **20** (A1, A2, B1–B6, B8, C1, C2, D1–D3, E1, E3-half, E6, F1-failure, F3, G1–G3, G5-mechanics, H1)

**D. Findings touching behavior/API/data/schema/tests/deployment:** 17 affect deployment or tests; **2** touch the public MCP surface (D1 descriptions, E6 export surface); **0** require DB-schema or persisted-data migration; runtime-behavior changes are limited to the one-line server-card payload fix (C2) and the future provision-ref semantics decision (E5/correctness §2)

**E. Top 10 by confidence × value:**
1. Resolve committed merge markers in PRIVACY.md / DISCLAIMER.md (A1, A2)
2. Delete `--oauth` branch + redirect machinery in http-smoke.py (C1)
3. Rewrite/replace the Polish-law data-error issue template (B1)
4. Sync SECURITY.md + SECURITY-SETUP.md to the scanner reality (B2, B3)
5. Fix misleading `Privacy Act`/`SOCI` examples in MCP tool descriptions (D1) — tiny diff, directly improves client behavior
6. Correct the served server-card homepage URL (C2)
7. Delete PERFORMANCE.md (executed plan posing as backlog) (B4)
8. Repair the coverage gate red on current tree (F1)
9. Update TOOLS.md parameter drift + placeholder heading (D2)
10. Exclude data/database.db from Docker build context (H1)

**F. Completeness assessment:** All ~70 non-data source/config files were read in full by area audits with mandated cross-file proof; all 27 tracked docs were fact-checked against live code/DB; the four highest-severity classes (conflict markers, phantom scanners, OAuth residue, schema-copy errors) were independently re-verified, as were the test/contract/coverage executions and four SQL ground-truth queries. Not verified: GitHub-side facts inherited from a sub-agent's API probes (upstream archive status, asset existence, CODEOWNERS team resolution); end-to-end HTTP runtime behavior (no server was started); whether the minimal F1 fix fully restores the 100% gate given the apparent v8 attribution quirk; anything requiring network access to njt.hu; and consumer-side assumptions about who imports the published `dist/` internals. Most likely blind spots remaining: string-built identifiers buried inside seed-data content conventions, external MCP-client reliance on the exact description wording slated for correction, and organization-level GitHub settings (branch protection, secret wiring) invisible to the repo.

---
---

# RE-REVIEW ADDENDUM — codebase-memory-mcp graph-assisted pass (2026-08-27)

Method: the repository was re-examined through the indexed knowledge graph (`home-laci-Documents-Coding-magyar-jogszabaly-mcp`; 63,542 nodes / 67,769 edges, 0 parse failures, all source files covered) via schema inspection, architecture overview, and targeted Cypher: zero-inbound-export scans (Functions/Interfaces/Types), cross-directory `IMPORTS` edges, `EnvVar` nodes, `Route`/`HTTP_CALLS` inventory, `SEMANTICALLY_RELATED` pairs, and complexity/loop-depth hot-path queries. Critical caveat established up front: CALLS/USAGE extraction is sparse for this codebase (182 CALLS edges repo-wide), so *absence* of an inbound edge proves nothing — the 81 "zero-cross-file-consumer" functions returned include obviously live code (`buildDatabase`, `createMCPServer`, `sweepIdleSessions`, `deduplicateResults`). The graph can therefore corroborate positives (edges found) but never alone establish death; all dead-code verdicts below remain grep/manual-based, with the graph as independent confirmation.

## 1. Corroborations (audit claims confirmed by an independent method)

| Claim | Graph evidence |
|---|---|
| E6/F3/G3 dead exports (`TOOLS`, `computeDbFingerprint`, `decodeHtmlEntities`, `realDbExists`) | Inbound `USAGE`/`CALLS` edges exist **only from the symbol's own file** (registry.ts ×2 for TOOLS; db-info.ts ×1; parser.ts ×1; test-db.ts ×1) — zero cross-file consumers anywhere in the graph |
| G1 premise "no `src/` imports from `scripts/`" | Zero `IMPORTS` edges between `src/`↔`scripts/`, `src/`↔`tests/`, or `tests/`→`scripts/` |
| B6 / env inventory (only `PORT` + `HUNGARIAN_LAW_DB_PATH`; no `OAUTH_*`/`BASE_URL`) | Graph-wide `EnvVar` extraction finds exactly two literal reads — `PORT` (src) and `HTTP_SMOKE_URL` (python client). No `OAUTH_ENABLED`/`BASE_URL` node exists. Notably the graph *misses* `HUNGARIAN_LAW_DB_PATH` because it's read via `process.env[DB_ENV_VAR]` bracket access — direct confirmation that dynamic-access searching (done manually in the original audit) is mandatory and was necessary |
| C1 (no server-side OAuth) | The only 3 `Route` nodes and all 3 `HTTP_CALLS` edges in the entire graph are the smoke-test client's URL strings (`/oauth/register`, `/oauth/token`, `/.well-known/oauth-protected-resource`, all in `http-smoke.py`). No server handler node references any OAuth route |

## 2. Confidence upgrades (manually verified during re-review)

- **E3 (queryStrategy dead states): medium → high.** Direct read of `search-legislation.ts:63,99,126` confirms: `'none'` is initialized but unobservable in any returned value; `'exact'` is assigned but never compared; only `=== 'fallback'` (line 126) affects output. Inlining as `ftsQuery !== queryVariants[0]` is behavior-identical. ~4 lines.
- **E4 (fts-query duplicate multi-term branch): medium → high.** Direct read of `fts-query.ts:54–72` confirms the second `if (terms.length > 1)` guard (:70) duplicates the first (:54); the OR-fallback push can move into the first block. ~3 lines.

## 3. New findings surfaced by the graph

**N1. `http-server.ts` `main()` is the repo's complexity peak — optional shrink observation**
- Category: `shrink` (maintainability; owner's call — not dead code)
- Location: `src/http-server.ts` (`main`: complexity 31, cognitive 95 — highest in the repository; runner-up `parseHungarianHtml` cx 23 is offline pipeline code)
- What: if the HTTP server keeps growing, splitting `main` into per-concern handlers (session mgmt / routing / lifecycle) would cap the hotspot. The original audit already documented the deliberate ceilings inside it (`ponytail:` session-cap comment), so this is a "watch it" note, not a cleanup directive.
- Est. reduction: 0 (pure restructuring). Impact: none immediate. Confidence: **medium** (metric-driven; no behavioral defect implied)

**N2. Semantic-similarity lead investigated and rejected** *(recorded for transparency)*
- The graph flagged `parseSectionFromText` ↔ `parseArticleFromText` (`scripts/lib/parser.ts:138–152`) at 0.98 similarity — the only ≥0.9 same-file pair repo-wide. Direct read shows two 6-line twins differing by exactly one regex (§ vs Cikk/Article). Merging saves ~3 lines but replaces two obvious functions with a regex-parameterized helper. **Rejected as a dedup finding** — similarity score alone is not duplication worth removing. Other ≥0.75 pairs are the expected intra-tool-family relationships (EU tools, resolve-document-envelope users) already covered by E5.

## 4. Differences vs. the original audit (summary)

- **Retracted findings: none.** Every graph-checkable claim held up.
- **Upgraded:** E3 and E4 from medium → high confidence (now directly code-verified).
- **Added:** N1 (low-priority shrink observation on `http-server.ts main()` complexity); totals in the SUMMARY section above are unchanged in substance — finding count stays 33 cleanup findings + 4 correctness items + 1 new observation (N1), removable-line estimate stays ≈330–400 (N1 removes no lines).
- **Rejected lead:** N2 (parser twin functions) — investigated, not a finding.
- **Tool-fit conclusion:** codebase-memory-mcp is strong for *corroboration* (callers found, import topology, env/route inventories, similarity candidates, complexity hotspots) and it independently caught the same OAuth/env/import facts as the manual audit; it is *not* sufficient for dead-code proof here due to sparse CALLS/USAGE extraction for TypeScript, missing bracket-access env reads, and no modeling of docs/workflows/Docker — the domains where most of this repo's actual waste lives. The original audit's grep-and-run methodology remains the load-bearing evidence; the graph pass raised confidence on the code-level findings and found nothing that contradicts them.

# Audit v3 — Remediation TODO

Source: `AUDIT_V3.md` (2026-08-26 audit + 2026-08-27 graph re-review addendum).

## Tier 1 — mechanical fixes (this pass)

- [x] **A1** Resolve 11 committed merge-conflict hunks in `PRIVACY.md`
- [x] **A2** Resolve 16 committed merge-conflict hunks in `DISCLAIMER.md`
- [x] **B1** Rewrite `.github/ISSUE_TEMPLATE/data-error.md` for Hungarian law (was Polish)
- [x] **B2** Fix `SECURITY.md:26–28` phantom scanners (Socket/Gitleaks/CodeQL don't exist)
- [x] **B3** Fix `.github/SECURITY-SETUP.md` (remove Azure Key Vault/mcp-publisher flow, "6 scanners in ci.yml")
- [x] **B4** Delete `PERFORMANCE.md` (executed plan posing as open checklist)
- [x] **B5** Delete `REGISTRY.md` (stale 4314/130124 numbers, duplicate of package.json/server.json) — *decision default; can be restored+synced if registry memo wanted*
- [x] **B6** Remove commented `OAUTH_ENABLED`/`BASE_URL` from `docker-compose.yml`
- [x] **B8** Remove `npm audit --omit dev || true` from `publish.yml:31`
- [x] **C1** Remove dead `--oauth` branch + redirect machinery from `scripts/http-smoke.py`
- [x] **C2** Fix server-card homepage → fork-consistent URL (`src/http-server.ts:72`)
- [x] **D1** Replace unresolvable "Privacy Act 1988"/"SOCI" examples in MCP descriptions (`registry.ts`, `validate-citation.ts` docstring, `statute-id.ts` docstrings) with corpus-resolving IDs
- [x] **D2** Fix `TOOLS.md`: placeholder heading §10, 6 tools' missing params; `README.md:103` format_citation formats
- [x] **D3** Fix `CONTRIBUTING.md:27` Zod claim (project uses plain JSON-Schema)
- [x] **H1** `.dockerignore`: exclude `data/database.db*` from build context, drop dead `__tests__` line
- [x] **Verify** `npm run lint` + `npm test` + `npm run test:contract` + `npm run build` + boot HTTP server and smoke it (`/health`, MCP initialize, `search_legislation`, server-card)

### Verification log (2026-08-27)

- `npm run lint` (tsc --noEmit): clean
- `npm test`: 6 files, **85/85 passed**
- `npm run test:contract` (real 282 MB DB): **22/22 passed**
- `npm run build`: clean
- Live server (`node dist/src/http-server.js`, PORT=3199, real DB):
  - `/health` → `{"status":"ok","version":"1.0.0"}`
  - server card → homepage `https://ansvar.eu`, zero "Ansvar-Systems" residue
  - `python3 scripts/http-smoke.py` → **passed** (initialize + search_legislation, post-`--oauth`-removal)
  - MCP `validate_citation` with the three NEW schema examples → all `valid: true`
    (note: `"… s3"` without a space does NOT parse — examples use the verified `"s 3"` form)
  - MCP `get_eu_basis` on `act-cxii-2011-info-self-determination` → real EU refs returned
- Example-truthfulness source: live SQLite queries (Infotörvény = `act-cxii-2011-info-self-determination`, implements GDPR; **no NIS2 mapping exists in the corpus**, so no NIS2 example was written)

## Tier 2 — needs an owner decision (NOT in this pass)

- [ ] **B7** CI/SAST triggers fire on `main` only while PRs target `dev` — add `dev` to triggers (recommended) or change CONTRIBUTING
- [ ] **G5** `download-db.sh` pinned to archived upstream releases — needs a release/tag strategy (works today by version coincidence; breaks on any version bump)
- [ ] **G4** Regenerate `census.json` `laws[]` (4314 listed vs 4326 seeds/total) on next data pass — never hand-edit
- [ ] **E5 + correctness §1–2** Unify provision-ref lookup tolerances across validate/retrieve tools (incl. `get_provision` fuzzy `%ref%` fallback returning wrong provisions) — behavior decision first
- [ ] **H2** Add dependabot docker ecosystem; bump ancient `semgrep/semgrep:1.79` pin
- [ ] **H3** Version-sync assertion (package.json ↔ server.json ↔ download URL) in publish.yml

## Tier 3 — touch only when already editing that file

- [ ] **E1** Hoist duplicated annotations literal in `registry.ts` (13×)
- [ ] **E3** Inline dead-state `queryStrategy` in `search-legislation.ts:63,99,126`
- [ ] **E4** Fold duplicate `terms.length > 1` branch in `fts-query.ts:54–72`
- [ ] **F2** Drop unused fixture rows (`test-db.ts` p2/s2, third eu_reference) + re-run tests
- [ ] **F3/G3** Un-export `TestDbOptions`/`realDbExists`; parser lib internals
- [ ] **G1** Single-source seed-shape types (build-db.ts vs lib/parser.ts)
- [ ] **G2** `check-updates.ts`: `AbortSignal.timeout(15_000)`
- [ ] **N1** Split `http-server.ts` `main()` (cx 31 / cognitive 95) when it next grows

## Explicitly skipped

- **E6** export-keyword trims (`TOOLS`, `ParsedCitation`, `GetProvisionInput`, `computeDbFingerprint`) — `dist/` is importable; keywords cost nothing
- **G4-deletion** truncating census `laws[]` — it is the human data-review diff surface
- sources.yml, census bulk, branding/identity, compose env restatements — see AUDIT_V3.md "CHECKED BUT KEPT"

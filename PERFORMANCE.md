# Performance notes & plan

Measured on dev @4f5e057 against the real 295 MB database (4,326 documents / 130,220 provisions).
Round 1: solo investigation. Round 2: four parallel audit workers. All numbers below are measured, not estimated.

## Findings (ranked)

| # | Sev | Finding | Evidence | Fix |
|---|-----|---------|----------|-----|
| 1 | P1 | Session Map grows unbounded (`src/http-server.ts`) | ~68 KB/abandoned session; garbage POSTs leak ~115 KB each; +19.9 MB RSS / 300 sessions; no TTL/cap | TTL/LRU eviction |
| 2 | P1 | High-fanout FTS queries seconds-slow (`search-legislation.ts`) | `'a'` median 1,468 ms; `'és'` 323 ms — bm25 sort + snippet over thousands of matched rows | Rank first, fetch later, snippet last |
| 3 | P1 | `get_eu_basis` article N+1 | 19 groups -> +10 ms (9.26 -> 19.19 ms e2e); combined query = 1.37 ms | One query + JS grouping |
| 4 | P2 | `JSON.stringify(…, null, 2)` pretty-print in tool responses (`registry.ts`) | +7.3 ms CPU, +4% bytes on largest response | Drop indent |
| 5 | P2 | Immutable facts recomputed per call | COUNT(*)s ARE about/list_sources calls (4.7/6.7 ms); `/health` probe 3.53 ms = 65% e2e; `readDbMetadata` 0.55 ms x25 sites; `euAvailable` 45–55% of small EU calls; icon.png 260 KB reread/request | Cache once (DB is readonly) |
| 6 | P2 | `resolveDocumentId` fuzzy miss 17 ms | Two sequential 3-column LIKE scans; second `LOWER()` pass redundant for ASCII (LIKE already folds case) | One merged LOWER() pass |
| 7 | P2 | OR-chains defeat cheap filtering | validate_citation 6-way OR 3.51 ms vs 0.83 ms exact-only; miss scans whole doc (7.02 ms) | Exact-first, LIKE on miss |
| 8 | P2 | `snippet()` costs ~40x raw content read | tok=32: 43.3 ms vs 0.8 ms raw for hot-20 rows | Snippet only final deduped rows |
| 9 | P3 | Missing composite index `(document_id, provision_ref)` | Lookups filter every doc row post-index (worst 1,599) | Add at next data rebuild |
| 10 | P3 | Cold boot 550 ms | `NODE_COMPILE_CACHE` -> 445 ms | Env line when touching Docker |
| 11 | — | Payload: max provision 662 KB, 166 > 100 KB | API design decision | Deferred |
| 12 | — | WASM driver 2-4x slower than native | Architecture decision | Deferred |

## Plan

### Tier 1 — do now (low risk)
- [ ] 1. Session TTL/LRU eviction (cap + oldest-evict + idle sweep)
- [ ] 4. Drop pretty-printing in tool responses
- [ ] 5. Startup caches: counts, built_at metadata, EU availability, icon buffer
- [ ] 3. `get_eu_basis`: single combined article query
- [ ] 6. `resolveDocumentId`: single merged LOWER() scan

### Tier 2 — worth doing (needs care)
- [ ] 2+8. Search restructure: phase A ranks WITHOUT snippet -> dedupe -> phase B fetches snippets via `MATCH ? AND rowid IN (...)` with the same query string (bare rowid IN drops highlight markers — not byte-identical; MATCH+IN measured 20 ms / identical output; naive joinless re-MATCH variant exploded to 18.7 s on `'a*'`). Golden contract suite must stay byte-identical.

### Deferred
- 9 composite index (next data rebuild), 10 NODE_COMPILE_CACHE, 11 pagination (API decision), 12 native driver swap, 7 exact-first OR-chains (bundle with Tier 2 follow-up).

## Expected effect
Typical tool call −10 ms fixed overhead; searches 35–45 ms -> ~15–18 ms; pathological queries seconds -> sub-400 ms; OOM risk eliminated.

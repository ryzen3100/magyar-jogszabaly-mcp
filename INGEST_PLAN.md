# Full-corpus ingestion plan — data/update track

Status: **in progress on branch `data/full-corpus-2026-08-30` (goal mode,
started 2026-08-30).** Scope: the complete njt.hu register. Author-type census
(live probe 2026-08-30, one search walk per dropdown code): **273 types,
~82,400 docs** — határozatok ~36,800 (Korm. határozat ~14,050, KE ~11,900, ME
~3,300, OGY ~2,850, AB ~2,200), rendeletek ~25,550 (Korm. rendelet ~10,400,
MNB ~900, BM ~1,200, plus the ministerial long tail), utasítások ~12,800,
törvény family ~4,600, közlemények/helyesbítések ~2,600. `DefaultAuthorTypes`
now carries all 273 codes. Also fixed while starting this run: the ingest
fetch path now stores pages stripped to the law-content region
(`stripHTMLBody`, ~45% smaller cache), which surfaced and fixed a pre-existing
parser bug inherited from the TS original — the last provision of 8,158/14,731
docs (55%) swallowed page footer/scripts (the `njtConfig` JS block) into its
content; re-parsing the stripped cache cleans those seeds.

Original status: **planned, not started.** Tackle as a dedicated effort — this is the
biggest job the repo does. Written 2026-08-29 after the coffee-shop question
test: the current corpus answers only from parliamentary acts, so questions
whose real answers live in government decrees (e.g. 210/2009. (IX. 29.) Korm.
rendelet, 531/2017. (XII. 29.) Korm. rendelet — kiskereskedelmi/üzletnyitás)
cannot be answered at all. Verified against the DB: of 4,326 documents, only 2
mention "Korm. rendelet" in the title.

## Root cause: the discovery regex silently drops every Korm. rendelet (verified)

Found 2026-08-29 while auditing the upstream Ansvar-Systems repositories
(`Hungarian-law-mcp` and its four sector siblings). Two facts, then the fix:

1. **Even the upstream full-corpus artifacts contain zero Korm. rendeletek.**
   Their git-tracked `data/seed/` (4,314 files) and their published release DB
   (296 MB on disk, 4,314 docs / 130,124 provisions — downloaded from
   `gh release download -R Ansvar-Systems/Hungarian-law-mcp`, checked in
   SQLite) hold only parliamentary acts. This is not a fork defect; the TS
   original never scraped decrees either.
2. **Why:** njt.hu stores *all* statute types in one document-ID space,
   `YYYY-N-SS-EE`. Parliamentary acts are `YYYY-N-00-00` (2012. évi I. törvény
   → `2012-1-00-00`); Korm. rendeletek reuse the same year+number scheme with
   non-zero block numbers (210/2009. (IX. 29.) Korm. rendelet →
   `2009-210-20-22`, URL `https://njt.hu/jogszabaly/2009-210-20-22` — verified
   live). The discovery search (`author_type: "0000"` = all document types)
   *does* return decrees, but the result parser keeps only IDs matching
   `[0-9]{4}-[0-9A-Z]+-00-00` — `internal/ingest/discovery.go:70-72`
   (`mainLinkPattern`), ported verbatim from the TS `parseSearchResultPage`.
   Every rendelet hit was therefore downloaded, seen, and thrown away by the
   regex.

**Fix (code change, separate PR to `dev` before any data run):** widen
`mainLinkPattern` to accept the full `YYYY-N-SS-EE` space (last two groups
`[0-9A-Z]{1,2}` instead of literal `00-00`), and decide the scope of what the
widened discovery keeps:

- Everything (Korm. rendeletek, miniszteri rendeletek, egyéb jogszabályok) —
  the honest full corpus, but njt.hu's complete index is an order of magnitude
  larger than 4.3k docs; expect the fetch phase to grow from hours to days and
  the seed tree to grow by tens of thousands of files.
- Korm. rendeletek only (minimum for the acceptance questions): try a
  dedicated search with the jogalkotó/type filter set to Kormány instead of
  `author_type: "0000"`, or post-filter discovered IDs by their document-page
  title. Probe the exact filter key against
  `https://www.njt.hu/ajax/get_search_url.json` first (plain `curl -L` needs
  the identifying UA; an unauthenticated probe returned `{"success":false}`).

Everything else in the pipeline already handles the widened IDs: seed naming
is `hu-law-` + lowercased doc ID, block hydration (`njtGetBlock.json`) is
ID-agnostic, and `build-db` is seed-driven.

## Upstream repo audit (2026-08-29) — what we can and cannot reuse

Cloned to /tmp and inspected all five Ansvar-Systems repos:

| Repo | Data in repo? | Reusable? |
|---|---|---|
| `Hungarian-law-mcp` | yes — 4,314 seed JSONs (206 MB), same corpus as ours | no new data (it's our corpus minus rendeletek) |
| `hungarian-financial-regulation-mcp` | no — mnb.hu scraper only, DB in private release | no |
| `hungarian-competition-mcp` | no — gvh.hu scraper only, fake sample rows | no |
| `hungarian-cybersecurity-mcp` | no — nki/cert.hu scraper only | no |
| `hungarian-data-protection-mcp` | no — naih.hu PDF scraper only | no |

The law-mcp release DB was the one plausible shortcut and it is ruled out: it
contains the same 4,314 acts and zero Korm. rendeletek. The only genuinely
valuable takeaways are (a) the confirmation above that the TS scraper had the
same discovery bug, and (b) that all five siblings share the polite-crawler
pattern we already ported (1.2–1.5 s rate limit, identifying UA, backoff,
metadata-only preservation).

## How this was found

While testing the deployed dev container through its MCP tools, the
natural-language acceptance question was:

> **"Milyen engedély kell ahhoz, hogy nyissak egy kávézót?"**
> *(What permits do I need to open a coffee shop?)*

The search returned 5 results — all parliamentary acts matched on the generic
token `engedély` (energetika, szőlőtelepítés, távhő, hulladékgazdálkodás,
harmadik országbeli munkavállalás) — none of them the actual regulating
legislation. The expected answers (known ground truth) were:

- 210/2009. (IX. 29.) Korm. rendelet 1. §, 6. § és 22. §
- 531/2017. (XII. 29.) Korm. rendelet 1. §

Direct SQLite checks against `data/database.db` then showed the real cause —
not search ranking, but missing data:

```sql
SELECT id, title FROM legal_documents
WHERE id LIKE 'hu-law-2009-210%' OR title LIKE '%210/2009%';   -- 0 rows
SELECT id, title FROM legal_documents
WHERE id LIKE 'hu-law-2017-531%' OR title LIKE '%531/2017%';   -- 0 rows
SELECT type, COUNT(*) FROM legal_documents GROUP BY type;      -- statute: 4326
SELECT COUNT(*) FROM legal_documents
WHERE title LIKE '%Korm. rendelet%';                           -- 2
```

The decrees that regulate café/restaurant opening were never ingested: the
seed corpus was built from the **curated acts discovery**, not the full
njt.hu corpus. Only a full-corpus ingest (below) can close the gap.

## Goal

Extend the corpus from the curated act set to the **full njt.hu corpus**
including Korm. rendeletek, then rebuild the DB, so provision lookups and
search cover decree-level law.

## Steps (per AGENTS.md — data updates only on a dedicated branch)

1. `git checkout -b data/update-YYYY-MM-DD go-port` (branch off the latest
   `go-port`; PRs for data updates target `dev` per CONTRIBUTING.md).
2. `go run ./cmd/ingest -full -resume` — networked, rate-limited. **Requires
   the discovery-regex fix from the "Root cause" section first** (without it
   the run re-fetches 4.3k acts and still yields zero rendeletek). Budget
   **hours** for the acts-plus-Korm.-rendelet scope, potentially **days** if
   the widened discovery keeps the full njt.hu index. Flags worth
   considering: `-refresh-discovery` (mandatory on the first run after the
   regex fix, or the stale all-acts discovery cache is reused), `-skip-fetch`
   only when reusing cached HTML.
3. Review by hand: `data/seed/` (file count, a few spot reads),
   `data/census.json`, `sources.yml`. Ingestion does NOT update the census or
   source metadata automatically. Never fabricate text when the source is
   metadata-only.
4. `go run ./cmd/build-db` — expect the DB to grow well past 282 MB.
5. `go vet ./... && go test ./... && go run ./cmd/check-updates`
   (check-updates needs network; exit 0 = current).
6. Sanity-test through the dev Docker deployment: rebuild the image, recreate
   the container with a **fresh volume**, and re-run the acceptance questions:
   - "Milyen engedély kell ahhoz, hogy nyissak egy kávézót?" → should surface
     210/2009. (IX. 29.) Korm. rendelet 1. §, 6. §, 22. § and 531/2017.
     (XII. 29.) Korm. rendelet 1. §.
   - "Hány nap szabadság jár egy 42 éves munkavállalónak?" → should surface
     Mt. 115–117. § (2012. évi I. törvény).
7. PR → `dev` (never push directly to `main`).

## Starting state: nothing is cached — everything must be scraped

Verified 2026-08-29 against the data folder:

- `data/seed/`: 4,326 files, all parliamentary acts. The 38 files whose title
  contains "rendelet" are törvények that *mention* decrees (plus a 1945
  conversion act) — **zero actual Korm. rendelet seeds**.
- `data/source/`: does not exist — no cached HTML from any previous full run.
- Only `census.json` and `eu-mappings.json` describe the current act-only
  corpus.

Consequence: the first `-full` run does **everything over the network** —
discovery of the full njt.hu corpus plus rate-limited fetching of every
document. `-resume` only helps on re-runs after an interruption. There is no
offline shortcut.

## Accepted costs

- **Repo size**: every seed JSON is committed; even the acts-plus-decrees
  scope adds thousands of seed files on top of the current 4,326 acts, the
  full-index scope adds tens of thousands.
- **Time**: the `-full` discovery/fetch is rate-limited against njt.hu;
  hours to days depending on scope, run in the background, `-resume` makes it
  restartable.
- **DB size**: more documents → bigger baked Docker image (the upstream
  acts-only release DB is already 296 MB).

## Known follow-ups that pair well with the new corpus

- **FTS ranking on the full corpus (blocking for end-user quality, code fix)**:
  post-merge MCP testing (2026-08-31, full 72k DB) shows natural-language
  questions ("Milyen engedély kell egy kávézóhoz?", "hány nap szabadság…",
  "partnerem nem fizet…") rank noise (KE/OGY határozatok, utasítások,
  AB határozatok, old rendeletek) in the top 30 while the target acts
  (210/2009, Mt. 2012, Ptk.) are absent; keyword-form queries rank correctly.
  Pre-corpus the same questions ranked fine. Fix = query-layer weights:
  doc-type boost (acts/törvények over utasítás/határozat), in-force boost,
  title-match boost.
- **Consolidation / currency gap (blocking for legal accuracy, data fix)**:
  found via the kifőzde question. The current framework docs are missing
  while their repealed predecessors are present and marked in_force:
  - `73/2016. (XII. 2.) Korm. rendelet` (current exec rendelet for
    commercial/hospitality activities — defines vendéglátási egység types
    incl. kifőzde) → "Document not found"; only repealed 210/2009 present,
    and `validate_citation` reports it `in_force`.
  - "2016. évi XIV. törvény" resolves to the Mongolia visa-treaty
    promulgation act, not the kereskedelmi törvény — the current act is not
    retrievable by citation.
  - Jöt. (2016. évi LXVIII.) stored text appears to be the base publication:
    later-inserted provisions (e.g. kifőzde/házipálinka rules) don't FTS.
  Root cause hypothesis: the ingest stores the njt.hu base-publication text
  plus per-doc status metadata, without follow-up amendments or consolidated
  currency status. Fix direction: ingest consolidated versions (njt
  "hatályos szöveg") or merge amendment acts, and derive in_force status from
  repeal data rather than publication metadata.
- OR-tier ranking precision: per-document matched-term-count boosting
  (natural questions recall the right content but rank generic-token-heavy
  docs first — see the `ponytail:` ceiling note in `internal/fts`).
- Btk part-prefixed citations: corpus stores Btk sections without the
  "6:" part prefix, so "6:272. §" validates as not-found; a prefix-drop
  fallback candidate in `statute.SectionRefCandidates` would cover it.
- Definitions extraction for the new njt layout — narrower than it sounds.
  Audit 2026-08-30 (seed-vs-Ansvar-DB comparison, all 4,314 overlap docs):
  provision content is byte-identical to Ansvar's corpus (130,124 provisions,
  0 diffs); the overlap corpus loses no definitions (5,099 vs 5,087). Of the
  10,417 new-layout docs, 9,465 have 0 definitions but ~98.5% genuinely
  contain none (amendment/promulgation decrees); only ~142 docs (~1.4%) have
  definitions in the scraped HTML that go unextracted, and the text usually
  survives inside provision content (e.g. `hu-law-2024-35-20-22` s157) — a
  classification loss, not a text loss. Fix = add the definition-phrase
  pattern ("alkalmazásában … minősül/érti") to the parser, not a rewrite.
- Idea (uncommitted, just a thought): a `data/source/*.html` → `*.md` pass for
  LLM/RAG use — extract the law-only `#jogszab` region (drops the ~45% Angular
  boilerplate) and map the semantic classes (`szakasz-jel`, `fejezetCim`) to
  headings; no server "print view" exists (it's client-side DOM cloning), so
  offline extraction of the already-scraped HTML is the only path.

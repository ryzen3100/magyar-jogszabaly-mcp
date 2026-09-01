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

- **FTS ranking on the full corpus (blocking for end-user quality, code fix)** —
  FIXED 2026-08-31 on `fix/fts-ranking-weights`: query-layer weights (doc-type
  boost for törvény/rendelet over utasítás/határozat, in-force boost, title-match
  boost) plus prefixed OR tiers and document-level idf re-ranking. Acceptance:
  kávézó/szabadság/bankjegy questions and keyword forms pass; the "partnerem nem
  fizet" question still misses Ptk. (base text lacks the "elvégzett munkáért"
  phrasing — pairs with the consolidation gap below):
  post-merge MCP testing (2026-08-31, full 72k DB) shows natural-language
  questions ("Milyen engedély kell egy kávézóhoz?", "hány nap szabadság…",
  "partnerem nem fizet…") rank noise (KE/OGY határozatok, utasítások,
  AB határozatok, old rendeletek) in the top 30 while the target acts
  (210/2009, Mt. 2012, Ptk.) are absent; keyword-form queries rank correctly.
  Pre-corpus the same questions ranked fine. Fix = query-layer weights:
  doc-type boost (acts/törvények over utasítás/határozat), in-force boost,
  title-match boost.
- **Consolidation / currency gap — INVESTIGATED & CORRECTED 2026-09-01
  (`fix/status-and-discovery-gaps`); the original entry's root-cause
  hypothesis was wrong on all three points. Evidence: live probes against
  njt.hu through the existing rate-limited ingest machinery (2026-09-01);
  discovery cache `data/source/law-discovery-all-sha256-1a99c73d8e7173fc.json`
  (72,922 laws, walked 2026-08-30).**
  - **Sub-item 1 — 978 empty-status seeds: FIXED.** The offline definitions
    re-parse (commit `2fb1b312b`) regenerated 978 seed files with
    `"status": ""` (the field existed with real values in the discovery
    cache for all 978; distribution: 674 repealed / 304 in_force).
    `build.go` was silently forcing empty → `in_force` at build time, so the
    true njt.hu status was unknown. Fix: statuses merged back from the
    discovery cache instead of a 978-fetch re-run — a 30-doc stratified live
    sample (20 repealed + 10 in_force, spread over 1990–2024) verified via
    per-document evszam+sorszam searches showed **30/30 exact agreement**
    between the cache and njt.hu's current classification (0 mismatches, 0
    not-listed), so the cache merge is equivalent to a re-fetch without the
    network load. Seeds re-swept after the patch: 0 empty statuses remain;
    `data/database.db` rebuilt; `check_currency` echoes the restored
    statuses. The `build.go` empty-status default stays as a safety net
    (comment updated): the `legal_documents.status` CHECK constraint would
    abort the whole build on a single empty seed, and offline re-parses are
    a recurring repo practice.
  - **Sub-item 2 — njt.hu consolidated text ("hatályos szöveg"):
    INVESTIGATED, no fetch mode needed — the plain document page already
    serves it.** Verified live: `GET /jogszabaly/<id>` server-renders the
    **current consolidated** text with változásjelző change-footnotes
    (e.g. 210/2009's page shows the 4. melléklet as "megállapított" by
    457/2017 and 2026-dated amendment footnotes), not the base publication —
    the entry's "stored text = base publication" premise is disproven: the
    Jöt. seed body matches today's live page (same "főzde" wording; same
    post-publication provisions). Versioned time-states exist but are
    client-side only: the Angular app lists time-states via
    `POST /ajax/collectAllDocumentVersion.json` (form-encoded
    `documentId=<id>`, returns `{data:[{version, comingIntoForce, expiresOn,
    current}]}`; Jöt. 2016 has 61 versions) and navigates to
    `/jogszabaly/<id>.<version>` — but that URL serves the same server HTML
    (byte-identical except the page-bar ID) and `njtGetBlock.json` ignores
    the version suffix (identical responses for base/.60/.61), so per-version
    text is assembled in the browser and **cannot be scraped server-side;
    there is no server-side consolidated endpoint to implement a fetch mode
    against.** The "kifőzde/házipálinka missing from Jöt." premise also
    dissolves: kifőzde never appears in Jöt. (the word belongs to
    institutional-kitchen decrees), and the household-distilling rules are
    present in the current Jöt. under "főzde" wording (4 hits, also in the
    seed). **Real defect found instead (follow-up): annex blocks
    (`mellekletCimke`/`mellekletTitle`/`mellekletPont`, jhIds
    `ME<n>@…`) are not recognized by `accumulateSections`, so every
    document's mellékletek are dumped into the LAST §-provision (210/2009:
    all 6 annexes incl. the vendéglátóhely üzlettípus list — Étterem, Büfé,
    Cukrászda, kávézó-type — appended to a 36 KB `s34`). The text is
    searchable there, so FTS/answers mostly work, but provision-level
    granularity is lost. Fix direction: key annex blocks by their `ME…`
    jhId and emit them as separate provisions; then offline re-parse.
    FIXED 2026-09-01 (PR: parser-and-citation-gaps): annex blocks now form
    their own provisions — section "N. melléklet" (letter forms "3/a.
    melléklet", slash forms "5/B. melléklet"), provision_ref
    `s<n>mellklet`, printed header text kept as content ("N. számú
    melléklet" old-layout headers normalize to the canonical "N.
    melléklet" label). Three corpus realities shaped the fix: (1) the
    cimke header div is prefixed with an `<!--i-->` comment that defeats
    class extraction, so headers are also detected by their `ME<n>`
    jhId — and the jhId fallback requires the header text to actually
    mention melléklet, so layout artifacts carrying a plain ME<n> id are
    not fabricated into provisions; (2) ~5.4k határozatok/utasítások
    have NO § markers — their only structure is annexes — so the
    `len(sections)==0` legacy fallback now counts only non-annex
    sections (annex-consumed blocks are skipped there), keeping the
    operative pont/tablazat text that the old parse rescued; (3) §-shaped
    text inside an open annex (quoted legal-basis excerpts on annex
    forms) stays annex content — the old parse shredded those into
    thousands of bogus single-quote § provisions. 18,972 seeds re-parsed
    offline (no network; 18,924 + 48 in the follow-up round), 0 content
    losses (per-seed word multiset old ⊆ new),
    `TestStrippedHTMLReproducesSeeds` 68,116 seeds byte-exact. DB:
    provisions 1,174,254 → 1,113,243 (e.g. hu-law-2010-10-b0-2y 2,706 →
    21 real §s + 4 annex provisions), definitions 24,762 → 25,141,
    47,341 annex provisions across 18,972 docs. (25,141 is the build
    summary's pre-dedup attempt count; the `definitions` table itself
    holds 25,109 rows — `INSERT OR IGNORE` drops 32 same-document
    duplicate terms. README cites the SQLite row count.) Corpus word total
    110,045,104 → 112,596,695 (+2.55M recovered annex headers/titles);
    the only DB-level word deficits are 66 duplicate-copies of 11
    tokens, each verified against the source HTML as old block-join
    fusion artifacts (e.g. "körülírt ,,végrehajtásért" was fused to
    "körülírt,,végrehajtásért") whose source text survives in the new
    seeds. Known trade-off (conscious, text is conserved): in ~373
    old-layout docs a melléklet header is followed by szelet blocks
    carrying the annex form's printed legal-basis excerpts ("22. § (1)
    …"); those stay inside the annex provision instead of becoming
    standalone § provisions — matching how the njt page renders the
    form — while the act's own §s keep their provisions. The
    TestStrippedHTMLReproducesSeeds/TestReparseAnnexSeeds walks run in a
    GOMAXPROCS worker pool (~21 min → ~2.5 min per pass on 22 cores).
  - **Sub-item 3 — `73/2016. (XII. 2.) Korm. rendelet`: NOT A DISCOVERY
    MISS — the decree does not exist; citation in the original entry was a
    misidentification.** Verified 2026-09-01: njt.hu's own search with
    evszam=2016 & sorszam=73 returns exactly 7 documents (the LXXIII.
    törvény, an FM rendelet, the III. 31. egyházi Korm. rendelet, an NFM
    rendelet, two határozatok, an HM utasítás) — identical to the discovery
    cache; the XII. 2. kereskedelmi rendelet is absent from njt's index
    under that year+number, absent from njt full-text search, and absent
    from the public web (exact-phrase search). Discovery faithfulness was
    quantified per the plan's own suggestion (live search-result totals vs
    discovery cache): author_type=2220 + evszam=2016 → live 501 / cache 501
    (0 missing), evszam=2019 → live 371 / cache 371 (0 missing). The actual
    legal framework: the kereskedelmi törvény is **2005. évi CLXIV.** (in
    corpus, in_force) and its exec decree is **210/2009. (IX. 29.) Korm.
    rendelet** — present, njt-classified `in_force` (njt's own
    classification; the tools faithfully echo it — do not fix client-side),
    with the vendéglátóhely üzlettípus definitions in its 4. melléklet (see
    the annex-classification defect above). "2016. évi XIV. törvény" was
    likewise a wrong expectation — njt assigns that number to the Mongolia
    visa-treaty promulgation act, so the tools resolved it correctly.
- Decree + section citations don't resolve: "210/2009. Korm. rendelet
  1. §" failed in validate_citation/get_provision while document-only
  decree citations worked — ParseCitation only knew act-style titles
  ("2012. évi I. törvény N. §"), and the title-substring pass cannot
  match anyway since njt decree titles insert the promulgation date
  ("210/2009. (IX. 29.) Korm. rendelet a kereskedelmi …").
  FIXED 2026-09-01 (PR: parser-and-citation-gaps): ParseCitation gained a
  decree grammar (doc part anchored on the year/number identifier and
  containing "rendelet"; section grammar as hungarianFullRe; greedy doc
  capture covers the full-title form) and ResolveDocumentID a decree pass
  that matches the identifier and the rendelet type as two ordered
  literal substrings anchored at the title start (amendment decrees cite
  other identifiers mid-title, which would otherwise flag a false
  ambiguity), with a typed promulgation date verified against the title —
  an ambiguous year/number pair (two ministry rendeletek share 1/2017)
  answers not-found instead of guessing, and "73/2016. (XII. 2.) Korm.
  rendelet" stays correctly not-found (no such decree; the year/number
  exists only as 73/2016. (III. 31.), whose date does not match the
  typed one). SectionRefCandidates also learns annex refs
  ("4. melléklet" and "6. számú melléklet" → the stored section label +
  the `s4mellklet`
  provision_ref form). Note: validate_citation still validates
  "…rendelet 4. melléklet" as document-only (no provision check) — the
  provision-level annex lookup runs through get_provision.
- EU-reference insert failures: 2 inserts failed on every build (silent
  bare-catch in the TS era; counted-and-warned without row detail since
  the Go port). FIXED 2026-09-01 (PR: parser-and-citation-gaps): root
  cause is the citation "COMMISSION REGULATION 302/2005/EURATOM" —
  ExtractEUReferences uppercased the community to "EURATOM", violating
  the eu_documents CHECK (community IN ('EU','EC','EEC','Euratom')); the
  OR-IGNORE insert silently dropped the row and the reference insert then
  failed on the foreign key (hu-law-2007-7-20-1u:s39,
  hu-law-2022-4-20-8l:s39 → regulation:2005/302). The community is now
  normalized to "Euratom" at extraction, failing rows are logged with
  their ids, and two tests pin zero failures: a Build-level regression
  test on the exact citation and the gated corpus-wide
  TestEUReferenceInsertsZeroFailures (HU_EU_VERIFY=1; baseline 2, now 0;
  EU references 339 → 354).
- OR-tier ranking precision: per-document matched-term-count boosting
  (natural questions recall the right content but rank generic-token-heavy
  docs first — see the `ponytail:` ceiling note in `internal/fts`).
- Btk part-prefixed citations: corpus stores Btk sections without the
  "6:" part prefix, so "6:272. §" validates as not-found; a prefix-drop
  fallback candidate in `statute.SectionRefCandidates` would cover it.
  FIXED 2026-08-31 (PR #17): `SectionRefCandidates` now emits the
  prefix-dropped form ("6:272. §" → "272"/"s272").
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
  FIXED 2026-08-31 (PR #17): `qualifiesPattern` added; 222 affected seeds
  re-parsed offline, definitions 25,866 → 26,177.
- Definition-term cleanup: the PR #17 prose pattern emitted ~5% garbage terms
  (clause fragments like "a közalkalmazott akkor", "a továbbiakban: támogatás)
  az EUMSz…", truncated dative stems like "sajtótermék árusításá").
  FIXED 2026-08-31 on `fix/small-followups`: guard in the shared definition
  `add` path drops candidates containing " akkor" or "a továbbiakban" (which
  also sweeps the pre-existing numbered-pattern "…(a továbbiakban" fragments),
  and `qualifyTerm` now restores the dative linking vowel ("kategóriának" →
  "kategória", not "kategóriá"), with the 7 restored terms verified against
  source text. Also fixed: the PR #17 `verbInTermRe` guard matched "érti"
  mid-word ("tértivevény"), dropping real numbered definitions — the verb
  must now be standalone. Full-corpus offline re-parse (no network; 0
  provision mismatches) plus a term-level sweep for seeds without cached
  HTML: garbage terms 1,562 → 0 in seeds, definitions 26,142 → 24,762
  (build-db) across 1,132 seed files. A >60-rune length cap was considered and
  rejected: real prose-path terms run 64–104 runes (e.g. "hulladékgazdálkodási
  közszolgáltatási résztevékenység körébe tartozó hulladékkal kapcsolatos
  tevékenység"), interleaved with long garbage at 85–105 — no separating
  threshold exists.
- Idea (uncommitted, just a thought): a `data/source/*.html` → `*.md` pass for
  LLM/RAG use — extract the law-only `#jogszab` region (drops the ~45% Angular
  boilerplate) and map the semantic classes (`szakasz-jel`, `fejezetCim`) to
  headings; no server "print view" exists (it's client-side DOM cloning), so
  offline extraction of the already-scraped HTML is the only path.

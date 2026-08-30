# Ansvar-Systems upstream sector repos — findings for possible future MCPs

Written 2026-08-29 after auditing all five sibling repositories of
`Ansvar-Systems/Hungarian-law-mcp` (the TypeScript original this Go port came
from). Purpose: record what exists, where the data actually lives, and how
each scraper works, so a future "hungarian-*" MCP can be built without
re-doing the audit. Clones were inspected in `/tmp`; nothing was reused for
this repo except the confirmation that the upstream corpus has zero Korm.
rendeletek (see `INGEST_PLAN.md`).

Common pattern across all five (and Hungarian-law-mcp):

- The repo contains **scraper code only**; the corpus is deliberately not
  committed ("TDM and standards-licensing constraints … we host the corpus on
  Ansvar infrastructure rather than redistribute it" — every README says
  this).
- The built SQLite database ships **only as a GitHub Release asset**, fetched
  at Docker build time by the CI workflow (`gh release download … database.db.gz`
  → `data/database.db`). All release assets were **publicly downloadable**
  via `gh release download -R Ansvar-Systems/<repo>` as of 2026-08-29.
- README/CONTRIBUTING in several siblings reference `npm run build:db` and
  `sources.yml` that **do not exist** in the repo — docs are stale/aspirational
  in most of them; only Hungarian-law-mcp actually has the seed-JSON +
  build-db pipeline it documents.
- Sample seed data inside the sector repos is **fabricated test stubs**
  (garbled Hungarian, invented case numbers) — never treat it as source text.
- Polite-crawler conventions shared by all: 1.5 s (1.2 s in law-mcp) minimum
  request gap, identifying UA (`Ansvar…Crawler/1.0 (+https://ansvar.eu;
  compliance research)`), 3 retries with 2 s × attempt backoff, 30 s timeout,
  raw-HTML caching for `--resume`.

## 1. Hungarian-law-mcp — general legislation (our origin)

- Repo: https://github.com/Ansvar-Systems/Hungarian-law-mcp (255 MB clone;
  the only sibling with real data committed)
- Data in repo: `data/seed/` — 4,314 seed JSONs, 206 MB, ~130k provisions,
  verbatim njt.hu Hungarian text, all parliamentary acts (1870s → 2025).
  `data/census.json` (1.3 MB) lists every law. Raw HTML cache (`data/source/`)
  is gitignored = lost. `data/database.db` in the repo is an 8 KB empty
  placeholder.
- Release: `v1.0.0` (2026-03-06), asset `database-hungarian.db.gz` (93 MB gz /
  296 MB raw, 4,314 docs, 130,124 provisions) — inspected in SQLite:
  acts only, **zero Korm. rendeletek**.
- Scraper: `scripts/ingest.ts` (~760 lines) + `scripts/lib/{fetcher,parser}.ts`
  - Discovery: POST `https://njt.hu/ajax/get_search_url.json`, crawl
    `https://njt.hu/search/{path}/{page}/{pageSize}` result pages.
  - Per-act: `https://njt.hu/jogszabaly/{docId}`; **deferred-block hydration**
    via POST `https://njt.hu/ajax/njtGetBlock.json` in chunks of 20 (njt.hu
    lazy-loads section text — this is what gets full-text coverage).
  - Parser: regex HTML parsing, no DOM lib; metadata-only sources stored as
    METADATA_ONLY, never fabricated.
  - Known bug (we share it): result parsing only keeps doc IDs
    `[0-9]{4}-[0-9A-Z]+-00-00`, i.e. parliamentary acts — that is why the
    whole corpus is acts-only. Fix documented in `INGEST_PLAN.md`.
- Build: `scripts/build-db.ts` (better-sqlite3): `legal_documents`,
  `legal_provisions` (UNIQUE(document_id, provision_ref)), FTS5
  `provisions_fts` (unicode61), `definitions`, `cross_references`,
  `eu_documents`/`eu_references` (EU citations regex-extracted from provision
  text), `db_metadata`; dedupes provisions keeping longest content.
  Plus `scripts/check-updates.ts`, `scripts/drift-detect.ts`
  (`fixtures/golden-hashes.json`).

## 2. hungarian-financial-regulation-mcp — MNB

- Repo: https://github.com/Ansvar-Systems/hungarian-financial-regulation-mcp
  (~444 KB, code only)
- Release: `v1.0.0` (2026-04-09), assets `database.db.gz` (1.3 MB) and
  `sector-db-mcoMJp.gz` (1.35 MB).
- Scraper: `scripts/ingest-mnb.ts` (~1,200 lines, cheerio + better-sqlite3)
  - Sources (hard-coded mnb.hu listings): `/felugyelet/szabalyozas/jogszabalyok/mnb-rendeletek`,
    `.../ajanlasok`, `.../vezetoi-korlevelek`,
    `/penzugyi-stabilitas/makroprudencialis-politika/rendeletek-hatarozatok`,
    plus enforcement page `/felugyelet/engedelyezes-es-intezmenyfelugyeles/hatarozatok-es-vegzesek-keresese`.
  - Heuristic cheerio parsing (links/tables/`<li>` filtered by `\d{1,3}/\d{4}`);
    refs like `MNB rendelet 14/2025`; **PDFs skipped entirely** (most MNB
    regulations are PDFs — the big quality hole) and detail text truncated at
    15,000 chars.
  - Writes **directly to SQLite** (`data/mnb.db`): `sourcebooks`,
    `provisions`, `enforcement_actions` (H-EN-I-B-NNN/YYYY refs, firm names,
    HUF amounts), FTS5. No seed-JSON intermediate, no build-db step.
- Quality verdict if rebuilding: scraper yield is weak (PDF gap, guessing
  selectors); plan for a real PDF pipeline instead.

## 3. hungarian-competition-mcp — GVH

- Repo: https://github.com/Ansvar-Systems/hungarian-competition-mcp (~488 KB,
  code only)
- Release: `v1.0.0` (2026-04-09), assets `database.db.gz` (2.5 MB) and
  `sector-db-6NUtGE.gz` (2.5 MB).
- Scraper: `scripts/ingest-gvh.ts` (~1,089 lines, cheerio)
  - Phase 1 discovery: year listings `https://www.gvh.hu/dontesek/versenyhivatali_dontesek/dontesek-{YYYY}`
    (2021+) and `.../archiv/dontesek_{YYYY}` (pre-2021), Liferay portlet
    pagination (`…_pageNumber/N`). Documented caveat: gvh.hu is an Aurelia SPA
    over Solr — static HTML may yield limited content, so crawler yield is
    uncertain.
  - Phase 2: decision detail pages, requires ≥100 chars of text else skipped;
    regex-extracted metadata: date (`"2024. március 15."`), parties
    (`eljárás alá vont : …`), fines (`850.000.000 Ft`), article refs
    (`Tpvt. 11. §`, `EUMSZ 101. cikk`, `Fttv.`), case type/outcome/sector via
    keyword heuristics (not authoritative). Case refs normalized
    `vj-04202446` → `Vj-04/2024/46`.
  - Writes directly to SQLite (`data/gvh.db`): `decisions`, `mergers`,
    `sectors`, FTS5. No seed files.

## 4. hungarian-cybersecurity-mcp — NKI / CERT-Hungary

- Repo: https://github.com/Ansvar-Systems/hungarian-cybersecurity-mcp (~496 KB,
  code only)
- Release: `v1.0.0` (2026-04-09), asset `sector-db-srQ7eD.gz` (**3.8 KB** —
  effectively empty).
- Scraper: `scripts/ingest-cert-hu.ts` (~1,527 lines, cheerio)
  - Sources: `https://nki.gov.hu/figyelmeztetesek/riasztas/` (alerts),
    `.../cve-serulekenysegek/` (CVEs), `.../tajekoztatas/` (notifications),
    `https://cert.hu/en/alerts` (`?page=N` pagination, caps at 50/30 list
    pages).
  - Parsing: NKI WordPress `h4>a` + sibling-date listings; CVE `tr.vulnrow`
    tables; cert.hu heading+date heuristics. `htmlToText()` regex strip with
    content-container selector fallbacks.
  - Derived metadata is regex-heuristic guessing: severity
    (`inferSeverity()`, e.g. `/kritikus|critical/`), affected products
    (~40-vendor regex list), topics/type. Full text capped at 50,000 chars;
    checkpoint file `data/ingest-progress.json` for resume.
  - Writes directly to SQLite: `guidance`, `advisories`, `frameworks`, FTS5.
- Hygiene note: `src/db.ts` header still says "German Cybersecurity (BSI) MCP
  server" — the siblings are templated copies of each other.

## 5. hungarian-data-protection-mcp — NAIH

- Repo: https://github.com/Ansvar-Systems/hungarian-data-protection-mcp
  (~464 KB, code only)
- Release: `v1.0.0` (2026-04-09), assets `database.db.gz` (86 KB) and
  `sector-db-RcgoO1.gz` (87 KB) — small; the real corpus is clearly larger
  than what was published, or the release is stale.
- Scraper: `scripts/ingest-naih.ts` (~1,202 lines, cheerio)
  - Sources: `https://www.naih.hu/hatarozatok-vegzesek` (decisions),
    `/adatvedelmi-ajanlasok` + `/tajekoztatok-kozlemenyek` (guidance),
    paginated `?start=0,50,100…`; detail pages `/hatarozatok-vegzesek/file/{id}-{slug}`;
    PDFs via `?download=ID:slug`.
  - **PDF → text without a PDF library**: hand-rolled extractor — zlib-inflate
    `/FlateDecode` streams, regex `Tj`/`TJ` operators inside `BT…ET` blocks.
    Falls back to a `[PDF tartalom — letöltve: …]` placeholder under 100
    chars. PDFs cached in `data/pdf-cache/`.
  - Derived metadata (regex heuristics): decision type
    (`bírság`/`figyelmeztetés`/`végzés`/`határozat`), entity name
    (Kft/Zrt/Nyrt), fine amount, GDPR article numbers, topic tags; summary =
    first >80-char paragraph truncated to 500 chars.
  - Writes directly to SQLite (`data/naih.db`): `decisions` (reference
    UNIQUE, e.g. `NAIH-2021-2858`), `guidelines`, `topics`, FTS5.
    `--resume` via `.ingest-naih-state.json`.

## If we build any of these later

1. **Do not reuse the sibling release DBs as ground truth** — download them
   only to bootstrap/compare; the sector scrapers truncate and guess, and the
   sample data in-repo is fabricated.
2. **Port the Hungarian-law-mcp architecture, not the siblings'**: seed-JSON
   intermediate + build-db + census (that is the model this repo already
   follows and is the only one that supports offline rebuilds and honest
   provenance).
3. Each new MCP = new repo on this Go template (`internal/ingest` pattern):
   swap the discovery/fetch/parser trio per source, keep the rate limiter,
   resume cache, metadata-only rule, and build-db seed pipeline as-is.
4. For NAIH/MNB, invest in a real PDF text extraction path first (the
   hand-rolled stream regex works only for uncompressed/simple PDFs); for
   gvh.hu, verify the SPA problem before promising coverage.
5. Release assets verified public on 2026-08-29 via
   `gh release download -R Ansvar-Systems/<repo>`; if a future download 404s,
   the Ansvar policy may have changed — check the README's redistribution
   note.

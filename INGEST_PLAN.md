# Full-corpus ingestion plan — data/update track

Status: **planned, not started.** Tackle as a dedicated effort — this is the
biggest job the repo does. Written 2026-08-29 after the coffee-shop question
test: the current corpus answers only from parliamentary acts, so questions
whose real answers live in government decrees (e.g. 210/2009. (IX. 29.) Korm.
rendelet, 531/2017. (XII. 29.) Korm. rendelet — kiskereskedelmi/üzletnyitás)
cannot be answered at all. Verified against the DB: of 4,326 documents, only 2
mention "Korm. rendelet" in the title.

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
2. `go run ./cmd/ingest -full -resume` — networked, rate-limited, expect
   **hours**. Flags worth considering: `-refresh-discovery` if the discovery
   cache is stale, `-skip-fetch` only when reusing cached HTML.
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

- **Repo size**: every seed JSON is committed; the full corpus grows the repo
  substantially (seed files for thousands of decrees on top of the current
  4,326 acts).
- **Time**: the `-full` discovery/fetch is rate-limited against njt.hu;
  budget hours, run in the background, `-resume` makes it restartable.
- **DB size**: more documents → bigger baked Docker image.

## Known follow-ups that pair well with the new corpus

- OR-tier ranking precision: per-document matched-term-count boosting
  (natural questions recall the right content but rank generic-token-heavy
  docs first — see the `ponytail:` ceiling note in `internal/fts`).
- Btk part-prefixed citations: corpus stores Btk sections without the
  "6:" part prefix, so "6:272. §" validates as not-found; a prefix-drop
  fallback candidate in `statute.SectionRefCandidates` would cover it.

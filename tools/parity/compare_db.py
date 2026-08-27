#!/usr/bin/env python3
"""Logical parity check between two builds of the Hungarian law database.

Compares a TypeScript-built DB (scripts/build-db.ts) against a Go-built DB
(cmd/build-db) at the content level. Rowids and build timestamps legitimately
differ between builds, so they are excluded: last_updated / created_at /
last_verified / built_at columns and the db_metadata 'builder' key.

Usage:
    compare_db.py <ts.db> <go.db>

Prints PASS/FAIL per check; exits 1 if any check fails.
"""

import hashlib
import json
import sqlite3
import sys

COUNT_TABLES = [
    "legal_documents",
    "legal_provisions",
    "definitions",
    "eu_documents",
    "eu_references",
]

# Content-set checks: each row is hashed, hashes sorted, sets compared.
CONTENT_CHECKS = [
    (
        "legal_documents",
        "SELECT id, type, title, title_en, short_name, status, issued_date,"
        " in_force_date, url, description FROM legal_documents ORDER BY id",
    ),
    (
        "legal_provisions",
        "SELECT document_id, provision_ref, chapter, section, title, content"
        " FROM legal_provisions ORDER BY document_id, provision_ref",
    ),
    (
        "definitions",
        "SELECT document_id, term, term_en, definition, source_provision"
        " FROM definitions ORDER BY document_id, term",
    ),
    (
        "eu_documents",
        "SELECT id, type, year, number, community, celex_number, title,"
        " title_en, short_name, adoption_date, entry_into_force_date, in_force,"
        " amended_by, repeals, url_eur_lex, description FROM eu_documents ORDER BY id",
    ),
    (
        "eu_references",
        "SELECT source_type, source_id, document_id, provision_id,"
        " eu_document_id, eu_article, reference_type, reference_context,"
        " full_citation, is_primary_implementation, implementation_status"
        " FROM eu_references ORDER BY source_id, eu_document_id, eu_article",
    ),
    (
        "db_metadata",
        "SELECT key, value FROM db_metadata"
        " WHERE key NOT IN ('builder', 'built_at') ORDER BY key",
    ),
]

FTS_TERMS = ["adat", "kiberbiztonsagi", "üzleti titok"]

failures = 0


def check(label, ok, detail=""):
    global failures
    print(f"{'PASS' if ok else 'FAIL'}  {label}" + (f"  ({detail})" if detail and not ok else ""))
    if not ok:
        failures += 1


def fetch(db, sql, args=()):
    con = sqlite3.connect(f"file:{db}?mode=ro", uri=True)
    try:
        return con.execute(sql, args).fetchall()
    finally:
        con.close()


def row_hash(row):
    """Stable hash of one row (hashable, JSON-stable representation)."""
    vals = [v.decode("utf-8") if isinstance(v, bytes) else v for v in row]
    blob = json.dumps(vals, ensure_ascii=False, default=str).encode("utf-8")
    return hashlib.sha256(blob).hexdigest()


def row_hashes(rows):
    """Per-row hashes (list of hex digests, sorted)."""
    return sorted(row_hash(r) for r in rows)


def first_diff(a_rows, b_rows):
    """Human-readable sample of rows present in one set only."""
    a = {row_hash(r): r for r in a_rows}
    b = {row_hash(r): r for r in b_rows}
    only_a = [a[h] for h in sorted(set(a) - set(b))]
    only_b = [b[h] for h in sorted(set(b) - set(a))]
    parts = [f"ts-only={len(only_a)} go-only={len(only_b)}"]
    if only_a:
        parts.append("first ts-only: " + str(only_a[0])[:300])
    if only_b:
        parts.append("first go-only: " + str(only_b[0])[:300])
    return "; ".join(parts)


def main():
    if len(sys.argv) != 3:
        print(__doc__.strip(), file=sys.stderr)
        sys.exit(2)
    ts_db, go_db = sys.argv[1], sys.argv[2]

    for table in COUNT_TABLES:
        a = fetch(ts_db, f"SELECT COUNT(*) FROM {table}")[0][0]
        b = fetch(go_db, f"SELECT COUNT(*) FROM {table}")[0][0]
        check(f"count {table}", a == b, f"ts={a} go={b}")

    for label, sql in CONTENT_CHECKS:
        a_rows = fetch(ts_db, sql)
        b_rows = fetch(go_db, sql)
        if row_hashes(a_rows) == row_hashes(b_rows):
            check(f"content {label}", True)
        else:
            check(f"content {label}", False, first_diff(a_rows, b_rows))

    fts_sql = "SELECT COUNT(*) FROM provisions_fts WHERE provisions_fts MATCH ?"
    for term in FTS_TERMS:
        a = fetch(ts_db, fts_sql, (term,))[0][0]
        b = fetch(go_db, fts_sql, (term,))[0][0]
        check(f"fts MATCH {term!r}", a == b, f"ts={a} go={b}")

    if failures:
        print(f"\n{failures} check(s) FAILED")
        sys.exit(1)
    print("\nAll checks passed.")


if __name__ == "__main__":
    main()

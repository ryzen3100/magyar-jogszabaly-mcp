#!/usr/bin/env bash
# Download the prebuilt database artifact for the requested version.
# Integrity: verifies against the release's .sha256 asset when present.
set -euo pipefail
VERSION=$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' server.json | head -1)
REPO="Ansvar-Systems/Hungarian-law-mcp"
TAG="v${VERSION}"
ASSET="database-hungarian.db.gz"
OUTPUT="data/database.db"
URL="https://github.com/${REPO}/releases/download/${TAG}/${ASSET}"
echo "[download-db] Downloading database from GitHub releases..."
mkdir -p data
curl -fSL --retry 3 --retry-delay 5 "$URL" -o "${OUTPUT}.tmp.gz"

# Verify against the release's checksum asset when the upstream publishes one.
if curl -fsSL --retry 3 --retry-delay 5 "${URL}.sha256" -o "${OUTPUT}.tmp.gz.sha256"; then
  expected="$(awk '{print $1}' "${OUTPUT}.tmp.gz.sha256")"
  actual="$(sha256sum "${OUTPUT}.tmp.gz" | awk '{print $1}')"
  if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
    echo "[download-db] ERROR: checksum mismatch for ${ASSET} (expected ${expected:-<empty>}, got ${actual})" >&2
    rm -f "${OUTPUT}.tmp.gz" "${OUTPUT}.tmp.gz.sha256"
    exit 1
  fi
  echo "[download-db] Checksum OK"
else
  echo "[download-db] WARNING: no ${ASSET}.sha256 asset on release ${TAG} — proceeding without integrity check" >&2
fi
rm -f "${OUTPUT}.tmp.gz.sha256"

gunzip -c "${OUTPUT}.tmp.gz" > "${OUTPUT}.tmp"
rm -f "${OUTPUT}.tmp.gz"
mv "${OUTPUT}.tmp" "$OUTPUT"

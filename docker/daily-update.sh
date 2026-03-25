#!/bin/bash
# Napi Hungarian Law MCP adatbázis frissítő
# Futtatás: crontab -e → 0 4 * * * /opt/hungarian-law-mcp/daily-update.sh
set -euo pipefail

LOG="/var/log/hungarian-law-update.log"
COMPOSE_DIR="/opt/hungarian-law-mcp"
REPO_DIR="/opt/hungarian-law-mcp/Hungarian-law-mcp"

exec >> "$LOG" 2>&1
echo "=== $(date -Iseconds) === START ==="

# 1. Pull latest code
cd "$REPO_DIR"
git pull --ff-only origin main || echo "[WARN] git pull failed, continuing with local version"

# 2. Install deps for scripts
npm ci --ignore-scripts

# 3. Check if update needed
set +e
node --import tsx scripts/check-updates.ts 2>&1
UPDATE_STATUS=$?
set -e

if [ "$UPDATE_STATUS" -eq 0 ]; then
    echo "[INFO] Database is fresh, no update needed."
    echo "=== $(date -Iseconds) === DONE (no update) ==="
    exit 0
fi

echo "[INFO] Update available, running ingest..."

# 4. Ingest
node --import tsx scripts/ingest.ts --full --resume --page-size 50

# 5. Build DB
node --import tsx scripts/build-db.ts

# 6. Copy DB into docker volume
VOLUME_PATH=$(docker volume inspect hungarian-law-docker_law-data -f '{{.Mountpoint}}' 2>/dev/null || docker volume inspect hungarian-law-mcp_law-data -f '{{.Mountpoint}}')
cp data/database.db "$VOLUME_PATH/database.db"

# 7. Restart container to pick up new DB
cd "$COMPOSE_DIR"
docker compose restart hungarian-law-mcp

echo "=== $(date -Iseconds) === DONE (updated) ==="

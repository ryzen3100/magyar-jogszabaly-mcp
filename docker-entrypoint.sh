#!/bin/sh
set -eu

DB_PATH="${HUNGARIAN_LAW_DB_PATH:-/data/database.db}"
DB_DIR="$(dirname "$DB_PATH")"
BOOTSTRAP_DB="/app/dist/data/database.db"
INIT_MARKER="$DB_DIR/.initialized"

is_true() {
  case "$(printf '%s' "${1:-}" | tr '[:upper:]' '[:lower:]')" in
    1|true|yes|y|on) return 0 ;;
    *) return 1 ;;
  esac
}

remove_db_files() {
  rm -f "$DB_PATH" "$DB_PATH-wal" "$DB_PATH-shm"
}

copy_bootstrap_db() {
  if [ ! -f "$BOOTSTRAP_DB" ]; then
    echo "Bootstrap database not found at $BOOTSTRAP_DB" >&2
    exit 1
  fi

  echo "Initializing persistent database at $DB_PATH"
  mkdir -p "$DB_DIR"
  cp "$BOOTSTRAP_DB" "$DB_PATH"
}

rebuild_db() {
  if [ ! -d /app/dist/data/seed ]; then
    echo "Seed files are not available at /app/dist/data/seed; cannot rebuild database." >&2
    exit 1
  fi

  echo "Rebuilding database from bundled seed files"
  remove_db_files
  rm -f "$BOOTSTRAP_DB" "$BOOTSTRAP_DB-wal" "$BOOTSTRAP_DB-shm"
  node /app/dist/scripts/build-db.js
  cp "$BOOTSTRAP_DB" "$DB_PATH"
}

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$DB_DIR"
  chown -R nodejs:nodejs "$DB_DIR"
  exec su-exec nodejs "$0" "$@"
fi

mkdir -p "$DB_DIR"

if [ ! -f "$INIT_MARKER" ]; then
  if is_true "${REBUILD_DB_ON_FIRST_RUN:-false}"; then
    rebuild_db
  elif [ ! -s "$DB_PATH" ]; then
    copy_bootstrap_db
  fi
  touch "$INIT_MARKER"
elif [ ! -s "$DB_PATH" ]; then
  copy_bootstrap_db
fi

exec "$@"

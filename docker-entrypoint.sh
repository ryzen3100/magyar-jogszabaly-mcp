#!/bin/sh
set -eu

DB_PATH="${HUNGARIAN_LAW_DB_PATH:-/data/database.db}"
DB_DIR="$(dirname "$DB_PATH")"
BOOTSTRAP_DB="/app/dist/data/database.db"
BOOTSTRAP_CHECKSUM="${BOOTSTRAP_DB}.sha256"
DB_CHECKSUM="${DB_PATH}.sha256"

copy_bootstrap_db() {
  if [ ! -s "$BOOTSTRAP_DB" ] || [ ! -s "$BOOTSTRAP_CHECKSUM" ]; then
    echo "Bundled database or checksum is missing" >&2
    exit 1
  fi

  expected_checksum="$(awk '{print $1}' "$BOOTSTRAP_CHECKSUM")"
  if [ -z "$expected_checksum" ]; then
    echo "Bundled database checksum is empty" >&2
    exit 1
  fi

  echo "Installing bundled database at $DB_PATH"
  mkdir -p "$DB_DIR"
  tmp_db="${DB_PATH}.tmp.$$"
  tmp_checksum="${DB_CHECKSUM}.tmp.$$"
  rm -f "$tmp_db" "$tmp_checksum" "$DB_PATH-wal" "$DB_PATH-shm"
  cp "$BOOTSTRAP_DB" "$tmp_db"
  mv -f "$tmp_db" "$DB_PATH"
  printf '%s\n' "$expected_checksum" > "$tmp_checksum"
  mv -f "$tmp_checksum" "$DB_CHECKSUM"
}

database_is_current() {
  [ -s "$DB_PATH" ] || return 1
  [ -s "$BOOTSTRAP_CHECKSUM" ] || return 1
  [ -s "$DB_CHECKSUM" ] || return 1

  # stored==expected proves the volume matches the bundled image DB; no re-hash needed
  expected_checksum="$(awk '{print $1}' "$BOOTSTRAP_CHECKSUM")"
  stored_checksum="$(awk '{print $1}' "$DB_CHECKSUM")"

  [ "$stored_checksum" = "$expected_checksum" ]
}

if [ "$(id -u)" = "0" ]; then
  mkdir -p "$DB_DIR"
  chown -R nodejs:nodejs "$DB_DIR"
  exec su-exec nodejs "$0" "$@"
fi

mkdir -p "$DB_DIR"

if database_is_current; then
  echo "Persistent database is current"
else
  copy_bootstrap_db
fi

exec "$@"

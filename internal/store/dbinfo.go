package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// dbEnvVar mirrors DB_ENV_VAR in src/constants.ts.
const dbEnvVar = "HUNGARIAN_LAW_DB_PATH"

// isoMillis renders times exactly like JavaScript Date#toISOString:
// UTC, millisecond precision, trailing 'Z'.
const isoMillis = "2006-01-02T15:04:05.000Z07:00"

// ResolveDbPath resolves the law database path: env override first, then the
// standard data/ locations relative to the running executable — the Go port
// of resolveDbPath in src/db-info.ts. Like the TypeScript version (which
// throws out of main), it errors when nothing is found.
func ResolveDbPath() (string, error) {
	if p := os.Getenv(dbEnvVar); p != "" {
		return p, nil
	}
	if exe, err := os.Executable(); err == nil {
		for _, candidate := range []string{
			filepath.Join(exe, "..", "data", "database.db"),
			filepath.Join(exe, "..", "..", "data", "database.db"),
		} {
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("database not found; set %s or place database.db in data/", dbEnvVar)
}

// DbFingerprint computes the sampled sha256 fingerprint of the database file:
// first 64 KiB, hex, first 12 chars (computeDbFingerprint in src/db-info.ts).
// The whole 64 KiB buffer is hashed — zero-padded past EOF — exactly like the
// TypeScript Buffer.alloc(SAMPLE) behaviour. Errors are returned so callers
// can fall back to 'unknown' + now, as the non-fatal TS catch does.
func DbFingerprint(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 64*1024)
	if _, err := io.ReadFull(f, buf); err != nil &&
		!errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])[:12], nil
}

// DbBuiltOrMtime returns db_metadata.built_at when present, else the file's
// mtime as a JavaScript-style ISO string — buildAboutContext in
// src/db-info.ts. If the file cannot be stat'ed it falls back to now,
// mirroring the non-fatal catch in computeDbFingerprint.
func DbBuiltOrMtime(ctx context.Context, db *sql.DB, path string) string {
	if m := ReadDbMetadata(ctx, db); m.HasBuiltAt {
		return m.BuiltAt
	}
	if st, err := os.Stat(path); err == nil {
		return st.ModTime().UTC().Format(isoMillis)
	}
	return time.Now().UTC().Format(isoMillis)
}

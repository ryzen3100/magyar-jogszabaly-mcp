package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDaysSince(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		iso    string
		want   int
		wantOK bool
	}{
		{"rfc3339 with millis (the build-db format)", "2026-08-26T18:03:04.347Z", 0, true},
		{"just under a day floors to zero", "2026-08-26T12:00:01Z", 0, true},
		{"exactly one day", "2026-08-26T12:00:00Z", 1, true},
		{"stale", "2026-05-01T00:00:00Z", 118, true},
		{"sqlite datetime", "2026-08-20 12:00:00", 7, true},
		{"date only", "2026-08-01", 26, true},
		{"future timestamp floors negative", "2026-08-27T13:00:00Z", -1, true},
		{"garbage", "not-a-date", 0, false},
		{"empty", "", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := daysSince(now, tc.iso)
			if ok != tc.wantOK || (ok && got != tc.want) {
				t.Fatalf("daysSince(now, %q) = (%d, %v), want (%d, %v)", tc.iso, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestReadBuiltAt pins the missing-table vs missing-row distinction that the
// TS port must preserve (two different error messages) and that
// store.ReadDbMetadata collapses.
func TestReadBuiltAt(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := readBuiltAt(db); err == nil || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing table must be a plain error, got %v", err)
	}

	if _, err := db.Exec("CREATE TABLE db_metadata (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := readBuiltAt(db); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing row must be sql.ErrNoRows, got %v", err)
	}

	if _, err := db.Exec("INSERT INTO db_metadata VALUES ('built_at', '')"); err != nil {
		t.Fatal(err)
	}
	// Empty string is falsy in TS: same message as a missing row.
	if _, err := readBuiltAt(db); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("empty value must be sql.ErrNoRows, got %v", err)
	}

	if _, err := db.Exec("UPDATE db_metadata SET value = '2026-08-26T18:03:04.347Z'"); err != nil {
		t.Fatal(err)
	}
	v, err := readBuiltAt(db)
	if err != nil || v != "2026-08-26T18:03:04.347Z" {
		t.Fatalf("readBuiltAt = (%q, %v), want (built_at, nil)", v, err)
	}
}

// TestCensusParsing pins the undefined-vs-zero semantics of the TS interface:
// absent keys must be nil pointers, present zeroes must not be.
func TestCensusParsing(t *testing.T) {
	var both censusData
	if err := json.Unmarshal([]byte(`{"total_laws":4326,"total_provisions":130220}`), &both); err != nil {
		t.Fatal(err)
	}
	if both.TotalLaws == nil || *both.TotalLaws != 4326 || both.TotalProvisions == nil || *both.TotalProvisions != 130220 {
		t.Fatalf("unexpected parse: %+v", both)
	}

	var onlyLaws censusData
	if err := json.Unmarshal([]byte(`{"total_laws":5}`), &onlyLaws); err != nil {
		t.Fatal(err)
	}
	if onlyLaws.TotalLaws == nil || *onlyLaws.TotalLaws != 5 || onlyLaws.TotalProvisions != nil {
		t.Fatalf("absent total_provisions must stay nil: %+v", onlyLaws)
	}

	var zero censusData
	if err := json.Unmarshal([]byte(`{"total_laws":0}`), &zero); err != nil {
		t.Fatal(err)
	}
	if zero.TotalLaws == nil || *zero.TotalLaws != 0 {
		t.Fatalf("zero total_laws must parse as 0, not undefined: %+v", zero)
	}

	if err := json.Unmarshal([]byte("nope"), &censusData{}); err == nil {
		t.Fatal("expected parse error for invalid JSON")
	}
}

// TestCheckPortal pins the reachability classification (301/302/403 tolerated,
// other 4xx/5xx not) and that the returned error names the cause instead of
// being collapsed to a bare false.
func TestCheckPortal(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		wantErr     bool
		errContains string
	}{
		{"200 is reachable", http.StatusOK, false, ""},
		{"403 bot-block is tolerated", http.StatusForbidden, false, ""},
		{"301 without Location is tolerated", http.StatusMovedPermanently, false, ""},
		{"302 without Location is tolerated", http.StatusFound, false, ""},
		{"404 is not tolerated unlike 403", http.StatusNotFound, true, "404"},
		{"500 names the status", http.StatusInternalServerError, true, "500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			err := checkPortal(srv.Client(), srv.URL)
			if (err != nil) != tc.wantErr {
				t.Fatalf("checkPortal(%d) error = %v, wantErr %v", tc.status, err, tc.wantErr)
			}
			if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
				t.Fatalf("checkPortal(%d) error %q must name the status", tc.status, err)
			}
		})
	}

	t.Run("connection failure carries the cause", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		srv.Close() // nothing listens anymore
		err := checkPortal(srv.Client(), srv.URL)
		if err == nil {
			t.Fatal("expected error for a refused connection")
		}
	})
}

// TestReadCensus exercises the file-level branches main() classifies: a valid
// file parses, a malformed file errors without fs.ErrNotExist (the TS
// JSON.parse catch), a missing file reports fs.ErrNotExist (the TS existsSync
// branch), and an empty object parses with nil counts, which main() reports as
// "no expected document count".
func TestReadCensus(t *testing.T) {
	dir := t.TempDir()

	valid := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(valid, []byte(`{"total_laws":4326,"total_provisions":130220}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := readCensus(valid)
	if err != nil || c.TotalLaws == nil || *c.TotalLaws != 4326 || c.TotalProvisions == nil || *c.TotalProvisions != 130220 {
		t.Fatalf("readCensus(valid) = (%+v, %v)", c, err)
	}

	malformed := filepath.Join(dir, "malformed.json")
	if err := os.WriteFile(malformed, []byte(`{"total_laws":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readCensus(malformed); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("readCensus(malformed) error = %v, want a parse error", err)
	}

	if _, err := readCensus(filepath.Join(dir, "missing.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("readCensus(missing) error = %v, want fs.ErrNotExist", err)
	}

	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err := readCensus(empty); err != nil || c.TotalLaws != nil || c.TotalProvisions != nil {
		t.Fatalf("readCensus(empty object) = (%+v, %v), want nil counts and no error", c, err)
	}
}

package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// writeCheckDB creates a minimal database file carrying just the tables and
// rows run() queries, for the offline classification test below.
func writeCheckDB(t *testing.T, builtAt string, docs, provisions int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "database.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range []string{
		`CREATE TABLE db_metadata (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE legal_documents (id TEXT)`,
		`CREATE TABLE legal_provisions (id TEXT)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	if builtAt != "" {
		if _, err := db.Exec(`INSERT INTO db_metadata VALUES ('built_at', ?)`, builtAt); err != nil {
			t.Fatal(err)
		}
	}
	for i := range docs {
		if _, err := db.Exec(`INSERT INTO legal_documents VALUES (?)`, fmt.Sprintf("doc-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := range provisions {
		if _, err := db.Exec(`INSERT INTO legal_provisions VALUES (?)`, fmt.Sprintf("prov-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// stubTransport answers every request with a fixed status without touching
// the network; deadTransport fails like an unreachable portal.
type stubTransport struct{ status int }

func (t stubTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		Status:     fmt.Sprintf("%d stub", t.status),
		StatusCode: t.status,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     http.Header{},
	}, nil
}

type deadTransport struct{}

func (deadTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

// TestRunClassification pins the 0/1/2 exit-code contract of run() offline:
// the clock is fixed and the portal is a stubbed HTTP client. It also pins
// the stream discipline — "ERROR:" lines only on stderr, the RESULT line
// only on stdout.
func TestRunClassification(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	fresh := now.AddDate(0, 0, -7).Format(time.RFC3339)
	stale := now.AddDate(0, 0, -(maxDBAgeDays + 1)).Format(time.RFC3339)
	okPortal := &http.Client{Transport: stubTransport{http.StatusOK}}
	deadPortal := &http.Client{Transport: deadTransport{}}
	dir := t.TempDir()
	currentCensus := filepath.Join(dir, "census.json")
	if err := os.WriteFile(currentCensus, []byte(`{"total_laws":1,"total_provisions":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	censusMissing := filepath.Join(dir, "absent.json")

	tests := []struct {
		name       string
		db         func(t *testing.T) string
		census     string
		portal     *http.Client
		want       int
		wantErr    string // substring that must land on stderr
		wantStdout string // substring that must land on stdout ("" for early exits)
	}{
		{
			name:       "fresh database",
			db:         func(t *testing.T) string { return writeCheckDB(t, fresh, 1, 1) },
			census:     currentCensus,
			portal:     okPortal,
			want:       0,
			wantStdout: "RESULT: Database appears current",
		},
		{
			name:       "stale database",
			db:         func(t *testing.T) string { return writeCheckDB(t, stale, 1, 1) },
			census:     currentCensus,
			portal:     okPortal,
			want:       1,
			wantStdout: "RESULT: Updates detected",
		},
		{
			name:       "portal unreachable",
			db:         func(t *testing.T) string { return writeCheckDB(t, fresh, 1, 1) },
			census:     currentCensus,
			portal:     deadPortal,
			want:       2,
			wantErr:    "is unreachable",
			wantStdout: "RESULT: Freshness check failed",
		},
		{
			name:       "census missing",
			db:         func(t *testing.T) string { return writeCheckDB(t, fresh, 1, 1) },
			census:     censusMissing,
			portal:     okPortal,
			want:       2,
			wantErr:    "ERROR: census.json is missing",
			wantStdout: "RESULT: Freshness check failed",
		},
		{
			name:    "database missing",
			db:      func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent.db") },
			census:  currentCensus,
			portal:  okPortal,
			want:    2,
			wantErr: "ERROR: Database not found",
		},
		{
			name:       "no built_at row",
			db:         func(t *testing.T) string { return writeCheckDB(t, "", 1, 1) },
			census:     currentCensus,
			portal:     okPortal,
			want:       2,
			wantErr:    "ERROR: No built_at in db_metadata",
			wantStdout: "RESULT: Freshness check failed",
		},
		{
			name:       "invalid built_at",
			db:         func(t *testing.T) string { return writeCheckDB(t, "not-a-date", 1, 1) },
			census:     currentCensus,
			portal:     okPortal,
			want:       2,
			wantErr:    "ERROR: Database built_at metadata is invalid",
			wantStdout: "RESULT: Freshness check failed",
		},
		{
			name:       "database without data",
			db:         func(t *testing.T) string { return writeCheckDB(t, fresh, 0, 0) },
			census:     currentCensus,
			portal:     okPortal,
			want:       2,
			wantErr:    "ERROR: Database contains no legal data",
			wantStdout: "RESULT: Freshness check failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			got := run(tt.db(t), tt.census, func() time.Time { return now }, tt.portal, &stdout, &stderr)
			if got != tt.want {
				t.Fatalf("run() = %d, want %d\nstdout:\n%s\nstderr:\n%s", got, tt.want, stdout.String(), stderr.String())
			}
			if tt.wantErr != "" && !strings.Contains(stderr.String(), tt.wantErr) {
				t.Fatalf("stderr missing %q:\n%s", tt.wantErr, stderr.String())
			}
			if tt.wantStdout != "" && !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Fatalf("stdout missing %q:\n%s", tt.wantStdout, stdout.String())
			}
			// Stream discipline: ERROR lines never on stdout.
			if strings.Contains(stdout.String(), "ERROR:") {
				t.Errorf("stdout carries ERROR lines:\n%s", stdout.String())
			}
		})
	}
}

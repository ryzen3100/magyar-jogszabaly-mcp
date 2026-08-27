// Command check-updates is the Go port of scripts/check-updates.ts: it
// reports whether the local law database is stale or missing expected
// legislation, so CI and developers can decide when to re-run ingestion.
//
// Exit codes (same contract as the TypeScript original):
//
//	0 = database is fresh, no updates detected
//	1 = updates detected (stale DB, missing documents, or new content upstream)
//	2 = check failed (DB missing, portal unreachable, unexpected error)
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store"
)

// Paths are cwd-relative: the TypeScript original resolves them against its
// own script directory (the repo root when run via npm), which for a compiled
// binary translates to running from the repository root.
const (
	dbPath     = "data/database.db"
	censusPath = "data/census.json"

	maxDBAgeDays = 90
	portalURL    = "https://njt.hu"
	portalName   = "Nemzeti Jogszabalytár (National Legislation Database)"
	portalAgent  = "@ansvar/hungarian-law-mcp/1.0 (data-freshness-check)"
)

// censusData mirrors the TS CensusData interface; pointers distinguish absent
// keys (undefined in TS) from present zeroes.
type censusData struct {
	TotalLaws       *int `json:"total_laws"`
	TotalProvisions *int `json:"total_provisions"`
}

func main() {
	fmt.Println("Hungarian Law MCP — Data Freshness Check")
	fmt.Printf("Portal: %s (%s)\n", portalName, portalURL)
	fmt.Println()

	// --- 1. Database existence ---
	if _, err := os.Stat(dbPath); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: Database not found at", dbPath)
		fmt.Fprintln(os.Stderr, `Run "go run ./cmd/build-db" first.`)
		os.Exit(2)
	}

	updatesNeeded := false
	checkError := false

	db, err := store.OpenReadOnly(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Unexpected error:", err)
		os.Exit(2)
	}
	// The TS original opens eagerly (better-sqlite3 throws on a corrupt or
	// unopenable file); sql.Open is lazy, so ping to surface that here.
	if err := db.Ping(); err != nil {
		fmt.Fprintln(os.Stderr, "Unexpected error:", err)
		os.Exit(2)
	}

	// --- 2. Database age check ---
	builtAt, err := readBuiltAt(db)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		checkError = true
		fmt.Println("ERROR: No built_at in db_metadata — cannot assess age")
	case err != nil:
		checkError = true
		fmt.Println("ERROR: db_metadata table is missing")
	default:
		if age, ok := daysSince(time.Now(), builtAt); !ok {
			checkError = true
			fmt.Println("ERROR: Database built_at metadata is invalid")
		} else if age > maxDBAgeDays {
			fmt.Printf("STALE: Database is %d days old (threshold: %d days)\n", age, maxDBAgeDays)
			updatesNeeded = true
		} else {
			fmt.Printf("OK: Database is %d days old (threshold: %d days)\n", age, maxDBAgeDays)
		}
	}

	// --- 3. Document and provision count check ---
	dbDocCount := countTable(db, "legal_documents", &checkError)
	dbProvCount := countTable(db, "legal_provisions", &checkError)

	if dbDocCount < 1 || dbProvCount < 1 {
		checkError = true
		fmt.Println("ERROR: Database contains no legal data")
	}

	// Compare against census if available
	census, censusErr := readCensus(censusPath)
	switch {
	case os.IsNotExist(censusErr):
		checkError = true
		fmt.Println("ERROR: census.json is missing")
	case censusErr != nil:
		// TS folds read errors and JSON errors into one catch.
		checkError = true
		fmt.Println("ERROR: Could not parse census.json")
	default:
		expectedDocuments := census.TotalLaws
		expectedProvisions := census.TotalProvisions

		if expectedDocuments == nil {
			checkError = true
			fmt.Println("ERROR: census.json has no expected document count")
		} else if dbDocCount < *expectedDocuments {
			fmt.Printf("MISSING: DB has %d documents but census expects %d\n", dbDocCount, *expectedDocuments)
			updatesNeeded = true
		} else {
			fmt.Printf("OK: DB documents (%d) >= census expected (%d)\n", dbDocCount, *expectedDocuments)
		}

		if expectedProvisions != nil {
			if dbProvCount < *expectedProvisions {
				fmt.Printf("MISSING: DB has %d provisions but census expects %d\n", dbProvCount, *expectedProvisions)
				updatesNeeded = true
			} else {
				fmt.Printf("OK: DB provisions (%d) >= census expected (%d)\n", dbProvCount, *expectedProvisions)
			}
		}
	}

	_ = db.Close()

	// --- 4. Source portal reachability ---
	fmt.Println()
	fmt.Printf("Checking portal: %s\n", portalURL)
	if checkPortal(portalClient(), portalURL) {
		fmt.Printf("OK: %s is reachable\n", portalName)
	} else {
		checkError = true
		fmt.Printf("ERROR: %s is unreachable\n", portalName)
	}

	// --- Result ---
	fmt.Println()
	if checkError {
		fmt.Println("RESULT: Freshness check failed")
		os.Exit(2)
	} else if updatesNeeded {
		fmt.Println("RESULT: Updates detected — re-ingestion recommended")
		os.Exit(1)
	}
	fmt.Println("RESULT: Database appears current — no updates needed")
}

// readBuiltAt ports the TS "SELECT value FROM db_metadata WHERE key =
// 'built_at'". It returns sql.ErrNoRows when the row is missing (or the value
// is empty, which TS treats as falsy) so the caller can print the exact TS
// message, and the raw query error when the table itself is missing — a
// distinction store.ReadDbMetadata deliberately collapses.
func readBuiltAt(db *sql.DB) (string, error) {
	var v string
	if err := db.QueryRow("SELECT value FROM db_metadata WHERE key = 'built_at'").Scan(&v); err != nil {
		return "", err
	}
	if v == "" {
		return "", sql.ErrNoRows
	}
	return v, nil
}

// daysSince ports daysSince(): whole days from isoDate to now, floored, with
// ok=false for unparseable input (TS returns null). The extra layouts cover
// SQLite-style timestamps that new Date() also accepts.
func daysSince(now time.Time, isoDate string) (int, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if dt, err := time.Parse(layout, isoDate); err == nil {
			return int(math.Floor(now.Sub(dt).Hours() / 24)), true
		}
	}
	return 0, false
}

// countTable ports one TS COUNT(*) try/catch block: print the count, or flag a
// check error. SafeCount collapses query errors to 0, so a missing table is
// detected separately to keep the TS "Cannot count ..." message distinct from
// a genuine count of zero (TS prints both messages in the error case).
func countTable(db *sql.DB, table string, checkError *bool) int {
	n := store.SafeCount(db, "SELECT COUNT(*) AS count FROM "+table)
	if n > 0 || tableExists(db, table) {
		fmt.Printf("DB %s: %d\n", strings.TrimPrefix(table, "legal_"), n)
		return n
	}
	*checkError = true
	fmt.Println("ERROR: Cannot count " + table)
	return n
}

func tableExists(db *sql.DB, name string) bool {
	var n int
	err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&n)
	return err == nil && n > 0
}

// readCensus reads census.json; the returned error is fs.ErrNotExist when the
// file is missing (TS checks existsSync first) and a parse error otherwise,
// matching the TS catch around readFileSync + JSON.parse.
func readCensus(path string) (censusData, error) {
	var c censusData
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	return c, nil
}

// portalClient ports the TS fetch options: a 15s abort timeout and Node's
// default of following up to 20 redirects (Go's default client stops at 10).
func portalClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 20 {
				return errors.New("stopped after 20 redirects")
			}
			return nil
		},
	}
}

// checkPortal ports checkPortal(): a HEAD request counts as reachable when
// the final status is <400 or one of the explicitly tolerated codes (301/302
// redirects, and 403, which portals return when bot-blocked).
func checkPortal(client *http.Client, url string) bool {
	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", portalAgent)
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode < 400 ||
		res.StatusCode == 301 || res.StatusCode == 302 || res.StatusCode == 403
}

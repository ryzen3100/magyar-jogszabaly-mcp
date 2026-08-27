// Shared test helpers: thin runners that invoke a handler with raw JSON args
// and return the marshaled response envelope.
package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

// testArgs decodes a JSON object literal into the argument map the Handler
// contract carries, so tests keep writing arguments as JSON strings. An
// empty literal is the empty (absent) argument map.
func testArgs(t *testing.T, raw string) map[string]any {
	t.Helper()
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("bad test args %s: %v", raw, err)
	}
	return m
}

func runGetProvisionJSON(t *testing.T, db *sql.DB, rawArgs string) (string, error) {
	t.Helper()
	results, meta, err := GetProvision(context.Background(), db, testArgs(t, rawArgs))
	if err != nil {
		return "", err
	}
	return MarshalResponse(results, meta), nil
}

func runValidateCitationJSON(t *testing.T, db *sql.DB, rawArgs string) (string, error) {
	t.Helper()
	results, meta, err := ValidateCitation(context.Background(), db, testArgs(t, rawArgs))
	if err != nil {
		return "", err
	}
	return MarshalResponse(results, meta), nil
}

func runBuildLegalStanceJSON(t *testing.T, db *sql.DB, rawArgs string) (string, error) {
	t.Helper()
	results, meta, err := BuildLegalStance(context.Background(), db, testArgs(t, rawArgs))
	if err != nil {
		return "", err
	}
	return MarshalResponse(results, meta), nil
}

func runFormatCitationJSON(t *testing.T, db *sql.DB, rawArgs string) (string, error) {
	t.Helper()
	results, meta, err := FormatCitation(context.Background(), db, testArgs(t, rawArgs))
	if err != nil {
		return "", err
	}
	return MarshalResponse(results, meta), nil
}

func runCheckCurrencyJSON(t *testing.T, db *sql.DB, rawArgs string) (string, error) {
	t.Helper()
	results, meta, err := CheckCurrency(context.Background(), db, testArgs(t, rawArgs))
	if err != nil {
		return "", err
	}
	return MarshalResponse(results, meta), nil
}

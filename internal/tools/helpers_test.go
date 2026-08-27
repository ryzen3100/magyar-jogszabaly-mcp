// Shared test helpers: thin runners that invoke a handler with raw JSON args
// and return the marshaled response envelope.
package tools

import (
	"database/sql"
	"encoding/json"
	"testing"
)

func runGetProvisionJSON(t *testing.T, db *sql.DB, rawArgs string) (string, error) {
	t.Helper()
	results, meta, err := GetProvision(db, json.RawMessage(rawArgs))
	if err != nil {
		return "", err
	}
	return MarshalResponse(results, meta), nil
}

func runValidateCitationJSON(t *testing.T, db *sql.DB, rawArgs string) (string, error) {
	t.Helper()
	results, meta, err := ValidateCitation(db, json.RawMessage(rawArgs))
	if err != nil {
		return "", err
	}
	return MarshalResponse(results, meta), nil
}

func runBuildLegalStanceJSON(t *testing.T, db *sql.DB, rawArgs string) (string, error) {
	t.Helper()
	results, meta, err := BuildLegalStance(db, json.RawMessage(rawArgs))
	if err != nil {
		return "", err
	}
	return MarshalResponse(results, meta), nil
}

func runFormatCitationJSON(t *testing.T, db *sql.DB, rawArgs string) (string, error) {
	t.Helper()
	results, meta, err := FormatCitation(db, json.RawMessage(rawArgs))
	if err != nil {
		return "", err
	}
	return MarshalResponse(results, meta), nil
}

func runCheckCurrencyJSON(t *testing.T, db *sql.DB, rawArgs string) (string, error) {
	t.Helper()
	results, meta, err := CheckCurrency(db, json.RawMessage(rawArgs))
	if err != nil {
		return "", err
	}
	return MarshalResponse(results, meta), nil
}

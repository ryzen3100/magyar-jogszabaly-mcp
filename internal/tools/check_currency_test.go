package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/store/storetest"
)

func TestCheckCurrencyNotFound(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	out, err := runHandlerJSON(t, CheckCurrency, db, `{"document_id": "missing-doc"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"document_id":"missing-doc"`,
		`"title":"Unknown"`,
		`"status":"not_found"`,
		`"issued_date":null`,
		`"in_force_date":null`,
		`Document not found: \"missing-doc\"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
}

func TestCheckCurrencyStatusWarnings(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	tests := []struct {
		name    string
		doc     string
		status  string
		warning string
	}{
		{"repealed", "doc-repealed", "repealed", "This statute has been repealed and is no longer in force."},
		{"not yet in force", "doc-future", "not_yet_in_force", "This statute has not yet entered into force."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runHandlerJSON(t, CheckCurrency, db, `{"document_id": "`+tc.doc+`"}`)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, `"status":"`+tc.status+`"`) {
				t.Errorf("status wrong: %s", out)
			}
			if !strings.Contains(out, tc.warning) {
				t.Errorf("missing warning in %s", out)
			}
		})
	}

	t.Run("in force has no warnings", func(t *testing.T) {
		out, err := runHandlerJSON(t, CheckCurrency, db, `{"document_id": "doc-inforce"}`)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			`"status":"in_force"`, `"warnings":[]`, `"issued_date":"2020-01-01"`, `"in_force_date":"2020-06-01"`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %s in %s", want, out)
			}
		}
	})
}

func TestCheckCurrencyResolvesByTitle(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	out, err := runHandlerJSON(t, CheckCurrency, db, `{"document_id": "In Force Act"}`)
	if err != nil {
		t.Fatal(err)
	}
	// Result carries the resolved id, not the input.
	if !strings.Contains(out, `"document_id":"doc-inforce"`) {
		t.Errorf("expected resolved id: %s", out)
	}
}

func TestCheckCurrencyMissingRequiredArg(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	_, _, err := CheckCurrency(context.Background(), db, testArgs(t, `{}`))
	if err == nil || err.Error() != `missing required argument "document_id"` {
		t.Errorf("err = %v, want missing required argument", err)
	}
}

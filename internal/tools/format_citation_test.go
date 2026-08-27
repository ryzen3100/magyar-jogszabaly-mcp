package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store/storetest"
)

func TestFormatCitationStyles(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	tests := []struct {
		name      string
		rawArgs   string
		formatted string
		format    string
		original  string
	}{
		{
			name:      "section first full",
			rawArgs:   `{"citation": "Section 3, In Force Act"}`,
			formatted: "In Force Act 3. §",
			format:    "full",
			original:  "Section 3, In Force Act",
		},
		{
			name:      "explicit full",
			rawArgs:   `{"citation": "In Force Act s 3", "format": "full"}`,
			formatted: "In Force Act 3. §",
			format:    "full",
			original:  "In Force Act s 3",
		},
		{
			name:      "pinpoint",
			rawArgs:   `{"citation": "In Force Act Section 3", "format": "pinpoint"}`,
			formatted: "3. §",
			format:    "pinpoint",
			original:  "In Force Act Section 3",
		},
		{
			name:      "no section",
			rawArgs:   `{"citation": "In Force Act"}`,
			formatted: "In Force Act",
			format:    "full",
			original:  "In Force Act",
		},
		{
			name:      "no section full",
			rawArgs:   `{"citation": "In Force Act", "format": "full"}`,
			formatted: "In Force Act",
			format:    "full",
			original:  "In Force Act",
		},
		{
			name:      "no section pinpoint",
			rawArgs:   `{"citation": "In Force Act", "format": "pinpoint"}`,
			formatted: "In Force Act",
			format:    "pinpoint",
			original:  "In Force Act",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runHandlerJSON(t, FormatCitation, db, tc.rawArgs)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				`"formatted":` + quoteJSON(tc.formatted),
				`"format":"` + tc.format + `"`,
				`"original":` + quoteJSON(tc.original),
			} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %s in %s", want, out)
				}
			}
		})
	}
}

func TestFormatCitationOriginalEchoesUntrimmed(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	out, err := runHandlerJSON(t, FormatCitation, db, `{"citation": "  In Force Act s 3  "}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"original":"  In Force Act s 3  "`) {
		t.Errorf("original must echo the untrimmed input: %s", out)
	}
	if !strings.Contains(out, `"formatted":"In Force Act 3. §"`) {
		t.Errorf("formatted must use the trimmed parse: %s", out)
	}
}

func TestFormatCitationStructuredResolvesDBTitle(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	if _, err := db.Exec(`INSERT INTO legal_documents (id, type, title, status)
		VALUES ('hu-law-2012-1-00-00', 'statute', 'Full Title Act', 'in_force')`); err != nil {
		t.Fatal(err)
	}

	out, err := runHandlerJSON(t, FormatCitation, db, `{"citation": "hu-law-2012-1-00-00 s3"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"formatted":"Full Title Act 3. §"`) {
		t.Errorf("structured reference should resolve DB title: %s", out)
	}
}

func TestFormatCitationMissingRequiredArg(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	_, _, err := FormatCitation(context.Background(), db, testArgs(t, `{}`))
	if err == nil || err.Error() != `missing required argument "citation"` {
		t.Errorf("err = %v, want missing required argument", err)
	}
}

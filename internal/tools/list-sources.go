// list_sources — Return provenance metadata for all data sources.
// Port of src/tools/list-sources.ts.

package tools

import (
	"context"
	"database/sql"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store"
)

// sourceInfo mirrors the TS SourceInfo; the four descriptive strings are
// copied verbatim from list-sources.ts.
type sourceInfo struct {
	Name      string   `json:"name"`
	Authority string   `json:"authority"`
	URL       string   `json:"url"`
	License   string   `json:"license"`
	Coverage  string   `json:"coverage"`
	Languages []string `json:"languages"`
}

// listSourcesDatabase mirrors the TS inline database object; field order =
// TS insertion order. built_at is omitted (not null) when db_metadata has no
// built_at — hence *string with omitempty.
type listSourcesDatabase struct {
	Tier           string  `json:"tier"`
	SchemaVersion  string  `json:"schema_version"`
	BuiltAt        *string `json:"built_at,omitempty"`
	DocumentCount  int     `json:"document_count"`
	ProvisionCount int     `json:"provision_count"`
}

type listSourcesResult struct {
	Sources  []sourceInfo        `json:"sources"`
	Database listSourcesDatabase `json:"database"`
}

// ListSources implements the list_sources MCP tool. It takes no arguments.
func ListSources(ctx context.Context, db *sql.DB) (any, ResponseMetadata, error) {
	meta := store.ReadDbMetadata(ctx, db)

	var builtAt *string
	if meta.HasBuiltAt {
		builtAt = &meta.BuiltAt
	}

	return listSourcesResult{
		Sources: []sourceInfo{
			{
				Name:      "Nemzeti Jogszabálytár (National Legislation Database)",
				Authority: "Magyar Közlöny (Hungarian Official Gazette)",
				URL:       "https://njt.hu",
				License:   "Official legal text publication (see portal terms at njt.hu)",
				Coverage: "Curated set of key Hungarian statutes covering data protection, cybersecurity, " +
					"electronic commerce, telecommunications, public procurement, trade secrets, " +
					"trust services, and criminal cybercrime provisions",
				Languages: []string{"hu", "en"},
			},
		},
		Database: listSourcesDatabase{
			Tier:           meta.Tier,
			SchemaVersion:  meta.SchemaVersion,
			BuiltAt:        builtAt,
			DocumentCount:  store.CachedCount(ctx, db, "SELECT COUNT(*) as count FROM legal_documents"),
			ProvisionCount: store.CachedCount(ctx, db, "SELECT COUNT(*) as count FROM legal_provisions"),
		},
	}, GenerateResponseMetadata(ctx, db), nil
}

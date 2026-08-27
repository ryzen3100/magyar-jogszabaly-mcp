// HTTP-only resources — port of the resources handlers in src/http-server.ts.
// Like the prompts, these exist only in HTTP mode.
package server

import (
	"context"
	"database/sql"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/tools"
)

// registerResources adds the two HTTP-mode resources. The TS
// ReadResourceRequestSchema handler stringifies the exact same envelopes the
// tools return: list_sources is enveloped ({results, _metadata}), about is
// the bare stats object.
func registerResources(s *mcp.Server, db *sql.DB, about *tools.AboutContext) {
	s.AddResource(&mcp.Resource{
		URI:         "hungarian-law://sources",
		Name:        "Data Sources & Provenance",
		Description: "Authoritative legal data sources, coverage scope, and database freshness metadata",
		MIMEType:    "application/json",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		results, meta, err := tools.ListSources(db)
		if err != nil {
			return nil, err
		}
		return textResource(req.Params.URI, tools.MarshalResponse(results, meta)), nil
	})

	s.AddResource(&mcp.Resource{
		URI:         "hungarian-law://stats",
		Name:        "Database Statistics",
		Description: "Document counts, provision counts, definition counts, and EU reference coverage",
		MIMEType:    "application/json",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		stats, _, err := tools.GetAbout(db, about)
		if err != nil {
			return nil, err
		}
		return textResource(req.Params.URI, tools.MarshalBare(stats)), nil
	})
}

func textResource(uri, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: text}},
	}
}

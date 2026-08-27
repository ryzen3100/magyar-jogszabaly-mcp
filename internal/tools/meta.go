// Package tools implements the MCP tool handlers for Hungarian Law MCP.
//
// This file ports the response-envelope helpers of src/utils/metadata.ts:
// the _metadata block attached to every tool response and the compact JSON
// serialization of the two-key envelope.
package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store"
)

const (
	dataSourceConst   = "Nemzeti Jogszabálytár (National Legislation Database) (njt.hu) — Magyar Közlöny (Hungarian Official Gazette)"
	jurisdictionConst = "HU"
	disclaimerConst   = "This data is sourced from the Nemzeti Jogszabálytár (National Legislation Database) under public domain. " +
		"The authoritative versions are maintained by Magyar Közlöny (Hungarian Official Gazette). " +
		"Always verify with the official Nemzeti Jogszabálytár (National Legislation Database) portal (njt.hu)."
)

// ResponseMetadata is the _metadata block present on every tool response.
// Freshness, Note and QueryStrategy are omitted from the JSON when empty —
// the Go equivalent of the TypeScript `undefined`-key dropping.
// Field order matches the TypeScript object insertion order.
type ResponseMetadata struct {
	DataSource    string `json:"data_source"`
	Jurisdiction  string `json:"jurisdiction"`
	Disclaimer    string `json:"disclaimer"`
	Freshness     string `json:"freshness,omitempty"`
	Note          string `json:"note,omitempty"`
	QueryStrategy string `json:"query_strategy,omitempty"`
}

// GenerateResponseMetadata builds the base metadata block — port of
// generateResponseMetadata in src/utils/metadata.ts. Freshness mirrors
// readDbMetadata(ctx, db).built_at (key omitted when absent).
func GenerateResponseMetadata(ctx context.Context, db *sql.DB) ResponseMetadata {
	meta := ResponseMetadata{
		DataSource:   dataSourceConst,
		Jurisdiction: jurisdictionConst,
		Disclaimer:   disclaimerConst,
	}
	if m := store.ReadDbMetadata(ctx, db); m.HasBuiltAt {
		meta.Freshness = m.BuiltAt
	}
	return meta
}

// ToolResponse is the envelope every tool serializes into its single text
// content block — port of ToolResponse<T> in src/utils/metadata.ts.
type ToolResponse struct {
	Results  any              `json:"results"`
	Metadata ResponseMetadata `json:"_metadata"`
}

// marshalCompact serializes v as compact JSON without HTML escaping, matching
// JSON.stringify (which does NOT escape <>&; json.Marshal does).
func marshalCompact(v any) string {
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "{}"
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

// MarshalResponse serializes the envelope as compact JSON. Marshaling cannot
// fail for the types handlers produce; on the impossible error it returns
// "{}" so a broken response can never crash the server.
func MarshalResponse(results any, meta ResponseMetadata) string {
	return marshalCompact(ToolResponse{Results: results, Metadata: meta})
}

// MarshalBare serializes a result object without the envelope — used by the
// `about` tool, whose TypeScript registry stringifies getAbout's return
// object directly (registry.ts:378) with no results/_metadata wrapper.
func MarshalBare(v any) string { return marshalCompact(v) }

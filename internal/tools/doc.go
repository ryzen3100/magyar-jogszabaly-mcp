// Package tools implements the MCP tools of the Hungarian Law server: one
// handler file per tool, a shared registry (registry.go) used by both the
// stdio and HTTP entrypoints, and the JSON-Schema input declarations each
// tool advertises (schemas.go). Parameter contracts are documented in
// TOOLS.md; port-provenance notes live next to their handlers.
package tools

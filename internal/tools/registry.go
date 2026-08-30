// Registry for Hungarian Law MCP tools — port of src/tools/registry.ts.
// Shared between the stdio and HTTP entrypoints.

package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AboutContext carries the precomputed values the about tool needs — port of
// AboutContext in src/tools/about.ts. A nil *AboutContext means the about
// tool is not configured (registered) but still answered with an explicit
// error when called.
type AboutContext struct {
	Version     string
	Fingerprint string
	DBBuilt     string
}

// Handler is the common signature of every non-about tool handler. ctx is
// the request context, so client disconnects cancel in-flight DB work; args
// is the unvalidated argument map decoded from the call payload — handlers
// parse and validate their own input. A returned error becomes an in-band
// "Error: …" tool result, never a protocol error.
type Handler func(ctx context.Context, db *sql.DB, args map[string]any) (results any, meta ResponseMetadata, err error)

// decodeCallArgs parses the raw call payload into the argument map carried
// by Handler; an empty payload is an empty map.
func decodeCallArgs(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode arguments: %w", err)
	}
	return args, nil
}

// decodeArgs re-encodes the raw argument map into a handler's typed args
// struct, preserving the pointer-optionality of absent keys.
func decodeArgs(args map[string]any, out any) error {
	if len(args) == 0 {
		return nil
	}
	buf, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("encode arguments: %w", err)
	}
	if err := json.Unmarshal(buf, out); err != nil {
		return fmt.Errorf("decode arguments: %w", err)
	}
	return nil
}

// Handlers maps tool name → handler for the 12 non-about tools — port of the
// HANDLERS record in src/tools/registry.ts.
func Handlers() map[string]Handler {
	return map[string]Handler{
		"search_legislation":            SearchLegislation,
		"get_provision":                 GetProvision,
		"validate_citation":             ValidateCitation,
		"build_legal_stance":            BuildLegalStance,
		"format_citation":               FormatCitation,
		"check_currency":                CheckCurrency,
		"get_eu_basis":                  GetEUBasis,
		"get_hungarian_implementations": GetHungarianImplementations,
		"search_eu_implementations":     SearchEUImplementations,
		"get_provision_eu_basis":        GetProvisionEUBasis,
		"validate_eu_compliance":        ValidateEUCompliance,
		"list_sources": func(ctx context.Context, db *sql.DB, _ map[string]any) (any, ResponseMetadata, error) {
			return ListSources(ctx, db)
		},
	}
}

// toolDef is one entry of the registration list, in TypeScript TOOLS order.
type toolDef struct {
	name        string
	description string
	title       string
	schema      *jsonschema.Schema
}

// toolDefs lists all 13 tools in src/tools/registry.ts order
// (TOOLS + LIST_SOURCES_TOOL + ABOUT_TOOL).
func toolDefs() []toolDef {
	return []toolDef{
		{
			name: "search_legislation",
			description: "Search Hungarian statutes and regulations by keyword using full-text " +
				"search (FTS5 with BM25 ranking). " +
				"Returns matching provisions with document context, snippets with >>> <<< " +
				"markers around matched terms, and relevance scores. " +
				"Supports FTS5 syntax: quoted phrases (\"exact match\"), boolean operators " +
				"(AND, OR, NOT), and prefix wildcards (term*). " +
				"Results are in English. Default limit is 10 results. For broad topics, increase the limit. " +
				"Do NOT use this for retrieving a known provision — use get_provision instead.",
			title:  "Search legislation",
			schema: searchLegislationSchema,
		},
		{
			name: "get_provision",
			description: "Retrieve the full text of a specific provision (section) from an Hungarian statute. " +
				"Specify a document_id (Act title, abbreviation, or internal ID) and optionally a section or provision_ref. " +
				"Omit section/provision_ref to get ALL provisions in the statute (use sparingly — can be large). " +
				"Returns provision text, chapter, section number, and metadata. " +
				"Supports Act title references (e.g., \"2011. évi CXII. törvény\"), abbreviations, and full titles. " +
				"Use this when you know WHICH provision you want. For discovery, use search_legislation instead.",
			title:  "Get provision text",
			schema: getProvisionSchema,
		},
		{
			name: "validate_citation",
			description: "Validate an Hungarian legal citation against the database — zero-hallucination check. " +
				"Parses the citation, checks that the document and provision exist, and returns warnings about status " +
				"(repealed, amended). Use this to verify any citation BEFORE including it in a legal analysis. " +
				"Supports formats: \"2011. évi CXII. törvény 3. §\", \"act-cxii-2011-info-self-determination s 3\", \"s 3\".",
			title:  "Validate citation",
			schema: validateCitationSchema,
		},
		{
			name: "build_legal_stance",
			description: "Build a comprehensive set of citations for a legal question by " +
				"searching across all Hungarian statutes simultaneously. " +
				"Returns aggregated results from multiple relevant provisions, useful for legal research on a topic. " +
				"Use this for broad legal questions like \"What are the penalties for data breaches in Hungary?\" " +
				"rather than looking up a specific known provision.",
			title:  "Build legal stance",
			schema: buildLegalStanceSchema,
		},
		{
			name: "format_citation",
			description: "Format an Hungarian legal citation per standard conventions. " +
				"Two formats: \"full\" (formal, e.g., \"Infotörvény 3. §\" from \"Section 3 Infotörvény\"), " +
				"\"pinpoint\" (section reference only, e.g., \"3. §\").",
			title:  "Format citation",
			schema: formatCitationSchema,
		},
		{
			name: "check_currency",
			description: "Check whether an Hungarian statute is currently in force, amended, repealed, or not yet in force. " +
				"Returns the document status, issued date, in-force date, and warnings. " +
				"Essential before citing any provision — always verify currency.",
			title:  "Check currency status",
			schema: checkCurrencySchema,
		},
		{
			name: "get_eu_basis",
			description: "Get the EU legal basis that an Hungarian statute references or aligns with. " +
				"As an EU Member State, Hungary transposes EU directives and implements EU regulations " +
				"(e.g., Infotörvény — the Hungarian data protection act — implements GDPR). " +
				"Returns EU document identifiers, reference types, and alignment status.",
			title:  "Get EU legal basis",
			schema: getEUBasisSchema,
		},
		{
			name: "get_hungarian_implementations",
			description: "Find all Hungarian statutes that reference or align with a specific EU directive or regulation. " +
				"Given an EU document ID (e.g., \"regulation:2016/679\" for GDPR), returns matching Hungarian statutes. " +
				"Note: Hungary is an EU Member State and transposes EU directives into national law.",
			title:  "Find Hungarian implementations",
			schema: getHungarianImplementationsSchema,
		},
		{
			name: "search_eu_implementations",
			description: "Search for EU directives and regulations that are referenced by Hungarian legislation. " +
				"Search by keyword, type (directive/regulation), or year range.",
			title:  "Search EU implementations",
			schema: searchEUImplementationsSchema,
		},
		{
			name: "get_provision_eu_basis",
			description: "Get the EU legal basis for a SPECIFIC provision within an Hungarian statute. " +
				"More granular than get_eu_basis (which operates at the statute level). " +
				"Use this for pinpoint EU alignment checks at the provision level.",
			title:  "Get provision EU basis",
			schema: getProvisionEUBasisSchema,
		},
		{
			name: "validate_eu_compliance",
			description: "Check EU alignment status for an Hungarian statute or provision. " +
				"Detects references to EU directives, alignment status, and cross-references. " +
				"Returns compliance status (compliant, partial, unclear, not_applicable) with warnings. " +
				"Note: As an EU Member State, Hungary is bound by EU law. This checks transposition and compliance status.",
			title:  "Validate EU compliance",
			schema: validateEUComplianceSchema,
		},
		{
			name: "list_sources",
			description: "Returns detailed provenance metadata for all data sources used by this server, " +
				"including the Nemzeti Jogszabálytár (National Legislation Database) " +
				"(Magyar Közlöny (Hungarian Official Gazette)). " +
				"Use this to understand what data is available, its authority, coverage scope, and known limitations. " +
				"Also returns dataset statistics (document counts, provision counts) and database build timestamp. " +
				"Call this FIRST when you need to understand what Hungarian legal data this server covers.",
			title:  "List data sources",
			schema: emptyObjectSchema,
		},
		{
			name: "about",
			description: "Server metadata, dataset statistics, freshness, and provenance. " +
				"Call this to verify data coverage, currency, and content basis before relying on results.",
			title:  "About this server",
			schema: emptyObjectSchema,
		},
	}
}

// annotations mirrors the identical annotation block every TypeScript tool
// carries: { title, readOnlyHint: true, destructiveHint: false,
// idempotentHint: true, openWorldHint: false }.
func annotations(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    true,
		DestructiveHint: new(false),
		IdempotentHint:  true,
		OpenWorldHint:   new(false),
	}
}

// Register adds all tools to the MCP server — port of registerTools in
// src/tools/registry.ts. The about tool is registered only when about is
// non-nil (mirroring the conditional ABOUT_TOOL push in buildTools), but a
// call to it is still answered with "about tool not configured" when about
// is nil, exactly as the TypeScript dispatcher does.
func Register(s *mcp.Server, db *sql.DB, about *AboutContext) {
	handlers := Handlers()
	handler := dispatch(db, about, handlers)
	for _, def := range toolDefs() {
		if def.name == "about" && about == nil {
			continue
		}
		s.AddTool(&mcp.Tool{
			Name:        def.name,
			Description: def.description,
			InputSchema: def.schema,
			Annotations: annotations(def.title),
		}, handler)
	}
}

// dispatch builds the single MCP tool-call handler that special-cases `about`
// and routes everything else through the Handlers map, passing the request
// context through so cancellation reaches the DB queries. Errors become
// in-band "Error: …" text results with IsError set — a non-nil Go error
// return would instead surface as a JSON-RPC protocol error, which the
// TypeScript server never produces.
func dispatch(db *sql.DB, about *AboutContext, handlers map[string]Handler) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
		name := req.Params.Name
		// Panic isolation: the go-sdk contains no recover(), so an uncaught
		// panic would kill the stdio server with a silent EOF. Log name, value
		// and stack to stderr (never stdout — that is the protocol channel in
		// stdio mode) and answer with a generic in-band error.
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "panic in tool %q: %v\n%s\n", name, r, debug.Stack())
				result = errorResult("Internal tool error")
				err = nil
			}
		}()

		var (
			results    any
			meta       ResponseMetadata
			handlerErr error
		)
		if name == "about" {
			if about == nil {
				return errorResult(errAboutNotConfigured.Error()), nil
			}
			results, meta, handlerErr = GetAbout(ctx, db, about)
		} else {
			h, ok := handlers[name]
			if !ok {
				return errorResult(fmt.Sprintf("Error: Unknown tool %q.", name)), nil
			}
			var args map[string]any
			args, handlerErr = decodeCallArgs(req.Params.Arguments)
			if handlerErr == nil {
				results, meta, handlerErr = h(ctx, db, args)
			}
		}
		if handlerErr != nil {
			// Server-side record: in-band results are invisible to the
			// operator, so a broken DB would otherwise go unnoticed (stderr is
			// safe in both transports — stdout is the protocol channel in
			// stdio mode).
			fmt.Fprintf(os.Stderr, "tool %q failed: %v\n", name, handlerErr)
			return errorResult("Error: " + handlerErr.Error()), nil
		}
		text := MarshalResponse(results, meta)
		if name == "about" {
			// TypeScript stringifies getAbout's bare return object directly
			// (registry.ts:378) — the only tool without the results/_metadata
			// envelope.
			text = MarshalBare(results)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil
	}
}

// errorResult builds the { content: [{type:'text', text}], isError: true }
// shape the TypeScript registry returns for failures.
func errorResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		IsError: true,
	}
}

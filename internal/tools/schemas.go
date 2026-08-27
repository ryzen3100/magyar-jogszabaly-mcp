// Input schemas for every tool — verbatim ports of the inputSchema objects
// in src/tools/registry.ts (property names, types, descriptions, enum values,
// required lists, additionalProperties). PropertyOrder preserves the
// TypeScript property insertion order in the rendered JSON.
package tools

import (
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
)

func str(desc string) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Description: desc}
}

func num(desc string, def string) *jsonschema.Schema {
	s := &jsonschema.Schema{Type: "number", Description: desc}
	if def != "" {
		s.Default = json.RawMessage(def)
	}
	return s
}

func boolean(desc string, def string) *jsonschema.Schema {
	s := &jsonschema.Schema{Type: "boolean", Description: desc}
	if def != "" {
		s.Default = json.RawMessage(def)
	}
	return s
}

// emptyObjectSchema is { type: 'object', properties: {}, additionalProperties: false }.
// A schema of exactly {"not":{}} is this library's canonical `false`.
var emptyObjectSchema = &jsonschema.Schema{
	Type:                 "object",
	Properties:           map[string]*jsonschema.Schema{},
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
}

var searchLegislationSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"query": str("Search query in English. Supports FTS5 syntax: " +
			"\"personal information\" for exact phrase, privacy* for prefix."),
		"document_id": str("Optional: filter results to a specific statute by its document ID."),
		"status": {
			Type:        "string",
			Description: "Optional: filter by legislative status.",
			Enum:        []any{"in_force", "amended", "repealed"},
		},
		"limit": num("Maximum results to return (default: 10, max: 50).", "10"),
	},
	PropertyOrder: []string{"query", "document_id", "status", "limit"},
	Required:      []string{"query"},
}

var getProvisionSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"document_id": str("Statute identifier: Act title (e.g., \"2011. évi CXII. törvény\"), abbreviation, " +
			"or internal document ID (e.g., \"act-cxii-2011-info-self-determination\")."),
		"section":       str("Section number (e.g., \"13\", \"8\"). Omit to get all provisions."),
		"provision_ref": str("Direct provision reference (e.g., \"s13\"). Alternative to section parameter."),
	},
	PropertyOrder: []string{"document_id", "section", "provision_ref"},
	Required:      []string{"document_id"},
}

var validateCitationSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"citation": str("Citation string to validate. Examples: \"2011. évi CXII. törvény 3. §\", \"act-cxii-2011-info-self-determination s 3\"."),
	},
	PropertyOrder: []string{"citation"},
	Required:      []string{"citation"},
}

var buildLegalStanceSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"query":       str("Legal question or topic to research (e.g., \"personal information\", \"critical infrastructure\")."),
		"document_id": str("Optional: limit search to one statute by document ID."),
		"limit":       num("Max results per category (default: 5, max: 20).", "5"),
	},
	PropertyOrder: []string{"query", "document_id", "limit"},
	Required:      []string{"query"},
}

var formatCitationSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"citation": str("Citation string to format."),
		"format": {
			Type:        "string",
			Description: "Output format (default: \"full\").",
			Enum:        []any{"full", "pinpoint"},
			Default:     json.RawMessage(`"full"`),
		},
	},
	PropertyOrder: []string{"citation", "format"},
	Required:      []string{"citation"},
}

var checkCurrencySchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"document_id": str("Statute identifier (Act title, abbreviation, or ID)."),
	},
	PropertyOrder: []string{"document_id"},
	Required:      []string{"document_id"},
}

var getEUBasisSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"document_id":      str("Hungarian statute identifier."),
		"include_articles": boolean("Include specific EU article references (default: false).", "false"),
		"reference_types": {
			Type:        "array",
			Description: "Optional: filter by reference type (e.g., \"implements\", \"transposes\").",
			Items:       &jsonschema.Schema{Type: "string"},
		},
	},
	PropertyOrder: []string{"document_id", "include_articles", "reference_types"},
	Required:      []string{"document_id"},
}

var getHungarianImplementationsSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"eu_document_id": str("EU document ID (e.g., \"regulation:2016/679\" for GDPR, \"directive:2022/2555\" for NIS2)."),
		"primary_only":   boolean("Return only primary referencing statutes (default: false).", "false"),
		"in_force_only":  boolean("Return only currently in-force statutes (default: false).", "false"),
	},
	PropertyOrder: []string{"eu_document_id", "primary_only", "in_force_only"},
	Required:      []string{"eu_document_id"},
}

var searchEUImplementationsSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"query": str("Keyword search across EU document titles."),
		"type": {
			Type:        "string",
			Description: "Filter by EU document type.",
			Enum:        []any{"directive", "regulation"},
		},
		"year_from":                    num("Filter by year (from).", ""),
		"year_to":                      num("Filter by year (to).", ""),
		"has_hungarian_implementation": boolean("If true, only return EU documents referenced by Hungarian legislation.", ""),
		"limit":                        num("Max results (default: 20, max: 100).", "20"),
	},
	PropertyOrder: []string{"query", "type", "year_from", "year_to", "has_hungarian_implementation", "limit"},
}

var getProvisionEUBasisSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"document_id":   str("Hungarian statute identifier."),
		"provision_ref": str("Provision reference (e.g., \"s13\" or \"13\")."),
	},
	PropertyOrder: []string{"document_id", "provision_ref"},
	Required:      []string{"document_id", "provision_ref"},
}

var validateEUComplianceSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"document_id":    str("Hungarian statute identifier."),
		"eu_document_id": str("Optional: check against a specific EU document."),
	},
	PropertyOrder: []string{"document_id", "eu_document_id"},
	Required:      []string{"document_id"},
}

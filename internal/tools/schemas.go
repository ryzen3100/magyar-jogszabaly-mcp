// Input schemas for every tool — verbatim ports of the inputSchema objects
// in src/tools/registry.ts (property names, types, descriptions, enum values,
// required lists, additionalProperties). PropertyOrder preserves the
// TypeScript property insertion order in the rendered JSON.
package tools

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
)

// Argument caps shared by the JSON schemas below and the handlers' own
// enforcement — the SDK treats input schemas as advisory, so handlers must
// validate the same limits themselves (see checkMaxLength etc.).
const (
	maxQueryLength        = 512
	maxDocumentIDLength   = 256
	maxEuDocumentIDLength = 128
	maxCitationLength     = 512
	maxRefLength          = 64
	maxEnumLength         = 20
	maxReferenceTypes     = 16
	maxReferenceTypeLen   = 64
)

// Enum values enforced by the handlers; the schemas below declare the same
// lists verbatim.
var (
	statusEnumValues = []string{"in_force", "amended", "repealed"}
	formatEnumValues = []string{"full", "pinpoint"}
	euTypeEnumValues = []string{"directive", "regulation"}
)

func str(desc string, maxLength int) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Description: desc, MaxLength: &maxLength}
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

func intPtr(v int) *int { return &v }

// validateArgs returns the first non-nil argument check error, so handlers
// can list their checks in schema order.
func validateArgs(checks ...error) error {
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	return nil
}

// checkRequired reports the missing-argument error for a schema-required
// field the decoded args left absent.
func checkRequired[T any](name string, v *T) error {
	if v == nil {
		return fmt.Errorf("missing required argument %q", name)
	}
	return nil
}

// checkMaxLength enforces a schema maxLength on a decoded string argument.
func checkMaxLength(name string, v *string, maxLength int) error {
	if v != nil && utf8.RuneCountInString(*v) > maxLength {
		return fmt.Errorf("invalid argument %q: longer than %d characters", name, maxLength)
	}
	return nil
}

// checkEnum enforces a declared enum on a decoded string argument; empty
// stays allowed, matching the falsy guards around the filter use.
func checkEnum(name string, v *string, allowed ...string) error {
	if v == nil || *v == "" || slices.Contains(allowed, *v) {
		return nil
	}
	return fmt.Errorf("invalid argument %q: must be one of %s", name, strings.Join(allowed, ", "))
}

// checkStringList enforces maxItems and per-item maxLength on a decoded
// string-array argument.
func checkStringList(name string, v []string, maxItems, itemMaxLen int) error {
	if len(v) > maxItems {
		return fmt.Errorf("invalid argument %q: more than %d items", name, maxItems)
	}
	for _, s := range v {
		if utf8.RuneCountInString(s) > itemMaxLen {
			return fmt.Errorf("invalid argument %q: item longer than %d characters", name, itemMaxLen)
		}
	}
	return nil
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
		"query": str("Search query in English. Supports FTS5 syntax: "+
			"\"personal information\" for exact phrase, privacy* for prefix.", maxQueryLength),
		"document_id": str("Optional: filter results to a specific statute by its document ID.", maxDocumentIDLength),
		"status": {
			Type:        "string",
			Description: "Optional: filter by legislative status.",
			Enum:        []any{"in_force", "amended", "repealed"},
			MaxLength:   intPtr(maxEnumLength),
		},
		"limit": num("Maximum results to return (default: 10, max: 50).", "10"),
	},
	PropertyOrder: []string{"query", "document_id", "status", "limit"},
	Required:      []string{"query"},
}

var getProvisionSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"document_id": str("Statute identifier: Act title (e.g., \"2011. évi CXII. törvény\"), abbreviation, "+
			"or internal document ID (e.g., \"act-cxii-2011-info-self-determination\").", maxDocumentIDLength),
		"section":       str("Section number (e.g., \"13\", \"8\"). Omit to get all provisions.", maxRefLength),
		"provision_ref": str("Direct provision reference (e.g., \"s13\"). Alternative to section parameter.", maxRefLength),
	},
	PropertyOrder: []string{"document_id", "section", "provision_ref"},
	Required:      []string{"document_id"},
}

var validateCitationSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"citation": str("Citation string to validate. Examples: \"2011. évi CXII. törvény 3. §\", "+
			"\"act-cxii-2011-info-self-determination s 3\".", maxCitationLength),
	},
	PropertyOrder: []string{"citation"},
	Required:      []string{"citation"},
}

var buildLegalStanceSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"query": str("Legal question or topic to research "+
			"(e.g., \"personal information\", \"critical infrastructure\").", maxQueryLength),
		"document_id": str("Optional: limit search to one statute by document ID.", maxDocumentIDLength),
		"limit":       num("Max results per category (default: 5, max: 20).", "5"),
	},
	PropertyOrder: []string{"query", "document_id", "limit"},
	Required:      []string{"query"},
}

var formatCitationSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"citation": str("Citation string to format.", maxCitationLength),
		"format": {
			Type:        "string",
			Description: "Output format (default: \"full\").",
			Enum:        []any{"full", "pinpoint"},
			Default:     json.RawMessage(`"full"`),
			MaxLength:   intPtr(maxEnumLength),
		},
	},
	PropertyOrder: []string{"citation", "format"},
	Required:      []string{"citation"},
}

var checkCurrencySchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"document_id": str("Statute identifier (Act title, abbreviation, or ID).", maxDocumentIDLength),
	},
	PropertyOrder: []string{"document_id"},
	Required:      []string{"document_id"},
}

var getEUBasisSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"document_id":      str("Hungarian statute identifier.", maxDocumentIDLength),
		"include_articles": boolean("Include specific EU article references (default: false).", "false"),
		"reference_types": {
			Type:        "array",
			Description: "Optional: filter by reference type (e.g., \"implements\", \"transposes\").",
			Items:       &jsonschema.Schema{Type: "string", MaxLength: intPtr(maxReferenceTypeLen)},
			MaxItems:    intPtr(maxReferenceTypes),
		},
	},
	PropertyOrder: []string{"document_id", "include_articles", "reference_types"},
	Required:      []string{"document_id"},
}

var getHungarianImplementationsSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"eu_document_id": str("EU document ID (e.g., \"regulation:2016/679\" for GDPR, "+
			"\"directive:2022/2555\" for NIS2).", maxEuDocumentIDLength),
		"primary_only":  boolean("Return only primary referencing statutes (default: false).", "false"),
		"in_force_only": boolean("Return only currently in-force statutes (default: false).", "false"),
	},
	PropertyOrder: []string{"eu_document_id", "primary_only", "in_force_only"},
	Required:      []string{"eu_document_id"},
}

var searchEUImplementationsSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"query": str("Keyword search across EU document titles.", maxQueryLength),
		"type": {
			Type:        "string",
			Description: "Filter by EU document type.",
			Enum:        []any{"directive", "regulation"},
			MaxLength:   intPtr(maxEnumLength),
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
		"document_id":   str("Hungarian statute identifier.", maxDocumentIDLength),
		"provision_ref": str("Provision reference (e.g., \"s13\" or \"13\").", maxRefLength),
	},
	PropertyOrder: []string{"document_id", "provision_ref"},
	Required:      []string{"document_id", "provision_ref"},
}

var validateEUComplianceSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"document_id":    str("Hungarian statute identifier.", maxDocumentIDLength),
		"eu_document_id": str("Optional: check against a specific EU document.", maxEuDocumentIDLength),
	},
	PropertyOrder: []string{"document_id", "eu_document_id"},
	Required:      []string{"document_id"},
}

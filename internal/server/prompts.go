// HTTP-only prompts — port of the prompts handlers in src/http-server.ts.
// The stdio entrypoint advertises tools only; prompts exist solely in HTTP
// mode.

package server

import (
	"cmp"
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerPrompts adds the two HTTP-mode prompts. Both render as a single
// user text message, exactly like the TS GetPromptRequestSchema handler.
func registerPrompts(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name: "legal_review",
		Description: "Review a Hungarian legal document, contract, or policy for compliance issues, risks, and missing elements. " +
			"Returns structured findings with risk levels and specific legal references.",
		Arguments: []*mcp.PromptArgument{
			{Name: "document_text", Description: "The full text of the document to review", Required: true},
			{Name: "focus_area", Description: "Optional focus: gdpr, contract, employment, consumer, corporate"},
		},
	}, renderPrompt)

	s.AddPrompt(&mcp.Prompt{
		Name: "legal_research",
		Description: "Research a Hungarian legal question across all statutes. " +
			"Returns relevant provisions, EU cross-references, and practical guidance for SMEs.",
		Arguments: []*mcp.PromptArgument{
			{Name: "question", Description: "The legal question in plain language (Hungarian or English)", Required: true},
		},
	}, renderPrompt)
}

// renderPrompt builds the single user message for both prompts. Missing or
// empty arguments fall back to the same defaults as the TS
// `args?.focus_area || 'all'` expressions.
func renderPrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := req.Params.Arguments
	var text string
	switch req.Params.Name {
	case "legal_review":
		text = fmt.Sprintf(
			"Review the following Hungarian legal document for compliance issues, risks, and missing elements.\n\nFocus area: %s\n\nDocument:\n%s",
			cmp.Or(args["focus_area"], "all"),
			cmp.Or(args["document_text"], "(no document provided)"))
	case "legal_research":
		text = fmt.Sprintf(
			"Research this Hungarian legal question using the legislation database. Cite specific provisions with section numbers.\n\nQuestion: %s",
			cmp.Or(args["question"], "(no question provided)"))
	default:
		return nil, fmt.Errorf("unknown prompt: %s", req.Params.Name)
	}
	return &mcp.GetPromptResult{
		Messages: []*mcp.PromptMessage{{Role: mcp.Role("user"), Content: &mcp.TextContent{Text: text}}},
	}, nil
}

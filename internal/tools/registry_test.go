package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/store/storetest"
)

// callDispatch drives the registry dispatcher with a raw call, like the TS
// registry tests do through the mock server.
func callDispatch(t *testing.T, db *sql.DB, about *AboutContext, handlers map[string]Handler, name string, args json.RawMessage) *mcp.CallToolResult {
	t.Helper()
	result, err := dispatch(db, about, handlers)(context.Background(), &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Name: name, Arguments: args},
	})
	if err != nil {
		t.Fatalf("dispatch returned protocol error: %v", err)
	}
	return result
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("expected exactly one content block, got %d", len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return text.Text
}

func TestDispatchUnknownTool(t *testing.T) {
	db := storetest.NewTestDb(t)

	res := callDispatch(t, db, nil, Handlers(), "missing_tool", json.RawMessage(`{}`))
	if !res.IsError {
		t.Error("expected isError")
	}
	if got, want := resultText(t, res), `Error: Unknown tool "missing_tool".`; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
}

func TestDispatchAboutWithoutContext(t *testing.T) {
	db := storetest.NewTestDb(t)

	res := callDispatch(t, db, nil, Handlers(), "about", json.RawMessage(`{}`))
	if !res.IsError {
		t.Error("expected isError")
	}
	if got := resultText(t, res); got != "about tool not configured" {
		t.Errorf("text = %q", got)
	}
}

func TestDispatchHandlerErrorBecomesToolResult(t *testing.T) {
	db := storetest.NewTestDb(t)

	handlers := map[string]Handler{
		"search_legislation": func(_ context.Context, _ *sql.DB, _ map[string]any) (any, ResponseMetadata, error) {
			return nil, ResponseMetadata{}, errForTest("forced failure")
		},
	}
	res := callDispatch(t, db, &AboutContext{}, handlers, "search_legislation", json.RawMessage(`{"query":"x"}`))
	if !res.IsError {
		t.Error("expected isError")
	}
	if got := resultText(t, res); got != "Error: forced failure" {
		t.Errorf("text = %q", got)
	}
}

func TestDispatchSuccessEnvelope(t *testing.T) {
	db := storetest.NewTestDb(t)

	handlers := map[string]Handler{
		"search_legislation": func(ctx context.Context, db *sql.DB, _ map[string]any) (any, ResponseMetadata, error) {
			return []SearchLegislationResult{{DocumentID: "doc-inforce", DocumentTitle: "In Force Act", ProvisionRef: "s1", Section: "1", Snippet: "snip", Relevance: -1.5}}, GenerateResponseMetadata(ctx, db), nil
		},
	}
	res := callDispatch(t, db, nil, handlers, "search_legislation", json.RawMessage(`{"query":"x"}`))
	if res.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, res))
	}
	text := resultText(t, res)
	if !strings.HasPrefix(text, `{"results":[{`) || !strings.Contains(text, `"_metadata":{`) {
		t.Errorf("unexpected envelope: %s", text)
	}
	// The text block must be the exact compact serialization of the envelope.
	var probe map[string]any
	if err := json.Unmarshal([]byte(text), &probe); err != nil {
		t.Fatalf("envelope is not valid JSON: %v", err)
	}
	if _, ok := probe["structuredContent"]; ok {
		t.Error("no structured content is expected in the wire result")
	}
}

func TestToolDefsOrderAndCompleteness(t *testing.T) {
	want := []string{
		"search_legislation",
		"get_provision",
		"validate_citation",
		"build_legal_stance",
		"format_citation",
		"check_currency",
		"get_eu_basis",
		"get_hungarian_implementations",
		"search_eu_implementations",
		"get_provision_eu_basis",
		"validate_eu_compliance",
		"list_sources",
		"about",
	}
	defs := toolDefs()
	if len(defs) != len(want) {
		t.Fatalf("tool count = %d, want %d", len(defs), len(want))
	}
	for i, def := range defs {
		if def.name != want[i] {
			t.Errorf("position %d = %q, want %q", i, def.name, want[i])
		}
		if def.description == "" || def.title == "" || def.schema == nil {
			t.Errorf("tool %q incomplete", def.name)
		}
	}
}

func TestHandlersCoverAllNonAboutTools(t *testing.T) {
	handlers := Handlers()
	if len(handlers) != 12 {
		t.Fatalf("handler count = %d, want 12", len(handlers))
	}
	for _, name := range []string{
		"search_legislation", "get_provision", "validate_citation", "build_legal_stance",
		"format_citation", "check_currency", "get_eu_basis", "get_hungarian_implementations",
		"search_eu_implementations", "get_provision_eu_basis", "validate_eu_compliance", "list_sources",
	} {
		if _, ok := handlers[name]; !ok {
			t.Errorf("handler %q missing", name)
		}
	}
	if _, ok := handlers["about"]; ok {
		t.Error("about must not be in the Handlers map")
	}
}

func TestMarshalResponseOmitsEmptyOptionals(t *testing.T) {
	meta := GenerateResponseMetadata(context.Background(), storetest.NewTestDb(t))
	out := MarshalResponse([]any{}, meta)
	want := `{"results":[],"_metadata":{"data_source":` + quoteJSON(meta.DataSource) +
		`,"jurisdiction":"HU","disclaimer":` + quoteJSON(meta.Disclaimer) +
		`,"freshness":"2026-02-21T00:00:00Z"}}`
	if out != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
}

func TestMarshalResponseIncludesNoteAndStrategy(t *testing.T) {
	meta := GenerateResponseMetadata(context.Background(), storetest.NewTestDb(t))
	meta.Note = "n"
	meta.QueryStrategy = "broadened"
	out := MarshalResponse(map[string]any{}, meta)
	if !strings.Contains(out, `,"note":"n","query_strategy":"broadened"`) {
		t.Errorf("optional keys must come last in order: %s", out)
	}
}

func TestDispatchRecoversHandlerPanic(t *testing.T) {
	db := storetest.NewTestDb(t)

	handlers := map[string]Handler{
		"search_legislation": func(_ context.Context, _ *sql.DB, _ map[string]any) (any, ResponseMetadata, error) {
			panic("boom")
		},
	}
	res := callDispatch(t, db, nil, handlers, "search_legislation", json.RawMessage(`{}`))
	if !res.IsError {
		t.Error("expected isError")
	}
	if got := resultText(t, res); got != "Internal tool error" {
		t.Errorf("text = %q, want %q", got, "Internal tool error")
	}
}

// errForTest is a tiny helper so test handlers can return a plain error.
func errForTest(msg string) error { return &testError{msg} }

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

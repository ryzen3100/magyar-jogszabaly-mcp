package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/store/storetest"
)

// callDispatch drives the registry dispatcher with a raw call, like the TS
// registry tests do through the mock server.
func callDispatch(
	t *testing.T, db *sql.DB, about *AboutContext, handlers map[string]Handler, name string, args json.RawMessage,
) *mcp.CallToolResult {
	t.Helper()
	result, err := dispatch(db, about, handlers)(t.Context(), &mcp.CallToolRequest{
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	db := storetest.NewTestDb(t)

	handlers := map[string]Handler{
		"search_legislation": func(ctx context.Context, db *sql.DB, _ map[string]any) (any, ResponseMetadata, error) {
			return []SearchLegislationResult{{
				DocumentID: "doc-inforce", DocumentTitle: "In Force Act", ProvisionRef: "s1",
				Section: "1", Snippet: "snip", Relevance: -1.5,
			}}, GenerateResponseMetadata(ctx, db), nil
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	meta := GenerateResponseMetadata(t.Context(), storetest.NewTestDb(t))
	out := MarshalResponse([]any{}, meta)
	want := `{"results":[],"_metadata":{"data_source":` + quoteJSON(meta.DataSource) +
		`,"jurisdiction":"HU","disclaimer":` + quoteJSON(meta.Disclaimer) +
		`,"freshness":"2026-02-21T00:00:00Z"}}`
	if out != want {
		t.Errorf("got  %s\nwant %s", out, want)
	}
}

func TestMarshalResponseIncludesNoteAndStrategy(t *testing.T) {
	t.Parallel()
	meta := GenerateResponseMetadata(t.Context(), storetest.NewTestDb(t))
	meta.Note = "n"
	meta.QueryStrategy = "broadened"
	out := MarshalResponse(map[string]any{}, meta)
	if !strings.Contains(out, `,"note":"n","query_strategy":"broadened"`) {
		t.Errorf("optional keys must come last in order: %s", out)
	}
}

func TestDispatchRecoversHandlerPanic(t *testing.T) {
	t.Parallel()
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

// connectTools registers the full tool set on an in-memory SDK server and
// returns a connected client session, exercising the Register wiring path
// (AddTool order, schemas, annotations) end to end.
func connectTools(t *testing.T, db *sql.DB, about *AboutContext) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	Register(s, db, about)
	if _, err := s.Connect(t.Context(), serverTransport, nil); err != nil {
		t.Fatalf("connect server: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestRegisterAllThirteenTools(t *testing.T) {
	t.Parallel()
	cs := connectTools(t, storetest.NewTestDb(t), &AboutContext{Version: "1.0.0"})

	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 13 {
		t.Fatalf("registered tools = %d, want 13", len(res.Tools))
	}
	byName := make(map[string]*mcp.Tool, len(res.Tools))
	for _, tl := range res.Tools {
		byName[tl.Name] = tl
	}
	for _, def := range toolDefs() {
		tl, ok := byName[def.name]
		if !ok {
			t.Errorf("tool %q not registered", def.name)
			continue
		}
		if tl.InputSchema == nil {
			t.Errorf("tool %q has no input schema", def.name)
		}
		if tl.Annotations == nil || tl.Annotations.Title != def.title {
			t.Errorf("tool %q annotations = %+v, want title %q", def.name, tl.Annotations, def.title)
		}
	}
}

func TestRegisterNilAboutSkipsAboutTool(t *testing.T) {
	t.Parallel()
	cs := connectTools(t, storetest.NewTestDb(t), nil)

	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 12 {
		t.Fatalf("registered tools = %d, want 12 (about skipped)", len(res.Tools))
	}
	if slices.ContainsFunc(res.Tools, func(tl *mcp.Tool) bool { return tl.Name == "about" }) {
		t.Fatal("about must not be registered when the AboutContext is nil")
	}
}

func TestDispatchAboutSuccessBareJSON(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)

	res := callDispatch(t, db, &AboutContext{Version: "1.0.0", Fingerprint: "fp", DBBuilt: "2026-02-21T00:00:00Z"},
		Handlers(), "about", json.RawMessage(`{}`))
	if res.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, res))
	}
	text := resultText(t, res)
	var probe map[string]any
	if err := json.Unmarshal([]byte(text), &probe); err != nil {
		t.Fatalf("payload is not valid JSON: %v\n%s", err, text)
	}
	if probe["name"] != "Hungarian Law MCP" || probe["version"] != "1.0.0" {
		t.Fatalf("payload = %s", text)
	}
	if _, ok := probe["stats"]; !ok {
		t.Fatalf("payload missing stats: %s", text)
	}
	// about is the only tool stringified WITHOUT the results/_metadata
	// envelope (registry.ts:378 parity).
	if strings.Contains(text, `"results"`) || strings.Contains(text, `"_metadata"`) {
		t.Errorf("about must be bare JSON without the envelope: %s", text)
	}
}

// sampleArgs holds one safe value per argument name, satisfying the handlers'
// maxLength/enum checks without touching optional arguments.
var sampleArgs = map[string]any{
	"query":          "személyes adat",
	"document_id":    "doc-inforce",
	"provision_ref":  "s1",
	"citation":       "In Force Act s 1",
	"eu_document_id": "regulation:2016/679",
}

// lenientRequired names the tools whose schema marks query as required while
// the handler (TS parity: runSearch's falsy guard) degrades a missing query
// to empty results instead of erroring — the one deliberate schema/handler
// divergence this test allows.
var lenientRequired = map[string]bool{
	"search_legislation": true,
	"build_legal_stance": true,
}

// TestSchemaRequiredMatchesHandlerEnforcement pins the required lists in
// schemas.go to what the handlers actually enforce, so future drift in either
// direction (schema demands what the handler ignores, or vice versa) fails
// here.
func TestSchemaRequiredMatchesHandlerEnforcement(t *testing.T) {
	t.Parallel()
	db := storetest.NewTestDb(t)
	handlers := Handlers()

	for _, def := range toolDefs() {
		schema := def.schema
		if schema.Type != "object" {
			t.Errorf("%s: schema type = %q, want object", def.name, schema.Type)
		}
		for _, req := range schema.Required {
			if _, ok := schema.Properties[req]; !ok {
				t.Errorf("%s: required field %q is not declared in properties", def.name, req)
			}
		}
		if def.name == "about" {
			continue // dispatch special-cases it; no Handler entry exists
		}
		h, ok := handlers[def.name]
		if !ok {
			t.Errorf("%s: no handler in Handlers()", def.name)
			continue
		}

		// Omit one required field at a time: every tool must answer with
		// exactly that field's missing-argument error — except the two
		// documented lenient ones, which must degrade silently.
		for _, req := range schema.Required {
			args := make(map[string]any, len(schema.Required))
			for _, other := range schema.Required {
				if other == req {
					continue
				}
				v, ok := sampleArgs[other]
				if !ok {
					t.Fatalf("%s: no sample value for required field %q — add one to sampleArgs", def.name, other)
				}
				args[other] = v
			}
			_, _, err := h(t.Context(), db, args)
			if lenientRequired[def.name] {
				if err != nil {
					t.Errorf("%s: omitting %q must degrade to empty results, got %v", def.name, req, err)
				}
				continue
			}
			if err == nil || !strings.Contains(err.Error(), "missing required argument") ||
				!strings.Contains(err.Error(), `"`+req+`"`) {
				t.Errorf("%s: omitting required %q → err %v, want missing-argument error naming it", def.name, req, err)
			}
		}

		// Providing every schema-required field must never trip any
		// missing-argument guard — catches handlers demanding a field the
		// schema does not require.
		args := make(map[string]any, len(schema.Required))
		for _, req := range schema.Required {
			v, ok := sampleArgs[req]
			if !ok {
				t.Fatalf("%s: no sample value for required field %q — add one to sampleArgs", def.name, req)
			}
			args[req] = v
		}
		if _, _, err := h(t.Context(), db, args); err != nil && strings.Contains(err.Error(), "missing required argument") {
			t.Errorf("%s: handler enforces a missing argument the schema does not require: %v", def.name, err)
		}

		// Empty args: required-carrying tools (except the lenient two) must
		// fail their guard; the rest must sail through.
		_, _, err := h(t.Context(), db, map[string]any{})
		switch {
		case len(schema.Required) > 0 && !lenientRequired[def.name]:
			if err == nil || !strings.Contains(err.Error(), "missing required argument") {
				t.Errorf("%s: empty args → err %v, want missing-argument error", def.name, err)
			}
		case lenientRequired[def.name]:
			if err != nil {
				t.Errorf("%s: empty args must degrade to empty results, got %v", def.name, err)
			}
		default:
			if err != nil {
				t.Errorf("%s: schema requires nothing, but empty args errored: %v", def.name, err)
			}
		}
	}
}

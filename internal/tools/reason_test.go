// Tests for the per-tool-call self-narration surface (`_reason`).
// Pinned at the spec/engine layer because every UI surface depends on
// these contracts being stable: the schema MUST advertise _reason as
// optional, the engine MUST strip it before dispatch, and the publisher
// callback MUST receive the trimmed text exactly once per call.

package tools

import (
	"context"
	"sync"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// TestJSONSchemaIncludesReasonField: every tool's generated schema must
// expose the optional virtual `_reason` property. Provider serializers
// pass this schema verbatim to the model, so a regression here means
// models can never learn to fill the field.
func TestJSONSchemaIncludesReasonField(t *testing.T) {
	spec := ToolSpec{
		Name: "demo",
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true},
		},
	}
	schema := spec.JSONSchema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema missing properties block")
	}
	reason, ok := props[ReasonField].(map[string]any)
	if !ok {
		t.Fatalf("schema missing %q property; got %v", ReasonField, props)
	}
	if reason["type"] != "string" {
		t.Errorf("%s.type = %v, want string", ReasonField, reason["type"])
	}
	if _, hasDesc := reason["description"]; !hasDesc {
		t.Errorf("%s missing description (host pickers need it)", ReasonField)
	}
	// _reason MUST stay optional. If a future change makes it required
	// the schema's `required` array would include it — assert against
	// that explicitly so a regression bites in CI.
	if req, ok := schema["required"].([]string); ok {
		for _, r := range req {
			if r == ReasonField {
				t.Fatalf("%s must be optional, found in required: %v", ReasonField, req)
			}
		}
	}
}

// TestExtractReasonHappyPath: a non-empty string is stripped from the
// map, trimmed, and returned with ok=true.
func TestExtractReasonHappyPath(t *testing.T) {
	params := map[string]any{
		"path":      "main.go",
		ReasonField: "  checking how the SSE handler closes the stream  ",
	}
	reason, ok := ExtractReason(params)
	if !ok {
		t.Fatal("ExtractReason returned ok=false on a normal call")
	}
	if reason != "checking how the SSE handler closes the stream" {
		t.Errorf("reason text not trimmed: %q", reason)
	}
	if _, still := params[ReasonField]; still {
		t.Errorf("_reason was not stripped from params")
	}
	if params["path"] != "main.go" {
		t.Errorf("ExtractReason mutated unrelated keys")
	}
}

// TestExtractReasonMissing: when the field isn't present, ok=false and
// the map is left alone — the most common case.
func TestExtractReasonMissing(t *testing.T) {
	params := map[string]any{"path": "main.go"}
	reason, ok := ExtractReason(params)
	if ok {
		t.Fatalf("ExtractReason returned ok=true on missing field; reason=%q", reason)
	}
	if len(params) != 1 {
		t.Errorf("ExtractReason mutated map without _reason; got %v", params)
	}
}

// TestExtractReasonEmptyAndWhitespace: empty / whitespace strings are
// stripped (so they don't reach the tool) but ok=false (so no UI event
// fires for a useless narration).
func TestExtractReasonEmptyAndWhitespace(t *testing.T) {
	for _, tc := range []string{"", "   ", "\t\n"} {
		params := map[string]any{ReasonField: tc, "path": "x"}
		reason, ok := ExtractReason(params)
		if ok {
			t.Errorf("ExtractReason(%q): want ok=false, got ok=true reason=%q", tc, reason)
		}
		if _, still := params[ReasonField]; still {
			t.Errorf("ExtractReason(%q): _reason not stripped", tc)
		}
	}
}

// TestExtractReasonNonStringStripped: a non-string value (model bug)
// must be stripped silently rather than reaching the tool — tools never
// declare a _reason arg of any other type.
func TestExtractReasonNonStringStripped(t *testing.T) {
	params := map[string]any{ReasonField: 42, "path": "x"}
	reason, ok := ExtractReason(params)
	if ok || reason != "" {
		t.Errorf("non-string _reason should yield empty/false; got reason=%q ok=%v", reason, ok)
	}
	if _, still := params[ReasonField]; still {
		t.Error("non-string _reason was not stripped")
	}
}

// TestExtractReasonNilMap: defensive nil-input handling — no panic, no
// allocation, ok=false.
func TestExtractReasonNilMap(t *testing.T) {
	reason, ok := ExtractReason(nil)
	if ok || reason != "" {
		t.Errorf("nil map: want ok=false reason=\"\"; got ok=%v reason=%q", ok, reason)
	}
}

// TestEngineExecuteStripsReasonAndPublishes: Execute() peels _reason,
// invokes the publisher exactly once with (toolName, trimmed reason),
// and the underlying tool sees a params map without the field.
func TestEngineExecuteStripsReasonAndPublishes(t *testing.T) {
	cfg := config.DefaultConfig()
	eng := New(*cfg)

	var mu sync.Mutex
	var calls []struct{ name, reason string }
	eng.SetReasoningPublisher(func(name, reason string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, struct{ name, reason string }{name, reason})
	})

	// Register a probe tool that records the params it received.
	probe := &reasonProbeTool{}
	eng.Register(probe)

	params := map[string]any{
		"foo":       "bar",
		ReasonField: "  why I am calling this  ",
	}
	if _, err := eng.Execute(context.Background(), "reason_probe", Request{Params: params}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("publisher fired %d times, want 1; %+v", len(calls), calls)
	}
	if calls[0].name != "reason_probe" {
		t.Errorf("publisher got name %q, want reason_probe", calls[0].name)
	}
	if calls[0].reason != "why I am calling this" {
		t.Errorf("publisher got reason %q (expected trimmed)", calls[0].reason)
	}
	if probe.lastParams == nil {
		t.Fatal("probe tool was not called")
	}
	if _, has := probe.lastParams[ReasonField]; has {
		t.Errorf("tool implementation saw _reason in params: %v", probe.lastParams)
	}
	if probe.lastParams["foo"] != "bar" {
		t.Errorf("tool implementation lost unrelated params: %v", probe.lastParams)
	}
}

// TestEngineExecuteWithoutReasonNoPublish: a tool call without _reason
// must NOT trigger the publisher — silent narration would clutter the
// UI and waste cycles on the empty-string case.
func TestEngineExecuteWithoutReasonNoPublish(t *testing.T) {
	cfg := config.DefaultConfig()
	eng := New(*cfg)
	fired := 0
	eng.SetReasoningPublisher(func(_, _ string) { fired++ })
	eng.Register(&reasonProbeTool{})

	if _, err := eng.Execute(context.Background(), "reason_probe", Request{Params: map[string]any{"foo": "bar"}}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if fired != 0 {
		t.Errorf("publisher fired %d times on a no-reason call; want 0", fired)
	}
}

// TestEngineExecuteNilPublisherStillStrips: when no publisher is wired,
// Execute() still strips _reason — keeps the tool implementation
// contract clean even in embedded use without an event bus.
func TestEngineExecuteNilPublisherStillStrips(t *testing.T) {
	cfg := config.DefaultConfig()
	eng := New(*cfg)
	probe := &reasonProbeTool{}
	eng.Register(probe)

	params := map[string]any{ReasonField: "noisy", "foo": "bar"}
	if _, err := eng.Execute(context.Background(), "reason_probe", Request{Params: params}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if probe.lastParams == nil {
		t.Fatal("probe tool was not called")
	}
	if _, has := probe.lastParams[ReasonField]; has {
		t.Errorf("tool saw _reason despite nil publisher: %v", probe.lastParams)
	}
}

// reasonProbeTool records the params it was called with. Lives in the
// tools package so tests can register it without import cycles.
type reasonProbeTool struct {
	lastParams map[string]any
}

func (p *reasonProbeTool) Name() string        { return "reason_probe" }
func (p *reasonProbeTool) Description() string { return "test probe" }
func (p *reasonProbeTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "reason_probe",
		Summary: "records params for assertion",
		Risk:    RiskRead,
		Args: []Arg{
			{Name: "foo", Type: ArgString},
		},
	}
}
func (p *reasonProbeTool) Execute(_ context.Context, req Request) (Result, error) {
	p.lastParams = req.Params
	return Result{Output: "ok"}, nil
}

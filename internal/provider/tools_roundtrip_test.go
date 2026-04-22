package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// TestAnthropicToolCallRoundTrip verifies that:
//  1. ToolDescriptor + ToolChoice serialize into Anthropic's body shape.
//  2. A prior tool round-trip (assistant tool_use + user tool_result) is
//     rewritten as typed content blocks on the outgoing request.
//  3. A response carrying a tool_use block is surfaced as resp.ToolCalls with
//     StopReason=StopTool.
func TestAnthropicToolCallRoundTrip(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "model":"claude-sonnet-4-6",
  "stop_reason":"tool_use",
  "usage":{"input_tokens":7,"output_tokens":9},
  "content":[
    {"type":"text","text":"calling grep"},
    {"type":"tool_use","id":"toolu_abc","name":"grep_codebase","input":{"pattern":"Engine","path":"internal"}}
  ]
}`))
	}))
	defer srv.Close()

	p := NewAnthropicProvider("claude-sonnet-4-6", "k", srv.URL, 2048, 1000000)
	resp, err := p.Complete(context.Background(), CompletionRequest{
		System: "you are helpful",
		Messages: []Message{
			{Role: types.RoleUser, Content: "find Engine"},
			{
				Role:    types.RoleAssistant,
				Content: "let me look",
				ToolCalls: []ToolCall{
					{ID: "toolu_prev", Name: "read_file", Input: map[string]any{"path": "README.md"}},
				},
			},
			{
				Role:       types.RoleUser,
				Content:    "file contents omitted",
				ToolCallID: "toolu_prev",
				ToolName:   "read_file",
			},
		},
		Tools: []ToolDescriptor{
			{
				Name:        "grep_codebase",
				Description: "search the tree",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pattern": map[string]any{"type": "string"},
					},
				},
			},
		},
		ToolChoice: "any",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// --- response parse checks ---
	if resp.StopReason != StopTool {
		t.Fatalf("expected StopTool, got %q", resp.StopReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	got := resp.ToolCalls[0]
	if got.ID != "toolu_abc" || got.Name != "grep_codebase" {
		t.Fatalf("unexpected tool call header: %+v", got)
	}
	if !reflect.DeepEqual(got.Input, map[string]any{"pattern": "Engine", "path": "internal"}) {
		t.Fatalf("unexpected tool call input: %#v", got.Input)
	}
	if resp.Text != "calling grep" {
		t.Fatalf("expected text separated from tool_use, got %q", resp.Text)
	}

	// --- request serialization checks ---
	toolsField, ok := captured["tools"].([]any)
	if !ok || len(toolsField) != 1 {
		t.Fatalf("expected tools array with 1 entry, got %#v", captured["tools"])
	}
	toolEntry := toolsField[0].(map[string]any)
	if toolEntry["name"] != "grep_codebase" {
		t.Fatalf("expected grep_codebase, got %v", toolEntry["name"])
	}
	if _, ok := toolEntry["input_schema"].(map[string]any); !ok {
		t.Fatalf("expected input_schema object, got %#v", toolEntry["input_schema"])
	}

	choice, _ := captured["tool_choice"].(map[string]any)
	if choice["type"] != "any" {
		t.Fatalf("expected tool_choice type=any, got %#v", captured["tool_choice"])
	}

	msgs, _ := captured["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d (%#v)", len(msgs), msgs)
	}

	// Second message must be assistant with text + tool_use blocks.
	second := msgs[1].(map[string]any)
	if second["role"] != "assistant" {
		t.Fatalf("expected assistant role, got %v", second["role"])
	}
	blocks := second["content"].([]any)
	var sawTextBlock, sawToolUse bool
	for _, b := range blocks {
		blk := b.(map[string]any)
		switch blk["type"] {
		case "text":
			sawTextBlock = true
		case "tool_use":
			sawToolUse = true
			if blk["id"] != "toolu_prev" || blk["name"] != "read_file" {
				t.Fatalf("unexpected tool_use block: %#v", blk)
			}
		}
	}
	if !sawTextBlock || !sawToolUse {
		t.Fatalf("expected text+tool_use blocks in assistant content, got %#v", blocks)
	}

	// Third message is the user tool_result.
	third := msgs[2].(map[string]any)
	if third["role"] != "user" {
		t.Fatalf("expected user role for tool_result, got %v", third["role"])
	}
	resultBlocks := third["content"].([]any)
	if len(resultBlocks) != 1 {
		t.Fatalf("expected 1 tool_result block, got %d", len(resultBlocks))
	}
	resultBlock := resultBlocks[0].(map[string]any)
	if resultBlock["type"] != "tool_result" || resultBlock["tool_use_id"] != "toolu_prev" {
		t.Fatalf("unexpected tool_result block: %#v", resultBlock)
	}
}

// TestOpenAIToolCallRoundTrip is the OpenAI mirror: descriptors → tools[] with
// type=function + function.parameters; assistant tool_calls + role=tool
// messages on the outbound request; tool_calls[] on the response mapped to
// resp.ToolCalls with StopReason=StopTool.
func TestOpenAIToolCallRoundTrip(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "model":"gpt-5.4",
  "usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12},
  "choices":[{
    "finish_reason":"tool_calls",
    "message":{
      "role":"assistant",
      "content":"",
      "tool_calls":[
        {"id":"call_xyz","type":"function","function":{"name":"grep_codebase","arguments":"{\"pattern\":\"Engine\"}"}}
      ]
    }
  }]
}`))
	}))
	defer srv.Close()

	p := NewOpenAICompatibleProvider("openai", "gpt-5.4", "k", srv.URL, 4096, 128000, 0)
	resp, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{
			{Role: types.RoleUser, Content: "find Engine"},
			{
				Role:    types.RoleAssistant,
				Content: "",
				ToolCalls: []ToolCall{
					{ID: "call_prev", Name: "read_file", Input: map[string]any{"path": "README.md"}},
				},
			},
			{
				Role:       types.RoleUser,
				Content:    "file contents omitted",
				ToolCallID: "call_prev",
				ToolName:   "read_file",
			},
		},
		Tools: []ToolDescriptor{
			{
				Name:        "grep_codebase",
				Description: "search the tree",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"pattern": map[string]any{"type": "string"},
					},
				},
			},
		},
		ToolChoice: "any",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// --- response parse checks ---
	if resp.StopReason != StopTool {
		t.Fatalf("expected StopTool, got %q", resp.StopReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	got := resp.ToolCalls[0]
	if got.ID != "call_xyz" || got.Name != "grep_codebase" {
		t.Fatalf("unexpected tool call header: %+v", got)
	}
	if !reflect.DeepEqual(got.Input, map[string]any{"pattern": "Engine"}) {
		t.Fatalf("unexpected tool call input: %#v", got.Input)
	}

	// --- request serialization checks ---
	toolsField, ok := captured["tools"].([]any)
	if !ok || len(toolsField) != 1 {
		t.Fatalf("expected tools array with 1 entry, got %#v", captured["tools"])
	}
	toolEntry := toolsField[0].(map[string]any)
	if toolEntry["type"] != "function" {
		t.Fatalf("expected type=function, got %v", toolEntry["type"])
	}
	fn := toolEntry["function"].(map[string]any)
	if fn["name"] != "grep_codebase" {
		t.Fatalf("expected function.name=grep_codebase, got %v", fn["name"])
	}
	if _, ok := fn["parameters"].(map[string]any); !ok {
		t.Fatalf("expected function.parameters object, got %#v", fn["parameters"])
	}

	if captured["tool_choice"] != "required" {
		t.Fatalf("expected tool_choice=required, got %#v", captured["tool_choice"])
	}

	msgs := captured["messages"].([]any)
	// messages: user (find Engine), assistant (tool_calls), tool (role=tool).
	if len(msgs) < 3 {
		t.Fatalf("expected at least 3 messages, got %d", len(msgs))
	}

	// Assistant turn with tool_calls array.
	assistantTurn := msgs[1].(map[string]any)
	if assistantTurn["role"] != "assistant" {
		t.Fatalf("expected assistant role, got %v", assistantTurn["role"])
	}
	toolCalls, _ := assistantTurn["tool_calls"].([]any)
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 assistant tool_call, got %d", len(toolCalls))
	}
	call := toolCalls[0].(map[string]any)
	if call["id"] != "call_prev" || call["type"] != "function" {
		t.Fatalf("unexpected tool_call header: %#v", call)
	}
	fnCall := call["function"].(map[string]any)
	if fnCall["name"] != "read_file" {
		t.Fatalf("expected function.name=read_file, got %v", fnCall["name"])
	}
	// arguments must be a JSON-encoded string per OpenAI spec.
	argsRaw, ok := fnCall["arguments"].(string)
	if !ok {
		t.Fatalf("expected arguments to be string, got %T", fnCall["arguments"])
	}
	var argsDecoded map[string]any
	if err := json.Unmarshal([]byte(argsRaw), &argsDecoded); err != nil {
		t.Fatalf("arguments string not valid JSON: %v (%q)", err, argsRaw)
	}
	if argsDecoded["path"] != "README.md" {
		t.Fatalf("unexpected decoded args: %#v", argsDecoded)
	}

	// Tool result turn: role=tool with tool_call_id.
	toolTurn := msgs[2].(map[string]any)
	if toolTurn["role"] != "tool" || toolTurn["tool_call_id"] != "call_prev" {
		t.Fatalf("unexpected tool role turn: %#v", toolTurn)
	}
	if toolTurn["name"] != "read_file" {
		t.Fatalf("expected name=read_file on tool turn, got %v", toolTurn["name"])
	}
}

// TestAnthropicToolChoice_Auto_OmitsField — for "auto" / default the request
// should not carry tool_choice (Anthropic treats absence as auto).
func TestAnthropicToolChoice_Auto_OmitsField(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"c","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()

	p := NewAnthropicProvider("c", "k", srv.URL, 2048, 1000000)
	_, err := p.Complete(context.Background(), CompletionRequest{
		Messages:   []Message{{Role: types.RoleUser, Content: "hi"}},
		Tools:      []ToolDescriptor{{Name: "noop"}},
		ToolChoice: "auto",
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, ok := captured["tool_choice"]; ok {
		t.Fatalf("expected tool_choice to be omitted on auto, got %#v", captured["tool_choice"])
	}
}

package provider

import (
	"strings"
	"testing"
)

// When a MiniMax-style endpoint wraps tool calls in XML inside a text
// block, splitAnthropicContent must recover the calls, coerce typed
// parameter values, and strip the XML from the user-visible text.
func TestSplitAnthropicContent_ExtractsMiniMaxXMLToolCalls(t *testing.T) {
	body := buildMiniMaxXMLBody()
	blocks := []anthropicContentBlock{{Type: "text", Text: body}}

	text, calls := splitAnthropicContent(blocks)

	if len(calls) != 2 {
		t.Fatalf("expected 2 synthesized tool calls, got %d", len(calls))
	}
	if calls[0].Name != "read_file" {
		t.Errorf("first call name: want read_file, got %q", calls[0].Name)
	}
	if got, ok := calls[0].Input["line_start"].(int64); !ok || got != 340 {
		t.Errorf("line_start coercion: want int64(340), got %T(%v)", calls[0].Input["line_start"], calls[0].Input["line_start"])
	}
	if got, ok := calls[0].Input["path"].(string); !ok || !strings.HasSuffix(got, "engine.go") {
		t.Errorf("path passthrough: got %T(%v)", calls[0].Input["path"], calls[0].Input["path"])
	}
	if calls[1].Name != "grep_codebase" {
		t.Errorf("second call name: want grep_codebase, got %q", calls[1].Name)
	}
	if b, ok := calls[1].Input["case_sensitive"].(bool); !ok || b != false {
		t.Errorf("bool coercion: want false, got %T(%v)", calls[1].Input["case_sensitive"], calls[1].Input["case_sensitive"])
	}

	if strings.Contains(text, "<invoke") || strings.Contains(text, "<minimax:tool_call") {
		t.Errorf("cleaned text still carries XML: %q", text)
	}
	if !strings.Contains(text, "Here's my plan") {
		t.Errorf("prose outside tool-call block should be preserved; got %q", text)
	}
}

// Real Anthropic responses with tool_use blocks must not trip the XML
// fallback. The guard short-circuits before any regex work runs.
func TestSplitAnthropicContent_NativeToolUseSkipsXMLFallback(t *testing.T) {
	blocks := []anthropicContentBlock{
		{Type: "text", Text: "Looking into it."},
		{Type: "tool_use", ID: "toolu_01", Name: "read_file", Input: []byte(`{"path":"foo.go"}`)},
	}
	text, calls := splitAnthropicContent(blocks)
	if len(calls) != 1 || calls[0].ID != "toolu_01" {
		t.Fatalf("expected native tool_use call preserved, got %#v", calls)
	}
	if text != "Looking into it." {
		t.Errorf("text should be pass-through; got %q", text)
	}
}

// Plain prose that mentions tool-call-ish syntax informally must not
// create synthetic calls. The guard requires the exact open-invoke
// substring so ordinary mentions of tool names are safe.
func TestSplitAnthropicContent_ProseIsNotMistakenForToolCall(t *testing.T) {
	blocks := []anthropicContentBlock{{
		Type: "text",
		Text: "You could invoke read_file with a path argument.",
	}}
	_, calls := splitAnthropicContent(blocks)
	if len(calls) != 0 {
		t.Fatalf("prose should not yield synthetic tool calls, got %d", len(calls))
	}
}

// buildMiniMaxXMLBody assembles the raw text MiniMax's M2 model emits
// for a two-tool turn. Built via string concatenation because having a
// literal closing-parameter tag inline in this source file would
// collide with how the surrounding tool-use wire format frames our
// Write request for creating this file.
func buildMiniMaxXMLBody() string {
	openP := "<" + "parameter name="
	closeP := "</" + "parameter>"
	closeI := "</" + "invoke>"
	closeW := "</" + "minimax:tool_call>"
	var sb strings.Builder
	sb.WriteString("Here's my plan, then calls:\n")
	sb.WriteString("<minimax:tool_call>\n")
	sb.WriteString("<invoke name=\"read_file\">\n")
	sb.WriteString(openP + "\"path\">D:/Codebox/PROJECTS/DFMC/internal/ast/engine.go" + closeP + "\n")
	sb.WriteString(openP + "\"line_start\">340" + closeP + "\n")
	sb.WriteString(openP + "\"line_end\">420" + closeP + "\n")
	sb.WriteString(closeI + "\n")
	sb.WriteString("<invoke name=\"grep_codebase\">\n")
	sb.WriteString(openP + "\"pattern\">splitAnthropic" + closeP + "\n")
	sb.WriteString(openP + "\"case_sensitive\">false" + closeP + "\n")
	sb.WriteString(closeI + "\n")
	sb.WriteString(closeW + "\n")
	sb.WriteString("Let me know if that works.\n")
	return sb.String()
}

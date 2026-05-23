package engine

// history_tool_tail_test.go — pins the contract that prior assistant
// turns carry a compact tool-work tail back into the next request.
//
// User-stated invariant (paraphrased from a strongly-worded turn):
//   "context window neden tool calling veya arada assistant'in mesajı
//    dışında yapılan iş summary si vs içermiyor, sadece benim mesajım
//    assistant message mi olacak — bunun doğrusu her şeyi netleştir."
//
// The fix must NOT smuggle raw tool output back into the prompt
// (those payloads are kilobytes; one careless turn would blow the
// budget). It MUST surface enough about what was called for the model
// to recognise its own prior work — tool names, the touched file or
// command, success/failure.

import (
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestRenderHistoricalToolTail_IncludesToolNameAndPath(t *testing.T) {
	msg := types.Message{
		Role:    types.RoleAssistant,
		Content: "I checked the file.",
		ToolCalls: []types.ToolCallRecord{
			{
				Name:      "read_file",
				Params:    map[string]any{"path": "ui/tui/tui.go"},
				Timestamp: time.Now(),
			},
		},
		Results: []types.ToolResultRecord{
			{
				Name:    "read_file",
				Output:  "package tui\n\nimport ...",
				Success: true,
			},
		},
	}
	tail := renderHistoricalToolTail(msg)
	if tail == "" {
		t.Fatal("expected non-empty tail for an assistant turn with a successful tool call")
	}
	for _, want := range []string{"prior tools", "read_file", "ui/tui/tui.go"} {
		if !strings.Contains(tail, want) {
			t.Errorf("tail missing %q. Got: %s", want, tail)
		}
	}
}

func TestRenderHistoricalToolTail_MarksFailures(t *testing.T) {
	msg := types.Message{
		Role: types.RoleAssistant,
		ToolCalls: []types.ToolCallRecord{
			{Name: "edit_file", Params: map[string]any{"file_path": "main.go"}},
		},
		Results: []types.ToolResultRecord{
			{Name: "edit_file", Output: "old_string not found in main.go", Success: false},
		},
	}
	tail := renderHistoricalToolTail(msg)
	if !strings.Contains(tail, "✗") {
		t.Errorf("expected failure marker. Got: %s", tail)
	}
	if !strings.Contains(tail, "old_string not found") {
		t.Errorf("expected failure reason. Got: %s", tail)
	}
}

func TestRenderHistoricalToolTail_OmitsForPureTextTurns(t *testing.T) {
	msg := types.Message{
		Role:    types.RoleAssistant,
		Content: "Just text, no tools.",
	}
	if tail := renderHistoricalToolTail(msg); tail != "" {
		t.Errorf("text-only assistant turn should produce empty tail, got %q", tail)
	}
}

func TestRenderHistoricalToolTail_DoesNotEmbedRawToolOutput(t *testing.T) {
	// A 5 KiB tool result must NOT appear verbatim in the tail —
	// otherwise the budget cap is meaningless. The tail truncates
	// each result to ~80 chars so the whole turn re-injection cost
	// stays bounded.
	huge := strings.Repeat("X", 5000)
	msg := types.Message{
		Role: types.RoleAssistant,
		ToolCalls: []types.ToolCallRecord{
			{Name: "run_command", Params: map[string]any{"command": "go test ./..."}},
		},
		Results: []types.ToolResultRecord{
			{Name: "run_command", Output: huge, Success: true},
		},
	}
	tail := renderHistoricalToolTail(msg)
	if strings.Contains(tail, strings.Repeat("X", 200)) {
		t.Errorf("tail leaked huge output verbatim, length=%d", len(tail))
	}
	if len(tail) > 300 {
		t.Errorf("tail too large: %d chars", len(tail))
	}
}

func TestRenderHistoricalToolTail_CapsManyTools(t *testing.T) {
	// 20 tool calls in a single round should not produce a 20-entry
	// list — the tail caps and adds a "+N more" suffix.
	msg := types.Message{Role: types.RoleAssistant}
	for i := 0; i < 20; i++ {
		msg.ToolCalls = append(msg.ToolCalls, types.ToolCallRecord{
			Name:   "read_file",
			Params: map[string]any{"path": "f.go"},
		})
		msg.Results = append(msg.Results, types.ToolResultRecord{
			Name: "read_file", Output: "ok", Success: true,
		})
	}
	tail := renderHistoricalToolTail(msg)
	if !strings.Contains(tail, "+12 more") {
		t.Errorf("expected '+12 more' suffix when count exceeds cap. Got: %s", tail)
	}
}

func TestRenderHistoricalToolTail_StripsANSIFromResults(t *testing.T) {
	// Tool results stored in conversation history can contain ANSI escape
	// sequences from the original execution (colors, cursor moves, etc).
	// The history tail must strip these so the token budget isn't wasted
	// on invisible formatting that the model can't render anyway.
	msg := types.Message{
		Role: types.RoleAssistant,
		ToolCalls: []types.ToolCallRecord{
			{Name: "run_command", Params: map[string]any{"command": "git push"}},
		},
		Results: []types.ToolResultRecord{
			{
				Name:    "run_command",
				Output:  "\x1b[32mEnumerating objects: 42, done.\x1b[0m\n\x1b[33mProgress: resolved 100, reused 90\x1b[0m\nTo github.com:org/repo.git\n   abc123..def456  main -> main",
				Success: true,
			},
		},
	}
	tail := renderHistoricalToolTail(msg)
	if strings.Contains(tail, "\x1b[") {
		t.Errorf("tail contains raw ANSI escape sequences: %q", tail)
	}
	// The hint should pick up a cleaned first line, not "Enumerating..."
	// which is a noise line. After compressToolResult strips it, the
	// first surviving line should be the push destination or similar.
	if strings.Contains(tail, "Enumerating objects") {
		t.Errorf("tail leaked git progress noise: %q", tail)
	}
}

func TestRenderHistoricalToolTail_CollapsesRepeatsInResults(t *testing.T) {
	// Repeated lines in tool output should be collapsed before the hint
	// is extracted, so the tail doesn't carry redundant content.
	repeated := strings.Repeat("warning: something\n", 10) + "done\n"
	msg := types.Message{
		Role: types.RoleAssistant,
		ToolCalls: []types.ToolCallRecord{
			{Name: "run_command", Params: map[string]any{"command": "go build"}},
		},
		Results: []types.ToolResultRecord{
			{Name: "run_command", Output: repeated, Success: true},
		},
	}
	tail := renderHistoricalToolTail(msg)
	// After compressToolResult, the 10 repeated lines collapse to one
	// with a count suffix. The hint picks the first line of that.
	if !strings.Contains(tail, "warning: something") {
		t.Errorf("expected hint to contain cleaned first line, got: %q", tail)
	}
	// The tail itself should be short (<200 chars) since hint caps at 80.
	if len(tail) > 300 {
		t.Errorf("tail too long after compression: %d chars, content: %q", len(tail), tail)
	}
}

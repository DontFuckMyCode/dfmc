package engine

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// TestBuildTrajectoryHints_UnwrapsToolCallDoubleWrap pins the coach
// adapter against the "double-wrap" dispatch shape:
//
//   tool_call -> args -> { name:"tool_call", args:{ name:"write_file", ... } }
//
// The engine's toolCallTool.Execute auto-unwraps one redundant layer
// (meta.go:maxToolCallUnwrapDepth) so the real dispatch reaches
// write_file, but the TRACE carries the original outer input. Before
// this fix extractBridgedInnerName returned the string "tool_call" as
// the inner name - so entry.Inner was the meta wrapper, not the real
// backend tool, and the trajectory analyzer lost the ability to emit
// path/tool-specific coaching (mutation hints, grep narrowing, etc.).
// The coach should see the deepest real tool, matching the engine's
// own unwrap depth.
func TestBuildTrajectoryHints_UnwrapsToolCallDoubleWrap(t *testing.T) {
	traces := []nativeToolTrace{
		{
			Call: provider.ToolCall{
				Name: "tool_call",
				Input: map[string]any{
					"name": "tool_call",
					"args": map[string]any{
						"name": "write_file",
						"args": map[string]any{"path": "internal/auth/token.go"},
					},
				},
			},
			Result: tools.Result{Output: "wrote"},
			Step:   1,
		},
	}
	hints := buildTrajectoryHints(traces, traces, nil)
	if hints == nil || len(hints.Hints) == 0 {
		t.Fatalf("expected mutation hint for double-wrapped write_file, got none")
	}
	if !strings.Contains(hints.Hints[0], "internal/auth/token.go") {
		t.Fatalf("hint should carry inner path from the double-wrapped call, got %q", hints.Hints[0])
	}
}

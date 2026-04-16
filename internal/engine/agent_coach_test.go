package engine

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// TestBuildTrajectoryHints_UnwrapsBridgedToolCall asserts the adapter
// surfaces the *inner* tool name to the trajectory analyzer when the model
// invokes through the meta-tool "tool_call" proxy. Without this unwrap, the
// analyzer would treat every bridged call as a generic "tool_call" and lose
// tool-specific coaching (mutation → validate, grep → narrow, etc.).
func TestBuildTrajectoryHints_UnwrapsBridgedToolCall(t *testing.T) {
	traces := []nativeToolTrace{
		{
			Call: provider.ToolCall{
				Name: "tool_call",
				Input: map[string]any{
					"name": "write_file",
					"args": map[string]any{"path": "internal/auth/token.go"},
				},
			},
			Result: tools.Result{Output: "wrote"},
			Step:   1,
		},
	}
	hints := buildTrajectoryHints(traces, traces, nil)
	if len(hints) == 0 {
		t.Fatalf("expected mutation hint for bridged write_file")
	}
	if !strings.Contains(hints[0], "internal/auth/token.go") {
		t.Fatalf("hint should carry inner path, got %q", hints[0])
	}
}

// TestBuildTrajectoryHints_FailureSurfaces verifies a failed bridged call
// still produces a retry-safety hint.
func TestBuildTrajectoryHints_FailureSurfaces(t *testing.T) {
	traces := []nativeToolTrace{
		{
			Call: provider.ToolCall{
				Name: "tool_call",
				Input: map[string]any{
					"name": "run_command",
					"args": map[string]any{"command": "go test ./..."},
				},
			},
			Err:  "exit status 1: build failed",
			Step: 1,
		},
	}
	hints := buildTrajectoryHints(traces, traces, nil)
	if len(hints) == 0 {
		t.Fatalf("expected failure hint")
	}
	if !strings.Contains(strings.ToLower(hints[0]), "failed") {
		t.Fatalf("hint should flag the failure, got %q", hints[0])
	}
}

// TestAppendRecentHints_BoundsWindow ensures the de-dup window doesn't grow
// unbounded across a long loop.
func TestAppendRecentHints_BoundsWindow(t *testing.T) {
	window := []string{}
	for i := 0; i < 20; i++ {
		window = appendRecentHints(window, []string{"hint " + string(rune('A'+i))})
	}
	if len(window) > 8 {
		t.Fatalf("window should be bounded to 8, got %d", len(window))
	}
	// Oldest should have rolled off.
	if window[0] == "hint A" {
		t.Fatalf("oldest hint should have rolled off, window=%v", window)
	}
}

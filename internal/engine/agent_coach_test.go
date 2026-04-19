package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
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

func TestBuildCoachSnapshot_AddsConcreteHints(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	e := &Engine{ProjectRoot: root}
	completion := nativeToolCompletion{
		Answer:     "done",
		TokenCount: 46000,
		Context: []types.ContextChunk{
			{Path: "internal/auth/token.go", Source: "query-match"},
		},
		ToolTraces: []nativeToolTrace{
			{
				Call: provider.ToolCall{
					Name:  "write_file",
					Input: map[string]any{"path": "internal/auth/token.go"},
				},
			},
		},
	}

	snap := e.buildCoachSnapshot("fix parseToken", completion)
	if !strings.Contains(snap.ValidationHint, "go test ./internal/auth/... -count=1") {
		t.Fatalf("expected targeted go test hint, got %q", snap.ValidationHint)
	}
	if !strings.Contains(snap.TightenHint, "review [[file:internal/auth/token.go]] parseToken") {
		t.Fatalf("expected concrete tighten hint, got %q", snap.TightenHint)
	}
	if !strings.Contains(snap.RetrievalHint, "review [[file:internal/auth/token.go]] parseToken") {
		t.Fatalf("expected concrete retrieval hint, got %q", snap.RetrievalHint)
	}
}

func TestBuildCoachSnapshot_PrefersSpecificContextSourceForHints(t *testing.T) {
	e := &Engine{ProjectRoot: t.TempDir()}
	completion := nativeToolCompletion{
		Answer:     "done",
		TokenCount: 38000,
		Context: []types.ContextChunk{
			{Path: "ui/tui/conversations.go", Source: "hotspot"},
			{Path: "ui/tui/describe.go", Source: "query-match"},
		},
	}

	snap := e.buildCoachSnapshot("review describeWorkflow", completion)
	if !strings.Contains(snap.TightenHint, "review [[file:ui/tui/describe.go]] describeWorkflow") {
		t.Fatalf("expected tighten hint to prefer query-match file, got %q", snap.TightenHint)
	}
	if !strings.Contains(snap.RetrievalHint, "review [[file:ui/tui/describe.go]] describeWorkflow") {
		t.Fatalf("expected retrieval hint to prefer query-match file, got %q", snap.RetrievalHint)
	}
}

func TestBuildCoachSnapshot_SuppressesGenericRetrievalHintForFileScopedReview(t *testing.T) {
	e := &Engine{ProjectRoot: t.TempDir()}
	completion := nativeToolCompletion{
		Answer: "done",
		Context: []types.ContextChunk{
			{Path: "ui/tui/tui.go", Source: "query-match"},
		},
	}

	snap := e.buildCoachSnapshot("review [[file:ui/tui/tui.go]]", completion)
	if snap.UsefulQueryIdentifier != "" {
		t.Fatalf("expected no useful identifier for generic file-scoped review, got %q", snap.UsefulQueryIdentifier)
	}
	if snap.RetrievalHint != "" {
		t.Fatalf("expected empty retrieval hint for generic file-scoped review, got %q", snap.RetrievalHint)
	}
}

func TestBuildCoachSnapshot_RetrievalHintUsesExistingFileMarker(t *testing.T) {
	e := &Engine{ProjectRoot: t.TempDir()}
	completion := nativeToolCompletion{
		Answer: "done",
		Context: []types.ContextChunk{
			{Path: "ui/tui/tui.go", Source: "query-match"},
		},
	}

	snap := e.buildCoachSnapshot("review [[file:ui/tui/tui.go]] renderActiveView", completion)
	if !snap.QuestionHasFileMarker {
		t.Fatal("expected file marker to be detected")
	}
	if !strings.Contains(snap.RetrievalHint, "existing [[file:...]] marker") {
		t.Fatalf("expected retrieval hint to reuse existing file marker wording, got %q", snap.RetrievalHint)
	}
	if strings.Contains(snap.RetrievalHint, "review [[file:ui/tui/tui.go]]") {
		t.Fatalf("expected retrieval hint not to repeat the full file retry template, got %q", snap.RetrievalHint)
	}
}

func TestRecommendCoachValidationHintShellSafety(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\nname='demo'\n"), 0o644); err != nil {
		t.Fatalf("write pyproject.toml: %v", err)
	}
	got := recommendCoachValidationHint(root, nil, []string{`pkg/my tests/it's_bad.py`})
	want := "run `pytest 'pkg/my tests/it'\\''s_bad.py' -q`"
	if got != want {
		t.Fatalf("validation hint mismatch:\nwant: %q\n got: %q", want, got)
	}
}

package engine

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestBuildContextDebugStatusKeepsChunkContent(t *testing.T) {
	report := ContextInStatus{
		Query:              "explain context",
		Task:               "explain",
		Provider:           "local",
		Model:              "debug",
		ProviderMaxContext: 32000,
		MaxTokensTotal:     1200,
		TokenCount:         7,
		FileCount:          1,
		Reasons:            []string{"task=explain"},
		Files: []ContextInFileStatus{{
			Path:      "internal/context/manager.go",
			LineStart: 10,
			LineEnd:   12,
			Reason:    "matched query terms",
		}},
	}
	chunks := []types.ContextChunk{{
		Path:       "D:/repo/internal/context/manager.go",
		Language:   "go",
		Content:    "func Build() {}\n// exact payload",
		LineStart:  10,
		LineEnd:    12,
		TokenCount: 7,
		Score:      0.91,
		Source:     "symbol",
	}}

	debug := buildContextDebugStatus(report, chunks)

	if debug.Query != report.Query || debug.TokenCount != report.TokenCount {
		t.Fatalf("debug metadata mismatch: %#v", debug)
	}
	if len(debug.Files) != 1 {
		t.Fatalf("expected one debug file, got %d", len(debug.Files))
	}
	file := debug.Files[0]
	if file.Path != "internal/context/manager.go" {
		t.Fatalf("expected normalized report path, got %q", file.Path)
	}
	if file.Content != chunks[0].Content {
		t.Fatalf("debug content was not preserved: %q", file.Content)
	}
	if file.Reason != "matched query terms" {
		t.Fatalf("expected reason to carry through, got %q", file.Reason)
	}
}

func TestActiveContextDebugReturnsClone(t *testing.T) {
	e := &Engine{}
	e.setLastContextDebugStatus(ContextDebugStatus{
		Query: "first",
		Files: []ContextDebugFileStatus{{
			Path:    "a.go",
			Content: "package a",
		}},
	})

	debug := e.ActiveContextDebug()
	debug.Files[0].Content = "mutated"

	again := e.ActiveContextDebug()
	if again.Files[0].Content != "package a" {
		t.Fatalf("ActiveContextDebug leaked mutable slice: %q", again.Files[0].Content)
	}
}

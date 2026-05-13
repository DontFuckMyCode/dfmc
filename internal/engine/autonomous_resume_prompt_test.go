package engine

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/provider"
)

func TestBuildAutonomousResumePrompt_ContainsTaskAndProgress(t *testing.T) {
	e := &Engine{}
	seed := &parkedAgentState{
		Question: "refactor the auth middleware",
		Traces: []nativeToolTrace{
			{Call: provider.ToolCall{Name: "read_file", ID: "1"}},
			{Call: provider.ToolCall{Name: "read_file", ID: "2"}},
			{Call: provider.ToolCall{Name: "edit_file", ID: "3"}},
		},
	}
	prompt := e.buildAutonomousResumePrompt(seed)

	if !strings.Contains(prompt, "[DFMC autonomous continuation]") {
		t.Error("prompt should contain continuation header")
	}
	if !strings.Contains(prompt, "refactor the auth middleware") {
		t.Error("prompt should contain original task")
	}
	if !strings.Contains(prompt, "Progress so far:") {
		t.Error("prompt should contain progress summary")
	}
	if !strings.Contains(prompt, "read_file") {
		t.Error("progress summary should mention tool names from traces")
	}
	if !strings.Contains(prompt, "[done: true]") {
		t.Error("prompt should instruct model to end with [done: true]")
	}
}

func TestBuildAutonomousResumePrompt_EmptyQuestion(t *testing.T) {
	e := &Engine{}
	seed := &parkedAgentState{
		Question: "",
		Traces:   nil,
	}
	prompt := e.buildAutonomousResumePrompt(seed)

	if strings.Contains(prompt, "Original task:") {
		t.Error("should not include task section when question is empty")
	}
	if strings.Contains(prompt, "Progress so far:") {
		t.Error("should not include progress section when traces is empty")
	}
	if !strings.Contains(prompt, "[DFMC autonomous continuation]") {
		t.Error("should still contain header")
	}
}

func TestBuildAutonomousResumePrompt_TruncatesLongQuestion(t *testing.T) {
	e := &Engine{}
	longQ := strings.Repeat("x", 500)
	seed := &parkedAgentState{
		Question: longQ,
		Traces:   nil,
	}
	prompt := e.buildAutonomousResumePrompt(seed)

	taskIdx := strings.Index(prompt, "Original task: ")
	if taskIdx == -1 {
		t.Fatal("expected Original task section")
	}
	taskLine := prompt[taskIdx:]
	// 300 rune limit + "Original task: " prefix + newline
	taskEnd := strings.Index(taskLine, "\n")
	taskContent := taskLine[len("Original task: "):taskEnd]
	if len([]rune(taskContent)) > 310 {
		t.Errorf("task should be truncated to ~300 runes, got %d", len([]rune(taskContent)))
	}
}

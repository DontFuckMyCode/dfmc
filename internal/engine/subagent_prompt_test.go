package engine

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func TestBuildSubagentPromptIncludesRuntimeContext(t *testing.T) {
	prompt := buildSubagentPrompt(tools.SubagentRequest{
		Task:         "Inspect scheduler conflicts",
		Role:         "reviewer",
		AllowedTools: []string{"read_file", "grep_codebase"},
	}, nil, subagentPromptEnvironment{
		ProjectRoot:      `D:\Codebox\PROJECTS\DFMC`,
		Provider:         "stub",
		Model:            "stub-model",
		MaxSteps:         8,
		BackendToolCount: 3,
		BackendToolNames: []string{"grep_codebase", "read_file"},
	})

	for _, want := range []string{
		"Runtime context:",
		`project_root: D:\Codebox\PROJECTS\DFMC`,
		"provider/model: stub / stub-model",
		"max_tool_steps: 8",
		"backend_tools: 3 available through tool_call/tool_batch_call; sample: grep_codebase, read_file",
		"Allowed tools for this delegation: read_file, grep_codebase.",
		"Task:\nInspect scheduler conflicts",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestSubagentPromptToolSampleSortsAndCaps(t *testing.T) {
	got := subagentPromptToolSample([]tools.ToolSpec{
		{Name: "write_file"},
		{Name: " read_file "},
		{Name: ""},
		{Name: "grep_codebase"},
	}, 2)
	if len(got) != 2 || got[0] != "grep_codebase" || got[1] != "read_file" {
		t.Fatalf("unexpected sample: %#v", got)
	}
}

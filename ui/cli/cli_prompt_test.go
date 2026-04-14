package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunPromptListAndRender(t *testing.T) {
	eng := newCLITestEngine(t)

	if code := runPrompt(context.Background(), eng, []string{"list"}, true); code != 0 {
		t.Fatalf("prompt list exit=%d", code)
	}

	if code := runPrompt(context.Background(), eng, []string{"render", "--query", "security audit auth"}, true); code != 0 {
		t.Fatalf("prompt render exit=%d", code)
	}
}

func TestRunPromptUsesProjectOverride(t *testing.T) {
	eng := newCLITestEngine(t)
	project := t.TempDir()
	eng.ProjectRoot = project

	dir := filepath.Join(project, ".dfmc", "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir prompts dir: %v", err)
	}
	override := `
id: system.security
type: system
task: security
priority: 999
body: |
  PROJECT OVERRIDE {{project_root}} {{user_query}}
`
	if err := os.WriteFile(filepath.Join(dir, "override.yaml"), []byte(override), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}

	code := runPrompt(context.Background(), eng, []string{
		"render",
		"--task", "security",
		"--query", "auth security review",
	}, true)
	if code != 0 {
		t.Fatalf("prompt render override exit=%d", code)
	}
}

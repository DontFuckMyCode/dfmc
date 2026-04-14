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

	if code := runPrompt(context.Background(), eng, []string{
		"render",
		"--query", "security audit auth",
		"--runtime-tool-style", "function-calling",
		"--runtime-max-context", "1000",
	}, true); code != 0 {
		t.Fatalf("prompt render runtime override exit=%d", code)
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

func TestRunPromptStatsFailOnUnknownPlaceholder(t *testing.T) {
	eng := newCLITestEngine(t)
	project := t.TempDir()
	eng.ProjectRoot = project

	dir := filepath.Join(project, ".dfmc", "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir prompts dir: %v", err)
	}
	override := `
id: system.general.unknown_var
type: system
task: general
priority: 100
body: |
  Unknown var -> {{custom_unknown_var}}
`
	if err := os.WriteFile(filepath.Join(dir, "unknown_var.yaml"), []byte(override), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}

	code := runPrompt(context.Background(), eng, []string{
		"stats",
		"--max-template-tokens", "1000",
		"--fail-on-warning",
	}, true)
	if code != 1 {
		t.Fatalf("expected prompt stats to fail with warnings, got exit=%d", code)
	}
}

func TestRunPromptStatsAllowVarPasses(t *testing.T) {
	eng := newCLITestEngine(t)
	project := t.TempDir()
	eng.ProjectRoot = project

	dir := filepath.Join(project, ".dfmc", "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir prompts dir: %v", err)
	}
	override := `
id: system.general.allowed_var
type: system
task: general
priority: 100
body: |
  Allowed var -> {{custom_allowed_var}}
`
	if err := os.WriteFile(filepath.Join(dir, "allowed_var.yaml"), []byte(override), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}

	code := runPrompt(context.Background(), eng, []string{
		"stats",
		"--max-template-tokens", "1000",
		"--allow-var", "custom_allowed_var",
		"--fail-on-warning",
	}, true)
	if code != 0 {
		t.Fatalf("expected prompt stats to pass when var is allowed, got exit=%d", code)
	}
}

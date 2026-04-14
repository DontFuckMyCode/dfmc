package promptlib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestDetectTask(t *testing.T) {
	cases := map[string]string{
		"security audit auth flow": "security",
		"please review this file":  "review",
		"refactor this module":     "refactor",
		"let's make a plan":        "planning",
		"fix this panic":           "debug",
		"guvenlik denetimi yap":    "security",
		"g\u00fcvenlik denetimi":   "security",
		"adim adim roadmap cikar":  "planning",
		"ad\u0131m ad\u0131m plan": "planning",
	}
	for query, want := range cases {
		if got := DetectTask(query); got != want {
			t.Fatalf("DetectTask(%q)=%q want=%q", query, got, want)
		}
	}
}

func TestInferLanguage(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "a/main.go"},
		{Path: "b/auth.go"},
		{Path: "c/handler.ts"},
	}
	if got := InferLanguage("please inspect this", chunks); got != "go" {
		t.Fatalf("InferLanguage from chunks=%q want=go", got)
	}
	if got := InferLanguage("python icin guvenlik", nil); got != "python" {
		t.Fatalf("InferLanguage explicit=%q want=python", got)
	}
}

func TestRenderDefaultSystemPrompt(t *testing.T) {
	lib := New()
	out := lib.Render(RenderRequest{
		Type:     "system",
		Task:     "security",
		Language: "go",
		Vars: map[string]string{
			"project_root":  "/tmp/project",
			"user_query":    "find auth vulnerabilities",
			"context_files": "- auth.go:1-80",
		},
	})
	if !strings.Contains(strings.ToLower(out), "security") {
		t.Fatalf("expected security guidance, got: %s", out)
	}
	if !strings.Contains(out, "/tmp/project") {
		t.Fatalf("expected project_root injected, got: %s", out)
	}
	if !strings.Contains(out, "auth.go:1-80") {
		t.Fatalf("expected context_files injected, got: %s", out)
	}
}

func TestLoadOverridesFromProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	project := t.TempDir()
	promptsDir := filepath.Join(project, ".dfmc", "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	override := `
id: system.security
type: system
task: security
priority: 999
body: |
  OVERRIDE SECURITY PROMPT {{project_root}} {{user_query}}
`
	if err := os.WriteFile(filepath.Join(promptsDir, "security_override.yaml"), []byte(override), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}

	lib := New()
	if err := lib.LoadOverrides(project); err != nil {
		t.Fatalf("LoadOverrides: %v", err)
	}

	out := lib.Render(RenderRequest{
		Type: "system",
		Task: "security",
		Vars: map[string]string{
			"project_root": project,
			"user_query":   "check auth",
		},
	})
	if !strings.Contains(out, "OVERRIDE SECURITY PROMPT") {
		t.Fatalf("expected override prompt, got: %s", out)
	}
}

func TestDecodeMarkdownFrontMatterTemplate(t *testing.T) {
	data := []byte(`---
id: system.plan.custom
type: system
task: planning
language: go
priority: 77
---
Plan for {{project_root}}
`)
	tpl, ok := decodeMarkdownTemplate("system.plan.go.md", data)
	if !ok {
		t.Fatal("expected markdown template decode success")
	}
	if tpl.ID != "system.plan.custom" || tpl.Task != "planning" || tpl.Language != "go" {
		t.Fatalf("unexpected template meta: %+v", tpl)
	}
}

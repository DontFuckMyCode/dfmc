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
		"run threat analysis":      "security",
		"step-by-step roadmap":     "planning",
		"step-by-step plan":        "planning",
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
	if got := InferLanguage("python security review", nil); got != "python" {
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

func TestRenderComposesTaskAndProfileFragments(t *testing.T) {
	lib := &Library{
		templates:   []Template{},
		loadedRoots: map[string]struct{}{},
	}
	lib.upsert(Template{
		ID:      "base",
		Type:    "system",
		Task:    "general",
		Compose: "replace",
		Body:    "BASE {{task}}",
	})
	lib.upsert(Template{
		ID:      "task.security",
		Type:    "system",
		Task:    "security",
		Compose: "append",
		Body:    "TASK {{task}}",
	})
	lib.upsert(Template{
		ID:      "profile.deep",
		Type:    "system",
		Task:    "general",
		Profile: "deep",
		Compose: "append",
		Body:    "PROFILE {{profile}}",
	})

	out := lib.Render(RenderRequest{
		Type:    "system",
		Task:    "security",
		Profile: "deep",
		Vars: map[string]string{
			"task":    "security",
			"profile": "deep",
		},
	})

	if !strings.Contains(out, "BASE security") {
		t.Fatalf("expected base fragment, got: %s", out)
	}
	if !strings.Contains(out, "TASK security") {
		t.Fatalf("expected task fragment, got: %s", out)
	}
	if !strings.Contains(out, "PROFILE deep") {
		t.Fatalf("expected profile fragment, got: %s", out)
	}
}

func TestRenderComposesRoleFragments(t *testing.T) {
	lib := &Library{
		templates:   []Template{},
		loadedRoots: map[string]struct{}{},
	}
	lib.upsert(Template{
		ID:      "base",
		Type:    "system",
		Task:    "general",
		Compose: "replace",
		Body:    "BASE {{role}}",
	})
	lib.upsert(Template{
		ID:      "role.reviewer",
		Type:    "system",
		Task:    "general",
		Role:    "code_reviewer",
		Compose: "append",
		Body:    "ROLE {{role}}",
	})

	out := lib.Render(RenderRequest{
		Type: "system",
		Task: "review",
		Role: "code_reviewer",
		Vars: map[string]string{
			"role": "code_reviewer",
		},
	})
	if !strings.Contains(out, "BASE code_reviewer") {
		t.Fatalf("expected role in base fragment, got: %s", out)
	}
	if !strings.Contains(out, "ROLE code_reviewer") {
		t.Fatalf("expected role fragment, got: %s", out)
	}
}

func TestNormalizeTemplateComposeMode(t *testing.T) {
	a := normalizeTemplate(Template{Type: "system", Task: "general", Compose: "append", Body: "x"})
	if a.Compose != "append" {
		t.Fatalf("expected append compose, got: %q", a.Compose)
	}
	b := normalizeTemplate(Template{Type: "system", Task: "general", Body: "x"})
	if b.Compose != "replace" {
		t.Fatalf("expected default replace compose, got: %q", b.Compose)
	}
}

package context

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestBuildContextFromQuery(t *testing.T) {
	tmp := t.TempDir()
	mainGo := filepath.Join(tmp, "main.go")
	authGo := filepath.Join(tmp, "auth.go")

	if err := os.WriteFile(mainGo, []byte(`package main
import "fmt"
func main(){ fmt.Println("ok") }`), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(authGo, []byte(`package main
type AuthService struct {}
func VerifyToken(token string) bool { return token != "" }`), 0o644); err != nil {
		t.Fatalf("write auth.go: %v", err)
	}

	ae := ast.New()
	cm := codemap.New(ae)
	if err := cm.BuildFromFiles(context.Background(), []string{mainGo, authGo}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}

	mgr := New(cm)
	chunks, err := mgr.Build("token auth verification", 3)
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one context chunk")
	}
	if chunks[0].Language == "" {
		t.Fatal("expected language to be populated in context chunk")
	}
}

func TestBuildSystemPromptUsesPromptLibrary(t *testing.T) {
	tmp := t.TempDir()
	mainGo := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(mainGo, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	ae := ast.New()
	cm := codemap.New(ae)
	if err := cm.BuildFromFiles(context.Background(), []string{mainGo}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}

	mgr := New(cm)
	chunks, err := mgr.Build("security audit", 2)
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	prompt := mgr.BuildSystemPrompt(tmp, "security audit this auth path", chunks, []string{"read_file", "grep_codebase"})
	if !strings.Contains(strings.ToLower(prompt), "security") {
		t.Fatalf("expected security-focused prompt, got: %s", prompt)
	}
	if !strings.Contains(prompt, tmp) {
		t.Fatalf("expected project root in prompt, got: %s", prompt)
	}
	if !strings.Contains(prompt, "read_file") {
		t.Fatalf("expected tools overview in prompt, got: %s", prompt)
	}
	if !strings.Contains(prompt, "Role: security_auditor") {
		t.Fatalf("expected resolved role in prompt, got: %s", prompt)
	}
}

func TestBuildSystemPromptWithRuntimeToolPolicy(t *testing.T) {
	tmp := t.TempDir()
	mgr := New(codemap.New(ast.New()))

	prompt := mgr.BuildSystemPromptWithRuntime(
		tmp,
		"security audit auth flow",
		nil,
		[]string{"read_file", "grep_codebase"},
		PromptRuntime{
			Provider:   "openai",
			Model:      "glm-5.1",
			ToolStyle:  "function-calling",
			MaxContext: 128000,
		},
	)

	if !strings.Contains(prompt, "strict function-call JSON") {
		t.Fatalf("expected function-calling policy in prompt, got: %s", prompt)
	}
	if !strings.Contains(prompt, "near 25600 tokens") {
		t.Fatalf("expected runtime-aware tool output budget in prompt, got: %s", prompt)
	}
}

func TestBuildSystemPromptInjectsFileMarkerContext(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "auth.go")
	src := `package auth

func VerifyToken(token string) bool {
	return token != ""
}
`
	if err := os.WriteFile(target, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ae := ast.New()
	cm := codemap.New(ae)
	if err := cm.BuildFromFiles(context.Background(), []string{target}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}
	mgr := New(cm)
	query := "please inspect [[file:auth.go#L1-L4]] and explain risks"
	prompt := mgr.BuildSystemPrompt(tmp, query, nil, nil)

	if !strings.Contains(prompt, "[[file:auth.go#L1-L4]]") {
		t.Fatalf("expected injected marker block in prompt, got: %s", prompt)
	}
	if !strings.Contains(prompt, "VerifyToken") {
		t.Fatalf("expected injected code snippet in prompt, got: %s", prompt)
	}
}

func TestBuildSystemPromptInjectsQueryCodeFenceContext(t *testing.T) {
	tmp := t.TempDir()
	mgr := New(codemap.New(ast.New()))
	query := "please inspect this snippet ```go\nfunc Verify(token string) bool { return token != \"\" }\n```"

	prompt := mgr.BuildSystemPrompt(tmp, query, nil, nil)
	if !strings.Contains(prompt, "[[query-code:1]]") {
		t.Fatalf("expected query-code marker in prompt, got: %s", prompt)
	}
	if !strings.Contains(prompt, "func Verify(token string) bool") {
		t.Fatalf("expected inline query code in prompt, got: %s", prompt)
	}
}

func TestBuildSystemPromptInjectsProjectBrief(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "auth.go")
	if err := os.WriteFile(target, []byte("package auth\nfunc VerifyToken(token string) bool { return token != \"\" }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	magicDir := filepath.Join(tmp, ".dfmc", "magic")
	if err := os.MkdirAll(magicDir, 0o755); err != nil {
		t.Fatalf("mkdir magic dir: %v", err)
	}
	magicPath := filepath.Join(magicDir, "MAGIC_DOC.md")
	if err := os.WriteFile(magicPath, []byte("# MAGIC DOC: Test\n\nCritical note: auth boundary is strict.\n"), 0o644); err != nil {
		t.Fatalf("write magic doc: %v", err)
	}

	ae := ast.New()
	cm := codemap.New(ae)
	if err := cm.BuildFromFiles(context.Background(), []string{target}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}
	mgr := New(cm)

	prompt := mgr.BuildSystemPrompt(tmp, "review auth flow", nil, nil)
	if !strings.Contains(prompt, "Critical note: auth boundary is strict.") {
		t.Fatalf("expected project brief in prompt, got: %s", prompt)
	}
}

func TestBuildSystemPromptInjectionBudgetCompactVsDeep(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "auth.go")
	var b strings.Builder
	b.WriteString("package auth\n\nfunc VerifyToken(token string) bool {\n")
	for i := 0; i < 420; i++ {
		b.WriteString("value := \"alpha beta gamma delta epsilon zeta eta theta iota kappa\"\n")
	}
	b.WriteString("return token != \"\"\n}\n")
	if err := os.WriteFile(target, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ae := ast.New()
	cm := codemap.New(ae)
	if err := cm.BuildFromFiles(context.Background(), []string{target}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}
	mgr := New(cm)

	compactQuery := "inspect [[file:auth.go#L1-L420]]"
	deepQuery := "inspect [[file:auth.go#L1-L420]] detailed"
	compactPrompt := mgr.BuildSystemPrompt(tmp, compactQuery, nil, nil)
	deepPrompt := mgr.BuildSystemPrompt(tmp, deepQuery, nil, nil)

	if !strings.Contains(compactPrompt, "truncated for token budget") {
		t.Fatalf("expected compact prompt injection to be token-trimmed, got: %s", compactPrompt)
	}
	if len(strings.Fields(deepPrompt)) <= len(strings.Fields(compactPrompt)) {
		t.Fatalf("expected deep prompt to allow larger injection budget")
	}
}

func TestBuildSystemPromptRespectsPromptTokenBudget(t *testing.T) {
	tmp := t.TempDir()
	mgr := New(codemap.New(ast.New()))
	runtime := PromptRuntime{
		Provider:    "openai",
		Model:       "glm-5.1",
		ToolStyle:   "function-calling",
		LowLatency:  true,
		MaxContext:  1000,
		DefaultMode: "chat",
	}
	query := "security audit auth flow " + strings.Repeat("alpha beta gamma delta epsilon zeta ", 180)

	prompt := mgr.BuildSystemPromptWithRuntime(tmp, query, nil, nil, runtime)
	task := "security"
	profile := ResolvePromptProfile(query, task, runtime)
	budget := PromptTokenBudget(task, profile, runtime)
	if got := len(strings.Fields(prompt)); got > budget {
		t.Fatalf("expected prompt tokens <= budget (%d), got %d", budget, got)
	}
}

func TestResolvePromptRoleByTaskAndQuery(t *testing.T) {
	if got := ResolvePromptRole("security audit auth flow", "security"); got != "security_auditor" {
		t.Fatalf("expected security_auditor, got %s", got)
	}
	if got := ResolvePromptRole("architecture proposal for auth boundary", "planning"); got != "architect" {
		t.Fatalf("expected architect override, got %s", got)
	}
}

func TestBuildWithOptions_RespectsTokenBudgets(t *testing.T) {
	tmp := t.TempDir()
	big := filepath.Join(tmp, "big.go")
	body := "package main\nfunc Big(){\n" + strings.Repeat("println(\"alpha beta gamma delta epsilon\")\n", 240) + "}\n"
	if err := os.WriteFile(big, []byte(body), 0o644); err != nil {
		t.Fatalf("write big file: %v", err)
	}

	ae := ast.New()
	cm := codemap.New(ae)
	if err := cm.BuildFromFiles(context.Background(), []string{big}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}

	mgr := New(cm)
	chunks, err := mgr.BuildWithOptions("explain Big", BuildOptions{
		MaxFiles:         5,
		MaxTokensTotal:   200,
		MaxTokensPerFile: 120,
		Compression:      "none",
		IncludeTests:     true,
		IncludeDocs:      true,
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	total := 0
	for _, c := range chunks {
		total += c.TokenCount
		if c.TokenCount > 120 {
			t.Fatalf("expected per-file token budget <= 120, got %d", c.TokenCount)
		}
	}
	if total > 200 {
		t.Fatalf("expected total budget <= 200, got %d", total)
	}
}

func TestBuildWithOptions_ExcludesTestsAndDocsWhenDisabled(t *testing.T) {
	tmp := t.TempDir()
	mainGo := filepath.Join(tmp, "auth.go")
	testGo := filepath.Join(tmp, "auth_test.go")
	docsDir := filepath.Join(tmp, "docs")
	docsGo := filepath.Join(docsDir, "helper.go")

	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(mainGo, []byte("package main\nfunc VerifyAuth() bool { return true }\n"), 0o644); err != nil {
		t.Fatalf("write auth.go: %v", err)
	}
	if err := os.WriteFile(testGo, []byte("package main\nfunc TestVerifyAuth() {}\n"), 0o644); err != nil {
		t.Fatalf("write auth_test.go: %v", err)
	}
	if err := os.WriteFile(docsGo, []byte("package docs\nfunc DocHelper() {}\n"), 0o644); err != nil {
		t.Fatalf("write docs helper: %v", err)
	}

	ae := ast.New()
	cm := codemap.New(ae)
	if err := cm.BuildFromFiles(context.Background(), []string{mainGo, testGo, docsGo}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}

	mgr := New(cm)
	chunks, err := mgr.BuildWithOptions("auth helper test docs", BuildOptions{
		MaxFiles:         10,
		MaxTokensTotal:   1200,
		MaxTokensPerFile: 400,
		Compression:      "standard",
		IncludeTests:     false,
		IncludeDocs:      false,
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	for _, c := range chunks {
		p := strings.ToLower(filepath.ToSlash(c.Path))
		if strings.HasSuffix(p, "_test.go") {
			t.Fatalf("test file should be excluded, got %s", p)
		}
		if strings.Contains(p, "/docs/") {
			t.Fatalf("docs file should be excluded, got %s", p)
		}
	}
}

func TestSummarizeContextFiles_CompactsPathsAndShowsOverflow(t *testing.T) {
	root := t.TempDir()
	chunks := []types.ContextChunk{
		{Path: filepath.Join(root, "internal", "auth", "middleware.go"), LineStart: 1, LineEnd: 40},
		{Path: filepath.Join(root, "internal", "auth", "service.go"), LineStart: 5, LineEnd: 80},
		{Path: filepath.Join(root, "internal", "auth", "token.go"), LineStart: 10, LineEnd: 60},
	}
	out := summarizeContextFiles(root, chunks, 2)
	if !strings.Contains(out, "internal/auth/middleware.go:1-40") {
		t.Fatalf("expected project-relative path, got: %s", out)
	}
	if !strings.Contains(out, "... +1 more files") {
		t.Fatalf("expected overflow marker, got: %s", out)
	}
}

package context

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
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

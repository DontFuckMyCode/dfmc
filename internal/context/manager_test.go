package context

import (
	"context"
	"fmt"
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
	cm := codemap.New(ae, nil)
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
	cm := codemap.New(ae, nil)
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

func TestBuildSystemPromptBundleSurfacesPromptOverrideLoadWarning(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".dfmc"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Project prompts path exists but is a file, not a directory.
	if err := os.WriteFile(filepath.Join(tmp, ".dfmc", "prompts"), []byte("not-a-dir"), 0o644); err != nil {
		t.Fatalf("write prompts sentinel: %v", err)
	}

	mgr := New(codemap.New(ast.New(), nil))
	bundle := mgr.BuildSystemPromptBundle(tmp, "review auth flow", nil, []string{"read_file"}, PromptRuntime{})
	text := bundle.Text()
	if !strings.Contains(text, "Prompt override warning:") {
		t.Fatalf("expected prompt override warning in bundle, got: %s", text)
	}
}

func TestBuildSystemPromptBundle_InjectsExplicitSkillSectionAndStripsMarker(t *testing.T) {
	tmp := t.TempDir()
	mgr := New(codemap.New(ast.New(), nil))

	bundle := mgr.BuildSystemPromptBundle(tmp, "[[skill:debug]] investigate auth refresh failure", nil, []string{"read_file"}, PromptRuntime{})
	text := bundle.Text()
	if strings.Contains(text, "[[skill:debug]]") {
		t.Fatalf("skill marker should be stripped from rendered prompt, got: %s", text)
	}
	if !strings.Contains(text, "Activated skill: debug") {
		t.Fatalf("expected explicit debug skill section, got: %s", text)
	}
	if !strings.Contains(text, "Root-cause the problem") {
		t.Fatalf("expected debug skill contract in prompt, got: %s", text)
	}
}

func TestBuildSystemPromptBundle_AutoSelectsAuditForSecurityTask(t *testing.T) {
	tmp := t.TempDir()
	mgr := New(codemap.New(ast.New(), nil))

	bundle := mgr.BuildSystemPromptBundle(tmp, "security audit auth middleware", nil, []string{"grep_codebase"}, PromptRuntime{})
	cacheable := bundle.CacheableText()
	if !strings.Contains(cacheable, "Activated skill: audit") {
		t.Fatalf("expected audit skill section in cacheable prompt text, got: %s", cacheable)
	}
	if !strings.Contains(cacheable, "Produce a triaged security report") {
		t.Fatalf("expected audit system instruction in prompt, got: %s", cacheable)
	}
}

func TestBuildSystemPromptWithRuntimeToolPolicy(t *testing.T) {
	tmp := t.TempDir()
	mgr := New(codemap.New(ast.New(), nil))

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
	for _, want := range []string{"Tool Calling Protocol:", "`_reason`", "Shell boundary:"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected enriched tool-call policy %q in prompt, got: %s", want, prompt)
		}
	}
}

func TestBuildSystemPromptUsesRichToolAndSkillInventory(t *testing.T) {
	tmp := t.TempDir()
	mgr := New(codemap.New(ast.New(), nil))

	prompt := mgr.BuildSystemPromptWithRuntime(
		tmp,
		"security audit auth flow",
		nil,
		[]string{"read_file", "grep_codebase", "run_command", "delegate_task", "tool_help"},
		PromptRuntime{MaxContext: 128000},
	)

	for _, want := range []string{
		"Tool catalog:",
		"[Read/search]",
		"read_file (recommended) - read focused file ranges",
		"grep_codebase (recommended) - search code text",
		"[Execute/verify]",
		"run_command - run build/test/lint",
		"Skills inventory:",
		"Active: audit",
		"audit (active)",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected rich prompt inventory %q, got:\n%s", want, prompt)
		}
	}
}

func TestResolvePromptProfileSupportsExplicitTierAndEnvOverride(t *testing.T) {
	if got := ResolvePromptProfile("#tier: thorough\nsummarize quickly", "general", PromptRuntime{}); got != "deep" {
		t.Fatalf("tier thorough should force deep, got %q", got)
	}
	t.Setenv("DFMC_PROFILE", "fast")
	if got := ResolvePromptProfile("security audit auth deeply", "security", PromptRuntime{MaxContext: 128000}); got != "compact" {
		t.Fatalf("DFMC_PROFILE=fast should force compact before query keywords, got %q", got)
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
	cm := codemap.New(ae, nil)
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

func TestExtractInjectedContextCapsHugeFileRanges(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "big.go")
	var b strings.Builder
	b.WriteString("package big\n")
	for i := 1; i <= absoluteInjectedFileLines+25; i++ {
		b.WriteString(fmt.Sprintf("var Line%d = %d\n", i, i))
	}
	if err := os.WriteFile(target, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	block := extractInjectedContext(tmp, "inspect [[file:big.go#L1-L999999]]", 1, 999999)
	if !strings.Contains(block, fmt.Sprintf("[[file:big.go#L1-L%d]]", absoluteInjectedFileLines)) {
		t.Fatalf("expected absolute line cap in marker, got:\n%s", block)
	}
	if strings.Contains(block, fmt.Sprintf("Line%d", absoluteInjectedFileLines+1)) {
		t.Fatalf("expected lines after cap to be omitted, got:\n%s", block)
	}
}

func TestBuildSystemPromptInjectsQueryCodeFenceContext(t *testing.T) {
	tmp := t.TempDir()
	mgr := New(codemap.New(ast.New(), nil))
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
	cm := codemap.New(ae, nil)
	if err := cm.BuildFromFiles(context.Background(), []string{target}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}
	mgr := New(cm)

	prompt := mgr.BuildSystemPrompt(tmp, "review auth flow", nil, nil)
	if !strings.Contains(prompt, "Critical note: auth boundary is strict.") {
		t.Fatalf("expected project brief in prompt, got: %s", prompt)
	}
}

func TestBuildSystemPromptFiltersProjectBriefByTaskAndQuery(t *testing.T) {
	tmp := t.TempDir()
	magicDir := filepath.Join(tmp, ".dfmc", "magic")
	if err := os.MkdirAll(magicDir, 0o755); err != nil {
		t.Fatalf("mkdir magic dir: %v", err)
	}
	magic := strings.Join([]string{
		"# MAGIC DOC: Test",
		"General project overview that should not dominate narrow prompts.",
		"## UI Notes",
		"Buttons and panels are mostly cosmetic right now.",
		"## Security Hotspots",
		"Auth token persistence and credential handling need concrete evidence before edits.",
		"## Documentation",
		"README examples need polish.",
	}, "\n")
	if err := os.WriteFile(filepath.Join(magicDir, "MAGIC_DOC.md"), []byte(magic), 0o644); err != nil {
		t.Fatalf("write magic doc: %v", err)
	}

	mgr := New(nil)
	prompt := mgr.BuildSystemPrompt(tmp, "security audit auth token storage", nil, nil)
	if !strings.Contains(prompt, "Project brief filtered for task=security") {
		t.Fatalf("expected filtered project brief marker, got: %s", prompt)
	}
	if !strings.Contains(prompt, "Auth token persistence") {
		t.Fatalf("expected security-relevant project brief section, got: %s", prompt)
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
	cm := codemap.New(ae, nil)
	if err := cm.BuildFromFiles(context.Background(), []string{target}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}
	mgr := New(cm)

	compactQuery := "summarize briefly [[file:auth.go#L1-L420]]"
	deepQuery := "review deeply thoroughly [[file:auth.go#L1-L420]]"
	compactPrompt := mgr.BuildSystemPrompt(tmp, compactQuery, nil, nil)
	deepPrompt := mgr.BuildSystemPrompt(tmp, deepQuery, nil, nil)

	if !strings.Contains(compactPrompt, "truncated for token budget") {
		t.Fatalf("expected compact prompt injection to be token-trimmed, got: %s", compactPrompt)
	}
	if len(strings.Fields(deepPrompt)) <= len(strings.Fields(compactPrompt)) {
		t.Fatalf("expected deep prompt (%d words) to allow larger injection budget than compact (%d words)",
			len(strings.Fields(deepPrompt)), len(strings.Fields(compactPrompt)))
	}
}

func TestBuildSystemPromptRespectsPromptTokenBudget(t *testing.T) {
	tmp := t.TempDir()
	mgr := New(codemap.New(ast.New(), nil))
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
	cm := codemap.New(ae, nil)
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

func TestBuildWithOptions_TrimsOversizedChunkToRemainingBudget(t *testing.T) {
	tmp := t.TempDir()
	first := filepath.Join(tmp, "first.go")
	second := filepath.Join(tmp, "second.go")
	body := "package main\nfunc Big(){\n" + strings.Repeat("println(\"alpha beta gamma delta epsilon\")\n", 220) + "}\n"
	if err := os.WriteFile(first, []byte(body), 0o644); err != nil {
		t.Fatalf("write first.go: %v", err)
	}
	if err := os.WriteFile(second, []byte(body), 0o644); err != nil {
		t.Fatalf("write second.go: %v", err)
	}

	ae := ast.New()
	cm := codemap.New(ae, nil)
	if err := cm.BuildFromFiles(context.Background(), []string{first, second}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}

	mgr := New(cm)
	chunks, err := mgr.BuildWithOptions("Big", BuildOptions{
		MaxFiles:         2,
		MaxTokensTotal:   150,
		MaxTokensPerFile: 120,
		Compression:      "none",
		IncludeTests:     true,
		IncludeDocs:      true,
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected both files to be represented, got %d chunks", len(chunks))
	}
	total := 0
	for _, c := range chunks {
		total += c.TokenCount
	}
	if total > 150 {
		t.Fatalf("expected trimmed context to stay within total budget, got %d", total)
	}
	if chunks[1].TokenCount <= 0 {
		t.Fatalf("expected second oversized chunk to be trimmed, not dropped: %+v", chunks[1])
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
	cm := codemap.New(ae, nil)
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

// Invalidate on nil receiver is safe.
func TestManager_Invalidate_NilReceiver(t *testing.T) {
	var m *Manager
	m.Invalidate("any/path.go") // must not panic
}

// Invalidate with empty path is a no-op.
func TestManager_Invalidate_EmptyPath(t *testing.T) {
	cm := codemap.New(ast.New(), nil)
	m := New(cm)
	m.Invalidate("") // empty path is a no-op
}

// Invalidate with valid path calls codemap.InvalidateFile.
func TestManager_Invalidate_ValidPath(t *testing.T) {
	cm := codemap.New(ast.New(), nil)
	m := New(cm)
	// Valid path with codemap - codemap.InvalidateFile is called.
	// This is a no-op since the file doesn't exist in codemap.
	m.Invalidate("nonexistent.go")
}

// firstProjectBriefLines returns nil for empty doc.
func TestFirstProjectBriefLines_Empty(t *testing.T) {
	got := firstProjectBriefLines("", 3)
	if len(got) != 0 {
		t.Errorf("empty doc: got %v, want nil", got)
	}
}

// firstProjectBriefLines returns first N lines.
func TestFirstProjectBriefLines_Basic(t *testing.T) {
	doc := "Line one\nLine two\nLine three\nLine four"
	got := firstProjectBriefLines(doc, 2)
	want := []string{"Line one", "Line two"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("first 2 lines: got %v, want %v", got, want)
	}
}

// firstProjectBriefLines returns all if fewer lines than N.
func TestFirstProjectBriefLines_ShortDoc(t *testing.T) {
	doc := "Line one\nLine two"
	got := firstProjectBriefLines(doc, 10)
	if len(got) != 2 {
		t.Errorf("short doc: got %v, want full doc", got)
	}
}

// projectBriefTaskTerms returns terms for known task names.
func TestProjectBriefTaskTerms(t *testing.T) {
	terms := projectBriefTaskTerms("security")
	if len(terms) == 0 {
		t.Fatal("expected task terms for security")
	}
}

// projectBriefTaskTerms returns nil for unknown task names.
func TestProjectBriefTaskTerms_Unknown(t *testing.T) {
	terms := projectBriefTaskTerms("random unknown task xyz")
	if terms != nil {
		t.Errorf("unknown task should return nil, got %v", terms)
	}
}

// ExtractQueryIdentifiers with short input returns nil.
func TestExtractQueryIdentifiers_ShortInput(t *testing.T) {
	got := ExtractQueryIdentifiers("ab") // less than 3 chars
	if len(got) != 0 {
		t.Errorf("short input: got %v, want nil", got)
	}
}

// ExtractQueryIdentifiers with valid identifiers returns them.
func TestExtractQueryIdentifiers_Valid(t *testing.T) {
	got := ExtractQueryIdentifiers("verify auth token in middleware")
	if len(got) == 0 {
		t.Fatal("expected identifiers from valid query")
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

package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// ─── placeholder.go ───

func TestPlaceholderProvider_Model(t *testing.T) {
	p := NewPlaceholderProvider("test", "gpt-5", true, 8000)
	if got := p.Model(); got != "gpt-5" {
		t.Fatalf("Model() = %q, want %q", got, "gpt-5")
	}
}

func TestPlaceholderProvider_Models_Custom(t *testing.T) {
	p := NewPlaceholderProvider("test", "gpt-5", true, 8000)
	p.models = []string{"gpt-5", "gpt-4"}
	got := p.Models()
	if len(got) != 2 || got[0] != "gpt-5" || got[1] != "gpt-4" {
		t.Fatalf("Models() = %v, want [gpt-5 gpt-4]", got)
	}
}

func TestPlaceholderProvider_CountTokens(t *testing.T) {
	p := NewPlaceholderProvider("test", "gpt-5", true, 8000)
	if got := p.CountTokens("hello world foo"); got != 3 {
		t.Fatalf("CountTokens = %d, want 3", got)
	}
}

func TestPlaceholderProvider_MaxContext_Set(t *testing.T) {
	p := NewPlaceholderProvider("test", "gpt-5", true, 64000)
	if got := p.MaxContext(); got != 64000 {
		t.Fatalf("MaxContext() = %d, want 64000", got)
	}
}

func TestPlaceholderProvider_MaxContext_Default(t *testing.T) {
	p := NewPlaceholderProvider("test", "gpt-5", true, 0)
	if got := p.MaxContext(); got != 128000 {
		t.Fatalf("MaxContext() = %d, want 128000", got)
	}
}

func TestPlaceholderProvider_Hints(t *testing.T) {
	p := NewPlaceholderProvider("test", "gpt-5", true, 8000)
	h := p.Hints()
	if h.ToolStyle != "provider-native" || !h.Cache || h.MaxContext != 8000 {
		t.Fatalf("Hints() = %+v", h)
	}
}

func TestPlaceholderProvider_Complete_Configured(t *testing.T) {
	p := NewPlaceholderProvider("test", "gpt-5", true, 8000)
	resp, err := p.Complete(context.Background(), CompletionRequest{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !strings.Contains(resp.Text, "test provider is configured") {
		t.Fatalf("unexpected response: %s", resp.Text)
	}
}

func TestPlaceholderProvider_Complete_NotConfigured(t *testing.T) {
	p := NewPlaceholderProvider("test", "gpt-5", false, 8000)
	_, err := p.Complete(context.Background(), CompletionRequest{})
	if err == nil {
		t.Fatal("expected error for unconfigured provider")
	}
}

func TestPlaceholderProvider_Stream_Configured(t *testing.T) {
	p := NewPlaceholderProvider("test", "gpt-5", true, 8000)
	ch, err := p.Stream(context.Background(), CompletionRequest{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var got []StreamEvent
	for ev := range ch {
		got = append(got, ev)
	}
	if len(got) < 3 {
		t.Fatalf("expected >=3 stream events, got %d", len(got))
	}
	if got[0].Type != StreamStart {
		t.Fatalf("first event = %v, want StreamStart", got[0].Type)
	}
}

func TestPlaceholderProvider_Stream_NotConfigured(t *testing.T) {
	p := NewPlaceholderProvider("test", "gpt-5", false, 8000)
	_, err := p.Stream(context.Background(), CompletionRequest{})
	if err == nil {
		t.Fatal("expected error for unconfigured stream")
	}
}

// ─── router.go: Primary, SetPrimary, Fallback, SetFallback, List ───

func TestRouter_Primary(t *testing.T) {
	cfg := config.ProvidersConfig{Primary: "offline"}
	r, err := NewRouter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Primary(); got != "offline" {
		t.Fatalf("Primary() = %q, want %q", got, "offline")
	}
}

func TestRouter_SetPrimary(t *testing.T) {
	cfg := config.ProvidersConfig{Primary: "offline"}
	r, err := NewRouter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.SetPrimary("anthropic")
	if got := r.Primary(); got != "anthropic" {
		t.Fatalf("SetPrimary: got %q", got)
	}
}

func TestRouter_Fallback(t *testing.T) {
	cfg := config.ProvidersConfig{
		Primary:  "offline",
		Fallback: []string{"openai", "anthropic"},
	}
	r, err := NewRouter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := r.Fallback()
	if len(got) != 2 || got[0] != "openai" || got[1] != "anthropic" {
		t.Fatalf("Fallback() = %v", got)
	}
}

func TestRouter_SetFallback(t *testing.T) {
	cfg := config.ProvidersConfig{Primary: "offline"}
	r, err := NewRouter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.SetFallback([]string{"OpenAI", "openai", "", "anthropic"})
	got := r.Fallback()
	// dedup + normalize
	if len(got) != 2 || got[0] != "openai" || got[1] != "anthropic" {
		t.Fatalf("SetFallback: got %v", got)
	}
}

func TestRouter_List(t *testing.T) {
	cfg := config.ProvidersConfig{
		Primary: "offline",
		Profiles: map[string]config.ModelConfig{
			"anthropic": {Model: "claude-sonnet-4-6"},
		},
	}
	r, err := NewRouter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	names := r.List()
	if len(names) < 2 {
		t.Fatalf("List() should have >=2 providers, got %d: %v", len(names), names)
	}
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["offline"] || !found["anthropic"] {
		t.Fatalf("List() missing expected providers: %v", names)
	}
}

// ─── circuit.go: RecordHealthForTest ───

func TestRouter_RecordHealthForTest_Success(t *testing.T) {
	cfg := config.ProvidersConfig{Primary: "offline"}
	r, err := NewRouter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.RecordHealthForTest("my-prov", false)
	st := r.CircuitState()
	if len(st) != 0 {
		t.Fatalf("no provider should be tripped after success; got %v", st)
	}
}

func TestRouter_RecordHealthForTest_Failure(t *testing.T) {
	cfg := config.ProvidersConfig{Primary: "offline"}
	r, err := NewRouter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// 3 consecutive failures = breakerThreshold
	for i := 0; i < 3; i++ {
		r.RecordHealthForTest("my-prov", true)
	}
	st := r.CircuitState()
	if len(st) != 1 || st[0] != "my-prov" {
		t.Fatalf("provider should be tripped; got %v", st)
	}
}

func TestRouter_RecordHealthForTest_Reset(t *testing.T) {
	cfg := config.ProvidersConfig{Primary: "offline"}
	r, err := NewRouter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		r.RecordHealthForTest("my-prov", true)
	}
	// Success resets
	r.RecordHealthForTest("my-prov", false)
	st := r.CircuitState()
	if len(st) != 0 {
		t.Fatalf("circuit should close after success; got %v", st)
	}
}

func TestRouter_ShouldSkipForCircuit_HalfOpen(t *testing.T) {
	cfg := config.ProvidersConfig{Primary: "offline"}
	r, err := NewRouter(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Trip it
	for i := 0; i < 3; i++ {
		r.RecordHealthForTest("prov", true)
	}
	if !r.shouldSkipForCircuit("prov") {
		t.Fatal("should skip tripped provider")
	}
	// Artificially expire cooldown
	r.healthMu.Lock()
	r.health["prov"].openedAt = time.Now().Add(-time.Hour)
	r.healthMu.Unlock()
	if r.shouldSkipForCircuit("prov") {
		t.Fatal("half-open: cooldown expired, should not skip")
	}
}

// ─── offline_analyzer_helpers.go: 0% functions ───

func TestFilterByKeywords(t *testing.T) {
	in := []offlineFinding{
		{Severity: "medium", Message: "error handling issue", Category: "error", Evidence: "nil pointer"},
		{Severity: "low", Message: "style issue", Category: "style", Evidence: "whitespace"},
		{Severity: "high", Message: "race condition", Category: "concurrency", Evidence: "shared state"},
	}
	got := filterByKeywords(in, []string{"error", "race"})
	if len(got) != 2 {
		t.Fatalf("filterByKeywords = %d findings, want 2", len(got))
	}
}

func TestFilterByKeywords_EmptyKeys(t *testing.T) {
	in := []offlineFinding{{Message: "foo"}}
	got := filterByKeywords(in, nil)
	if len(got) != 1 {
		t.Fatalf("empty keys should return all; got %d", len(got))
	}
}

func TestExtractTopSymbols(t *testing.T) {
	ch := types.ContextChunk{
		Path:     "foo.go",
		Language: "go",
		Content:  "package foo\n\nfunc Bar() {}\nfunc Baz() {}\nfunc Quux() {}\n",
	}
	syms := extractTopSymbols(ch)
	if len(syms) < 3 {
		t.Fatalf("expected >=3 symbols, got %d: %v", len(syms), syms)
	}
	if syms[0] != "Bar" {
		t.Fatalf("first symbol = %q, want Bar", syms[0])
	}
}

func TestExtractTopSymbols_Empty(t *testing.T) {
	ch := types.ContextChunk{Language: "go", Content: "// just a comment\n"}
	syms := extractTopSymbols(ch)
	if len(syms) != 0 {
		t.Fatalf("expected no symbols, got %v", syms)
	}
}

func TestBriefSymbols(t *testing.T) {
	if got := briefSymbols(nil); got != "no top-level symbols detected" {
		t.Fatalf("briefSymbols(nil) = %q", got)
	}
	if got := briefSymbols([]string{"a", "b"}); got != "a, b" {
		t.Fatalf("briefSymbols = %q", got)
	}
	if got := briefSymbols([]string{"a", "b", "c", "d", "e"}); !strings.Contains(got, "(+1)") {
		t.Fatalf("briefSymbols truncation: %q", got)
	}
}

// ─── offline_reports.go: 0% functions ───

func TestOfflineDebugReport(t *testing.T) {
	chunks := []types.ContextChunk{{
		Path: "main.py", Language: "python", LineStart: 1, LineEnd: 5,
		Content: "try:\n    pass\nexcept:\n    print('error')\neval('1+1')\n",
	}}
	report := offlineDebugReport(chunks, "app crashes on startup")
	if !strings.Contains(report, "Reported symptom") {
		t.Fatalf("debug report missing symptom header; got:\n%s", report)
	}
	if !strings.Contains(report, "Likely suspects") {
		t.Fatalf("debug report missing suspects; got:\n%s", report)
	}
}

func TestOfflineDebugReport_Empty(t *testing.T) {
	chunks := []types.ContextChunk{{
		Path: "clean.go", Language: "go", LineStart: 1, LineEnd: 3,
		Content: "package main\n\nfunc main() {}\n",
	}}
	report := offlineDebugReport(chunks, "")
	if !strings.Contains(report, "No obvious suspects") {
		t.Fatalf("expected no-suspects text; got:\n%s", report)
	}
}

func TestOfflineTestReport(t *testing.T) {
	chunks := []types.ContextChunk{
		{
			Path: "foo.go", Language: "go", LineStart: 1, LineEnd: 5,
			Content: "package foo\n\nfunc Bar() {}\nfunc Baz() {}\n",
		},
		{
			Path: "bar.go", Language: "go", LineStart: 1, LineEnd: 3,
			Content: "package bar\n\n// just constants\n",
		},
	}
	report := offlineTestReport(chunks)
	if !strings.Contains(report, "foo.go") {
		t.Fatalf("test report should mention foo.go; got:\n%s", report)
	}
	if !strings.Contains(report, "Test-writing checklist") {
		t.Fatalf("missing checklist; got:\n%s", report)
	}
}

func TestOfflineTestReport_AllTestFiles(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "foo_test.go", Language: "go", Content: "package foo\nfunc TestFoo(t *testing.T) {}\n"},
	}
	report := offlineTestReport(chunks)
	if !strings.Contains(report, "nothing to recommend") {
		t.Fatalf("expected no-recommendation text; got:\n%s", report)
	}
}

func TestOfflinePlanReport(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "a.go", Language: "go", LineStart: 1, LineEnd: 10},
		{Path: "b.go", Language: "typescript", LineStart: 5, LineEnd: 20},
	}
	report := offlinePlanReport(chunks, "migrate to Go")
	if !strings.Contains(report, "Goal: migrate to Go") {
		t.Fatalf("plan report missing goal; got:\n%s", report)
	}
	if !strings.Contains(report, "Phases") {
		t.Fatalf("plan report missing phases; got:\n%s", report)
	}
}

func TestOfflineExplainReport(t *testing.T) {
	chunks := []types.ContextChunk{
		{
			Path: "util.go", Language: "go", LineStart: 1, LineEnd: 5,
			Content: "package util\n\nfunc Hello() string { return \"hi\" }\nfunc Bye() string { return \"bye\" }\n",
		},
	}
	report := offlineExplainReport(chunks, "what does this do")
	if !strings.Contains(report, "Question:") || !strings.Contains(report, "util.go") {
		t.Fatalf("explain report missing content; got:\n%s", report)
	}
}

// ─── offline_scanners.go: 0% language scanners ───

func TestScanTSIssues(t *testing.T) {
	ch := types.ContextChunk{
		Path: "app.ts", Language: "typescript", LineStart: 1, LineEnd: 8,
		Content: "const x: any = 1;\nconsole.log(x);\n// @ts-ignore\nconsole.debug('hi');\n",
	}
	findings := scanTSIssues(ch)
	if len(findings) < 3 {
		t.Fatalf("expected >=3 TS findings, got %d: %+v", len(findings), findings)
	}
	cats := map[string]bool{}
	for _, f := range findings {
		cats[f.Category] = true
	}
	if !cats["type-safety"] {
		t.Fatal("missing type-safety finding")
	}
}

func TestScanPyIssues(t *testing.T) {
	ch := types.ContextChunk{
		Path: "main.py", Language: "python", LineStart: 1, LineEnd: 10,
		Content: "try:\n    pass\nexcept:\n    print('err')\ndef f(x=[]):\n    pass\neval('1')\n",
	}
	findings := scanPyIssues(ch)
	if len(findings) < 3 {
		t.Fatalf("expected >=3 Python findings, got %d", len(findings))
	}
	cats := map[string]bool{}
	for _, f := range findings {
		cats[f.Category] = true
	}
	if !cats["security"] {
		t.Fatal("missing eval/exec security finding")
	}
	if !cats["bug-risk"] {
		t.Fatal("missing mutable default finding")
	}
}

func TestScanRustIssues(t *testing.T) {
	ch := types.ContextChunk{
		Path: "main.rs", Language: "rust", LineStart: 1, LineEnd: 5,
		Content: "fn main() {\n    let x = y.unwrap();\n    unsafe { ptr::read(p) }\n}\n",
	}
	findings := scanRustIssues(ch)
	if len(findings) < 2 {
		t.Fatalf("expected >=2 Rust findings, got %d", len(findings))
	}
	cats := map[string]bool{}
	for _, f := range findings {
		cats[f.Category] = true
	}
	if !cats["error-handling"] || !cats["memory-safety"] {
		t.Fatalf("missing expected categories; got: %v", cats)
	}
}

func TestScanRustIssues_Suppressed(t *testing.T) {
	ch := types.ContextChunk{
		Path: "main.rs", Language: "rust", LineStart: 1, LineEnd: 2,
		Content: "let x = y.unwrap(); // safe: invariant guaranteed\nunsafe {} // safety: documented\n",
	}
	findings := scanRustIssues(ch)
	if len(findings) != 0 {
		t.Fatalf("suppressed findings should be 0, got %d", len(findings))
	}
}

func TestScanLanguageIssues_UnknownLang(t *testing.T) {
	ch := types.ContextChunk{Path: "a.rb", Language: "ruby", Content: "puts 'hi'"}
	if got := scanLanguageIssues(ch); got != nil {
		t.Fatalf("unknown language should return nil, got %v", got)
	}
}

// ─── offline_analyzer_helpers.go: shape detection edge cases ───

func TestLooksLikeFunctionStart_Languages(t *testing.T) {
	cases := []struct {
		lang string
		line string
		want bool
	}{
		{"go", "func Foo() {", true},
		{"go", "func()", true},
		{"go", "// not a func", false},
		{"python", "def bar():", true},
		{"python", "async def baz():", true},
		{"typescript", "function hello() {", true},
		{"javascript", "const f = () => {", true},
		{"rust", "fn main() {", true},
		{"rust", "pub fn foo() {", true},
		{"unknown", "func x()", false},
	}
	for _, tc := range cases {
		got := looksLikeFunctionStart(tc.lang, tc.line)
		if got != tc.want {
			t.Errorf("looksLikeFunctionStart(%q, %q) = %v, want %v", tc.lang, tc.line, got, tc.want)
		}
	}
}

func TestExtractFunctionName_Languages(t *testing.T) {
	cases := []struct {
		lang string
		line string
		want string
	}{
		{"go", "func MyFunc(x int) error {", "MyFunc"},
		{"go", "func (r *Receiver) Do() {", ""}, // receiver form: rest "(r *Receiver) Do() {", first '('/space at pos 0 → trim to ""
		{"python", "def my_func():", "my_func"},
		{"python", "async def baz():", "baz"},
		{"rust", "fn main() {", "main"},
		{"rust", "pub fn hello() {", "hello"},
		{"unknown", "func x()", "<anon>"},
	}
	for _, tc := range cases {
		got := extractFunctionName(tc.lang, tc.line)
		if got != tc.want {
			t.Errorf("extractFunctionName(%q, %q) = %q, want %q", tc.lang, tc.line, got, tc.want)
		}
	}
}

func TestLineNumber(t *testing.T) {
	ch := types.ContextChunk{LineStart: 10}
	if got := lineNumber(ch, 0); got != 10 {
		t.Fatalf("lineNumber(LineStart=10, idx=0) = %d, want 10", got)
	}
	ch2 := types.ContextChunk{LineStart: 0}
	if got := lineNumber(ch2, 5); got != 6 {
		t.Fatalf("lineNumber(LineStart=0, idx=5) = %d, want 6", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("truncate short = %q", got)
	}
	if got := truncate("a very long string", 5); got != "a ver…" {
		t.Fatalf("truncate long = %q", got)
	}
}

func TestSketchStructure(t *testing.T) {
	chunks := []types.ContextChunk{
		{Language: "go"}, {Language: "go"}, {Language: "python"},
	}
	got := sketchStructure(chunks)
	if !strings.Contains(got, "2 go") || !strings.Contains(got, "1 python") {
		t.Fatalf("sketchStructure = %q", got)
	}
	if got2 := sketchStructure(nil); got2 != "no structural data" {
		t.Fatalf("empty = %q", got2)
	}
}

func TestHumanLang(t *testing.T) {
	if got := humanLang(""); got != "text" {
		t.Fatalf("humanLang('') = %q", got)
	}
	if got := humanLang("go"); got != "go" {
		t.Fatalf("humanLang('go') = %q", got)
	}
}

// ─── offline_analyzer.go: analyzeOffline routing for debug/test/plan ───

func TestAnalyzeOffline_Debug(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "a.py", Language: "python", LineStart: 1, LineEnd: 3, Content: "try:\n    pass\nexcept:\n    pass\n"},
	}
	got := analyzeOffline("debug", "why crash?", chunks)
	if !strings.Contains(got, "debug") || !strings.Contains(got, "DFMC offline") {
		t.Fatalf("analyzeOffline(debug) = %q", got)
	}
}

func TestAnalyzeOffline_Test(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "a.go", Language: "go", LineStart: 1, LineEnd: 3, Content: "package a\n\nfunc Foo() {}\n"},
	}
	got := analyzeOffline("test", "", chunks)
	if !strings.Contains(got, "Test coverage") {
		t.Fatalf("analyzeOffline(test) = %q", got)
	}
}

func TestAnalyzeOffline_Planning(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "a.go", Language: "go", LineStart: 1, LineEnd: 5},
	}
	got := analyzeOffline("planning", "migrate to rust", chunks)
	if !strings.Contains(got, "Planning scaffold") {
		t.Fatalf("analyzeOffline(planning) = %q", got)
	}
}

// ─── offline security: remaining regex branches ───

func TestOfflineSecurityReport_AllRegexes(t *testing.T) {
	content := strings.Join([]string{
		`const apiKey = "AKIAIOSFODNN7EXAMPLE"`,
		`-----BEGIN RSA PRIVATE KEY-----`,
		`password = "supersecretvalue99"`,
		`db.Query("SELECT * FROM " + table)`,
		`exec("ls" + userinput)`,
		`md5()`,
		`http://example.com/api`,
		`math.rand.Seed(1)`,
	}, "\n")
	chunks := []types.ContextChunk{{
		Path: "vuln.go", Language: "go", LineStart: 1, LineEnd: 8, Content: content,
	}}
	report := offlineSecurityReport(chunks)
	for _, want := range []string{"AWS access key", "PEM private key", "hardcoded credential", "SQL string", "shell command", "MD5/SHA1", "http://", "non-crypto RNG"} {
		if !strings.Contains(report, want) {
			t.Errorf("security report missing %q", want)
		}
	}
}

func TestOfflineSecurityReport_Clean(t *testing.T) {
	chunks := []types.ContextChunk{{
		Path: "clean.go", Language: "go", LineStart: 1, LineEnd: 2,
		Content: "package main\n\nfunc main() { fmt.Println(\"hi\") }\n",
	}}
	report := offlineSecurityReport(chunks)
	if !strings.Contains(report, "No obvious security smells") {
		t.Fatalf("clean code should report no smells; got:\n%s", report)
	}
}

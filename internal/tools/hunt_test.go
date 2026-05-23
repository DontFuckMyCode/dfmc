package tools

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Tool metadata
// ---------------------------------------------------------------------------

func TestHuntTool_newReturnsNonNil(t *testing.T) {
	if NewHuntTool() == nil {
		t.Error("NewHuntTool() returned nil")
	}
}

func TestHuntTool_name(t *testing.T) {
	tool := NewHuntTool()
	if got := tool.Name(); got != "bug_hunt" {
		t.Errorf("Name() = %q, want bug_hunt", got)
	}
}

func TestHuntTool_description(t *testing.T) {
	tool := NewHuntTool()
	if got := tool.Description(); got == "" {
		t.Error("Description() returned empty string")
	}
}

func TestHuntTool_spec(t *testing.T) {
	tool := NewHuntTool()
	spec := tool.Spec()
	if spec.Name != "bug_hunt" {
		t.Errorf("Spec().Name = %q, want bug_hunt", spec.Name)
	}
	if spec.Title == "" {
		t.Error("Spec().Title is empty")
	}
	if len(spec.Args) == 0 {
		t.Error("Spec().Args is empty")
	}
}

func TestHuntTool_setEngine(t *testing.T) {
	tool := NewHuntTool()
	tool.SetEngine(nil) // should not panic
}

// ---------------------------------------------------------------------------
// parseHuntSeverity
// ---------------------------------------------------------------------------

func TestParseHuntSeverity(t *testing.T) {
	tests := []struct {
		input string
		want  BugSeverity
	}{
		{"critical", SevCritical},
		{"CRITICAL", SevCritical},
		{"Critical", SevCritical},
		{"high", SevHigh},
		{"HIGH", SevHigh},
		{"medium", SevMedium},
		{"Medium", SevMedium},
		{"low", SevLow},
		{"LOW", SevLow},
		{"info", SevInfo},
		{"", SevInfo},
		{"unknown", SevInfo},
		{"garbage", SevInfo},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := parseHuntSeverity(tt.input); got != tt.want {
				t.Errorf("parseHuntSeverity(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// compareHuntSeverity
// ---------------------------------------------------------------------------

func TestCompareHuntSeverity(t *testing.T) {
	tests := []struct {
		name string
		a    BugSeverity
		b    BugSeverity
		want int
	}{
		{"critical > high", SevCritical, SevHigh, 1},
		{"high > medium", SevHigh, SevMedium, 1},
		{"medium > low", SevMedium, SevLow, 1},
		{"low > info", SevLow, SevInfo, 1},
		{"equal", SevHigh, SevHigh, 0},
		{"info < critical", SevInfo, SevCritical, -4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareHuntSeverity(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("compareHuntSeverity(%s, %s) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// getHuntDetectors
// ---------------------------------------------------------------------------

func TestGetHuntDetectors_all(t *testing.T) {
	detectors := getHuntDetectors("")
	if len(detectors) == 0 {
		t.Fatal("expected detectors when categories is empty")
	}
	// 6 categories defined in hunt.go
	if got := len(detectors); got != 6 {
		t.Errorf("expected 6 detectors, got %d", got)
	}
}

func TestGetHuntDetectors_single(t *testing.T) {
	detectors := getHuntDetectors("unchecked-error")
	if got := len(detectors); got != 1 {
		t.Fatalf("expected 1 detector, got %d", got)
	}
}

func TestGetHuntDetectors_multiple(t *testing.T) {
	detectors := getHuntDetectors("secrets,sql-injection")
	if got := len(detectors); got != 2 {
		t.Fatalf("expected 2 detectors, got %d", got)
	}
}

func TestGetHuntDetectors_unknownCategory(t *testing.T) {
	detectors := getHuntDetectors("nonexistent")
	if got := len(detectors); got != 0 {
		t.Errorf("expected 0 detectors for unknown category, got %d", got)
	}
}

func TestGetHuntDetectors_mixed(t *testing.T) {
	detectors := getHuntDetectors("secrets,nonexistent,insecure-rand")
	if got := len(detectors); got != 2 {
		t.Fatalf("expected 2 detectors, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// runHuntDetector
// ---------------------------------------------------------------------------

// noOpHuntDetector never appends findings.
func noOpHuntDetector(fset *token.FileSet, n ast.Node, path string, out *[]BugFinding) {}

func TestRunHuntDetector_noFiles(t *testing.T) {
	findings := runHuntDetector(noOpHuntDetector, nil)
	if len(findings) != 0 {
		t.Errorf("expected 0 findings for nil file list, got %d", len(findings))
	}
}

func TestRunHuntDetector_emptySlice(t *testing.T) {
	findings := runHuntDetector(noOpHuntDetector, []string{})
	if len(findings) != 0 {
		t.Errorf("expected 0 findings for empty file list, got %d", len(findings))
	}
}

func TestRunHuntDetector_nonexistentFile(t *testing.T) {
	findings := runHuntDetector(noOpHuntDetector, []string{"__nonexistent__.go"})
	if len(findings) != 0 {
		t.Errorf("expected 0 findings for nonexistent file, got %d", len(findings))
	}
}

func TestRunHuntDetector_validFile(t *testing.T) {
	dir := t.TempDir()
	writeHuntGoFile(t, dir, "clean.go", "package p\nfunc Clean() {}\n")

	findings := runHuntDetector(noOpHuntDetector, []string{filepath.Join(dir, "clean.go")})
	if len(findings) != 0 {
		t.Errorf("expected 0 findings with noop detector, got %d", len(findings))
	}
}

func TestRunHuntDetector_unparseableFile(t *testing.T) {
	dir := t.TempDir()
	writeHuntGoFile(t, dir, "bad.go", "this is not valid Go!!!")

	findings := runHuntDetector(noOpHuntDetector, []string{filepath.Join(dir, "bad.go")})
	if len(findings) != 0 {
		t.Errorf("expected 0 findings for unparseable file, got %d", len(findings))
	}
}

// ---------------------------------------------------------------------------
// detectUncheckedError
// ---------------------------------------------------------------------------

func TestDetectUncheckedError(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantFind bool
	}{
		{
			name:     "json.Unmarshal ignored",
			src:      "package p\nimport \"encoding/json\"\nfunc foo() { json.Unmarshal([]byte(\"{}\"), nil) }\n",
			wantFind: true,
		},
		{
			name:     "ioutil.WriteFile ignored",
			src:      "package p\nimport \"io/ioutil\"\nfunc foo() { ioutil.WriteFile(\"f\", nil, 0) }\n",
			wantFind: true,
		},
		{
			name:     "os.Open ignored",
			src:      "package p\nimport \"os\"\nfunc foo() { os.Open(\"f\") }\n",
			wantFind: true,
		},
		{
			name:     "regexp.Compile ignored",
			src:      "package p\nimport \"regexp\"\nfunc foo() { regexp.Compile(\"a\") }\n",
			wantFind: true,
		},
		{
			name:     "template.ParseFiles ignored",
			src:      "package p\nimport \"text/template\"\nfunc foo() { template.ParseFiles(\"a\") }\n",
			wantFind: true,
		},
		{
			name:     "fmt.Println is not in known list",
			src:      "package p\nimport \"fmt\"\nfunc foo() { fmt.Println(\"hi\") }\n",
			wantFind: false,
		},
		{
			name:     "clean function",
			src:      "package p\nfunc clean() {}\n",
			wantFind: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := runHuntDetectorOnSrc(t, detectUncheckedError, tt.src)
			checkHuntFinding(t, findings, tt.wantFind, "unchecked-error", SevMedium)
		})
	}
}

// ---------------------------------------------------------------------------
// detectNilDereference
// ---------------------------------------------------------------------------

func TestDetectNilDereference(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantFind bool
	}{
		{
			name:     "nil field access",
			src:      "package p\nfunc foo() { var x *int; _ = *x }\n",
			wantFind: false, // only detects nil.SelectorExpr, not nil deref via UnaryExpr
		},
		{
			name:     "nil.something selector",
			src:      "package p\nfunc foo() { var v struct{ X int }; _ = nil.X }\n",
			wantFind: true,
		},
		{
			name:     "clean function",
			src:      "package p\nfunc clean() {}\n",
			wantFind: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := runHuntDetectorOnSrc(t, detectNilDereference, tt.src)
			checkHuntFinding(t, findings, tt.wantFind, "nil-dereference", SevCritical)
		})
	}
}

// ---------------------------------------------------------------------------
// detectConcurrentMap
// ---------------------------------------------------------------------------

func TestDetectConcurrentMap(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantFind bool
	}{
		{
			name:     "go statement",
			src:      "package p\nfunc foo() { go func() {}() }\n",
			wantFind: true,
		},
		{
			name:     "clean function",
			src:      "package p\nfunc clean() {}\n",
			wantFind: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := runHuntDetectorOnSrc(t, detectConcurrentMap, tt.src)
			checkHuntFinding(t, findings, tt.wantFind, "concurrent-map", SevHigh)
		})
	}
}

// ---------------------------------------------------------------------------
// detectSQLInjection
// ---------------------------------------------------------------------------

func TestDetectSQLInjection(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantFind bool
	}{
		{
			name:     "fmt.Sprintf in query context",
			src:      "package p\nimport \"fmt\"\nvar q = fmt.Sprintf(\"SELECT * FROM t WHERE id=%s\", id)\n",
			wantFind: true,
		},
		{
			name:     "fmt.Sprint",
			src:      "package p\nimport \"fmt\"\nvar s = fmt.Sprint(\"a\", \"b\")\n",
			wantFind: true,
		},
		{
			name:     "fmt.Fprint — not targeted",
			src:      "package p\nimport \"fmt\"\nfunc foo() { fmt.Fprint(nil, \"hi\") }\n",
			wantFind: false,
		},
		{
			name:     "clean function",
			src:      "package p\nfunc clean() {}\n",
			wantFind: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := runHuntDetectorOnSrc(t, detectSQLInjection, tt.src)
			checkHuntFinding(t, findings, tt.wantFind, "sql-injection", SevHigh)
		})
	}
}

// ---------------------------------------------------------------------------
// detectHardcodedSecrets
// ---------------------------------------------------------------------------

func TestDetectHardcodedSecrets(t *testing.T) {
	tests := []struct {
		name        string
		src         string
		wantFind    bool
		wantSnippet string
	}{
		{
			name:        "hardcoded password",
			src:         "package p\nvar s = \"password=supersecretvalue123\"\n",
			wantFind:    true,
			wantSnippet: `"password=supersecretvalue123"`,
		},
		{
			name:     "clean string",
			src:      "package p\nvar s = \"hello world\"\n",
			wantFind: false,
		},
		{
			name:     "short string skipped",
			src:      "package p\nvar s = \"ab\"\n",
			wantFind: false,
		},
		{
			name:        "api_key assignment",
			src:         "package p\nvar k = \"api_key=ABCDEFGH1234567890xyz\"\n",
			wantFind:    true,
			wantSnippet: `"api_key=ABCDEFGH1234567890xyz"`,
		},
		{
			name:     "int literal ignored",
			src:      "package p\nvar x = 42\n",
			wantFind: false,
		},
		{
			name:        "secret token",
			src:         "package p\nvar s = \"secret_token=abc123xyz\"\n",
			wantFind:    true,
			wantSnippet: `"secret_token=abc123xyz"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := runHuntDetectorOnSrc(t, detectHardcodedSecrets, tt.src)
			if tt.wantFind {
				if len(findings) == 0 {
					t.Fatal("expected a finding but got none")
				}
				if findings[0].Category != "secrets" {
					t.Errorf("category = %q, want secrets", findings[0].Category)
				}
				if findings[0].Severity != SevCritical {
					t.Errorf("severity = %q, want CRITICAL", findings[0].Severity)
				}
				if tt.wantSnippet != "" && findings[0].Code != tt.wantSnippet {
					t.Errorf("code = %q, want %q", findings[0].Code, tt.wantSnippet)
				}
			} else {
				if len(findings) > 0 {
					t.Fatalf("expected no findings, got %+v", findings)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detectInsecureRand
// ---------------------------------------------------------------------------

func TestDetectInsecureRand(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantFind bool
	}{
		{
			name:     "rand.Int call",
			src:      "package p\nimport \"math/rand\"\nvar _ = rand.Int()\n",
			wantFind: true,
		},
		{
			name:     "rand.Seed call",
			src:      "package p\nimport \"math/rand\"\nvar _ = rand.Seed(42)\n",
			wantFind: true,
		},
		{
			name:     "no rand usage",
			src:      "package p\nfunc foo() {}\n",
			wantFind: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := runHuntDetectorOnSrc(t, detectInsecureRand, tt.src)
			checkHuntFinding(t, findings, tt.wantFind, "insecure-rand", SevMedium)
		})
	}
}

// ---------------------------------------------------------------------------
// HuntTool.Execute — integration via temp project
// ---------------------------------------------------------------------------

func TestHuntExecute_noGoFiles(t *testing.T) {
	dir := t.TempDir()
	tool := NewHuntTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "No Go source files found") {
		t.Errorf("expected 'No Go source files found' in output, got: %s", res.Output)
	}
}

func TestHuntExecute_cleanProject(t *testing.T) {
	dir := t.TempDir()
	writeHuntGoFile(t, dir, "clean.go", "package p\nfunc Clean() {}\n")
	tool := NewHuntTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "No issues detected") {
		t.Errorf("expected clean report, got: %s", res.Output)
	}
	if res.Data == nil {
		t.Fatal("expected Data in result")
	}
}

func TestHuntExecute_withFinding(t *testing.T) {
	dir := t.TempDir()
	writeHuntGoFile(t, dir, "secret.go", "package p\nvar s = \"password=supersecretvalue123\"\n")
	tool := NewHuntTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "secrets") {
		t.Errorf("expected secrets finding, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "Bug Hunt Report") {
		t.Errorf("expected report header, got: %s", res.Output)
	}
}

func TestHuntExecute_severityFilter(t *testing.T) {
	dir := t.TempDir()
	// This file triggers insecure-rand (MEDIUM) and a hardcoded password (CRITICAL).
	writeHuntGoFile(t, dir, "mixed.go", "package p\nimport \"math/rand\"\nvar _ = rand.Int()\nvar s = \"password=supersecretvalue123\"\n")

	tool := NewHuntTool()
	// Filter to critical only — only the password finding should survive.
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"severity": "critical"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "secrets") {
		t.Errorf("expected secrets in critical-only output, got: %s", res.Output)
	}
	// The medium finding (insecure-rand) must be filtered out.
	if strings.Contains(res.Output, "insecure-rand") {
		t.Errorf("insecure-rand should be filtered out at severity=critical")
	}
}

func TestHuntExecute_categoryFilter(t *testing.T) {
	dir := t.TempDir()
	writeHuntGoFile(t, dir, "secret.go", "package p\nvar s = \"password=supersecretvalue123\"\n")

	tool := NewHuntTool()
	// Request only sql-injection category — should find nothing in this file.
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"categories": "sql-injection"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "No issues detected") {
		t.Errorf("expected no findings for wrong category, got: %s", res.Output)
	}
}

func TestHuntExecute_customPath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeHuntGoFile(t, sub, "secret.go", "package p\nvar s = \"password=supersecretvalue123\"\n")
	writeHuntGoFile(t, dir, "clean.go", "package p\nfunc Clean() {}\n")

	tool := NewHuntTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"path": sub},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "secrets") {
		t.Errorf("expected finding in subdir, got: %s", res.Output)
	}
}

func TestHuntExecute_nonexistentPath(t *testing.T) {
	tool := NewHuntTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: "/__nonexistent_path_for_hunt_test__",
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// filepath.Walk on a nonexistent dir yields os.IsNotExist which is suppressed,
	// so we get the "no files" message instead of an error.
	if !strings.Contains(res.Output, "No Go source files found") {
		t.Errorf("expected no-files message, got: %s", res.Output)
	}
}

func TestHuntExecute_skipsTestFiles(t *testing.T) {
	dir := t.TempDir()
	// A _test.go with a secret — should be skipped by the walker.
	writeHuntGoFile(t, dir, "secret_test.go", "package p_test\nvar s = \"password=supersecretvalue123\"\n")

	tool := NewHuntTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "No Go source files found") {
		t.Errorf("test files should be excluded, got: %s", res.Output)
	}
}

func TestHuntExecute_multipleCategories(t *testing.T) {
	dir := t.TempDir()
	// File with multiple issues: unchecked error + hardcoded secret
	writeHuntGoFile(t, dir, "multi.go", `package p
import (
	"encoding/json"
	"os"
)
var s = "api_key=ABCDEFGH1234567890xyz"
func foo() {
	json.Unmarshal(nil, nil)
	os.Open("f")
}
`)

	tool := NewHuntTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "secrets") {
		t.Errorf("expected secrets finding, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "unchecked-error") {
		t.Errorf("expected unchecked-error finding, got: %s", res.Output)
	}
}

func TestHuntExecute_excludesVendorAndOtherDirs(t *testing.T) {
	dir := t.TempDir()
	vendorDir := filepath.Join(dir, "vendor")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatalf("mkdir vendor: %v", err)
	}
	writeHuntGoFile(t, vendorDir, "secret.go", "package vendor\nvar s = \"password=supersecretvalue123\"\n")

	tool := NewHuntTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "No Go source files found") {
		t.Errorf("vendor dir should be excluded, got: %s", res.Output)
	}
}

func TestHuntExecute_nilDereference(t *testing.T) {
	dir := t.TempDir()
	writeHuntGoFile(t, dir, "nil.go", "package p\nfunc foo() { var v struct{ X int }; _ = nil.X }\n")

	tool := NewHuntTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "nil-dereference") {
		t.Errorf("expected nil-dereference finding, got: %s", res.Output)
	}
}

func TestHuntExecute_concurrentMap(t *testing.T) {
	dir := t.TempDir()
	writeHuntGoFile(t, dir, "conc.go", "package p\nfunc foo() { go func() {}() }\n")

	tool := NewHuntTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "concurrent-map") {
		t.Errorf("expected concurrent-map finding, got: %s", res.Output)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// writeHuntGoFile is a test helper that writes a Go source file in dir.
func writeHuntGoFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// runHuntDetectorOnSrc parses src as a Go file, then runs the given detector
// on every AST node, collecting findings.
func runHuntDetectorOnSrc(t *testing.T, detector func(*token.FileSet, ast.Node, string, *[]BugFinding), src string) []BugFinding {
	t.Helper()
	dir := t.TempDir()
	filename := filepath.Join(dir, "src.go")
	if err := os.WriteFile(filename, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse src: %v", err)
	}

	var findings []BugFinding
	ast.Inspect(f, func(n ast.Node) bool {
		detector(fset, n, dir, &findings)
		return true
	})
	return findings
}

// checkHuntFinding asserts whether findings were produced and validates
// category and severity of the first finding.
func checkHuntFinding(t *testing.T, findings []BugFinding, wantFind bool, wantCat string, wantSev BugSeverity) {
	t.Helper()
	if wantFind {
		if len(findings) == 0 {
			t.Fatal("expected a finding but got none")
		}
		if findings[0].Category != wantCat {
			t.Errorf("category = %q, want %q", findings[0].Category, wantCat)
		}
		if findings[0].Severity != wantSev {
			t.Errorf("severity = %q, want %q", findings[0].Severity, wantSev)
		}
		if findings[0].File == "" {
			t.Error("expected non-empty File")
		}
		if findings[0].Line == 0 {
			t.Error("expected non-zero Line")
		}
	} else {
		if len(findings) > 0 {
			t.Fatalf("expected no findings, got %+v", findings)
		}
	}
}
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
// parseAuditSeverity
// ---------------------------------------------------------------------------

func TestParseAuditSeverity(t *testing.T) {
	tests := []struct {
		input string
		want  AuditSeverity
	}{
		{"critical", AuditCritical},
		{"CRITICAL", AuditCritical},
		{"Critical", AuditCritical},
		{"high", AuditHigh},
		{"HIGH", AuditHigh},
		{"medium", AuditMedium},
		{"Medium", AuditMedium},
		{"low", AuditLow},
		{"LOW", AuditLow},
		{"info", AuditInfo},
		{"", AuditInfo},
		{"unknown", AuditInfo},
		{"garbage", AuditInfo},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := parseAuditSeverity(tt.input); got != tt.want {
				t.Errorf("parseAuditSeverity(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// compareAuditSeverity
// ---------------------------------------------------------------------------

func TestCompareAuditSeverity(t *testing.T) {
	tests := []struct {
		name string
		a    AuditSeverity
		b    AuditSeverity
		want int // >0 means a > b
	}{
		{"critical > high", AuditCritical, AuditHigh, 1},
		{"high > medium", AuditHigh, AuditMedium, 1},
		{"medium > low", AuditMedium, AuditLow, 1},
		{"low > info", AuditLow, AuditInfo, 1},
		{"equal", AuditHigh, AuditHigh, 0},
		{"info < critical", AuditInfo, AuditCritical, -4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareAuditSeverity(tt.a, tt.b)
			if (tt.want > 0 && got <= 0) || (tt.want < 0 && got >= 0) || (tt.want == 0 && got != 0) {
				t.Errorf("compareAuditSeverity(%s, %s) = %d, want sign=%d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// getAuditDetectors
// ---------------------------------------------------------------------------

func TestGetAuditDetectors_all(t *testing.T) {
	detectors := getAuditDetectors("")
	if len(detectors) == 0 {
		t.Fatal("expected all detectors when categories is empty")
	}
	// There are 9 entries in the all slice (insecure-crypto has 2)
	if got := len(detectors); got != 9 {
		t.Errorf("expected 9 detectors, got %d", got)
	}
}

func TestGetAuditDetectors_single(t *testing.T) {
	detectors := getAuditDetectors("secrets")
	if got := len(detectors); got != 1 {
		t.Fatalf("expected 1 detector for secrets, got %d", got)
	}
}

func TestGetAuditDetectors_multiple(t *testing.T) {
	detectors := getAuditDetectors("secrets,sql-injection")
	if got := len(detectors); got != 2 {
		t.Fatalf("expected 2 detectors, got %d", got)
	}
}

func TestGetAuditDetectors_insecureCryptoBundlesTwo(t *testing.T) {
	detectors := getAuditDetectors("insecure-crypto")
	if got := len(detectors); got != 2 {
		t.Fatalf("insecure-crypto should enable 2 detectors (insecure-rand + weak-crypto), got %d", got)
	}
}

func TestGetAuditDetectors_unknownCategory(t *testing.T) {
	detectors := getAuditDetectors("nonexistent")
	if got := len(detectors); got != 0 {
		t.Errorf("expected 0 detectors for unknown category, got %d", got)
	}
}

func TestGetAuditDetectors_mixed(t *testing.T) {
	detectors := getAuditDetectors("secrets,nonexistent,xss")
	if got := len(detectors); got != 2 {
		t.Fatalf("expected 2 detectors (secrets+xss), got %d", got)
	}
}

// ---------------------------------------------------------------------------
// auditNodeString
// ---------------------------------------------------------------------------

func TestAuditNodeString_nil(t *testing.T) {
	if got := auditNodeString(token.NewFileSet(), nil); got != "" {
		t.Errorf("expected empty string for nil node, got %q", got)
	}
}

func TestAuditNodeString_validNode(t *testing.T) {
	fset := token.NewFileSet()
	src := `package p`
	f, err := parser.ParseFile(fset, "test.go", src, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// f.Name is an *ast.Ident
	got := auditNodeString(fset, f.Name)
	if got != "p" {
		t.Errorf("auditNodeString(ident) = %q, want %q", got, "p")
	}
}

// ---------------------------------------------------------------------------
// runAuditDetector
// ---------------------------------------------------------------------------

// writeGoFile is a test helper that writes a Go source file in dir.
func writeGoFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// noOpDetector never appends findings.
func noOpDetector(fset *token.FileSet, n ast.Node, path string, lineCount int, out *[]AuditFinding) {}

func TestRunAuditDetector_noFiles(t *testing.T) {
	findings := runAuditDetector(noOpDetector, nil)
	if len(findings) != 0 {
		t.Errorf("expected 0 findings for no files, got %d", len(findings))
	}
}

func TestRunAuditDetector_nonexistentFile(t *testing.T) {
	findings := runAuditDetector(noOpDetector, []string{"__nonexistent__.go"})
	if len(findings) != 0 {
		t.Errorf("expected 0 findings for nonexistent file, got %d", len(findings))
	}
}

func TestRunAuditDetector_validFile(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "clean.go", "package p\nfunc Clean() {}\n")

	findings := runAuditDetector(noOpDetector, []string{filepath.Join(dir, "clean.go")})
	if len(findings) != 0 {
		t.Errorf("expected 0 findings with noop detector, got %d", len(findings))
	}
}

func TestRunAuditDetector_unparseableFile(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "bad.go", "this is not valid Go!!!")

	findings := runAuditDetector(noOpDetector, []string{filepath.Join(dir, "bad.go")})
	if len(findings) != 0 {
		t.Errorf("expected 0 findings for unparseable file, got %d", len(findings))
	}
}

// ---------------------------------------------------------------------------
// detectAuditHardcodedSecrets — CWE-798 / CWE-259
// ---------------------------------------------------------------------------

func TestDetectAuditHardcodedSecrets(t *testing.T) {
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := runDetectorOnSrc(t, detectAuditHardcodedSecrets, tt.src)
			if tt.wantFind {
				if len(findings) == 0 {
					t.Fatal("expected a finding but got none")
				}
				if findings[0].Category != "hardcoded-secret" {
					t.Errorf("category = %q, want hardcoded-secret", findings[0].Category)
				}
				if findings[0].Severity != AuditCritical {
					t.Errorf("severity = %q, want CRITICAL", findings[0].Severity)
				}
				if tt.wantSnippet != "" && findings[0].Snippet != tt.wantSnippet {
					t.Errorf("snippet = %q, want %q", findings[0].Snippet, tt.wantSnippet)
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
// detectAuditSQLInjection — CWE-89
// ---------------------------------------------------------------------------

func TestDetectAuditSQLInjection(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantFind bool
	}{
		{
			name:     "fmt.Sprintf with SELECT",
			src:      "package p\nimport \"fmt\"\nvar q = fmt.Sprintf(\"SELECT * FROM users WHERE id=%s\", id)\n",
			wantFind: true,
		},
		{
			name:     "fmt.Sprintf without SQL",
			src:      "package p\nimport \"fmt\"\nvar s = fmt.Sprintf(\"hello %s\", name)\n",
			wantFind: false,
		},
		{
			name:     "clean call",
			src:      "package p\nfunc foo() {}\n",
			wantFind: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := runDetectorOnSrc(t, detectAuditSQLInjection, tt.src)
			checkFinding(t, findings, tt.wantFind, "sql-injection", AuditHigh)
		})
	}
}

// ---------------------------------------------------------------------------
// detectCommandInjection — CWE-78
// ---------------------------------------------------------------------------

func TestDetectCommandInjection(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantFind bool
	}{
		{
			name:     "exec.Command with string literal arg",
			src:      "package p\nimport \"os/exec\"\nvar _ = exec.Command(\"ls\", \"-la\")\n",
			wantFind: true,
		},
		{
			name:     "exec.Command without string args",
			src:      "package p\nimport \"os/exec\"\nvar _ = exec.Command(args...)\n",
			wantFind: false,
		},
		{
			name:     "unrelated call",
			src:      "package p\nfunc foo() {}\n",
			wantFind: false,
		},
		{
			name:     "os.StartProcess with literal",
			src:      "package p\nimport \"os\"\nvar _, _ = os.StartProcess(\"/bin/sh\", nil, nil)\n",
			wantFind: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := runDetectorOnSrc(t, detectCommandInjection, tt.src)
			checkFinding(t, findings, tt.wantFind, "command-injection", AuditCritical)
		})
	}
}

// ---------------------------------------------------------------------------
// detectPathTraversal — CWE-22
// ---------------------------------------------------------------------------

func TestDetectPathTraversal(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantFind bool
	}{
		{
			name:     "os.Open with ../",
			src:      "package p\nimport \"os\"\nvar _ = os.Open(\"../../../etc/passwd\")\n",
			wantFind: true,
		},
		{
			name:     "os.Open clean path",
			src:      "package p\nimport \"os\"\nvar _ = os.Open(\"/tmp/safe.txt\")\n",
			wantFind: false,
		},
		{
			name:     "os.ReadFile with backslash traversal",
			src:      "package p\nimport \"os\"\nvar _ = os.ReadFile(\"..\\\\secret\")\n",
			wantFind: true,
		},
		{
			name:     "unrelated call",
			src:      "package p\nfunc foo() {}\n",
			wantFind: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := runDetectorOnSrc(t, detectPathTraversal, tt.src)
			checkFinding(t, findings, tt.wantFind, "path-traversal", AuditHigh)
		})
	}
}

// ---------------------------------------------------------------------------
// detectAuditInsecureRand — CWE-338
// ---------------------------------------------------------------------------

func TestDetectAuditInsecureRand(t *testing.T) {
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
			wantFind: true, // rand.Seed is a SelectorExpr with X=Ident{Name:"rand"}, which the detector catches
		},
		{
			name:     "no rand usage",
			src:      "package p\nfunc foo() {}\n",
			wantFind: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := runDetectorOnSrc(t, detectAuditInsecureRand, tt.src)
			checkFinding(t, findings, tt.wantFind, "insecure-random", AuditMedium)
		})
	}
}

// ---------------------------------------------------------------------------
// detectXSS — CWE-79
// ---------------------------------------------------------------------------

func TestDetectXSS(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantFind bool
	}{
		{
			name:     "fmt.Fprint call",
			src:      "package p\nimport \"fmt\"\nvar _, _ = fmt.Fprint(w, data)\n",
			wantFind: true,
		},
		{
			name:     "io.WriteString call",
			src:      "package p\nimport \"io\"\nvar _, _ = io.WriteString(w, data)\n",
			wantFind: true,
		},
		{
			name:     "w.Write call",
			src:      "package p\nvar _, _ = w.Write(data)\n",
			wantFind: true,
		},
		{
			name:     "unrelated call",
			src:      "package p\nfunc foo() {}\n",
			wantFind: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := runDetectorOnSrc(t, detectXSS, tt.src)
			checkFinding(t, findings, tt.wantFind, "xss", AuditHigh)
		})
	}
}

// ---------------------------------------------------------------------------
// detectUnsafeRedirect — CWE-601
// ---------------------------------------------------------------------------

func TestDetectUnsafeRedirect(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantFind bool
	}{
		{
			name:     "http.Redirect with literal URL",
			src:      "package p\nimport \"net/http\"\nvar _ = http.Redirect(w, \"/dest\", 302)\n",
			wantFind: true,
		},
		{
			name:     "http.Redirect with non-literal second arg",
			src:      "package p\nimport \"net/http\"\nvar _ = http.Redirect(w, r.URL, 302)\n",
			wantFind: false,
		},
		{
			name:     "no redirect",
			src:      "package p\nfunc foo() {}\n",
			wantFind: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := runDetectorOnSrc(t, detectUnsafeRedirect, tt.src)
			checkFinding(t, findings, tt.wantFind, "unsafe-redirect", AuditMedium)
		})
	}
}

// ---------------------------------------------------------------------------
// detectXXE — CWE-611
// ---------------------------------------------------------------------------

func TestDetectXXE(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantFind bool
	}{
		{
			name:     "xml.NewDecoder call",
			src:      "package p\nimport \"encoding/xml\"\nvar _ = xml.NewDecoder(r)\n",
			wantFind: true,
		},
		{
			name:     "unrelated call",
			src:      "package p\nfunc foo() {}\n",
			wantFind: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := runDetectorOnSrc(t, detectXXE, tt.src)
			checkFinding(t, findings, tt.wantFind, "xxe", AuditHigh)
		})
	}
}

// ---------------------------------------------------------------------------
// detectWeakCrypto — CWE-327
// ---------------------------------------------------------------------------

func TestDetectWeakCrypto(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantFind bool
	}{
		{
			name:     "md5.New",
			src:      "package p\nimport \"crypto/md5\"\nvar _ = md5.New()\n",
			wantFind: true,
		},
		{
			name:     "sha1.New",
			src:      "package p\nimport \"crypto/sha1\"\nvar _ = sha1.New()\n",
			wantFind: true,
		},
		{
			name:     "des.NewCipher",
			src:      "package p\nimport \"crypto/des\"\nvar _, _ = des.NewCipher(key)\n",
			wantFind: true,
		},
		{
			name:     "sha256.New is fine",
			src:      "package p\nimport \"crypto/sha256\"\nvar _ = sha256.New()\n",
			wantFind: false,
		},
		{
			name:     "unrelated call",
			src:      "package p\nfunc foo() {}\n",
			wantFind: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings := runDetectorOnSrc(t, detectWeakCrypto, tt.src)
			checkFinding(t, findings, tt.wantFind, "weak-crypto", AuditHigh)
		})
	}
}

// ---------------------------------------------------------------------------
// AuditTool.Execute — integration via temp project
// ---------------------------------------------------------------------------

func TestAuditExecute_noGoFiles(t *testing.T) {
	dir := t.TempDir()
	tool := NewAuditTool()
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

func TestAuditExecute_cleanProject(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "clean.go", "package p\nfunc Clean() {}\n")
	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "No security issues detected") {
		t.Errorf("expected clean report, got: %s", res.Output)
	}
	if res.Data == nil {
		t.Fatal("expected Data in result")
	}
}

func TestAuditExecute_withFinding(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "secret.go", "package p\nvar s = \"password=supersecretvalue123\"\n")
	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "hardcoded-secret") {
		t.Errorf("expected hardcoded-secret finding, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "Security Audit Report") {
		t.Errorf("expected report header, got: %s", res.Output)
	}
}

func TestAuditExecute_severityFilter(t *testing.T) {
	dir := t.TempDir()
	// This file triggers insecure-random (MEDIUM) and a hardcoded password (CRITICAL).
	writeGoFile(t, dir, "mixed.go", "package p\nimport \"math/rand\"\nvar _ = rand.Int()\nvar s = \"password=supersecretvalue123\"\n")

	tool := NewAuditTool()
	// Filter to critical only — only the password finding should survive.
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"severity": "critical"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	// The critical finding (hardcoded-secret) must be present.
	if !strings.Contains(res.Output, "hardcoded-secret") {
		t.Errorf("expected hardcoded-secret in critical-only output, got: %s", res.Output)
	}
	// The medium finding (insecure-random) must be filtered out.
	if strings.Contains(res.Output, "insecure-random") {
		t.Errorf("insecure-random should be filtered out at severity=critical")
	}
}

func TestAuditExecute_categoryFilter(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "secret.go", "package p\nvar s = \"password=supersecretvalue123\"\n")

	tool := NewAuditTool()
	// Request only sql-injection category — should find nothing in this file.
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"categories": "sql-injection"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "No security issues detected") {
		t.Errorf("expected no findings for wrong category, got: %s", res.Output)
	}
}

func TestAuditExecute_customPath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeGoFile(t, sub, "secret.go", "package p\nvar s = \"password=supersecretvalue123\"\n")
	writeGoFile(t, dir, "clean.go", "package p\nfunc Clean() {}\n")

	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"path": sub},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "hardcoded-secret") {
		t.Errorf("expected finding in subdir, got: %s", res.Output)
	}
}

func TestAuditExecute_nonexistentPath(t *testing.T) {
	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: "/__nonexistent_path_for_test__",
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

func TestAuditExecute_skipsTestFiles(t *testing.T) {
	dir := t.TempDir()
	// A _test.go with a secret — should be skipped by the walker.
	writeGoFile(t, dir, "secret_test.go", "package p_test\nvar s = \"password=supersecretvalue123\"\n")

	tool := NewAuditTool()
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

func TestAuditTool_nameAndSpec(t *testing.T) {
	tool := NewAuditTool()
	if tool.Name() != "audit" {
		t.Errorf("Name() = %q, want audit", tool.Name())
	}
	spec := tool.Spec()
	if spec.Name != "audit" {
		t.Errorf("Spec().Name = %q, want audit", spec.Name)
	}
}

func TestAuditTool_setEngine(t *testing.T) {
	tool := NewAuditTool()
	// SetEngine stores the engine but Execute doesn't use it — just verify no panic.
	tool.SetEngine(nil)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// runDetectorOnSrc parses src as a Go file, then runs the given detector on
// every AST node, collecting findings.
func runDetectorOnSrc(t *testing.T, detector func(*token.FileSet, ast.Node, string, int, *[]AuditFinding), src string) []AuditFinding {
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

	lineCount := len(strings.Split(src, "\n"))
	var findings []AuditFinding
	ast.Inspect(f, func(n ast.Node) bool {
		detector(fset, n, dir, lineCount, &findings)
		return true
	})
	return findings
}

// checkFinding asserts whether findings were produced and validates category
// and severity of the first finding.
func checkFinding(t *testing.T, findings []AuditFinding, wantFind bool, wantCat string, wantSev AuditSeverity) {
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
		if findings[0].CWE == "" {
			t.Error("expected non-empty CWE")
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

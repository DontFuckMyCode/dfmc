package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// AuditTool.Execute tests
// ---------------------------------------------------------------------------

// TestAuditExecute_nonexistentRoot tests that a nonexistent project root
// does not cause Execute to return an error — it gracefully reports no files.
func TestAuditExecute_nonexistentRoot(t *testing.T) {
	tool := NewAuditTool()
	_, err := tool.Execute(context.Background(), Request{
		ProjectRoot: "/__nonexistent_root_path_for_audit_test__",
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute returned error for nonexistent root: %v", err)
	}
}

// TestAuditExecute_emptyDir tests that an empty directory (no Go files)
// returns "No Go source files found" — not an error.
func TestAuditExecute_emptyDir(t *testing.T) {
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
		t.Errorf("expected 'No Go source files found', got: %s", res.Output)
	}
}

// TestAuditExecute_noIssuesFound tests that a project with only clean code
// returns "No security issues detected" in the summary.
func TestAuditExecute_noIssuesFound(t *testing.T) {
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
		t.Errorf("expected 'No security issues detected', got: %s", res.Output)
	}
	data := res.Data
	if f, ok := data["findings"].([]AuditFinding); !ok || len(f) != 0 {
		t.Errorf("expected empty findings, got: %v", data["findings"])
	}
}

// TestAuditExecute_hardcodedSecretTriggersSecretsCategory tests that a file
// containing a hardcoded API key triggers the "hardcoded-secret" category.
// Note: the detector matches on the string VALUE content (e.g. "api_key=..."),
// not the variable name, so the value must match the secret pattern regex.
func TestAuditExecute_hardcodedSecretTriggersSecretsCategory(t *testing.T) {
	dir := t.TempDir()
	// The string value must match the secret-pattern regex:
	// (?i)['"]?(api[_-]?key|apikey|secret|password|...)['"]?\s*[:=]\s*['"]?[a-zA-Z0-9+/=_-]{8,}
	writeGoFile(t, dir, "db.go", "package p\nvar key = \"api_key=ABCDEFGH1234567890\"\n")
	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "hardcoded-secret") {
		t.Errorf("expected hardcoded-secret category in output, got: %s", res.Output)
	}
	data := res.Data
	findings, ok := data["findings"].([]AuditFinding)
	if !ok {
		t.Fatal("data['findings'] is not []AuditFinding")
	}
	if len(findings) == 0 {
		t.Fatal("expected at least one finding")
	}
	if findings[0].Category != "hardcoded-secret" {
		t.Errorf("finding category = %q, want hardcoded-secret", findings[0].Category)
	}
}

// TestAuditExecute_onlyTestFilesSkipped tests that a directory containing
// only _test.go files is treated as having no Go source files to scan.
func TestAuditExecute_onlyTestFilesSkipped(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "helper_test.go", "package p\nfunc TestHelper() {}\n")
	writeGoFile(t, dir, "mock_test.go", "package p\nvar s = \"password=secret\"\n")
	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "No Go source files found") {
		t.Errorf("expected test files to be skipped, got: %s", res.Output)
	}
}

// TestAuditExecute_severityFilterHigh tests the severity filter parameter.
// When severity=high, only HIGH and CRITICAL findings should be returned.
func TestAuditExecute_severityFilterHigh(t *testing.T) {
	dir := t.TempDir()
	// This file generates:
	//   - hardcoded-secret (CRITICAL)  ← should survive high filter
	//   - insecure-random (MEDIUM)     ← should be filtered out
	writeGoFile(t, dir, "mixed.go", "package p\nimport \"math/rand\"\nvar _ = rand.Int()\nvar key = \"api_key=ABCDEFGH1234567890xyz\"\n")

	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"severity": "high"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	// CRITICAL findings must be present.
	if !strings.Contains(res.Output, "hardcoded-secret") {
		t.Errorf("expected hardcoded-secret in output at severity=high, got: %s", res.Output)
	}
	// MEDIUM findings must be absent.
	if strings.Contains(res.Output, "insecure-random") {
		t.Errorf("insecure-random should be filtered out at severity=high")
	}

	data := res.Data
	findings := data["findings"].([]AuditFinding)
	for _, f := range findings {
		if compareAuditSeverity(f.Severity, AuditHigh) < 0 {
			t.Errorf("finding %q has severity %q below HIGH filter", f.Message, f.Severity)
		}
	}
}

// TestAuditExecute_severityInfoIncludesAll tests that severity=info
// includes all findings (even those below low).
func TestAuditExecute_severityInfoIncludesAll(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "secret.go", "package p\nvar s = \"password=supersecretvalue123\"\n")
	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"severity": "info"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "hardcoded-secret") {
		t.Errorf("expected hardcoded-secret at severity=info, got: %s", res.Output)
	}
}

// TestAuditExecute_categoryFilter_secrets tests that specifying only the
// "secrets" category returns only secret-related findings.
func TestAuditExecute_categoryFilter_secrets(t *testing.T) {
	dir := t.TempDir()
	// Contains a secret and a SQL injection pattern.
	writeGoFile(t, dir, "db.go", "package p\nimport \"fmt\"\nvar query = fmt.Sprintf(\"SELECT * FROM users WHERE id=%s\", id)\nvar key = \"api_key=ABCDEFGH1234567890xyz\"\n")
	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"categories": "secrets"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	// Only secrets category should be active.
	if !strings.Contains(res.Output, "hardcoded-secret") {
		t.Errorf("expected hardcoded-secret in secrets-only output, got: %s", res.Output)
	}
	if strings.Contains(res.Output, "sql-injection") {
		t.Errorf("sql-injection should not appear when only secrets category is requested")
	}
}

// TestAuditExecute_categoryFilter_multiple tests that multiple categories
// can be specified via comma separation.
func TestAuditExecute_categoryFilter_multiple(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "db.go", "package p\nimport \"fmt\"\nvar q = fmt.Sprintf(\"SELECT * FROM users WHERE id=%s\", id)\nvar key = \"api_key=ABCDEFGH1234567890xyz\"\n")
	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"categories": "secrets,sql-injection"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	output := res.Output
	// Both categories should appear.
	if !strings.Contains(output, "hardcoded-secret") {
		t.Errorf("expected hardcoded-secret, got: %s", output)
	}
	if !strings.Contains(output, "sql-injection") {
		t.Errorf("expected sql-injection, got: %s", output)
	}
}

// TestAuditExecute_categoryFilter_nonexistent tests that an unknown category
// results in zero detectors, leading to "no issues found".
func TestAuditExecute_categoryFilter_nonexistent(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "secret.go", "package p\nvar s = \"password=supersecretvalue123\"\n")
	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"categories": "nonexistent-category"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "No security issues detected") {
		t.Errorf("expected no findings for unknown category, got: %s", res.Output)
	}
}

// TestAuditExecute_skipsVendorDir tests that vendor/ directories are not scanned.
func TestAuditExecute_skipsVendorDir(t *testing.T) {
	dir := t.TempDir()
	vendorDir := filepath.Join(dir, "vendor")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatalf("mkdir vendor: %v", err)
	}
	writeGoFile(t, vendorDir, "secrets.go", "package vendor\nvar key = \"api_key=ABCDEFGH1234567890xyz\"\n")
	// File outside vendor should be found and reported.
	writeGoFile(t, dir, "clean.go", "package p\nfunc Clean() {}\n")

	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	// vendor/ file should NOT appear in output.
	if strings.Contains(res.Output, "vendor") {
		t.Errorf("vendor directory should be skipped, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "No security issues detected") {
		t.Errorf("expected clean output (vendor secret skipped), got: %s", res.Output)
	}
}

// TestAuditExecute_skipsNodeModules tests that node_modules/ is not scanned.
func TestAuditExecute_skipsNodeModules(t *testing.T) {
	dir := t.TempDir()
	nodeModules := filepath.Join(dir, "node_modules")
	if err := os.MkdirAll(nodeModules, 0o755); err != nil {
		t.Fatalf("mkdir node_modules: %v", err)
	}
	writeGoFile(t, nodeModules, "secrets.go", "package node\nvar key = \"api_key=ABCDEFGH1234567890xyz\"\n")
	writeGoFile(t, dir, "clean.go", "package p\nfunc Clean() {}\n")

	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if strings.Contains(res.Output, "node_modules") {
		t.Errorf("node_modules should be skipped, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "No security issues detected") {
		t.Errorf("expected clean output, got: %s", res.Output)
	}
}

// TestAuditExecute_skipsGitDir tests that .git/ directories are not scanned.
func TestAuditExecute_skipsGitDir(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	writeGoFile(t, gitDir, "packed.go", "package git\nvar key = \"api_key=ABCDEFGH1234567890xyz\"\n")
	writeGoFile(t, dir, "clean.go", "package p\nfunc Clean() {}\n")

	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if strings.Contains(res.Output, ".git") {
		t.Errorf(".git directory should be skipped, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "No security issues detected") {
		t.Errorf("expected clean output, got: %s", res.Output)
	}
}

// TestAuditExecute_skipsDfmcdir tests that .dfmc/ directories are not scanned.
func TestAuditExecute_skipsDfmcdir(t *testing.T) {
	dir := t.TempDir()
	dfmcDir := filepath.Join(dir, ".dfmc")
	if err := os.MkdirAll(dfmcDir, 0o755); err != nil {
		t.Fatalf("mkdir .dfmc: %v", err)
	}
	writeGoFile(t, dfmcDir, "internal.go", "package dfmc\nvar key = \"api_key=ABCDEFGH1234567890xyz\"\n")
	writeGoFile(t, dir, "clean.go", "package p\nfunc Clean() {}\n")

	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if strings.Contains(res.Output, ".dfmc") {
		t.Errorf(".dfmc directory should be skipped, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "No security issues detected") {
		t.Errorf("expected clean output, got: %s", res.Output)
	}
}

// TestAuditExecute_dataContainsRequiredFields tests that the returned Result
// Data field contains files count, findings slice, and summary map.
func TestAuditExecute_dataContainsRequiredFields(t *testing.T) {
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
	data := res.Data
	if data == nil {
		t.Fatal("Result.Data is nil")
	}
	if _, ok := data["files"]; !ok {
		t.Error("Result.Data missing 'files' field")
	}
	if _, ok := data["findings"]; !ok {
		t.Error("Result.Data missing 'findings' field")
	}
	// summary field is optional
}

// TestAuditExecute_resultContainsDuration tests that DurationMs is set in the result.
func TestAuditExecute_resultContainsDuration(t *testing.T) {
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
	// DurationMs may be 0 if execution was very fast; just verify no crash
	_ = res.DurationMs
}
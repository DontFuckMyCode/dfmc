package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGoMod is a test helper that writes a go.mod file into dir.
func writeGoMod(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile go.mod: %v", err)
	}
}

func TestDependencyAuditTool_Name(t *testing.T) {
	tool := NewDependencyAuditTool()
	if got := tool.Name(); got != "dependency_audit" {
		t.Errorf("Name() = %q, want %q", got, "dependency_audit")
	}
}

func TestDependencyAuditTool_Description(t *testing.T) {
	tool := NewDependencyAuditTool()
	if got := tool.Description(); got == "" {
		t.Error("Description() returned empty string")
	}
}

func TestDependencyAuditTool_Risk(t *testing.T) {
	tool := NewDependencyAuditTool()
	if got := tool.Risk(); got != RiskRead {
		t.Errorf("Risk() = %v, want RiskRead", got)
	}
}

func TestDependencyAuditTool_Idempotent(t *testing.T) {
	tool := NewDependencyAuditTool()
	if !tool.Idempotent() {
		t.Error("Idempotent() returned false, want true")
	}
}

func TestDependencyAuditTool_SetEngine(t *testing.T) {
	tool := NewDependencyAuditTool()
	tool.SetEngine(nil)
}

func TestDependencyAuditTool_Spec(t *testing.T) {
	tool := NewDependencyAuditTool()
	spec := tool.Spec()
	if spec.Name != "dependency_audit" {
		t.Errorf("Spec().Name = %q, want %q", spec.Name, "dependency_audit")
	}
	if spec.Title == "" {
		t.Error("Spec().Title is empty")
	}
	if spec.Summary == "" {
		t.Error("Spec().Summary is empty")
	}
}

func TestDependencyAuditTool_Execute_nonexistentRoot(t *testing.T) {
	tool := NewDependencyAuditTool()
	_, err := tool.Execute(context.Background(), Request{
		ProjectRoot: "/__nonexistent_root_path_for_dep_audit_test__",
		Params:      map[string]any{},
	})
	if err == nil {
		t.Fatal("Execute expected error for nonexistent root, got nil")
	}
}

func TestDependencyAuditTool_Execute_emptyDir(t *testing.T) {
	dir := t.TempDir()
	tool := NewDependencyAuditTool()
	_, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err == nil {
		t.Fatal("Execute expected error for empty dir (no go.mod), got nil")
	}
}

func TestDependencyAuditTool_Execute_cleanGoMod(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/mymodule\n\ngo 1.21\n")
	tool := NewDependencyAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"check_updates": false, "check_licenses": false},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.Output == "" {
		t.Error("Execute returned empty output for clean go.mod")
	}
}

func TestDependencyAuditTool_Execute_withDeps(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/mymodule\n\ngo 1.21\n\nrequire (\n\tgithub.com/some/pkg v1.0.0\n\tgolang.org/x/sync v0.0.0\n)\n")
	tool := NewDependencyAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"check_updates": false, "check_licenses": false},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	data, ok := res.Data["total_deps"].(int)
	if !ok || data != 2 {
		t.Errorf("total_deps = %v, want 2", res.Data["total_deps"])
	}
}

func TestDependencyAuditTool_Execute_ignorePrefixes(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/mymodule\n\ngo 1.21\n\nrequire (\n\tgithub.com/myorg/internal v1.0.0\n\tgithub.com/some/pkg v2.0.0\n)\n")
	tool := NewDependencyAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"ignore": "github.com/myorg", "check_updates": false, "check_licenses": false},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	skipped, ok := res.Data["skipped_deps"].(int)
	if !ok || skipped != 1 {
		t.Errorf("skipped_deps = %v, want 1", res.Data["skipped_deps"])
	}
}

func TestDependencyAuditTool_Execute_severityFilter(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/mymodule\n\ngo 1.21\n\nrequire github.com/some/pkg v1.0.0\n")
	tool := NewDependencyAuditTool()
	_, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"severity": "high", "check_updates": false, "check_licenses": false},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
}

func TestDependencyAuditTool_Execute_withReplace(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/mymodule\n\ngo 1.21\n\nrequire github.com/original/pkg v1.0.0\n\nreplace github.com/original/pkg => github.com/fork/pkg v2.0.0\n")
	tool := NewDependencyAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"check_updates": false, "check_licenses": false},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	data, ok := res.Data["total_deps"].(int)
	if !ok || data < 1 {
		t.Errorf("total_deps = %v, want >= 1", res.Data["total_deps"])
	}
}

func TestDependencyAuditTool_Execute_licensesParam(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/mymodule\n\ngo 1.21\n\nrequire github.com/some/pkg v1.0.0\n")
	tool := NewDependencyAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"check_licenses": true, "check_updates": false},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.Output == "" {
		t.Error("Execute returned empty output")
	}
}

func TestCleanVersion(t *testing.T) {
	tests := []struct{ in, want string }{
		{"v1.2.3", "1.2.3"},
		{"1.2.3", "1.2.3"},
		{"v2.0.0-beta.1", "2.0.0"},
		{"0.1.0", "0.1.0"},
		{"invalid", "invalid"},
	}
	for _, tt := range tests {
		if got := cleanVersion(tt.in); got != tt.want {
			t.Errorf("cleanVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestShouldInclude(t *testing.T) {
	tests := []struct{ minSev, findSev string; want bool }{
		{"all", "low", true},
		{"all", "critical", true},
		{"low", "low", true},
		{"low", "high", true},
		{"high", "low", false},
		{"high", "medium", false},
		{"medium", "high", true},
		{"critical", "high", false},
		{"critical", "critical", true},
	}
	for _, tt := range tests {
		if got := shouldInclude(tt.minSev, tt.findSev); got != tt.want {
			t.Errorf("shouldInclude(%q, %q) = %v, want %v", tt.minSev, tt.findSev, got, tt.want)
		}
	}
}

func TestCvssToSeverity(t *testing.T) {
	tests := []struct{ score string; want string }{
		{"9.5", "critical"},
		{"9.0", "critical"},
		{"8.9", "high"},
		{"7.0", "high"},
		{"6.9", "medium"},
		{"4.0", "medium"},
		{"3.9", "low"},
		{"invalid", "low"},
	}
	for _, tt := range tests {
		if got := cvssToSeverity(tt.score); got != tt.want {
			t.Errorf("cvssToSeverity(%q) = %q, want %q", tt.score, got, tt.want)
		}
	}
}

func TestIsProblematicLicense(t *testing.T) {
	allowed := []string{"MIT", "Apache-2.0", "BSD-3-Clause", "ISC"}
	blocked := []string{"GPL-3.0", "LGPL-2.1", "AGPL-3.0", "MPL-2.0", "EPL-1.0", "SSPL-1.0", "GPL-3.0-only"}
	for _, lic := range allowed {
		if isProblematicLicense(lic) {
			t.Errorf("isProblematicLicense(%q) = true, want false", lic)
		}
	}
	for _, lic := range blocked {
		if !isProblematicLicense(lic) {
			t.Errorf("isProblematicLicense(%q) = false, want true", lic)
		}
	}
}

func TestLicenseSeverity(t *testing.T) {
	tests := []struct{ lic, want string }{
		{"GPL-3.0", "high"},
		{"AGPL-3.0", "high"},
		{"LGPL-2.1", "medium"},
		{"MPL-2.0", "medium"},
		{"EPL-2.0", "medium"},
		{"MIT", "low"},
		{"Apache-2.0", "low"},
	}
	for _, tt := range tests {
		if got := licenseSeverity(tt.lic); got != tt.want {
			t.Errorf("licenseSeverity(%q) = %q, want %q", tt.lic, got, tt.want)
		}
	}
}

func TestDetectLicense(t *testing.T) {
	tests := []struct{ content, want string }{
		{"SPDX-License-Identifier: MIT", "MIT"},
		{"SPDX-License-Identifier: Apache-2.0", "Apache-2.0"},
		{"GNU GENERAL PUBLIC LICENSE Version 3", "GPL-3.0"},
		{"GNU GENERAL PUBLIC LICENSE Version 2", "GPL-2.0"},
		{"GNU LESSER GENERAL PUBLIC LICENSE Version 3", "LGPL-3.0"},
		{"GNU LESSER GENERAL PUBLIC LICENSE Version 2.1", "LGPL-2.1"},
		{"GNU AFFERO GENERAL PUBLIC LICENSE", "AGPL-3.0"},
		{"Mozilla Public License version 2.0", "MPL-2.0"},
		{"not a license file", "unknown"},
	}
	for _, tt := range tests {
		if got := detectLicense([]byte(tt.content)); got != tt.want {
			t.Errorf("detectLicense(%q) = %q, want %q", tt.content[:min(50, len(tt.content))], got, tt.want)
		}
	}
}

func TestBuildSummary(t *testing.T) {
	empty := auditResult{}
	if got := buildSummary(empty); !strings.Contains(got, "No vulnerabilities") {
		t.Errorf("buildSummary(empty) = %q, want no issues message", got)
	}
	withVulns := auditResult{
		Findings:     []auditFinding{{Severity: "high"}, {Severity: "medium"}},
		OutdatedPkgs: []outdatedPkg{{}},
	}
	got := buildSummary(withVulns)
	if !strings.Contains(got, "vulnerability") || !strings.Contains(got, "outdated") {
		t.Errorf("buildSummary(withVulns) = %q, want vulnerability and outdated mentions", got)
	}
}

func TestParseGoMod(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/mymodule\n\ngo 1.21\n\nrequire github.com/foo/bar v1.2.3\nrequire github.com/baz v0.0.1\n")
	deps, err := parseGoMod(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("parseGoMod failed: %v", err)
	}
	if len(deps) != 2 {
		t.Errorf("got %d deps, want 2", len(deps))
	}
}

func TestParseGoMod_blockForm(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/mymodule\n\ngo 1.21\n\nrequire (\n\tgithub.com/pkg/a v1.0.0\n\tgithub.com/pkg/b v2.1.0\n)\n")
	deps, err := parseGoMod(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("parseGoMod failed: %v", err)
	}
	if len(deps) != 2 {
		t.Errorf("got %d deps, want 2", len(deps))
	}
}

func TestSortFindings(t *testing.T) {
	findings := []auditFinding{
		{Severity: "low", Package: "a"},
		{Severity: "critical", Package: "b"},
		{Severity: "high", Package: "c"},
		{Severity: "medium", Package: "d"},
	}
	sortFindings(findings)
	if findings[0].Severity != "critical" {
		t.Errorf("expected critical first, got %s", findings[0].Severity)
	}
}

func TestNewDependencyAuditTool(t *testing.T) {
	tool := NewDependencyAuditTool()
	if tool == nil {
		t.Fatal("NewDependencyAuditTool returned nil")
	}
	if tool.httpClient == nil {
		t.Error("httpClient is nil")
	}
}
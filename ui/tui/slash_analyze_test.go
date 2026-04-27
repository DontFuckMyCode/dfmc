package tui

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/security"
)

func TestFormatAnalyzeReport_Basic(t *testing.T) {
	r := engine.AnalyzeReport{
		Files:  10,
		Nodes:  100,
		Edges:  50,
		Cycles: 2,
	}
	got := formatAnalyzeReport(r)
	if got == "" {
		t.Error("formatAnalyzeReport returned empty")
	}
	if !contains(got, "Analyze:") {
		t.Error("expected 'Analyze:' in output")
	}
}

func TestFormatAnalyzeReport_WithHotspots(t *testing.T) {
	r := engine.AnalyzeReport{
		Files:  5,
		Nodes:  50,
		Edges:  25,
		Cycles: 1,
		HotSpots: []codemap.Node{
			{Name: "main.go", Kind: "func"},
			{Name: "server.go", Kind: "type"},
		},
	}
	got := formatAnalyzeReport(r)
	if !contains(got, "Hotspots") {
		t.Error("expected 'Hotspots' in output")
	}
}

func TestFormatAnalyzeReport_WithSecurity(t *testing.T) {
	r := engine.AnalyzeReport{
		Files:  5,
		Nodes:  50,
		Edges:  25,
		Cycles: 0,
		Security: &security.Report{
			FilesScanned:   10,
			Secrets:        []security.SecretFinding{{File: "test.go", Line: 1, Severity: "high", Pattern: "api_key"}},
			Vulnerabilities: []security.VulnerabilityFinding{{File: "test.go", Line: 2, Severity: "medium", Kind: "xss", Snippet: "innerHTML"}},
		},
	}
	got := formatAnalyzeReport(r)
	if !contains(got, "Security:") {
		t.Error("expected 'Security:' in output")
	}
}

func TestFormatAnalyzeReport_WithComplexity(t *testing.T) {
	r := engine.AnalyzeReport{
		Files:     5,
		Nodes:     50,
		Edges:     25,
		Cycles:    0,
		Complexity: &engine.ComplexityReport{
			Average:       5.5,
			Max:           20,
			ScannedSymbol: 100,
			TotalSymbols: 150,
		},
	}
	got := formatAnalyzeReport(r)
	if !contains(got, "Complexity:") {
		t.Error("expected 'Complexity:' in output")
	}
}

func TestFormatSecurityReport_NilSecurity(t *testing.T) {
	r := engine.AnalyzeReport{}
	got := formatSecurityReport(r)
	if got == "" {
		t.Error("formatSecurityReport returned empty for nil security")
	}
}

func TestFormatSecurityReport_WithSecrets(t *testing.T) {
	r := engine.AnalyzeReport{
		Security: &security.Report{
			FilesScanned: 5,
			Secrets: []security.SecretFinding{
				{File: "env", Line: 1, Severity: "high", Pattern: "api_key"},
			},
		},
	}
	got := formatSecurityReport(r)
	if !contains(got, "Secrets:") {
		t.Error("expected 'Secrets:' in output")
	}
}

func TestFormatSecurityReport_WithVulns(t *testing.T) {
	r := engine.AnalyzeReport{
		Security: &security.Report{
			FilesScanned: 5,
			Vulnerabilities: []security.VulnerabilityFinding{
				{File: "test.go", Line: 10, Severity: "high", Kind: "sql_injection", Snippet: "query"},
				{File: "test.go", Line: 20, Severity: "medium", Kind: "xss", Snippet: "innerHTML"},
			},
		},
	}
	got := formatSecurityReport(r)
	if !contains(got, "Vulnerabilities:") {
		t.Error("expected 'Vulnerabilities:' in output")
	}
	if !contains(got, "high=1") {
		t.Error("expected high=1 in severity breakdown")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
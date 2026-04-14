package promptlib

import "testing"

func TestBuildStatsReportUnknownPlaceholder(t *testing.T) {
	templates := []Template{
		{
			ID:   "system.custom",
			Type: "system",
			Task: "general",
			Body: "Hello {{project_root}} {{unknown_var}}",
		},
	}

	report := BuildStatsReport(templates, StatsOptions{MaxTemplateTokens: 2})
	if report.TemplateCount != 1 {
		t.Fatalf("expected template_count=1, got %d", report.TemplateCount)
	}
	if report.WarningCount == 0 {
		t.Fatalf("expected warnings, got %#v", report)
	}
	if len(report.Templates) != 1 {
		t.Fatalf("expected one template item, got %d", len(report.Templates))
	}
	if len(report.Templates[0].UnknownPlaceholders) != 1 || report.Templates[0].UnknownPlaceholders[0] != "unknown_var" {
		t.Fatalf("expected unknown_var, got %#v", report.Templates[0].UnknownPlaceholders)
	}
}

func TestBuildStatsReportAllowVar(t *testing.T) {
	templates := []Template{
		{
			ID:   "system.custom.allowed",
			Type: "system",
			Task: "general",
			Body: "Hello {{custom_allowed_var}}",
		},
	}

	report := BuildStatsReport(templates, StatsOptions{
		MaxTemplateTokens: 100,
		AllowVars:         []string{"custom_allowed_var"},
	})
	if report.WarningCount != 0 {
		t.Fatalf("expected zero warnings, got %#v", report)
	}
}

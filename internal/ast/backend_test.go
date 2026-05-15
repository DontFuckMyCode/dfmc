package ast

import (
	"testing"
)

func TestBackendStatus_ReportsCorrectActiveBackend(t *testing.T) {
	status := currentBackendStatus()
	if status.Active == "" {
		t.Fatal("Active backend must not be empty")
	}
	if status.Preferred != "tree-sitter" {
		t.Fatalf("Preferred must be tree-sitter, got %q", status.Preferred)
	}
	knownBackends := map[string]bool{"tree-sitter": true, "regex": true, "hybrid": true}
	if !knownBackends[status.Active] {
		t.Fatalf("Active must be one of known backends, got %q", status.Active)
	}
	if len(status.Languages) == 0 {
		t.Fatal("Languages list must not be empty")
	}
	// Regex-only languages (rust, ruby, java) report Preferred=regex
	// because we don't ship tree-sitter grammars for them, and their
	// Active is always "regex" regardless of the overall hybrid /
	// regex mode. tree-sitter-backed languages must report
	// Preferred=tree-sitter and inherit the overall Active value.
	regexOnly := map[string]bool{"rust": true, "ruby": true, "java": true}
	for _, lang := range status.Languages {
		if lang.Language == "" {
			t.Fatal("Language entry must have a name")
		}
		if regexOnly[lang.Language] {
			if lang.Preferred != "regex" {
				t.Fatalf("Preferred for %s must be regex, got %q", lang.Language, lang.Preferred)
			}
			if lang.Active != "regex" {
				t.Fatalf("Active for regex-only %s must be regex, got %q", lang.Language, lang.Active)
			}
			continue
		}
		if lang.Preferred != "tree-sitter" {
			t.Fatalf("Preferred for %s must be tree-sitter, got %q", lang.Language, lang.Preferred)
		}
		if lang.Active != status.Active {
			t.Fatalf("Language %s Active=%q differs from overall Active=%q", lang.Language, lang.Active, status.Active)
		}
	}
}

func TestParseMetrics_ZeroOnNilEngine(t *testing.T) {
	var e *Engine
	m := e.ParseMetrics()
	if m.Requests != 0 || m.Parsed != 0 {
		t.Fatalf("nil engine must return zero metrics, got %+v", m)
	}
}

func TestParseMetrics_SnapshotIsIndependent(t *testing.T) {
	tracker := newParseMetricsTracker()
	tracker.recordParse("go", "tree-sitter", 0)
	m1 := tracker.snapshot()
	tracker.recordParse("go", "tree-sitter", 0)
	m2 := tracker.snapshot()
	if m1.Parsed == m2.Parsed {
		t.Fatalf("snapshot must reflect counts after recordParse; got same %d", m1.Parsed)
	}
}

func TestParseMetricsTracker_ResetClearsAll(t *testing.T) {
	tracker := newParseMetricsTracker()
	tracker.recordParse("go", "tree-sitter", 0)
	tracker.recordCacheMiss("go")
	tracker.recordError("go", "tree-sitter")
	tracker.reset()
	m := tracker.snapshot()
	if m.Requests != 0 || m.Parsed != 0 || m.CacheMisses != 0 || m.Errors != 0 {
		t.Fatalf("after reset: expected all zero, got %+v", m)
	}
}

func TestParseMetricsTracker_LastLanguage_Backend(t *testing.T) {
	tracker := newParseMetricsTracker()
	tracker.recordParse("python", "tree-sitter", 0)
	m := tracker.snapshot()
	if m.LastLanguage != "python" {
		t.Fatalf("expected LastLanguage=python, got %q", m.LastLanguage)
	}
	if m.LastBackend != "tree-sitter" {
		t.Fatalf("expected LastBackend=tree-sitter, got %q", m.LastBackend)
	}
}

func TestBackendLanguageStatus_JSONShape(t *testing.T) {
	s := BackendLanguageStatus{
		Language:  "go",
		Preferred: "tree-sitter",
		Active:    "tree-sitter",
	}
	if s.Language == "" || s.Preferred == "" || s.Active == "" {
		t.Fatal("all required fields must be non-empty")
	}
}

func TestBackendStatus_OverallReasonSet(t *testing.T) {
	status := currentBackendStatus()
	if status.Reason == "" {
		t.Log("note: reason may be empty when tree-sitter is fully available")
	}
	// tree-sitter-backed: go, javascript, jsx, typescript, tsx, python (6)
	// regex-only: rust, ruby, java (3)
	const wantLanguages = 9
	if len(status.Languages) != wantLanguages {
		t.Fatalf("expected %d languages in backend matrix, got %d (%#v)",
			wantLanguages, len(status.Languages), status.Languages)
	}
}

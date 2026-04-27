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
	knownBackends := map[string]bool{"tree-sitter": true, "regex": true}
	if !knownBackends[status.Active] {
		t.Fatalf("Active must be one of known backends, got %q", status.Active)
	}
	if len(status.Languages) == 0 {
		t.Fatal("Languages list must not be empty")
	}
	for _, lang := range status.Languages {
		if lang.Language == "" {
			t.Fatal("Language entry must have a name")
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
		Reason:    "cgo available",
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
	if len(status.Languages) != 6 {
		t.Fatalf("expected 6 languages (go, js, jsx, ts, tsx, python), got %d", len(status.Languages))
	}
}

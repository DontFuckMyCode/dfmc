package tools

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// TestConfigCapsOverride verifies that ReadSnapshotCap / RecentFailureCap
// from cfg.Agent are honoured by the tools.Engine instead of the package
// constants. P12: tunables live in defaults.go; tests assert override
// works end-to-end.
func TestConfigCapsOverride(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agent.ReadSnapshotCap = 4
	cfg.Agent.RecentFailureCap = 6

	eng := New(*cfg)
	if eng.readSnapshotCap != 4 {
		t.Fatalf("readSnapshotCap override not applied: got %d, want 4", eng.readSnapshotCap)
	}
	if eng.recentFailureCap != 6 {
		t.Fatalf("recentFailureCap override not applied: got %d, want 6", eng.recentFailureCap)
	}
}

// TestConfigCapsZeroFallsBackToConst verifies that a zero value in cfg
// (e.g. partial config maps from older project files) falls back to the
// package-level constants rather than producing an unbounded cap.
func TestConfigCapsZeroFallsBackToConst(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agent.ReadSnapshotCap = 0
	cfg.Agent.RecentFailureCap = 0

	eng := New(*cfg)
	if eng.readSnapshotCap != maxReadSnapshots {
		t.Fatalf("zero readSnapshotCap should fall back to %d, got %d", maxReadSnapshots, eng.readSnapshotCap)
	}
	if eng.recentFailureCap != maxRecentFailures {
		t.Fatalf("zero recentFailureCap should fall back to %d, got %d", maxRecentFailures, eng.recentFailureCap)
	}
}

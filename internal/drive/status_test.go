package drive

import (
	"testing"
)

func TestBuildStatusInfo(t *testing.T) {
	tests := []struct {
		input         string
		wantTerminal  bool
		wantPending   bool
		wantVerifying bool
		wantWaiting   bool
		wantExternal  bool
	}{
		{"pending", false, true, false, false, false},
		{"running", false, false, false, false, false},
		{"done", true, false, false, false, false},
		{"blocked", true, false, false, false, false},
		{"skipped", true, false, false, false, false},
		{"verifying", false, true, true, false, false},
		{"waiting", false, true, false, true, false},
		{"external_review", false, true, false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			si := BuildStatusInfo(tt.input)

			if si.IsTerminal() != tt.wantTerminal {
				t.Errorf("IsTerminal(%q) = %v, want %v", tt.input, si.IsTerminal(), tt.wantTerminal)
			}
			if si.IsPending() != tt.wantPending {
				t.Errorf("IsPending(%q) = %v, want %v", tt.input, si.IsPending(), tt.wantPending)
			}
			if si.HasVerifying() != tt.wantVerifying {
				t.Errorf("HasVerifying(%q) = %v, want %v", tt.input, si.HasVerifying(), tt.wantVerifying)
			}
			if si.HasWaiting() != tt.wantWaiting {
				t.Errorf("HasWaiting(%q) = %v, want %v", tt.input, si.HasWaiting(), tt.wantWaiting)
			}
			if si.HasExternal() != tt.wantExternal {
				t.Errorf("HasExternal(%q) = %v, want %v", tt.input, si.HasExternal(), tt.wantExternal)
			}
		})
	}
}

func TestStatusHelpers(t *testing.T) {
	helpers := StatusHelpers{}

	// Test IsPending
	pendingStatuses := []TodoStatus{TodoPending, TodoVerifying, TodoWaiting, TodoExternalReview}
	nonPending := []TodoStatus{TodoRunning, TodoDone, TodoBlocked, TodoSkipped}

	for _, s := range pendingStatuses {
		if !helpers.IsPending(s) {
			t.Errorf("IsPending(%v) = false, want true", s)
		}
	}
	for _, s := range nonPending {
		if helpers.IsPending(s) {
			t.Errorf("IsPending(%v) = true, want false", s)
		}
	}

	// Test IsTerminal
	terminalStatuses := []TodoStatus{TodoDone, TodoBlocked, TodoSkipped}
	nonTerminal := []TodoStatus{TodoPending, TodoRunning, TodoVerifying, TodoWaiting}

	for _, s := range terminalStatuses {
		if !helpers.IsTerminal(s) {
			t.Errorf("IsTerminal(%v) = false, want true", s)
		}
	}
	for _, s := range nonTerminal {
		if helpers.IsTerminal(s) {
			t.Errorf("IsTerminal(%v) = true, want false", s)
		}
	}

	// Test IsActive
	activeStatuses := []TodoStatus{TodoPending, TodoRunning, TodoVerifying, TodoWaiting}
	nonActive := []TodoStatus{TodoDone, TodoBlocked, TodoSkipped, TodoExternalReview}

	for _, s := range activeStatuses {
		if !helpers.IsActive(s) {
			t.Errorf("IsActive(%v) = false, want true", s)
		}
	}
	for _, s := range nonActive {
		if helpers.IsActive(s) {
			t.Errorf("IsActive(%v) = true, want false", s)
		}
	}
}

func TestStatusInfoString(t *testing.T) {
	for _, s := range []string{"pending", "running", "done", "blocked", "verifying", "waiting", "external_review"} {
		si := BuildStatusInfo(s)
		if si.String() != s {
			t.Errorf("String() = %q, want %q", si.String(), s)
		}
	}
}

func TestStatusFlags(t *testing.T) {
	// Test flag combinations
	si := BuildStatusInfo("verifying")
	if !si.Flags.HasFlag(FlagPending) || !si.Flags.HasFlag(FlagVerifying) {
		t.Error("verifying should have FlagPending and FlagVerifying")
	}

	si = BuildStatusInfo("waiting")
	if !si.Flags.HasFlag(FlagPending) || !si.Flags.HasFlag(FlagWaiting) {
		t.Error("waiting should have FlagPending and FlagWaiting")
	}

	si = BuildStatusInfo("external_review")
	if !si.Flags.HasFlag(FlagPending) || !si.Flags.HasFlag(FlagExternal) {
		t.Error("external_review should have FlagPending and FlagExternal")
	}
}

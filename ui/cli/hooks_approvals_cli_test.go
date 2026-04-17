package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRunHooksCLI_TextMode(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		if rc := runHooksCLI(eng, nil, false); rc != 0 {
			t.Fatalf("runHooksCLI exit=%d", rc)
		}
	})
	// Empty dispatcher should say so explicitly — operators need a
	// clear signal that "nothing is registered" is the actual state,
	// not a broken command.
	if !strings.Contains(out, "none registered") {
		t.Fatalf("fresh engine should report 'none registered', got:\n%s", out)
	}
}

func TestRunHooksCLI_RejectsUnknownSubcommand(t *testing.T) {
	eng := newCLITestEngine(t)
	if rc := runHooksCLI(eng, []string{"teleport"}, false); rc != 2 {
		t.Fatalf("unknown subcommand should exit 2, got %d", rc)
	}
}

func TestRunHooksCLI_JSONShape(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		if rc := runHooksCLI(eng, nil, true); rc != 0 {
			t.Fatalf("runHooksCLI json exit=%d", rc)
		}
	})
	var payload struct {
		Total    int            `json:"total"`
		PerEvent map[string]any `json:"per_event"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal hooks json: %v\n%s", err, out)
	}
	if payload.Total < 0 {
		t.Fatalf("total must never be negative, got %d", payload.Total)
	}
}

func TestRunApprovalsCLI_NoGateByDefault(t *testing.T) {
	eng := newCLITestEngine(t)
	out := captureStdout(t, func() {
		if rc := runApprovalsCLI(eng, nil, false); rc != 0 {
			t.Fatalf("runApprovalsCLI exit=%d", rc)
		}
	})
	if !strings.Contains(out, "approval gate:") {
		t.Fatalf("expected approval gate line, got:\n%s", out)
	}
	if !strings.Contains(out, "off") {
		t.Fatalf("default config should show gate=off, got:\n%s", out)
	}
	if !strings.Contains(out, "recent denials: none") {
		t.Fatalf("fresh engine should report no recent denials, got:\n%s", out)
	}
}

func TestRunApprovalsCLI_WithGatedTools(t *testing.T) {
	eng := newCLITestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"write_file", "run_command"}
	out := captureStdout(t, func() {
		if rc := runApprovalsCLI(eng, nil, false); rc != 0 {
			t.Fatalf("runApprovalsCLI exit=%d", rc)
		}
	})
	if !strings.Contains(out, "write_file") || !strings.Contains(out, "run_command") {
		t.Fatalf("expected both gated tools listed, got:\n%s", out)
	}
}

func TestRunApprovalsCLI_JSONShape(t *testing.T) {
	eng := newCLITestEngine(t)
	eng.Config.Tools.RequireApproval = []string{"write_file"}
	out := captureStdout(t, func() {
		if rc := runApprovalsCLI(eng, nil, true); rc != 0 {
			t.Fatalf("runApprovalsCLI json exit=%d", rc)
		}
	})
	var payload struct {
		Gate struct {
			Active bool     `json:"active"`
			Count  int      `json:"count"`
			Tools  []string `json:"tools"`
		} `json:"gate"`
		RecentDenials []map[string]any `json:"recent_denials"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal approvals json: %v\n%s", err, out)
	}
	if !payload.Gate.Active || payload.Gate.Count != 1 {
		t.Fatalf("unexpected gate payload: %#v", payload.Gate)
	}
	if len(payload.Gate.Tools) != 1 || payload.Gate.Tools[0] != "write_file" {
		t.Fatalf("unexpected tool list: %v", payload.Gate.Tools)
	}
}

func TestRunApprovalsCLI_RejectsUnknownSubcommand(t *testing.T) {
	eng := newCLITestEngine(t)
	if rc := runApprovalsCLI(eng, []string{"yolo"}, false); rc != 2 {
		t.Fatalf("unknown subcommand should exit 2, got %d", rc)
	}
}

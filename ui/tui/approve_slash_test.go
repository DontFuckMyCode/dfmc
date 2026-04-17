package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// newApproveTestModel returns a Model pointed at a fresh (uninit) engine
// so we can drive Config.Tools.RequireApproval without spinning up bbolt,
// providers, or anything else. The /approve slash is a pure view over
// config + pendingApproval, so no Init is needed.
func newApproveTestModel(t *testing.T, gated []string, pending *pendingApproval) Model {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Tools.RequireApproval = gated
	eng := &engine.Engine{Config: cfg}
	m := NewModel(context.Background(), eng)
	m.pendingApproval = pending
	return m
}

func TestSlashApprove_ShowsOffWhenListIsEmpty(t *testing.T) {
	m := newApproveTestModel(t, nil, nil)
	next, _, handled := m.executeChatCommand("/approve")
	if !handled {
		t.Fatalf("/approve must be handled")
	}
	nm := next.(Model)
	last := nm.transcript[len(nm.transcript)-1].Content
	if !strings.Contains(strings.ToLower(last), "state:    off") {
		t.Fatalf("empty list should show state:off, got:\n%s", last)
	}
	if !strings.Contains(last, "require_approval") {
		t.Fatalf("should hint at config key 'require_approval', got:\n%s", last)
	}
}

func TestSlashApprove_ShowsGatedListWhenConfigured(t *testing.T) {
	m := newApproveTestModel(t, []string{"write_file", "apply_patch", "run_command"}, nil)
	next, _, handled := m.executeChatCommand("/approve")
	if !handled {
		t.Fatalf("/approve must be handled")
	}
	nm := next.(Model)
	last := nm.transcript[len(nm.transcript)-1].Content
	if !strings.Contains(last, "state:    ON") {
		t.Fatalf("populated list should show state:ON, got:\n%s", last)
	}
	for _, tool := range []string{"write_file", "apply_patch", "run_command"} {
		if !strings.Contains(last, tool) {
			t.Fatalf("gated list should mention %q, got:\n%s", tool, last)
		}
	}
}

func TestSlashApprove_ShowsPendingRequest(t *testing.T) {
	pending := &pendingApproval{
		ID: 7,
		Req: engine.ApprovalRequest{
			Tool:   "write_file",
			Source: "agent",
		},
	}
	m := newApproveTestModel(t, []string{"write_file"}, pending)
	next, _, _ := m.executeChatCommand("/approve")
	nm := next.(Model)
	last := nm.transcript[len(nm.transcript)-1].Content
	if !strings.Contains(last, "pending:") {
		t.Fatalf("should surface pending line, got:\n%s", last)
	}
	if !strings.Contains(last, "write_file") || !strings.Contains(last, "source=agent") {
		t.Fatalf("pending line should name the tool and source, got:\n%s", last)
	}
}

func TestSlashApprove_PermissionsAlias(t *testing.T) {
	// /permissions is the Claude-Code-familiar spelling; must route to the
	// same handler so users discover it either way.
	m := newApproveTestModel(t, []string{"write_file"}, nil)
	next, _, handled := m.executeChatCommand("/permissions")
	if !handled {
		t.Fatalf("/permissions must be handled as alias for /approve")
	}
	nm := next.(Model)
	last := nm.transcript[len(nm.transcript)-1].Content
	if !strings.Contains(last, "Tool approval gate") {
		t.Fatalf("/permissions alias should show the same gate snapshot, got:\n%s", last)
	}
}

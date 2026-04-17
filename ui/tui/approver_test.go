package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// seedPendingApproval parks a pending approval on the model so tests can
// exercise the y/n key handlers without spinning up the real teaApprover.
func seedPendingApproval(m *Model, tool string, params map[string]any) chan engine.ApprovalDecision {
	resp := make(chan engine.ApprovalDecision, 1)
	m.pendingApproval = &pendingApproval{
		ID: 1,
		Req: engine.ApprovalRequest{
			Tool:   tool,
			Params: params,
			Source: "agent",
		},
		resp: resp,
	}
	return resp
}

func TestApprovalModal_YapprovesAndClears(t *testing.T) {
	m := NewModel(context.Background(), nil)
	resp := seedPendingApproval(&m, "write_file", map[string]any{"path": "a.txt"})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	nm := next.(Model)
	if nm.pendingApproval != nil {
		t.Fatalf("y should clear pendingApproval")
	}
	select {
	case decision := <-resp:
		if !decision.Approved {
			t.Fatalf("y must deliver Approved=true, got %+v", decision)
		}
	default:
		t.Fatalf("y must resolve the channel")
	}
}

func TestApprovalModal_EnterApproves(t *testing.T) {
	m := NewModel(context.Background(), nil)
	resp := seedPendingApproval(&m, "run_command", nil)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm := next.(Model)
	if nm.pendingApproval != nil {
		t.Fatalf("enter should clear pendingApproval")
	}
	select {
	case decision := <-resp:
		if !decision.Approved {
			t.Fatalf("enter must approve, got %+v", decision)
		}
	default:
		t.Fatalf("enter must resolve the channel")
	}
}

func TestApprovalModal_NDenies(t *testing.T) {
	m := NewModel(context.Background(), nil)
	resp := seedPendingApproval(&m, "write_file", nil)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	nm := next.(Model)
	if nm.pendingApproval != nil {
		t.Fatalf("n should clear pendingApproval")
	}
	select {
	case decision := <-resp:
		if decision.Approved {
			t.Fatalf("n must deny, got approved")
		}
		if decision.Reason == "" {
			t.Fatalf("deny reason should be non-empty")
		}
	default:
		t.Fatalf("n must resolve the channel")
	}
}

func TestApprovalModal_EscDenies(t *testing.T) {
	m := NewModel(context.Background(), nil)
	resp := seedPendingApproval(&m, "edit_file", nil)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	nm := next.(Model)
	if nm.pendingApproval != nil {
		t.Fatalf("esc should clear pendingApproval")
	}
	select {
	case decision := <-resp:
		if decision.Approved {
			t.Fatalf("esc must deny")
		}
	default:
		t.Fatalf("esc must resolve the channel")
	}
}

func TestApprovalModal_UnrelatedKeysSwallowed(t *testing.T) {
	m := NewModel(context.Background(), nil)
	resp := seedPendingApproval(&m, "write_file", nil)

	// Typing "a" (a real rune) while the modal is up must neither land
	// in the composer nor resolve the approval.
	before := m.input
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	nm := next.(Model)
	if nm.input != before {
		t.Fatalf("stray runes should not reach the composer while approval modal is open; input=%q", nm.input)
	}
	if nm.pendingApproval == nil {
		t.Fatalf("approval should still be pending after a stray keystroke")
	}
	select {
	case <-resp:
		t.Fatalf("stray key must not resolve the channel")
	default:
	}
}

func TestApprovalModal_SecondRequestAutoDenied(t *testing.T) {
	m := NewModel(context.Background(), nil)
	first := seedPendingApproval(&m, "write_file", nil)

	secondResp := make(chan engine.ApprovalDecision, 1)
	pending := &pendingApproval{
		ID:   2,
		Req:  engine.ApprovalRequest{Tool: "run_command", Source: "agent"},
		resp: secondResp,
	}
	next, _ := m.Update(approvalRequestedMsg{Pending: pending})
	nm := next.(Model)
	if nm.pendingApproval == nil || nm.pendingApproval.ID != 1 {
		t.Fatalf("first pending approval must be preserved when a second arrives; got %+v", nm.pendingApproval)
	}
	select {
	case decision := <-secondResp:
		if decision.Approved {
			t.Fatalf("concurrent second approval must be auto-denied")
		}
	default:
		t.Fatalf("second approval must be resolved (auto-deny)")
	}
	// The first channel should still be open and waiting.
	select {
	case <-first:
		t.Fatalf("first approval should not have been resolved yet")
	default:
	}
}

func TestRenderApprovalModal_IncludesToolAndParams(t *testing.T) {
	p := &pendingApproval{
		ID: 1,
		Req: engine.ApprovalRequest{
			Tool:   "write_file",
			Source: "agent",
			Params: map[string]any{
				"path":    "README.md",
				"content": "hello world",
			},
		},
	}
	out := renderApprovalModal(p, 80)
	if !strings.Contains(out, "write_file") {
		t.Fatalf("modal must include tool name; got: %s", out)
	}
	if !strings.Contains(out, "agent") {
		t.Fatalf("modal must include source; got: %s", out)
	}
	if !strings.Contains(out, "path") || !strings.Contains(out, "README.md") {
		t.Fatalf("modal must include parameter lines; got: %s", out)
	}
	if !strings.Contains(out, "approve") || !strings.Contains(out, "deny") {
		t.Fatalf("modal must include action hints; got: %s", out)
	}
}

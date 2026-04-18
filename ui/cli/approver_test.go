package cli

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// newTestStdinApprover skips the real stdin-is-tty detection and wires
// in plumbable reader/writer — tests drive the approver deterministically
// instead of relying on whatever the CI runner considers a terminal.
func newTestStdinApprover(stdin io.Reader, stdout io.Writer, autoYes, autoNo bool) *stdinApprover {
	return &stdinApprover{
		reader:  bufio.NewReader(stdin),
		in:      stdin,
		out:     stdout,
		isTTY:   true,
		autoYes: autoYes,
		autoNo:  autoNo,
	}
}

func TestStdinApprover_AutoYes_NonDestructive(t *testing.T) {
	// DFMC_APPROVE=yes alone auto-approves only read-only tools; write/shell
	// require the second knob (see TestStdinApprover_AutoYes_DestructiveDeniedWithoutSecondKnob).
	var out bytes.Buffer
	ap := newTestStdinApprover(strings.NewReader(""), &out, true, false)
	decision := ap.RequestApproval(context.Background(), engine.ApprovalRequest{Tool: "read_file"})
	if !decision.Approved {
		t.Fatalf("DFMC_APPROVE=yes should auto-approve read_file, got %+v", decision)
	}
	if out.Len() != 0 {
		t.Fatalf("auto-yes path should not prompt, got output: %q", out.String())
	}
}

// TestStdinApprover_AutoYes_DestructiveDeniedWithoutSecondKnob pins the
// two-knob gate: a leaked DFMC_APPROVE=yes in CI must not be enough to
// silently grant write/shell access. Operators have to also set
// DFMC_APPROVE_DESTRUCTIVE=yes — the deny reason explains how.
func TestStdinApprover_AutoYes_DestructiveDeniedWithoutSecondKnob(t *testing.T) {
	var out bytes.Buffer
	ap := newTestStdinApprover(strings.NewReader(""), &out, true, false)
	for _, tool := range []string{"write_file", "edit_file", "apply_patch", "run_command", "delegate_task"} {
		decision := ap.RequestApproval(context.Background(), engine.ApprovalRequest{Tool: tool})
		if decision.Approved {
			t.Fatalf("DFMC_APPROVE=yes must NOT auto-approve %s without DFMC_APPROVE_DESTRUCTIVE=yes; got %+v", tool, decision)
		}
		if !strings.Contains(decision.Reason, "DFMC_APPROVE_DESTRUCTIVE") {
			t.Fatalf("deny reason for %s should explain the second knob, got %q", tool, decision.Reason)
		}
	}
}

// TestStdinApprover_AutoYes_DestructiveAllowedWithSecondKnob pins the
// other half: with both knobs set, every tool — including writes/shell
// — is auto-approved. This is the explicit "I know what I'm doing"
// configuration.
func TestStdinApprover_AutoYes_DestructiveAllowedWithSecondKnob(t *testing.T) {
	var out bytes.Buffer
	ap := newTestStdinApprover(strings.NewReader(""), &out, true, false)
	ap.autoYesDestructive = true
	for _, tool := range []string{"write_file", "run_command"} {
		decision := ap.RequestApproval(context.Background(), engine.ApprovalRequest{Tool: tool})
		if !decision.Approved {
			t.Fatalf("both knobs set must auto-approve %s, got %+v", tool, decision)
		}
	}
}

func TestStdinApprover_AutoNo(t *testing.T) {
	var out bytes.Buffer
	ap := newTestStdinApprover(strings.NewReader(""), &out, false, true)
	decision := ap.RequestApproval(context.Background(), engine.ApprovalRequest{Tool: "write_file"})
	if decision.Approved {
		t.Fatalf("DFMC_APPROVE=no should auto-deny, got %+v", decision)
	}
	if decision.Reason == "" {
		t.Fatalf("auto-deny should carry a reason")
	}
}

func TestStdinApprover_NonInteractiveDenies(t *testing.T) {
	var out bytes.Buffer
	ap := newTestStdinApprover(strings.NewReader(""), &out, false, false)
	ap.isTTY = false
	decision := ap.RequestApproval(context.Background(), engine.ApprovalRequest{Tool: "run_command"})
	if decision.Approved {
		t.Fatalf("non-interactive stdin must deny")
	}
	if !strings.Contains(decision.Reason, "non-interactive") {
		t.Fatalf("deny reason should mention non-interactive: %q", decision.Reason)
	}
}

func TestStdinApprover_YesOnTTY(t *testing.T) {
	var out bytes.Buffer
	ap := newTestStdinApprover(strings.NewReader("y\n"), &out, false, false)
	decision := ap.RequestApproval(context.Background(), engine.ApprovalRequest{
		Tool:   "write_file",
		Params: map[string]any{"path": "README.md"},
	})
	if !decision.Approved {
		t.Fatalf("y should approve, got %+v", decision)
	}
	if !strings.Contains(out.String(), "DFMC tool approval") {
		t.Fatalf("prompt should be printed to out: %q", out.String())
	}
	if !strings.Contains(out.String(), "README.md") {
		t.Fatalf("prompt should include the param value: %q", out.String())
	}
}

func TestStdinApprover_EmptyLineDenies(t *testing.T) {
	// A blank Enter must default to deny so a careless keystroke doesn't
	// greenlight a destructive tool.
	var out bytes.Buffer
	ap := newTestStdinApprover(strings.NewReader("\n"), &out, false, false)
	decision := ap.RequestApproval(context.Background(), engine.ApprovalRequest{Tool: "write_file"})
	if decision.Approved {
		t.Fatalf("blank-line answer must deny")
	}
}

func TestStdinApprover_ExplicitNoDenies(t *testing.T) {
	var out bytes.Buffer
	ap := newTestStdinApprover(strings.NewReader("no\n"), &out, false, false)
	decision := ap.RequestApproval(context.Background(), engine.ApprovalRequest{Tool: "write_file"})
	if decision.Approved {
		t.Fatalf("no must deny")
	}
}

func TestStdinApprover_ContextCancel(t *testing.T) {
	var out bytes.Buffer
	// An empty reader that would block forever on ReadString. Cancelling
	// the context must surface a deny without hanging the test.
	ap := newTestStdinApprover(blockingReader{}, &out, false, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	decision := ap.RequestApproval(ctx, engine.ApprovalRequest{Tool: "write_file"})
	if decision.Approved {
		t.Fatalf("canceled context must deny")
	}
	if !strings.Contains(decision.Reason, "canceled") {
		t.Fatalf("deny reason should mention cancellation: %q", decision.Reason)
	}
}

// blockingReader blocks forever on Read — mimics a stdin with nothing
// coming. Used to drive the ctx.Done path in the approver.
type blockingReader struct{}

func (blockingReader) Read(p []byte) (int, error) {
	select {}
}

func TestCompactJSONParams_TruncatesLongValues(t *testing.T) {
	long := strings.Repeat("x", 500)
	out := compactJSONParams(map[string]any{"content": long}, 120)
	if len(out) > 120 {
		t.Fatalf("compactJSONParams must respect max (got len=%d): %s", len(out), out)
	}
	if !strings.HasSuffix(out, "…") {
		t.Fatalf("truncated output should end with ellipsis, got %q", out[len(out)-10:])
	}
}

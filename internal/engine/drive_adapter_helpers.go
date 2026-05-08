package engine

// drive_adapter_helpers.go — prompt builders + auto-approver wiring
// for the drive runner. Sibling of drive_adapter.go which keeps the
// drive.Runner implementation (PlannerCall + ExecuteTodo) and the
// PublishDriveEvent bus bridge.
//
// Splitting these out keeps the runner methods focused on the
// drive↔engine boundary, while the prompt assembly + scoped tool
// approval surface — both reusable from non-runner callers — stay
// auditable in isolation.

import (
	"context"
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// buildDriveTodoPrompt is the prompt the executor sub-agent sees. The
// brief from prior TODOs goes first so the model has context before
// the actual instructions; success criteria and "report a brief" are
// pinned at the end so the model lands on them when generating its
// final answer.
//
// Keeping this in one place (not per-call template strings) means
// adjusting executor behavior is a one-file change.
func buildDriveTodoPrompt(req drive.ExecuteTodoRequest) string {
	var b strings.Builder
	if strings.TrimSpace(req.Brief) != "" {
		b.WriteString("Context from prior TODOs in this drive run:\n")
		b.WriteString(req.Brief)
		b.WriteString("\n\n")
	}
	b.WriteString("You are working on TODO ")
	b.WriteString(req.TodoID)
	b.WriteString(": ")
	b.WriteString(req.Title)
	if role := strings.TrimSpace(req.Role); role != "" {
		b.WriteString("\nWorker role: ")
		b.WriteString(role)
	}
	if len(req.Labels) > 0 {
		b.WriteString("\nLabels: ")
		b.WriteString(strings.Join(req.Labels, ", "))
	}
	if strings.TrimSpace(req.Verification) != "" {
		b.WriteString("\nVerification expectation: ")
		b.WriteString(req.Verification)
	}
	b.WriteString("\n\nInstructions:\n")
	b.WriteString(strings.TrimSpace(req.Detail))
	if len(req.Skills) > 0 {
		b.WriteString("\n\nSuggested runtime capabilities: ")
		b.WriteString(strings.Join(req.Skills, ", "))
		b.WriteString(". Use them if they sharpen the approach.")
	}
	b.WriteString("\n\nWhen finished, return a SHORT (under 200 tokens) brief covering: what you changed, which files, and how to verify. The brief is the only thing the next TODO will see from your work, so be concrete.")
	b.WriteString("\nIf you discover truly necessary follow-up child tasks that are missing from the plan, append one final JSON object after the brief with this exact top-level key: {\"spawn_todos\":[...]}.")
	b.WriteString("\nEach spawned todo should include title, detail, optional depends_on, file_scope, provider_tag, worker_class, skills, allowed_tools, labels, verification, and confidence. Keep spawned tasks to at most 4 and only emit them when the missing work is real.")
	return b.String()
}

func decorateDriveTaskWithSkills(names []string, input string) string {
	input = strings.TrimSpace(input)
	if len(names) == 0 {
		return input
	}
	out := input
	seen := map[string]struct{}{}
	for i := len(names) - 1; i >= 0; i-- {
		name := strings.TrimSpace(names[i])
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if out == "" {
			out = "[[skill:" + name + "]]"
			continue
		}
		out = "[[skill:" + name + "]]\n" + out
	}
	return out
}

func driveExecutorRole(role string) string {
	role = strings.TrimSpace(role)
	if role == "" {
		return "drive-executor"
	}
	return role
}

func stringSliceFromAny(raw any) []string {
	switch v := raw.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s := strings.TrimSpace(fmt.Sprint(item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// BeginAutoApprove activates a scoped auto-approval override for
// drive-owned tool calls. The previous Approver is preserved and
// restored when the returned release function is called. The wildcard
// "*" approves every drive-owned tool.
//
// Returns a no-op release when tools is empty so callers can always
// `defer release()` regardless of config.

// beginAutoApproveToken holds the ownership token for an active auto-approve
// override. Only the owner can release it.
type beginAutoApproveToken struct{}

func (r *driveRunner) BeginAutoApprove(tools []string) func() {
	if r == nil || r.e == nil || len(tools) == 0 {
		return func() {}
	}
	prev := r.e.approver()
	override := newDriveAutoApprover(prev, tools, "drive")
	token := &beginAutoApproveToken{}
	r.e.SetApproverWithToken(override, token)
	return func() {
		// Only restore if we are still the owner — prevents a concurrent
		// Drive run's release from clobbering our override.
		r.e.ReleaseApproverWithToken(token)
	}
}

// driveAutoApprover wraps an underlying Approver and unconditionally
// approves any tool in its allowlist. Tools NOT in the allowlist
// fall through to the wrapped Approver — so the user's TUI modal /
// stdin prompt still fires for sensitive operations they didn't
// pre-approve.
type driveAutoApprover struct {
	wrapped Approver
	allow   map[string]struct{}
	source  string
	any     bool // true when "*" is in the allowlist
}

func newDriveAutoApprover(wrapped Approver, tools []string, source string) *driveAutoApprover {
	a := &driveAutoApprover{
		wrapped: wrapped,
		allow:   make(map[string]struct{}, len(tools)),
		source:  strings.ToLower(strings.TrimSpace(source)),
	}
	for _, t := range tools {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if t == "*" {
			a.any = true
			continue
		}
		a.allow[strings.ToLower(t)] = struct{}{}
	}
	return a
}

// RequestApproval implements Approver. Auto-approves on hit, falls
// through to wrapped on miss. When wrapped is nil and the tool is
// not auto-approved we deny — matches the engine's existing fail-
// safe default in askToolApproval.
func (a *driveAutoApprover) RequestApproval(ctx context.Context, req ApprovalRequest) ApprovalDecision {
	name := strings.ToLower(strings.TrimSpace(req.Tool))
	source := strings.ToLower(strings.TrimSpace(req.Source))
	if a.source != "" && source != a.source {
		if a.wrapped == nil {
			return ApprovalDecision{Approved: false, Reason: "no approver registered"}
		}
		return a.wrapped.RequestApproval(ctx, req)
	}
	if a.any {
		return ApprovalDecision{Approved: true, Reason: "drive auto-approve (*)"}
	}
	if _, ok := a.allow[name]; ok {
		return ApprovalDecision{Approved: true, Reason: "drive auto-approve"}
	}
	if a.wrapped == nil {
		return ApprovalDecision{Approved: false, Reason: "no approver registered (drive auto-approve list does not cover " + req.Tool + ")"}
	}
	return a.wrapped.RequestApproval(ctx, req)
}

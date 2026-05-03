// Tool approval gate.
//
// The engine has a single entry point for tool execution
// (executeToolWithLifecycle). For agent-initiated calls we optionally
// route through an Approver so a UI layer can prompt the user before
// destructive operations actually run. User-initiated calls (typing
// /tool in the TUI, `dfmc tool run`, etc.) bypass the gate — the user
// already typed the command, asking a second time would be condescending.
//
// Defaults are deliberately conservative:
//   - No Approver registered ⇒ implicit DENY on gated tools (fail-safe).
//   - Empty RequireApproval list ⇒ nothing is gated (fail-open, opt-in).
//   - "*" in RequireApproval ⇒ every tool gated.
//
// The implicit-deny on nil Approver means a misconfigured install can't
// silently allow a destructive tool to run. Users who want the gate have
// to wire up an Approver explicitly (the TUI does, /api/v1 does not).

package engine

import (
	"context"
	"strings"
	"time"
)

// ApprovalRequest is the structured ask handed to an Approver.
type ApprovalRequest struct {
	Tool        string
	Params      map[string]any
	Source      string // "agent", "subagent", "plugin", etc.
	ProjectRoot string
}

// ApprovalDecision is the Approver's verdict. Approved=false denies;
// Reason surfaces on the `tool:denied` event and in the error returned
// to the caller so the UI can render a useful message.
type ApprovalDecision struct {
	Approved bool
	Reason   string
}

// Approver is the UI-side contract for tool approval. The engine calls
// RequestApproval synchronously; the UI is expected to block until the
// user responds or a timeout fires. Implementations should respect the
// context — if ctx is done, return an implicit deny with Reason="context
// canceled" so the engine can surface the right error.
type Approver interface {
	RequestApproval(ctx context.Context, req ApprovalRequest) ApprovalDecision
}

// ApproverFunc is the functional adapter for short-lived approvers
// (tests, plumbing helpers, one-off CLIs that auto-approve).
type ApproverFunc func(ctx context.Context, req ApprovalRequest) ApprovalDecision

// RequestApproval implements Approver.
func (f ApproverFunc) RequestApproval(ctx context.Context, req ApprovalRequest) ApprovalDecision {
	return f(ctx, req)
}

// SetApprover registers (or clears, when approver==nil) the Approver for
// this engine. Safe to call at any point in the engine lifecycle; later
// tool invocations will see the new Approver immediately.
func (e *Engine) SetApprover(approver Approver) {
	if e == nil {
		return
	}
	e.approvalMu.Lock()
	e.registeredApprover = approver
	e.approvalMu.Unlock()
}

// SetApproverWithToken registers the approver with an ownership token. The
// token must be presented on ReleaseApproverWithToken to restore the previous
// approver — concurrent or nested Drive runs cannot clobber each other's
// overrides.
func (e *Engine) SetApproverWithToken(approver Approver, token any) {
	if e == nil {
		return
	}
	e.approvalMu.Lock()
	defer e.approvalMu.Unlock()
	e.registeredApprover = approver
	e.approverToken = token
}

// ReleaseApproverWithToken restores the previous approver only if the supplied
// token matches the one recorded during the last SetApproverWithToken call.
// If the token does not match (another override was installed since), the
// release is a no-op — the newer override stays intact.
func (e *Engine) ReleaseApproverWithToken(token any) {
	if e == nil {
		return
	}
	e.approvalMu.Lock()
	defer e.approvalMu.Unlock()
	if e.approverToken == token {
		// Token match — we still own the slot; restore nil to clear.
		// Caller is responsible for ensuring no newer override was installed.
		e.registeredApprover = nil
		e.approverToken = nil
	}
	// Token mismatch: another override owns the slot; do nothing.
}

// approver returns the currently-registered Approver or nil.
func (e *Engine) approver() Approver {
	e.approvalMu.RLock()
	defer e.approvalMu.RUnlock()
	return e.registeredApprover
}

// requiresApproval reports whether a tool requires approval for the given
// source. "*" is the gate-everything wildcard. Zero-allocation on the hot
// path (len() check short-circuits the common case of no gate configured).
//
// For non-user sources (web, ws, mcp) we consult RequireApprovalNetwork
// first, falling back to RequireApproval if the network list is empty.
// This lets operators lock down network traffic independently from
// agent-loop traffic.
func (e *Engine) requiresApproval(name string, source Source) bool {
	if e == nil || e.Config == nil {
		return false
	}
	// SourceUser is real user input at the terminal; SourceCLI is the
	// operator's own tooling. Both bypass the gate.
	if source.IsPrivileged() {
		return false
	}
	// For web/ws/mcp, check RequireApprovalNetwork first (strictest gate).
	// For agent/subagent, use the regular RequireApproval list.
	var list []string
	switch source {
	case SourceWeb, SourceWS, SourceMCP:
		list = e.Config.Tools.RequireApprovalNetwork
		if len(list) == 0 {
			list = e.Config.Tools.RequireApproval
		}
	default: // SourceAgent, SourceSubagent, or any other source
		list = e.Config.Tools.RequireApproval
	}
	if len(list) == 0 {
		return false
	}
	name = strings.TrimSpace(name)
	for _, entry := range list {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry == "*" || entry == name {
			return true
		}
	}
	return false
}

// askToolApproval consults the registered Approver. With no Approver
// registered we deny by default — the safe failure mode. A UI that wants
// permissive behaviour can register an auto-approver.
func (e *Engine) askToolApproval(ctx context.Context, name string, params map[string]any, source Source) ApprovalDecision {
	ap := e.approver()
	if ap == nil {
		return ApprovalDecision{Approved: false, Reason: "no approver registered"}
	}
	req := ApprovalRequest{
		Tool:        name,
		Params:      params,
		Source:      string(source),
		ProjectRoot: e.ProjectRoot,
	}
	decision := ap.RequestApproval(ctx, req)
	return decision
}

// RecentDenial captures a single gated-call rejection for diagnostic
// surfacing. Sized to keep memory trivial; the engine stores the last
// recentDenialsCapacity entries only.
type RecentDenial struct {
	Tool   string
	Source string
	Reason string
	At     time.Time
}

const recentDenialsCapacity = 8

// recordDenial appends a RecentDenial to this engine's ring buffer.
// Called from executeToolWithLifecycle after the Approver returns
// Approved=false. RecentDenial.Source stays string for the public log
// surface; we cast at the boundary here.
func (e *Engine) recordDenial(name string, source Source, reason string) {
	if e == nil {
		return
	}
	e.approvalMu.Lock()
	defer e.approvalMu.Unlock()
	e.recentDenials = append(e.recentDenials, RecentDenial{
		Tool:   name,
		Source: string(source),
		Reason: reason,
		At:     time.Now(),
	})
	if len(e.recentDenials) > recentDenialsCapacity {
		// Drop oldest entries so we never grow unbounded.
		e.recentDenials = e.recentDenials[len(e.recentDenials)-recentDenialsCapacity:]
	}
}

// RecentDenials returns a copy of the current denial log, oldest first.
// Nil when nothing has been denied yet. Safe to call from any goroutine.
func (e *Engine) RecentDenials() []RecentDenial {
	if e == nil {
		return nil
	}
	e.approvalMu.RLock()
	defer e.approvalMu.RUnlock()
	if len(e.recentDenials) == 0 {
		return nil
	}
	out := make([]RecentDenial, len(e.recentDenials))
	copy(out, e.recentDenials)
	return out
}

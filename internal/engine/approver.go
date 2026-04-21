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
	"sync"
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

var (
	approverMu sync.RWMutex
	// approverPerEngine keyed by Engine pointer — lets us add SetApprover
	// without changing the Engine struct layout (which has many tests that
	// construct it in assorted ways). The extra indirection is invisible
	// outside this file.
	approverPerEngine = map[*Engine]Approver{}
)

// SetApprover registers (or clears, when approver==nil) the Approver for
// this engine. Safe to call at any point in the engine lifecycle; later
// tool invocations will see the new Approver immediately.
func (e *Engine) SetApprover(approver Approver) {
	if e == nil {
		return
	}
	approverMu.Lock()
	defer approverMu.Unlock()
	if approver == nil {
		delete(approverPerEngine, e)
		return
	}
	approverPerEngine[e] = approver
}

// approver returns the currently-registered Approver or nil.
func (e *Engine) approver() Approver {
	approverMu.RLock()
	defer approverMu.RUnlock()
	return approverPerEngine[e]
}

// requiresApproval answers "is this tool on the approval list?" It's a
// cheap string match against config.Tools.RequireApproval. "*" is the
// gate-everything wildcard. Zero-allocation on the hot path (len() check
// short-circuits the common case of no gate configured).
func (e *Engine) requiresApproval(name string) bool {
	if e == nil || e.Config == nil {
		return false
	}
	list := e.Config.Tools.RequireApproval
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
func (e *Engine) askToolApproval(ctx context.Context, name string, params map[string]any, source string) ApprovalDecision {
	ap := e.approver()
	if ap == nil {
		return ApprovalDecision{Approved: false, Reason: "no approver registered"}
	}
	req := ApprovalRequest{
		Tool:        name,
		Params:      params,
		Source:      source,
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

var (
	denialsMu        sync.RWMutex
	denialsPerEngine = map[*Engine][]RecentDenial{}
)

// recordDenial appends a RecentDenial to this engine's ring buffer.
// Called from executeToolWithLifecycle after the Approver returns
// Approved=false. Out-of-band storage (per-engine map) keeps the Engine
// struct layout stable.
func (e *Engine) recordDenial(name, source, reason string) {
	if e == nil {
		return
	}
	denialsMu.Lock()
	defer denialsMu.Unlock()
	entries := denialsPerEngine[e]
	entries = append(entries, RecentDenial{
		Tool:   name,
		Source: source,
		Reason: reason,
		At:     time.Now(),
	})
	if len(entries) > recentDenialsCapacity {
		// Drop oldest entries so we never grow unbounded.
		entries = entries[len(entries)-recentDenialsCapacity:]
	}
	denialsPerEngine[e] = entries
}

// RecentDenials returns a copy of the current denial log, oldest first.
// Nil when nothing has been denied yet. Safe to call from any goroutine.
func (e *Engine) RecentDenials() []RecentDenial {
	if e == nil {
		return nil
	}
	denialsMu.RLock()
	defer denialsMu.RUnlock()
	entries := denialsPerEngine[e]
	if len(entries) == 0 {
		return nil
	}
	out := make([]RecentDenial, len(entries))
	copy(out, entries)
	return out
}

// cleanupApproverState removes this engine's slot from both global maps
// keyed by *Engine pointer (approverPerEngine, denialsPerEngine). Called
// from Engine.Shutdown so long-running processes (the web server, the
// TUI host, integration test harnesses) that construct/destroy engines
// don't leak map entries forever. Safe to call multiple times — the
// delete on a missing key is a no-op.
//
// Without this hook the maps grow unbounded for the process lifetime
// because *Engine slots are pinned by the map reference even after the
// engine is otherwise unreachable, which also defeats GC of every
// downstream object the engine pointer transitively holds (Approver
// closure, denial entries, etc.). REPORT.md #1.
func (e *Engine) cleanupApproverState() {
	if e == nil {
		return
	}
	approverMu.Lock()
	delete(approverPerEngine, e)
	approverMu.Unlock()

	denialsMu.Lock()
	delete(denialsPerEngine, e)
	denialsMu.Unlock()
}

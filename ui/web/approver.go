// Web-surface adapter for the engine's tool-approval gate.
//
// The web API has no interactive prompt channel — a gated tool call
// fired from /api/v1/chat can't pop a y/n modal. To keep the semantics
// consistent with the CLI, we honour the same DFMC_APPROVE environment
// variable so operators can scope auto-approve/auto-deny per serve
// process. Unset ⇒ auto-deny with a reason that tells the user how to
// opt in. Choosing deny-by-default keeps a publicly-reachable `dfmc
// serve` from silently running destructive tools.

package web

import (
	"context"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// webApprover carries the sticky DFMC_APPROVE decision, resolved once at
// server startup so every call returns the same verdict without re-
// reading the environment. Operators flipping the flag need a restart
// of `dfmc serve`, which is the same cadence as every other config value.
type webApprover struct {
	autoYes bool
	autoNo  bool
}

func newWebApprover() *webApprover {
	env := strings.ToLower(strings.TrimSpace(os.Getenv("DFMC_APPROVE")))
	return &webApprover{
		autoYes: env == "yes" || env == "y" || env == "1" || env == "true",
		autoNo:  env == "no" || env == "n" || env == "0" || env == "false",
	}
}

// RequestApproval implements engine.Approver.
func (a *webApprover) RequestApproval(ctx context.Context, req engine.ApprovalRequest) engine.ApprovalDecision {
	if a.autoYes {
		return engine.ApprovalDecision{Approved: true, Reason: "DFMC_APPROVE=yes"}
	}
	if a.autoNo {
		return engine.ApprovalDecision{Approved: false, Reason: "DFMC_APPROVE=no"}
	}
	return engine.ApprovalDecision{
		Approved: false,
		Reason:   "web surface has no interactive prompt; set DFMC_APPROVE=yes on the serve process to auto-approve, or use the TUI to drive gated tools",
	}
}

package tools

// timeout.go — per-Execute deadline policy.
//
// The Engine wraps every tool dispatch in a context.WithTimeout unless
// the tool is in selfManagedTimeoutTools (run_command's own timer,
// web client's HTTP timeout, recursive sub-agent loops). Exceeding
// the cap surfaces as ToolTimeoutError, which Unwraps to
// context.DeadlineExceeded so transient-error classifiers keep
// working AND carries a self-teaching message naming the cap and
// the config override path the user can raise.

import (
	"context"
	"fmt"
	"time"
)

// selfManagedTimeoutTools is the static set of tools whose Execute owns
// its own deadline — wrapping them with the engine-level cap either
// fights an internal timeout (run_command, web client) or truncates a
// legitimately long inner loop (delegate_task, orchestrate). The outer
// gate falls back on the loop-level MaxToolTokens / MaxToolSteps caps
// for these.
var selfManagedTimeoutTools = map[string]struct{}{
	"run_command":      {},
	"web_fetch":        {},
	"web_search":       {},
	"delegate_task":    {},
	"orchestrate":      {},
	"patch_validation": {},
	"benchmark":        {},
}

// ToolTimeoutError is returned (wrapped via Unwrap to context.DeadlineExceeded)
// when a tool exceeds its per-Execute deadline. The engine wrapper uses
// errors.As to detect this and publish a distinct tool:timeout event,
// while the model still sees the self-teaching Error() message that
// names the cap and the config override path.
type ToolTimeoutError struct {
	Name  string
	Limit time.Duration
}

func (e *ToolTimeoutError) Error() string {
	return fmt.Sprintf("tool %q exceeded the %s execution timeout — narrow the call (e.g. tighter glob, smaller line range) or raise agent.tool_timeouts.%s in config", e.Name, e.Limit, e.Name)
}

// Unwrap exposes the underlying ctx sentinel so generic transient-error
// classifiers (the subagent retry layer in particular) keep working.
func (e *ToolTimeoutError) Unwrap() error { return context.DeadlineExceeded }

// toolTimeout resolves the per-Execute deadline for a given tool. Zero
// means "no engine-level deadline; let the tool or outer ctx decide".
// Lookup order:
//  1. Self-managed list — always 0 (tool owns its deadline).
//  2. cfg.Agent.ToolTimeouts[name] — explicit per-tool override; 0 opts out.
//  3. cfg.Agent.ToolDefaultTimeoutSeconds — fleet-wide default.
func (e *Engine) toolTimeout(name string) time.Duration {
	if _, ok := selfManagedTimeoutTools[name]; ok {
		return 0
	}
	if e.cfg.Agent.ToolTimeouts != nil {
		if s, ok := e.cfg.Agent.ToolTimeouts[name]; ok {
			if s <= 0 {
				return 0
			}
			return time.Duration(s) * time.Second
		}
	}
	if e.cfg.Agent.ToolDefaultTimeoutSeconds > 0 {
		return time.Duration(e.cfg.Agent.ToolDefaultTimeoutSeconds) * time.Second
	}
	return 0
}

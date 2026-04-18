// Tool-invocation methods for the Engine. Extracted from engine.go to
// keep construction/lifecycle there. Groups the public ListTools /
// CallTool entry points with the shared approval + hook + panic-guard
// lifecycle path used by both user-initiated and agent-initiated
// tool calls.

package engine

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/dontfuckmycode/dfmc/internal/hooks"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func (e *Engine) ListTools() []string {
	if e.Tools == nil {
		return nil
	}
	return e.Tools.List()
}

func (e *Engine) CallTool(ctx context.Context, name string, params map[string]any) (tools.Result, error) {
	if err := e.requireReady("tool call"); err != nil {
		return tools.Result{}, err
	}
	res, err := e.executeToolWithLifecycle(ctx, name, params, "user")
	if err != nil {
		e.EventBus.Publish(Event{
			Type:    "tool:error",
			Source:  "engine",
			Payload: err.Error(),
		})
		return res, err
	}
	e.EventBus.Publish(Event{
		Type:   "tool:complete",
		Source: "engine",
		Payload: map[string]any{
			"name":       name,
			"durationMs": res.DurationMs,
		},
	})
	return res, nil
}

// executeToolWithLifecycle is the single point of entry for every tool
// invocation in the engine. It owns:
//   - approval gate (config.Tools.RequireApproval / Approver callback)
//   - pre_tool/post_tool hook dispatch with full payload
//   - raw tools.Engine.Execute call
//
// Both the user-initiated CallTool path and the agent-loop-initiated
// path (agent_loop_native, subagent) funnel through here so hooks and
// approval behave identically regardless of who decided to fire the
// tool.
//
// The `source` tag distinguishes user-initiated calls ("user") from
// agent calls ("agent", "subagent"). The approval gate currently only
// gates agent-initiated calls — user typing /tool is already explicit
// consent.
// executeToolWithPanicGuard converts any panic raised inside a tool's
// Execute into a regular error. Without this guard, a nil-pointer or
// out-of-bounds inside any tool implementation kills the entire DFMC
// process — taking down the agent loop, every connected web/SSE
// client, the TUI session, and every queued reply. Worse, the panic
// happens at an unpredictable point in the agent's reasoning so the
// failure looks like a hang from the user's side.
//
// Tools are first-party but they exec subprocesses, parse arbitrary
// AST shapes, walk filesystems with surprising layouts. The blast
// radius of "one tool bug crashes everything" is large enough to
// justify the cost of a defer/recover wrapper. The agent loop already
// knows how to surface tool errors back to the model (`isError=true`
// tool_result), so the recovered panic is just another error from
// the loop's perspective.
//
// We attach a stack trace to the error so a crash dump in the
// conversation log lets us file a real bug report instead of "the
// thing died."
func (e *Engine) executeToolWithPanicGuard(ctx context.Context, name string, params map[string]any) (res tools.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			err = fmt.Errorf("tool %s panicked: %v\n%s", name, r, truncateStackForError(stack))
			// Reset res so the caller sees an empty Result + the error,
			// not whatever partial state the tool may have populated
			// before panicking.
			res = tools.Result{}
			e.EventBus.Publish(Event{
				Type:   "tool:panicked",
				Source: "engine",
				Payload: map[string]any{
					"name":  name,
					"panic": fmt.Sprintf("%v", r),
				},
			})
		}
	}()
	return e.Tools.Execute(ctx, name, tools.Request{
		ProjectRoot: e.ProjectRoot,
		Params:      params,
	})
}

// truncateStackForError keeps the first ~2 KiB of a stack trace so a
// recovered tool panic doesn't bloat the conversation JSONL with a
// 10 KiB Go runtime dump for every retry. The head frames are the
// useful bit anyway — they point at the panic site.
//
// The constant is named maxStackLen rather than `cap` because `cap`
// shadows the built-in cap() — a later refactor that introduced
// `cap(slice)` anywhere in this function (or any function inheriting
// the const via package scope, since this was at package level) would
// silently bind to the const and either fail to compile or, worse,
// type-check as int and produce wrong values.
func truncateStackForError(stack []byte) string {
	const maxStackLen = 2048
	if len(stack) <= maxStackLen {
		return string(stack)
	}
	return string(stack[:maxStackLen]) + "\n[stack truncated]"
}

func (e *Engine) executeToolWithLifecycle(ctx context.Context, name string, params map[string]any, source string) (tools.Result, error) {
	if e.Tools == nil {
		return tools.Result{}, fmt.Errorf("tool engine is not initialized")
	}
	// Approval gate — only engages for non-user sources and only when
	// the tool is on the approval list. Blocks until the Approver
	// responds or returns an implicit deny on timeout. See approver.go.
	if source != "user" && e.requiresApproval(name) {
		decision := e.askToolApproval(ctx, name, params, source)
		if !decision.Approved {
			reason := decision.Reason
			if reason == "" {
				reason = "user denied"
			}
			e.recordDenial(name, source, reason)
			e.EventBus.Publish(Event{
				Type:   "tool:denied",
				Source: "engine",
				Payload: map[string]any{
					"name":   name,
					"reason": reason,
					"source": source,
				},
			})
			return tools.Result{}, fmt.Errorf("tool %s denied: %s", name, reason)
		}
	}
	if e.Hooks != nil && e.Hooks.Count(hooks.EventPreTool) > 0 {
		e.Hooks.Fire(ctx, hooks.EventPreTool, hooks.Payload{
			"tool_name":    name,
			"tool_source":  source,
			"project_root": e.ProjectRoot,
		})
	}
	res, err := e.executeToolWithPanicGuard(ctx, name, params)
	if e.Hooks != nil && e.Hooks.Count(hooks.EventPostTool) > 0 {
		success := "true"
		if err != nil {
			success = "false"
		}
		e.Hooks.Fire(ctx, hooks.EventPostTool, hooks.Payload{
			"tool_name":        name,
			"tool_source":      source,
			"tool_success":     success,
			"tool_duration_ms": fmt.Sprintf("%d", res.DurationMs),
			"project_root":     e.ProjectRoot,
		})
	}
	return res, err
}

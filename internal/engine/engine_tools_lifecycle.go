package engine

// engine_tools_lifecycle.go — single-funnel tool execution path used by
// every CallTool entry (CLI/TUI user, web/WS/MCP network sources, the
// agent loop, and sub-agents). Owns the approval gate, pre/post hook
// dispatch, and the panic guard. Sibling to engine_tools.go which keeps
// CallTool / CallToolFromSource / Source const constants + cache-aware
// post-call invalidation + skill-policy lookup + path extraction helper.

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/dontfuckmycode/dfmc/internal/hooks"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

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
func (e *Engine) executeToolWithLifecycle(ctx context.Context, name string, params map[string]any, source Source) (tools.Result, error) {
	if e.Tools == nil {
		return tools.Result{}, errors.New("tool engine is not initialized")
	}
	sourceStr := string(source)
	// Sub-agent allowlist gate — fires before approval so unlisted tools
	// are refused without prompting even when the approver is permissive.
	if denial := checkSubagentAllowlist(ctx, name, metaInnerNames(name, params)); denial != "" {
		e.recordDenial(name, source, denial)
		e.EventBus.Publish(Event{
			Type:   "tool:denied",
			Source: "engine",
			Payload: map[string]any{
				"name":   name,
				"reason": denial,
				"source": sourceStr,
			},
		})
		return tools.Result{}, fmt.Errorf("tool %s denied: %s", name, denial)
	}
	// Sub-agent path-scope gate — refuses write tools whose path
	// argument escapes the allow_paths list. Runs after the allowlist
	// gate (so the tool itself is permitted) and before approval (so a
	// scope violation can never reach the approver). Read tools and
	// non-write paths are unaffected; see subagent_path_scope.go for
	// the rationale.
	if denial := checkSubagentPathScope(ctx, name, params); denial != "" {
		e.recordDenial(name, source, denial)
		e.EventBus.Publish(Event{
			Type:   "tool:denied",
			Source: "engine",
			Payload: map[string]any{
				"name":   name,
				"reason": denial,
				"source": sourceStr,
				"scope":  "path",
			},
		})
		return tools.Result{}, fmt.Errorf("tool %s denied: %s", name, denial)
	}
	// Approval gate — only engages for non-user sources and only when
	// the tool is on the approval list. Blocks until the Approver
	// responds or returns an implicit deny on timeout. See approver.go.
	if !source.IsUser() && e.requiresApproval(name, source) {
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
					"source": sourceStr,
				},
			})
			return tools.Result{}, fmt.Errorf("tool %s denied: %s", name, reason)
		}
	}
	// Peek at the params so we can mirror pre/post_tool hook fires onto
	// each inner backend tool name when the outer name is a meta wrapper
	// (tool_call / tool_batch_call). Approval stays at the meta level
	// so a 4-tool batch doesn't fire 4 prompts, but hooks fan out so an
	// operator-configured pre_tool for e.g. run_command still sees the
	// call. See engine_meta_hooks.go for the unwrap logic.
	//
	// metaInnerNames does a small param walk; lazy-compute it so the
	// common "no hooks configured" path doesn't pay the cost. Both
	// branches below need the same slice, so cache once on first use.
	var (
		innerNames    []string
		innerComputed bool
	)
	getInnerNames := func() []string {
		if !innerComputed {
			innerNames = metaInnerNames(name, params)
			innerComputed = true
		}
		return innerNames
	}
	if e.Hooks != nil && e.Hooks.Count(hooks.EventPreTool) > 0 {
		e.Hooks.Fire(ctx, hooks.EventPreTool, hooks.Payload{
			"tool_name":    name,
			"tool_source":  sourceStr,
			"project_root": e.ProjectRoot,
		})
		for _, inner := range getInnerNames() {
			e.Hooks.Fire(ctx, hooks.EventPreTool, hooks.Payload{
				"tool_name":    inner,
				"tool_source":  sourceStr,
				"wrapped_by":   name,
				"project_root": e.ProjectRoot,
			})
		}
	}
	res, err := e.executeToolWithPanicGuard(ctx, name, params)
	if err == nil {
		e.invalidateContextForTool(name, params)
		// Sensitive-path auto-audit: write tools that touch
		// auth/crypto/secret/etc. paths get a quick scanner pass so
		// the user sees a coach note when a write introduces a
		// likely vulnerability or leaked credential. Best-effort,
		// non-blocking — see security_audit.go for the heuristic.
		e.maybeAuditSensitiveWrite(name, params)
	} else {
		// Distinct event when the per-tool deadline killed Execute.
		// Operators care because the gate firing means either (a) the
		// model is making expensive calls that need narrower scope, or
		// (b) the cap is too tight for legitimate work and should be
		// raised.
		//
		// Dedupe contract: a real timeout fires THREE events on this
		// bus — tool:error (model-facing message), tool:timeout
		// (structural fact, this one), and the underlying tool's
		// tool:result with success=false from the executeToolCallsParallel
		// path. Subscribers counting failures must NOT add these
		// together; the canonical signal-of-record is tool:result. Use
		// tool:timeout for "the gate fired" telemetry and tool:error
		// for "the model needs to see why". Both fire within the same
		// handler tick so a (tool_name, ms-window of ~50ms) tuple is
		// a safe dedupe key for downstream metrics aggregators.
		var tte *tools.ToolTimeoutError
		if errors.As(err, &tte) {
			e.EventBus.Publish(Event{
				Type:   "tool:timeout",
				Source: "engine",
				Payload: map[string]any{
					"name":     tte.Name,
					"limit_ms": tte.Limit.Milliseconds(),
					"source":   sourceStr,
				},
			})
		}
	}
	if e.Hooks != nil && e.Hooks.Count(hooks.EventPostTool) > 0 {
		success := "true"
		if err != nil {
			success = "false"
		}
		e.Hooks.Fire(ctx, hooks.EventPostTool, hooks.Payload{
			"tool_name":        name,
			"tool_source":      sourceStr,
			"tool_success":     success,
			"tool_duration_ms": fmt.Sprintf("%d", res.DurationMs),
			"project_root":     e.ProjectRoot,
		})
		// Inner post_tool fires carry the outer meta success. For
		// tool_batch_call this is best-effort: one inner can fail while
		// others succeed and the outer may still report success=true
		// (the batch returns each entry's success separately). Hooks
		// that need per-inner granularity should subscribe to the
		// engine event bus's tool:step events instead.
		for _, inner := range getInnerNames() {
			e.Hooks.Fire(ctx, hooks.EventPostTool, hooks.Payload{
				"tool_name":        inner,
				"tool_source":      sourceStr,
				"tool_success":     success,
				"tool_duration_ms": fmt.Sprintf("%d", res.DurationMs),
				"wrapped_by":       name,
				"project_root":     e.ProjectRoot,
			})
		}
	}
	return res, err
}

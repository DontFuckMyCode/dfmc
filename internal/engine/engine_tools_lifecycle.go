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

// toolEventSeqCtxKey carries a per-tool-call sequence number across
// the lifecycle so every Event emitted for the same call (tool:call,
// tool:result, tool:error, tool:timeout, tool:denied, tool:panicked,
// tool:complete) can be stamped with one shared Seq. Subscribers
// dedupe on (Type, Seq) instead of the previous (tool_name, ~50ms)
// time-window heuristic.
type toolEventSeqCtxKey struct{}

// withToolEventSeq returns ctx annotated with seq. Zero values are
// preserved so the unset state stays detectable downstream.
func withToolEventSeq(ctx context.Context, seq uint64) context.Context {
	return context.WithValue(ctx, toolEventSeqCtxKey{}, seq)
}

// toolEventSeqFromContext returns the seq stamped on ctx, or 0 when
// no allocation happened (test fixtures that build events outside the
// lifecycle, engine-level events that aren't tied to a single tool
// call, etc.). The publisher writes 0 → omitempty drops the field
// from JSON so the wire format stays unchanged for non-tool events.
func toolEventSeqFromContext(ctx context.Context) uint64 {
	if ctx == nil {
		return 0
	}
	v, _ := ctx.Value(toolEventSeqCtxKey{}).(uint64)
	return v
}

// allocToolEventSeq returns the next seq value. Lock-free; safe to
// call from the parallel dispatcher's worker goroutines.
func (e *Engine) allocToolEventSeq() uint64 {
	if e == nil {
		return 0
	}
	return e.toolEventSeq.Add(1)
}

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
				Seq:    toolEventSeqFromContext(ctx),
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
//
// The function reads as a flat phase pipeline; each phase delegates to
// a named helper that owns one policy decision. Ordering matters and
// is documented at the top of this file. Inserting a new gate? Pick a
// phase boundary, not a hand-rolled branch inside an existing helper.
func (e *Engine) executeToolWithLifecycle(ctx context.Context, name string, params map[string]any, source Source) (tools.Result, error) {
	if e.Tools == nil {
		return tools.Result{}, errors.New("tool engine is not initialized")
	}
	seq := toolEventSeqFromContext(ctx)

	// Phase 1 — pre-approval dispatch gates (allowlists + path scope).
	if err := e.checkDispatchGates(ctx, name, params, source, seq); err != nil {
		return tools.Result{}, err
	}
	// Phase 2 — approval gate (non-user sources only).
	if err := e.checkApprovalGate(ctx, name, params, source, seq); err != nil {
		return tools.Result{}, err
	}
	// metaInnerNames does a small param walk; lazy-compute so the
	// common "no hooks configured" path doesn't pay the cost. Both
	// pre and post hook fanout need the same slice, so cache once.
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
	// Phase 3 — pre-tool hooks (outer + inner fanout).
	e.firePreToolHooks(ctx, name, source, getInnerNames)
	// Phase 4 — actual execution under the panic guard.
	res, err := e.executeToolWithPanicGuard(ctx, name, params)
	// Phase 5 — success-side side effects + meta-inner fanout, or
	// failure-side timeout-event surfacing.
	if err == nil {
		e.recordSuccessSideEffects(name, params, res)
	} else {
		e.emitToolTimeoutEvent(err, source, seq)
	}
	// Phase 6 — post-tool hooks (outer + inner fanout).
	e.firePostToolHooks(ctx, name, source, err == nil, res.DurationMs, getInnerNames)
	return res, err
}

// fireDenialEvent records a denial, publishes a tool:denied event with
// the supplied scope (empty → no "scope" field), and returns the
// wrapping error for the caller to surface to the model.
func (e *Engine) fireDenialEvent(name string, source Source, scope, reason string, seq uint64) error {
	e.recordDenial(name, source, reason)
	payload := map[string]any{
		"name":   name,
		"reason": reason,
		"source": string(source),
	}
	if scope != "" {
		payload["scope"] = scope
	}
	e.EventBus.Publish(Event{
		Type:    "tool:denied",
		Source:  "engine",
		Seq:     seq,
		Payload: payload,
	})
	return fmt.Errorf("tool %s denied: %s", name, reason)
}

// checkDispatchGates runs the three pre-approval policy checks: the
// sub-agent tool allowlist, the skill allowlist, and the sub-agent
// path-scope gate. Each gate fires before approval so unlisted tools
// (or out-of-scope paths) are refused without prompting even when the
// approver is permissive.
//
// Skill / subagent allowlists AND-compose — both must permit. Inner
// backend names are pre-walked here so a `tool_batch_call` carrying a
// disallowed inner is refused at the meta level, not at execute time.
func (e *Engine) checkDispatchGates(ctx context.Context, name string, params map[string]any, source Source, seq uint64) error {
	if denial := checkSubagentAllowlist(ctx, name, metaInnerNames(name, params)); denial != "" {
		return e.fireDenialEvent(name, source, "", denial, seq)
	}
	if denial := checkSkillAllowlist(ctx, name, metaInnerNames(name, params)); denial != "" {
		return e.fireDenialEvent(name, source, "skill", denial, seq)
	}
	// Path-scope gate refuses write tools whose path argument escapes
	// the allow_paths list. Runs after the allowlists (so the tool
	// itself is permitted) and before approval (so a scope violation
	// can never reach the approver). Read tools and non-write paths
	// are unaffected; see subagent_path_scope.go for the rationale.
	if denial := checkSubagentPathScope(ctx, name, params); denial != "" {
		return e.fireDenialEvent(name, source, "path", denial, seq)
	}
	return nil
}

// checkApprovalGate prompts the approver for non-user sources when the
// tool is on the approval list. Blocks until the Approver responds or
// times out (implicit deny). User-initiated calls (CallTool from the
// CLI/TUI) bypass this gate — typing /tool is already explicit consent.
func (e *Engine) checkApprovalGate(ctx context.Context, name string, params map[string]any, source Source, seq uint64) error {
	if source.IsUser() || !e.requiresApproval(name, source) {
		return nil
	}
	decision := e.askToolApproval(ctx, name, params, source)
	if decision.Approved {
		return nil
	}
	reason := decision.Reason
	if reason == "" {
		reason = "user denied"
	}
	return e.fireDenialEvent(name, source, "", reason, seq)
}

// firePreToolHooks dispatches the pre_tool hook event for the outer
// tool name and fans out to each inner backend name when the outer is
// a meta wrapper (tool_call / tool_batch_call). Approval stays at the
// meta level so a 4-tool batch doesn't fire 4 prompts, but hooks fan
// out so an operator-configured pre_tool for e.g. run_command still
// sees the call.
func (e *Engine) firePreToolHooks(ctx context.Context, name string, source Source, getInnerNames func() []string) {
	if e.Hooks == nil || e.Hooks.Count(hooks.EventPreTool) == 0 {
		return
	}
	sourceStr := string(source)
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

// firePostToolHooks dispatches the post_tool hook event for the outer
// tool name and fans out to each inner backend name. Inner fires carry
// the outer meta success — for tool_batch_call this is best-effort
// (one inner can fail while others succeed and the outer may still
// report success=true). Hooks needing per-inner granularity should
// subscribe to the engine event bus's tool:step events instead.
func (e *Engine) firePostToolHooks(ctx context.Context, name string, source Source, success bool, durationMs int64, getInnerNames func() []string) {
	if e.Hooks == nil || e.Hooks.Count(hooks.EventPostTool) == 0 {
		return
	}
	successStr := "true"
	if !success {
		successStr = "false"
	}
	sourceStr := string(source)
	durationStr := fmt.Sprintf("%d", durationMs)
	e.Hooks.Fire(ctx, hooks.EventPostTool, hooks.Payload{
		"tool_name":        name,
		"tool_source":      sourceStr,
		"tool_success":     successStr,
		"tool_duration_ms": durationStr,
		"project_root":     e.ProjectRoot,
	})
	for _, inner := range getInnerNames() {
		e.Hooks.Fire(ctx, hooks.EventPostTool, hooks.Payload{
			"tool_name":        inner,
			"tool_source":      sourceStr,
			"tool_success":     successStr,
			"tool_duration_ms": durationStr,
			"wrapped_by":       name,
			"project_root":     e.ProjectRoot,
		})
	}
}

// recordSuccessSideEffects runs the per-tool post-success bookkeeping:
// context cache invalidation, sensitive-write auto-audit, and the
// meta-inner fanout that wires both side effects to each successful
// inner backend call when the outer is a meta wrapper. Allowlist /
// skill / path-scope gates already pre-walk inner names at the top of
// the funnel, so no gate is duplicated here.
func (e *Engine) recordSuccessSideEffects(name string, params map[string]any, res tools.Result) {
	e.invalidateContextForTool(name, params)
	// Sensitive-path auto-audit: write tools that touch
	// auth/crypto/secret/etc. paths get a quick scanner pass so the
	// user sees a coach note when a write introduces a likely
	// vulnerability or leaked credential. Best-effort, non-blocking
	// — see security_audit.go for the heuristic.
	e.maybeAuditSensitiveWrite(name, params)
	for _, c := range metaSuccessfulInnerCalls(name, res, params) {
		e.invalidateContextForTool(c.Name, c.Args)
		e.maybeAuditSensitiveWrite(c.Name, c.Args)
	}
}

// emitToolTimeoutEvent publishes a structural tool:timeout event when
// the per-tool deadline killed Execute. Operators care because the
// gate firing means either (a) the model is making expensive calls
// that need narrower scope, or (b) the cap is too tight for legitimate
// work and should be raised.
//
// Dedupe contract: a real timeout fires THREE events on this bus —
// tool:error (model-facing message), tool:timeout (structural fact,
// this one), and the underlying tool's tool:result with success=false
// from the executeToolCallsParallel path. All three carry the SAME
// Event.Seq stamped at the start of the call so subscribers dedupe
// deterministically on (Type, Seq) tuples instead of the older
// (tool_name, ~50ms) time-window heuristic. The canonical
// signal-of-record is tool:result; tool:timeout is cause-attribution
// telemetry and tool:error is the model-visible payload.
func (e *Engine) emitToolTimeoutEvent(err error, source Source, seq uint64) {
	var tte *tools.ToolTimeoutError
	if !errors.As(err, &tte) {
		return
	}
	if e.AppLog != nil {
		e.AppLog.Warn("tool timeout", map[string]any{"tool": tte.Name, "limit_ms": tte.Limit.Milliseconds()})
	}
	e.EventBus.Publish(Event{
		Type:   "tool:timeout",
		Source: "engine",
		Seq:    seq,
		Payload: map[string]any{
			"name":     tte.Name,
			"limit_ms": tte.Limit.Milliseconds(),
			"source":   string(source),
		},
	})
}

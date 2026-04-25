package engine

// subagent_allowlist.go — runtime enforcement of the
// `delegate_task.allowed_tools` argument.
//
// Pre-fix: the allowed_tools list was rendered into the sub-agent's
// system prompt as a "Preferred tools: ..." hint. Models routinely
// ignored the hint, so a hostile prompt-injection in the sub-agent's
// task could persuade the sub-agent to call any tool the parent
// engine had registered — including run_command, write_file,
// apply_patch — defeating the per-delegation least-privilege model
// the operator was trying to enforce (VULN-035).
//
// Post-fix: the allowlist rides the `context.Context` passed into the
// sub-agent's tool loop. executeToolWithLifecycle reads it and refuses
// any tool not on the list before approval / hooks / Execute fire.
// Per-call context (vs engine-level state) means concurrent
// sub-agents with different allowlists don't trample each other.
//
// The list is **deny-by-default whitelist**: empty list (the default
// when delegate_task is called without the arg) means "no
// restriction" — every tool the engine knows is fair game, just like
// pre-fix. Only when the caller explicitly passes a non-empty list
// does the gate engage.

import (
	"context"
	"strings"
)

type subagentAllowlistKey struct{}

// withSubagentAllowlist attaches a list of permitted tool names to
// the context. The list is normalised to lowercase + duplicates
// removed at call time so the lookup in
// allowedBySubagentList stays cheap.
func withSubagentAllowlist(ctx context.Context, allow []string) context.Context {
	if len(allow) == 0 {
		return ctx
	}
	set := make(map[string]struct{}, len(allow))
	for _, name := range allow {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		set[key] = struct{}{}
	}
	if len(set) == 0 {
		return ctx
	}
	return context.WithValue(ctx, subagentAllowlistKey{}, set)
}

// subagentAllowlistFromContext returns the active set, or nil if the
// caller never attached one (in which case the gate is a no-op and
// every tool is allowed). The bool tells the caller whether
// enforcement is active — even an empty set after normalisation
// would be treated as "no restriction" by the helper above, so the
// returned set is always non-nil when active=true.
func subagentAllowlistFromContext(ctx context.Context) (set map[string]struct{}, active bool) {
	if ctx == nil {
		return nil, false
	}
	v := ctx.Value(subagentAllowlistKey{})
	if v == nil {
		return nil, false
	}
	s, ok := v.(map[string]struct{})
	if !ok || len(s) == 0 {
		return nil, false
	}
	return s, true
}

// checkSubagentAllowlist returns a non-empty error string if `name`
// (or any of its meta-wrapped inner names) is outside the active
// sub-agent allowlist. The empty string means "permit". When no
// allowlist is active the function always permits — the same code
// path serves agent / web / ws / mcp dispatches that aren't bound by
// a delegate_task allow_tools constraint.
//
// Meta tools (tool_call / tool_batch_call) are NOT auto-permitted —
// the backend tools dispatched inside meta.go don't re-enter
// executeToolWithLifecycle, so the gate has to check inner names at
// the outer call site or sub-agents would escape via meta wrapping.
// tool_search / tool_help are pure read-only discovery and always
// permitted.
//
// `innerNames` comes from metaInnerNames at the call site. If any
// single inner name is outside the allowlist the entire dispatch is
// refused — partial-batch execution would leak which inner tools
// existed and is harder to reason about than an all-or-nothing gate.
func checkSubagentAllowlist(ctx context.Context, name string, innerNames []string) string {
	set, active := subagentAllowlistFromContext(ctx)
	if !active {
		return ""
	}
	outer := strings.ToLower(strings.TrimSpace(name))
	switch outer {
	case "tool_search", "tool_help":
		return ""
	}
	candidates := make([]string, 0, 1+len(innerNames))
	if outer != "tool_call" && outer != "tool_batch_call" {
		candidates = append(candidates, outer)
	}
	for _, inner := range innerNames {
		key := strings.ToLower(strings.TrimSpace(inner))
		if key == "" {
			continue
		}
		candidates = append(candidates, key)
	}
	if len(candidates) == 0 {
		// A meta wrapper with zero parsable inner names — the inner
		// dispatch will fail anyway; let the existing meta error
		// surface rather than masking it here.
		return ""
	}
	for _, key := range candidates {
		if _, ok := set[key]; !ok {
			return "tool " + key + " not in sub-agent allowed_tools list"
		}
	}
	return ""
}

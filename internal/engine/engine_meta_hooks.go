// Hook-dispatch support for meta-wrapped tool calls.
//
// The agent loop's dominant dispatch path is tool_call / tool_batch_call,
// both of which reach backend tools through tools.Engine.Execute directly.
// That's correct for the engine's approval gate (one prompt per model
// turn, not one per inner tool — a batch of four would otherwise fire
// four approval prompts back-to-back) but it leaves configured
// pre_tool / post_tool hooks invisible to the operator: a hook bound
// to run_command would silently never fire while the agent is wrapping
// via tool_call.
//
// This file computes the list of inner backend tool names a meta
// wrapper is about to dispatch so executeToolWithLifecycle can fan
// hook payloads out to each inner name alongside the outer meta one.
// Approval stays at the meta boundary (see engine_tools.go).

package engine

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// metaSuccessfulInnerCalls returns the (name, args) pairs that were
// actually dispatched AND succeeded inside a meta wrapper, so the
// engine's success-side side effects (context invalidation, sensitive-
// write audit) can fan out to inner backend tools instead of stopping
// at the outer "tool_call" / "tool_batch_call" name.
//
// Contract per meta tool:
//   - tool_call: outer success ⟺ inner success (the meta tool returns
//     whatever the inner returned). So we just resolve the inner via
//     metaInnerToolCalls.
//   - tool_batch_call: outer success means "the batch dispatcher
//     didn't crash"; individual inner outcomes live in
//     res.Data["results"][i]["success"]. We zip those with the inner
//     calls extracted from params and return only the successful ones.
//
// Returns nil when the outer name is not a meta wrapper, OR when the
// meta result shape isn't recognisable (best-effort: a stale shape
// just means side effects don't fan out, not that they fire wrongly).
func metaSuccessfulInnerCalls(name string, res tools.Result, params map[string]any) []metaInnerToolCall {
	switch strings.TrimSpace(name) {
	case "tool_call":
		// Outer success implies inner success — return the resolved
		// inner call directly. metaInnerToolCalls peels redundant
		// tool_call(tool_call(...)) layers.
		return metaInnerToolCalls(name, params)
	case "tool_batch_call":
		inner := metaInnerToolCalls(name, params)
		if len(inner) == 0 || res.Data == nil {
			return nil
		}
		rawResults, ok := res.Data["results"].([]map[string]any)
		if !ok {
			return nil
		}
		out := make([]metaInnerToolCall, 0, len(inner))
		// Per meta_batch.go the results slice is the same length and
		// in the same order as the parsed calls — but we defensively
		// match on name AND require success=true.
		for i, entry := range rawResults {
			if i >= len(inner) {
				break
			}
			succ, _ := entry["success"].(bool)
			if !succ {
				continue
			}
			nm, _ := entry["name"].(string)
			if nm != "" && nm != inner[i].Name {
				// Shape drift between meta_batch and metaInnerToolCalls
				// — skip rather than fan side effects to a mismatched
				// (name, args) pair.
				continue
			}
			out = append(out, inner[i])
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return nil
}

// metaInnerNames returns the inner backend tool names when `name` is a
// meta wrapper, or nil for regular tools / malformed params. The unwrap
// logic mirrors meta.go's ToolCallTool.Execute: it accepts arg-key
// aliases the normalizer otherwise rewrites, peels redundant
// tool_call(tool_call(...)) layers up to maxMetaUnwrapDepth, and stops
// before descending into another meta tool.
func metaInnerNames(name string, params map[string]any) []string {
	switch strings.TrimSpace(name) {
	case "tool_call":
		if inner := unwrapToolCallName(params, 0); inner != "" {
			return []string{inner}
		}
		return nil
	case "tool_batch_call":
		raw, ok := params["calls"].([]any)
		if !ok {
			return nil
		}
		out := make([]string, 0, len(raw))
		for _, entry := range raw {
			obj, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			n := pickNameFromMetaMap(obj)
			if n == "" || isMetaToolName(n) {
				continue
			}
			out = append(out, n)
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return nil
}

// metaInnerToolCall represents one (name, args) pair extracted from a
// meta wrapper. metaInnerToolCalls returns the unwrapped backend
// calls so checks beyond name-only (path scope, etc.) can reach the
// inner params. Mirrors metaInnerNames' unwrap rules — peels nested
// tool_call layers, drops meta-in-meta entries — but carries args
// alongside name.
type metaInnerToolCall struct {
	Name string
	Args map[string]any
}

func metaInnerToolCalls(name string, params map[string]any) []metaInnerToolCall {
	switch strings.TrimSpace(name) {
	case "tool_call":
		if n, args := unwrapToolCall(params, 0); n != "" {
			return []metaInnerToolCall{{Name: n, Args: args}}
		}
		return nil
	case "tool_batch_call":
		raw, ok := params["calls"].([]any)
		if !ok {
			return nil
		}
		out := make([]metaInnerToolCall, 0, len(raw))
		for _, entry := range raw {
			obj, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			n := pickNameFromMetaMap(obj)
			if n == "" || isMetaToolName(n) {
				continue
			}
			out = append(out, metaInnerToolCall{Name: n, Args: readArgsObject(obj)})
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return nil
}

// unwrapToolCall is the args-aware sibling of unwrapToolCallName: it
// returns both the resolved backend tool name AND its inner args
// object. Recurses through nested tool_call layers up to
// MaxMetaUnwrapDepth.
func unwrapToolCall(params map[string]any, depth int) (string, map[string]any) {
	if depth > tools.MaxMetaUnwrapDepth {
		return "", nil
	}
	n := pickNameFromMetaMap(params)
	if n == "" {
		return "", nil
	}
	if n == "tool_call" {
		inner := readArgsObject(params)
		if inner == nil {
			return "", nil
		}
		return unwrapToolCall(inner, depth+1)
	}
	if isMetaToolName(n) {
		return "", nil
	}
	return n, readArgsObject(params)
}

func unwrapToolCallName(params map[string]any, depth int) string {
	if depth > tools.MaxMetaUnwrapDepth {
		return ""
	}
	n := pickNameFromMetaMap(params)
	if n == "" {
		return ""
	}
	if n == "tool_call" {
		inner := readArgsObject(params)
		if inner == nil {
			return ""
		}
		return unwrapToolCallName(inner, depth+1)
	}
	if isMetaToolName(n) {
		return ""
	}
	return n
}

func readArgsObject(params map[string]any) map[string]any {
	for _, k := range []string{"args", "input", "arguments", "params"} {
		if v, ok := params[k]; ok {
			if m, ok := v.(map[string]any); ok {
				return m
			}
		}
	}
	return nil
}

func pickNameFromMetaMap(m map[string]any) string {
	if s, ok := m["name"].(string); ok {
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	if s, ok := m["tool"].(string); ok {
		if t := strings.TrimSpace(s); t != "" {
			return t
		}
	}
	return ""
}

func isMetaToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case "tool_search", "tool_help", "tool_call", "tool_batch_call":
		return true
	}
	return false
}

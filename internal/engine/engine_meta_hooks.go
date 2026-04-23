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

import "strings"

const maxMetaUnwrapDepth = 4

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

func unwrapToolCallName(params map[string]any, depth int) string {
	if depth > maxMetaUnwrapDepth {
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

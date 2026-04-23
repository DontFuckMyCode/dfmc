package engine

// agent_coach.go — adapter between the engine's native tool-call traces and
// the trajectory analyzer in internal/context. The analyzer is provider-
// agnostic and knows nothing about provider.ToolCall shapes, so this file
// handles the translation and the de-dup window bookkeeping.

import (
	"encoding/json"
	"strings"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
)

// buildTrajectoryHints distills the most recent round of tool activity into
// up to 2 short coaching sentences. `fresh` is the slice added in the round
// that just completed; `all` is the full history including fresh. `recent`
// is the sliding de-dup window so the same hint doesn't fire on every round.
func buildTrajectoryHints(fresh, all []nativeToolTrace, recent []string) *ctxmgr.TrajectoryOutput {
	if len(fresh) == 0 {
		return nil
	}
	entriesFresh := traceEntriesFromNative(fresh)
	entriesAll := traceEntriesFromNative(all)
	return ctxmgr.TrajectoryHints(entriesFresh, entriesAll, recent)
}

func traceEntriesFromNative(traces []nativeToolTrace) []ctxmgr.TraceEntry {
	out := make([]ctxmgr.TraceEntry, 0, len(traces))
	for _, t := range traces {
		entry := ctxmgr.TraceEntry{
			Tool: t.Call.Name,
			Args: t.Call.Input,
			Ok:   t.Err == "",
			Err:  t.Err,
			Step: t.Step,
		}
		// Bridged path: meta-tool "tool_call" wraps the real backend tool
		// name inside input["name"]. Surface that to the analyzer so rules
		// can match the user-facing tool (e.g., "grep_codebase") rather
		// than the meta proxy.
		if t.Call.Name == "tool_call" || t.Call.Name == "tool_batch_call" {
			if inner := extractBridgedInnerName(t.Call.Input); inner != "" {
				entry.Inner = inner
			}
			if innerArgs := extractBridgedInnerArgs(t.Call.Input); innerArgs != nil {
				entry.Args = innerArgs
			}
		}
		output := strings.TrimSpace(t.Result.Output)
		entry.OutputChars = len(output)
		if len(output) > 400 {
			entry.OutputPreview = output[:400]
		} else {
			entry.OutputPreview = output
		}
		out = append(out, entry)
	}
	return out
}

// extractBridgedInnerName pulls the real backend tool name out of a
// tool_call meta-call's input ("name" key). tool_batch_call has a "calls"
// array; we take the first entry's name since repeat-tool rules already
// handle multi-call batching via the broader history.
func extractBridgedInnerName(input map[string]any) string {
	if input == nil {
		return ""
	}
	// Peel redundant tool_call(tool_call(...)) layers the same way the
	// engine's dispatcher does before reaching a real backend tool.
	// Without this the coach saw "tool_call" as the inner name for
	// any double-wrapped call and lost tool/path-specific hints.
	for depth := 0; depth < maxMetaUnwrapDepth; depth++ {
		v, ok := input["name"].(string)
		if !ok {
			break
		}
		name := strings.TrimSpace(v)
		if name != "tool_call" {
			return name
		}
		inner, ok := input["args"].(map[string]any)
		if !ok {
			break
		}
		input = inner
	}
	if calls, ok := input["calls"].([]any); ok && len(calls) > 0 {
		if first, ok := calls[0].(map[string]any); ok {
			if v, ok := first["name"].(string); ok {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

// extractBridgedInnerArgs pulls the inner "args" object out of a tool_call
// meta-call so trajectory rules can inspect the real tool arguments.
// Returns nil when the shape is unexpected (analyzer falls back to outer
// args in that case).
func extractBridgedInnerArgs(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	// Mirror the peel in extractBridgedInnerName so args track the same
	// backend tool the coach labels the entry with - otherwise the hint
	// text would carry outer {name,args} pairs instead of real params.
	for depth := 0; depth < maxMetaUnwrapDepth; depth++ {
		name, _ := input["name"].(string)
		if strings.TrimSpace(name) != "tool_call" {
			break
		}
		inner, ok := input["args"].(map[string]any)
		if !ok {
			break
		}
		input = inner
	}
	if args, ok := input["args"].(map[string]any); ok {
		return args
	}
	// Some providers serialize nested JSON as a string — try to decode once.
	if raw, ok := input["args"].(string); ok {
		m := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &m); err == nil {
			return m
		}
	}
	if calls, ok := input["calls"].([]any); ok && len(calls) > 0 {
		if first, ok := calls[0].(map[string]any); ok {
			if args, ok := first["args"].(map[string]any); ok {
				return args
			}
		}
	}
	return nil
}

// appendRecentHints adds new hints to the de-dup window, bounded to the
// last 8 entries. Older hints roll off — keeps the window small but still
// useful across multi-step loops.
func appendRecentHints(recent, add []string) []string {
	if len(add) == 0 {
		return recent
	}
	out := append([]string{}, recent...)
	for _, h := range add {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		out = append(out, h)
	}
	const windowSize = 8
	if len(out) > windowSize {
		out = out[len(out)-windowSize:]
	}
	return out
}

package engine

// agent_loop_result_batch.go — tool_batch_call-specific result helpers
// extracted from agent_loop_result.go to keep the core file focused on
// the per-call payload formatter + dedupe key. This sibling owns three
// related pieces:
//
//   - slimBatchInnerResults  proportionally caps each inner call's
//                            output/data so a wide batch can't blow the
//                            per-call char budget; returns a shallow-
//                            cloned map so the live trace.Data still
//                            holds the full payload for the TUI event.
//   - batchFanoutSummary     compresses the batch result into the
//                            (batch_count, batch_ok, batch_fail,
//                            batch_inner, batch_parallel) summary the
//                            TUI chip + telemetry both consume.
//   - formatBatchInnerLine   renders one indented sub-list line for the
//                            batch chip — `✓` / `✗` icon, name, target,
//                            optional why-reason, duration, error tail.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// slimBatchInnerResults detects a tool_batch_call-shaped Data map and caps
// each inner call's `output` and `data` to a proportional slice of the outer
// budget. Returns a shallow-cloned map so we don't mutate the live
// tools.Result held by the trace (the TUI still sees the full payload via
// the tool:result event, which uses the original Data). Second return is
// true when slimming actually happened.
func slimBatchInnerResults(data map[string]any, maxOutput, maxData int) (map[string]any, bool) {
	rawResults, ok := data["results"]
	if !ok {
		return data, false
	}
	var results []map[string]any
	switch v := rawResults.(type) {
	case []map[string]any:
		results = v
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				results = append(results, m)
			}
		}
	case string:
		var arr []any
		if err := json.Unmarshal([]byte(v), &arr); err == nil {
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
					results = append(results, m)
				}
			}
		}
	}
	if len(results) == 0 {
		return data, false
	}

	// Proportional budget per inner call with a sane floor (we want the
	// model to get *something* useful from each call even if there are
	// many). 400 chars ≈ 100 tokens — enough for a one-paragraph snippet
	// or a few lines of shell output.
	perOut := maxOutput / len(results)
	if perOut < 400 {
		perOut = 400
	}
	perData := maxData / len(results)
	if perData < 200 {
		perData = 200
	}

	clonedResults := make([]map[string]any, len(results))
	changed := false
	for i, r := range results {
		slot := make(map[string]any, len(r))
		for k, v := range r {
			slot[k] = v
		}
		if s, ok := slot["output"].(string); ok {
			compressed := compressToolResult(s)
			if trimmed := trimToolPayload(compressed, perOut); trimmed != s {
				slot["output"] = trimmed
				changed = true
			}
		}
		if inner, ok := slot["data"].(map[string]any); ok && len(inner) > 0 {
			if raw, err := json.Marshal(inner); err == nil && len(raw) > perData {
				slot["data"] = trimToolPayload(compressToolResult(string(raw)), perData)
				changed = true
			}
		}
		clonedResults[i] = slot
	}
	if !changed {
		return data, false
	}

	out := make(map[string]any, len(data))
	for k, v := range data {
		out[k] = v
	}
	out["results"] = clonedResults
	return out, true
}

func batchFanoutSummary(toolName string, data map[string]any) map[string]any {
	if toolName != "tool_batch_call" || data == nil {
		return nil
	}
	results, _ := data["results"].([]map[string]any)
	if results == nil {
		// fallback: some call paths stringify into []any
		if arr, ok := data["results"].([]any); ok {
			for _, v := range arr {
				if m, ok := v.(map[string]any); ok {
					results = append(results, m)
				}
			}
		}
	}
	if len(results) == 0 {
		return nil
	}
	ok, fail := 0, 0
	// inner is the per-call summary the TUI renders as indented sub-lines
	// under the batch chip. One line per call; cap at a sane number so a
	// 50-call fan-out doesn't explode the chip into a screenful.
	const maxInnerLines = 12
	inner := make([]string, 0, len(results))
	for _, r := range results {
		succ, _ := r["success"].(bool)
		if succ {
			ok++
		} else {
			fail++
		}
		if len(inner) < maxInnerLines {
			inner = append(inner, formatBatchInnerLine(r, succ))
		}
	}
	if extra := len(results) - len(inner); extra > 0 {
		inner = append(inner, fmt.Sprintf("… +%d more", extra))
	}
	out := map[string]any{
		"batch_count": len(results),
		"batch_ok":    ok,
		"batch_fail":  fail,
		"batch_inner": inner,
	}
	if p, ok := data["parallel"].(int); ok && p > 0 {
		out["batch_parallel"] = p
	}
	return out
}

// formatBatchInnerLine renders one inner-call line for the batch chip's
// indented sub-list. Shape:
//
//	"✓ read_file foo.go (5ms)"
//	"✗ run_command go build ./... — exit 1"
//
// Failures get the error tail so the model — and the user reading the
// TUI — can see WHY without expanding the tool panel. Successes stay
// short; the duration_ms gives a coarse perf signal.
func formatBatchInnerLine(r map[string]any, success bool) string {
	icon := "✗"
	if success {
		icon = "✓"
	}
	name, _ := r["name"].(string)
	if name == "" {
		name = "tool"
	}
	target, _ := r["target"].(string)
	durMs := 0
	if d, ok := r["duration_ms"].(int); ok {
		durMs = d
	} else if d, ok := r["duration_ms"].(int64); ok {
		durMs = int(d)
	} else if d, ok := r["duration_ms"].(float64); ok {
		durMs = int(d)
	}

	body := icon + " " + name
	if target != "" {
		body += " " + target
	}
	if reason, _ := r["reason"].(string); strings.TrimSpace(reason) != "" {
		body += " | why: " + strings.Join(strings.Fields(reason), " ")
	}
	if durMs > 0 {
		body += fmt.Sprintf(" (%dms)", durMs)
	}
	if !success {
		if errText, _ := r["error"].(string); errText != "" {
			tail := strings.TrimSpace(errText)
			if i := strings.IndexByte(tail, '\n'); i >= 0 {
				tail = strings.TrimSpace(tail[:i])
			}
			if len(tail) > 80 {
				tail = tail[:77] + "..."
			}
			body += " — " + tail
		}
	}
	return body
}

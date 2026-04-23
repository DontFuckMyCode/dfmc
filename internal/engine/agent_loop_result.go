// agent_loop_result.go — tool-result shaping helpers for the native
// agent loop. Pure functions, no Engine state:
//
//   - formatNativeToolResultPayloadWithLimits: renders a tools.Result
//     (or error) into the string tool_result payload the model sees.
//   - slimBatchInnerResults: proportionally caps each inner call's
//     output/data in a tool_batch_call fan-out so one big batch can't
//     blow the per-call char budget.
//   - findPriorIdenticalToolResult / canonicalToolCallKey: de-dupe
//     repeated tool calls in the history so the model doesn't re-read
//     the same payload from older turns.
//   - batchFanoutSummary / formatBatchInnerLine: fold the batch result
//     into the compact summary the TUI chip and the trace telemetry
//     both consume.
//
// Extracted from agent_loop_native.go to keep the main loop file
// focused on control flow.

package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func findPriorIdenticalToolResult(msgs []provider.Message, current provider.ToolCall, skipID string) int {
	currentKey, ok := canonicalToolCallKey(current)
	if !ok {
		return -1
	}
	// Map ToolCallID → index of its tool_result in msgs so we don't
	// re-scan for each candidate.
	resultIdx := make(map[string]int, len(msgs))
	for i, m := range msgs {
		if m.Role == types.RoleUser && strings.TrimSpace(m.ToolCallID) != "" {
			resultIdx[m.ToolCallID] = i
		}
	}
	// Walk assistant turns (most recent first) so when multiple
	// duplicates exist we stub the NEWEST of the priors — the older
	// ones are usually already stubbed from an earlier pass.
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != types.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID == skipID {
				continue
			}
			key, ok := canonicalToolCallKey(tc)
			if !ok || key != currentKey {
				continue
			}
			idx, found := resultIdx[tc.ID]
			if !found {
				continue
			}
			return idx
		}
	}
	return -1
}

// canonicalToolCallKey builds a stable hash key from a ToolCall's
// name + input. Returns ("", false) for empty calls so the caller
// can skip them.
func canonicalToolCallKey(tc provider.ToolCall) (string, bool) {
	name := strings.TrimSpace(tc.Name)
	if name == "" {
		return "", false
	}
	if tc.Input == nil {
		return name + "|", true
	}
	// json.Marshal on a map uses lexicographic key order, which is
	// exactly what we need for canonicalisation. Errors are ignored
	// — a map with a non-serialisable value just produces "null"
	// and we fall back to name-only comparison.
	raw, err := json.Marshal(tc.Input)
	if err != nil {
		return name + "|", true
	}
	return name + "|" + string(raw), true
}

// formatNativeToolResultPayloadWithLimits turns a tools.Result + error into
// the string payload sent back to the model as tool_result content. Failures
// are signalled with isError=true so the model can pivot rather than retry
// the same call. maxOutput/maxData = 0 falls back to unbounded trim.
func formatNativeToolResultPayloadWithLimits(res tools.Result, toolErr error, maxOutput, maxData int) (string, bool) {
	if toolErr != nil {
		// Critical: when a tool errors but ALSO produced output (the
		// classic case is run_command exiting non-zero — exec.ExitError
		// is wrapped while res.Output holds the captured stdout/stderr
		// with the actual compiler / test failure lines), we must
		// surface that output. Pre-fix the model only saw
		// "ERROR: command exited with code 1" with zero diagnostic
		// context and either retried blindly or apologised. Now the
		// payload pairs the wrapped error with the captured output and
		// any structured Data so the model can reason about WHY the
		// command failed and pivot accordingly.
		header := "ERROR: " + toolErr.Error()
		output := compressToolResult(strings.TrimSpace(res.Output))
		hasData := len(res.Data) > 0
		if output == "" && !hasData {
			return header, true
		}
		body := header
		if output != "" {
			body += "\n\nOUTPUT:\n" + trimToolPayload(output, maxOutput)
		}
		if hasData {
			if raw, err := json.MarshalIndent(res.Data, "", "  "); err == nil {
				body += "\n\nDATA:\n" + trimToolPayload(compressToolResult(string(raw)), maxData)
			}
		}
		if res.Truncated {
			body += "\n\n(output truncated by sandbox)"
		}
		return body, true
	}
	// RTK-style pass: strip ANSI, drop progress/spinner noise, collapse
	// repeated lines. Runs before char-budget trimming so we don't waste
	// budget on decorative bytes the model doesn't need.
	output := compressToolResult(strings.TrimSpace(res.Output))
	data := res.Data
	// For tool_batch_call fan-outs, cap each inner call's output/data
	// proportionally so a 10-file read doesn't eat 10x the budget of a
	// single read. Total model payload stays bounded by maxOutput/maxData
	// regardless of batch size.
	if data != nil {
		if trimmed, didTrim := slimBatchInnerResults(data, maxOutput, maxData); didTrim {
			data = trimmed
		}
	}
	hasData := len(data) > 0
	if output == "" && !hasData {
		return "(no output)", false
	}
	out := trimToolPayload(output, maxOutput)
	if hasData {
		if raw, err := json.MarshalIndent(data, "", "  "); err == nil {
			dataStr := trimToolPayload(compressToolResult(string(raw)), maxData)
			if out == "" {
				out = dataStr
			} else {
				out = out + "\n\nDATA:\n" + dataStr
			}
		}
	}
	if res.Truncated {
		out += "\n\n(output truncated by sandbox)"
	}
	return out, false
}

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

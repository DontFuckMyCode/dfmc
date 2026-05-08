// agent_loop_result.go — tool-result shaping helpers for the native
// agent loop. Pure functions, no Engine state:
//
//   - formatNativeToolResultPayloadWithLimits: renders a tools.Result
//     (or error) into the string tool_result payload the model sees.
//   - findPriorIdenticalToolResult / canonicalToolCallKey: de-dupe
//     repeated tool calls in the history so the model doesn't re-read
//     the same payload from older turns.
//   - truncationStats: per-call drop accounting surfaced on tool:result
//     so the TUI can flip from "compressed" to "truncated".
//
// Extracted from agent_loop_native.go to keep the main loop file
// focused on control flow. Batch-specific helpers (slimBatchInnerResults,
// batchFanoutSummary, formatBatchInnerLine) live in
// agent_loop_result_batch.go.

package engine

import (
	"encoding/json"
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

// truncationStats records what trimToolPayload had to drop from the
// raw post-compression bytes to fit the per-call budget. Surfaced on
// the tool:result event so the TUI can render a distinct "✂ N chars
// dropped" badge rather than burying the loss inside the generic
// compression %.
type truncationStats struct {
	OutputDropped bool
	OutputRunes   int
	DataDropped   bool
	DataRunes     int
	OriginalChars int
}

// HardTruncated reports whether ANY portion of the model-bound
// payload (output or data sidecar) lost bytes due to the per-call
// budget. The TUI uses this to flip the chip badge from "compressed"
// to "truncated" — the user is then warned the model is flying blind
// on part of the result.
func (t truncationStats) HardTruncated() bool { return t.OutputDropped || t.DataDropped }

// formatNativeToolResultPayloadWithLimits turns a tools.Result + error into
// the string payload sent back to the model as tool_result content. Failures
// are signalled with isError=true so the model can pivot rather than retry
// the same call. maxOutput/maxData = 0 falls back to unbounded trim.
func formatNativeToolResultPayloadWithLimits(res tools.Result, toolErr error, maxOutput, maxData int) (string, bool) {
	body, isErr, _ := formatNativeToolResultPayloadDetailed(res, toolErr, maxOutput, maxData)
	return body, isErr
}

// formatNativeToolResultPayloadDetailed is the audit-friendly variant
// that also returns truncation stats for the TUI's benefit. Existing
// call sites that don't care about the stats stay on the legacy
// 2-return signature above; the agent loop's publish path uses this
// detailed form so the user sees when the model got cut off.
func formatNativeToolResultPayloadDetailed(res tools.Result, toolErr error, maxOutput, maxData int) (string, bool, truncationStats) {
	stats := truncationStats{OriginalChars: len(res.Output)}
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
			return header, true, stats
		}
		body := header
		if output != "" {
			trimmed, dropped, dropRunes := trimToolPayloadDetail(output, maxOutput)
			body += "\n\nOUTPUT:\n" + trimmed
			stats.OutputDropped = dropped
			stats.OutputRunes = dropRunes
		}
		if hasData {
			if raw, err := json.MarshalIndent(res.Data, "", "  "); err == nil {
				trimmed, dropped, dropRunes := trimToolPayloadDetail(compressToolResult(string(raw)), maxData)
				body += "\n\nDATA:\n" + trimmed
				stats.DataDropped = dropped
				stats.DataRunes = dropRunes
			}
		}
		if res.Truncated {
			body += "\n\n(output truncated by sandbox)"
		}
		return body, true, stats
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
		return "(no output)", false, stats
	}
	out, outDropped, outDropRunes := trimToolPayloadDetail(output, maxOutput)
	stats.OutputDropped = outDropped
	stats.OutputRunes = outDropRunes
	if hasData {
		if raw, err := json.MarshalIndent(data, "", "  "); err == nil {
			dataStr, dataDrop, dataDropRunes := trimToolPayloadDetail(compressToolResult(string(raw)), maxData)
			stats.DataDropped = dataDrop
			stats.DataRunes = dataDropRunes
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
	return out, false, stats
}


package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// TestSlimBatchInnerResultsTrimsEachCallProportionally covers Fix B: when
// tool_batch_call hands the native loop a Data blob whose inner calls carry
// full file contents, the model-facing payload must cap each inner call's
// `output` and `data` at a proportional slice of the outer budget. Without
// this, a batch of 10 large reads inflates the next provider round by 10x
// what a single read would cost.
func TestSlimBatchInnerResultsTrimsEachCallProportionally(t *testing.T) {
	bigOutput := strings.Repeat("line of output content ", 400) // ~9200 chars
	data := map[string]any{
		"count":    2,
		"parallel": 2,
		"results": []map[string]any{
			{"name": "read_file", "success": true, "duration_ms": 5, "output": bigOutput},
			{"name": "read_file", "success": true, "duration_ms": 7, "output": bigOutput},
		},
	}

	maxOutput := 2000
	maxData := 1000

	trimmed, changed := slimBatchInnerResults(data, maxOutput, maxData)
	if !changed {
		t.Fatalf("expected slimming to fire on oversized batch payload")
	}

	results, ok := trimmed["results"].([]map[string]any)
	if !ok || len(results) != 2 {
		t.Fatalf("expected 2 inner results in trimmed payload, got %T / %v", trimmed["results"], trimmed["results"])
	}
	for i, r := range results {
		out, _ := r["output"].(string)
		perOut := maxOutput / len(results)
		if len([]rune(out)) > perOut+10 {
			t.Fatalf("inner result %d output not trimmed: len=%d, cap≈%d", i, len(out), perOut)
		}
		if r["name"] != "read_file" {
			t.Fatalf("inner result %d lost name field: %#v", i, r)
		}
		if r["success"] != true {
			t.Fatalf("inner result %d lost success field: %#v", i, r)
		}
	}

	// The original data map must not be mutated — TUI still reads it via
	// the tool:result event.
	origResults := data["results"].([]map[string]any)
	if len(origResults[0]["output"].(string)) != len(bigOutput) {
		t.Fatalf("original Data was mutated; TUI would lose full payload")
	}
}

// TestSlimBatchInnerResultsBelowBudgetIsNoOp: when inner calls already fit
// within their proportional budget, slimming must not rewrite anything —
// rewriting would cost allocations without saving tokens.
func TestSlimBatchInnerResultsBelowBudgetIsNoOp(t *testing.T) {
	data := map[string]any{
		"count": 2,
		"results": []map[string]any{
			{"name": "read_file", "success": true, "output": "small"},
			{"name": "read_file", "success": true, "output": "also small"},
		},
	}
	_, changed := slimBatchInnerResults(data, 10_000, 10_000)
	if changed {
		t.Fatal("slim must be a no-op when inner payloads already fit")
	}
}

// TestFormatNativeToolResultTrimsBatchInnerOutputs end-to-ends Fix B through
// the actual payload formatter the agent loop uses. The serialized payload
// sent to the model must be dramatically smaller than the raw Data blob
// when a batch returns several large inner outputs.
func TestFormatNativeToolResultTrimsBatchInnerOutputs(t *testing.T) {
	bigOutput := strings.Repeat("payload_chunk ", 600) // ~8400 chars
	res := tools.Result{
		Output: "#1 read_file: OK (5ms)\n#2 read_file: OK (7ms)",
		Data: map[string]any{
			"count":    2,
			"parallel": 2,
			"results": []map[string]any{
				{"name": "read_file", "success": true, "duration_ms": 5, "output": bigOutput},
				{"name": "read_file", "success": true, "duration_ms": 7, "output": bigOutput},
			},
		},
	}

	rawData, _ := json.Marshal(res.Data)
	rawSize := len(rawData)

	payload, isErr := formatNativeToolResultPayloadWithLimits(res, nil, 3000, 1500)
	if isErr {
		t.Fatalf("unexpected error flag on clean batch result")
	}
	if len(payload) >= rawSize {
		t.Fatalf("expected payload smaller than raw Data (%d bytes), got %d", rawSize, len(payload))
	}
	// Summary-line output must still be present.
	if !strings.Contains(payload, "#1 read_file: OK") {
		t.Fatalf("batch summary lines dropped from payload:\n%s", payload)
	}
	// Inner payload should still have *some* trace so the model can use it.
	if !strings.Contains(payload, "payload_chunk") {
		t.Fatalf("inner output contents missing entirely; model cannot use batch result:\n%s", payload)
	}
}

// Regression: a tool that errors AND captured output (the canonical
// case is `run_command` exiting non-zero — exec.ExitError is wrapped
// while res.Output holds the actual compiler/test failure lines) must
// surface BOTH to the model. Pre-fix the formatter only emitted
// "ERROR: command exited with code 1" and dropped the output, so the
// model had zero context to diagnose the failure and would either
// retry blindly or apologise. Post-fix the payload pairs the wrapped
// error header with the OUTPUT and DATA blocks the model needs.
func TestFormatNativeToolResultPayload_ErrorWithOutputSurfacesBoth(t *testing.T) {
	res := tools.Result{
		Output: "./foo.go:12:5: undefined: SomeMissingSymbol\n./foo.go:18:9: too many arguments in call to bar",
		Data: map[string]any{
			"command":   "go",
			"args":      []string{"build", "./..."},
			"exit_code": 1,
		},
	}
	exitErr := errors.New("command exited with code 1")

	payload, isErr := formatNativeToolResultPayloadWithLimits(res, exitErr, 4000, 1500)
	if !isErr {
		t.Fatal("error path must report isErr=true so the model can pivot")
	}
	if !strings.Contains(payload, "ERROR: command exited with code 1") {
		t.Fatalf("error header missing:\n%s", payload)
	}
	if !strings.Contains(payload, "OUTPUT:") {
		t.Fatalf("OUTPUT block missing — model can't see WHY the command failed:\n%s", payload)
	}
	if !strings.Contains(payload, "undefined: SomeMissingSymbol") {
		t.Fatalf("captured stderr lines must reach the model verbatim:\n%s", payload)
	}
	if !strings.Contains(payload, "DATA:") {
		t.Fatalf("DATA block missing — exit_code, command, args context lost:\n%s", payload)
	}
	if !strings.Contains(payload, `"exit_code": 1`) {
		t.Fatalf("exit_code must remain visible in DATA:\n%s", payload)
	}
}

// Real-world UX gap (TUI 2026-04-18): the chat showed
// "tool_batch_call · step 1 · 5 calls · 5 ok" — opaque about WHAT each
// call did. batchFanoutSummary now emits a `batch_inner` []string with
// one rendered line per inner call so the TUI can hang the per-call
// breakdown under the chip head. Failures get the error tail trimmed
// to fit; over-long fan-outs get a "+N more" sentinel instead of
// flooding the chip.
func TestBatchFanoutSummary_EmitsPerCallInnerLines(t *testing.T) {
	data := map[string]any{
		"results": []map[string]any{
			{"name": "read_file", "target": "foo.go", "success": true, "duration_ms": 5},
			{"name": "read_file", "target": "bar.go", "success": true, "duration_ms": 7},
			{"name": "grep_codebase", "target": "TODO", "success": false, "duration_ms": 3, "error": "pattern not found in any file"},
		},
		"parallel": 4,
	}
	out := batchFanoutSummary("tool_batch_call", data)
	if out == nil {
		t.Fatal("expected a summary map for tool_batch_call, got nil")
	}
	inner, _ := out["batch_inner"].([]string)
	if len(inner) != 3 {
		t.Fatalf("expected 3 inner lines, got %d: %v", len(inner), inner)
	}
	if !strings.Contains(inner[0], "✓") || !strings.Contains(inner[0], "read_file") || !strings.Contains(inner[0], "foo.go") || !strings.Contains(inner[0], "5ms") {
		t.Fatalf("first inner line missing fields: %q", inner[0])
	}
	if !strings.Contains(inner[2], "✗") || !strings.Contains(inner[2], "grep_codebase") || !strings.Contains(inner[2], "pattern not found") {
		t.Fatalf("failed-call inner line should surface the error tail: %q", inner[2])
	}
	// Counts still must agree with inner.
	if got, _ := out["batch_count"].(int); got != 3 {
		t.Fatalf("batch_count want 3, got %d", got)
	}
	if got, _ := out["batch_ok"].(int); got != 2 {
		t.Fatalf("batch_ok want 2, got %d", got)
	}
	if got, _ := out["batch_fail"].(int); got != 1 {
		t.Fatalf("batch_fail want 1, got %d", got)
	}
}

// Long fan-outs cap inner lines and append a "+N more" sentinel so
// the chip can't grow into a screenful.
func TestBatchFanoutSummary_LargeFanoutGetsCappedWithMoreSentinel(t *testing.T) {
	results := make([]map[string]any, 30)
	for i := range results {
		results[i] = map[string]any{
			"name":    "read_file",
			"target":  fmt.Sprintf("file_%02d.go", i),
			"success": true,
		}
	}
	out := batchFanoutSummary("tool_batch_call", map[string]any{"results": results})
	inner, _ := out["batch_inner"].([]string)
	if len(inner) > 13 { // 12 lines + "+N more" sentinel
		t.Fatalf("inner lines should cap around 12 + sentinel, got %d", len(inner))
	}
	last := inner[len(inner)-1]
	if !strings.Contains(last, "more") {
		t.Fatalf("expected '+N more' sentinel as last line, got %q", last)
	}
}

// Inverse: when there's NO output and NO data, the error-only payload
// is fine — we just emit the header. Belt-and-braces guard so the
// "(no output)" or zero-byte body case stays compact.
func TestFormatNativeToolResultPayload_ErrorNoOutputStaysHeaderOnly(t *testing.T) {
	res := tools.Result{} // no Output, no Data
	exitErr := errors.New("permission denied")

	payload, isErr := formatNativeToolResultPayloadWithLimits(res, exitErr, 4000, 1500)
	if !isErr {
		t.Fatal("isErr must be true on the error path")
	}
	if strings.Contains(payload, "OUTPUT:") || strings.Contains(payload, "DATA:") {
		t.Fatalf("nothing to surface beyond the header, but payload has sections:\n%s", payload)
	}
	if payload != "ERROR: permission denied" {
		t.Fatalf("expected bare header, got %q", payload)
	}
}

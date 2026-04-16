package engine

import (
	"encoding/json"
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

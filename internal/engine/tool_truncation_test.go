package engine

// tool_truncation_test.go — pins the contract that hard truncation
// of a tool result is observable to the TUI (not silently swallowed).
//
// Why this exists: a 50KB tool output gets cut to ~3.2KB before the
// model sees it — that's a known design choice (token budget), but
// the user complained "ben ne dönüyor görmeliyim" (I should see
// what's coming back). The fix routes hard truncation through the
// formatter's stats return value so the publish path can stamp
// hard_truncated=true on the tool:result event and the TUI chip
// can render a distinct "✂ N chars cut" badge.

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func TestFormatNativeToolResult_NoTruncation(t *testing.T) {
	res := tools.Result{Output: "small output that fits"}
	body, isErr, stats := formatNativeToolResultPayloadDetailed(res, nil, 200, 200)
	if isErr {
		t.Errorf("unexpected error flag")
	}
	if !strings.Contains(body, "small output") {
		t.Errorf("body missing input: %q", body)
	}
	if stats.HardTruncated() {
		t.Errorf("small output should not be hard-truncated")
	}
}

func TestFormatNativeToolResult_HardTruncatesLongOutput(t *testing.T) {
	huge := strings.Repeat("X", 5000)
	res := tools.Result{Output: huge}
	body, _, stats := formatNativeToolResultPayloadDetailed(res, nil, 500, 200)
	if !stats.HardTruncated() {
		t.Errorf("5KB output with 500-rune cap must trip HardTruncated")
	}
	if !stats.OutputDropped {
		t.Errorf("OutputDropped must be true for output overflow")
	}
	if stats.OutputRunes < 4000 {
		t.Errorf("expected ~4500 runes dropped, got %d", stats.OutputRunes)
	}
	if !strings.Contains(body, "[truncated]") {
		t.Errorf("model-bound body should carry truncation marker: %q", body[:min(200, len(body))])
	}
}

func TestFormatNativeToolResult_HardTruncatesErrorOutput(t *testing.T) {
	// Tool error path also has to flag truncation — run_command
	// failures with multi-MB stderr are exactly the kind of thing the
	// user wants to know got cut off. Build distinct lines so the
	// compressor's "repeated line collapse" doesn't shrink the input
	// below the cap before truncation gets a chance to fire.
	var b strings.Builder
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&b, "compile error %d at line %d: undefined symbol foo_%d\n", i, i*7, i)
	}
	huge := b.String()
	res := tools.Result{Output: huge}
	body, isErr, stats := formatNativeToolResultPayloadDetailed(res, errors.New("exit 1"), 1000, 200)
	if !isErr {
		t.Errorf("error path lost its isError flag")
	}
	if !stats.HardTruncated() {
		t.Errorf("error path failed to flag hard truncation (input %d chars, cap 1000)", len(huge))
	}
	if !strings.Contains(body, "ERROR: exit 1") {
		t.Errorf("error header missing: %q", body[:min(120, len(body))])
	}
}

func TestFormatNativeToolResult_DataAlsoTruncates(t *testing.T) {
	// Big DATA sidecar with small Output — Data truncation must
	// flag HardTruncated too, so the chip warns even when output
	// itself is small.
	huge := strings.Repeat("k", 5000)
	res := tools.Result{
		Output: "ok",
		Data: map[string]any{
			"summary": huge,
		},
	}
	_, _, stats := formatNativeToolResultPayloadDetailed(res, nil, 1000, 500)
	if !stats.DataDropped {
		t.Errorf("DataDropped must trip when DATA exceeds maxData")
	}
	if !stats.HardTruncated() {
		t.Errorf("HardTruncated must flag when DataDropped")
	}
}

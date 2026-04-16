package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func TestCompressToolResult_PreservesPlainOutput(t *testing.T) {
	in := "hello\nworld\n"
	got := compressToolResult(in)
	if got != "hello\nworld" {
		t.Fatalf("plain output should pass through (minus trailing blank), got %q", got)
	}
}

func TestCompressToolResult_StripsANSIEscapes(t *testing.T) {
	// Red "error" label from a colored build tool.
	in := "\x1b[31merror\x1b[0m: type mismatch at line 42"
	got := compressToolResult(in)
	if strings.Contains(got, "\x1b") {
		t.Fatalf("ANSI bytes survived compression: %q", got)
	}
	if !strings.Contains(got, "type mismatch at line 42") {
		t.Fatalf("semantic content lost: %q", got)
	}
}

func TestCompressToolResult_DropsGitPushBoilerplate(t *testing.T) {
	in := strings.Join([]string{
		"Enumerating objects: 5, done.",
		"Counting objects: 100% (5/5), done.",
		"Delta compression using up to 8 threads",
		"Writing objects: 100% (3/3), done.",
		"Total 3 (delta 1), reused 0 (delta 0)",
		"To github.com:user/repo.git",
		"   abc1234..def5678  feature -> feature",
	}, "\n")
	got := compressToolResult(in)
	// Expect the only surviving lines to be the remote URL and ref update.
	if strings.Contains(got, "Enumerating") || strings.Contains(got, "Writing objects") {
		t.Fatalf("boilerplate survived: %q", got)
	}
	if !strings.Contains(got, "abc1234..def5678") || !strings.Contains(got, "github.com:user/repo.git") {
		t.Fatalf("signal lines were dropped: %q", got)
	}
}

func TestCompressToolResult_CollapsesRepeatedLines(t *testing.T) {
	in := strings.Join([]string{
		"warning: unused import",
		"warning: unused import",
		"warning: unused import",
		"warning: unused import",
		"error: undefined symbol",
	}, "\n")
	got := compressToolResult(in)
	if !strings.Contains(got, "warning: unused import (×4)") {
		t.Fatalf("expected repeat-count collapse, got %q", got)
	}
	if !strings.Contains(got, "error: undefined symbol") {
		t.Fatalf("trailing error line was dropped: %q", got)
	}
}

func TestCompressToolResult_KeepsSmallRepeats(t *testing.T) {
	// A pair of identical warnings may still be meaningful — don't collapse.
	in := "warning: foo\nwarning: foo\nerror: bar"
	got := compressToolResult(in)
	if !strings.Contains(got, "warning: foo\nwarning: foo") {
		t.Fatalf("pairs should pass through untouched, got %q", got)
	}
	if strings.Contains(got, "(×2)") {
		t.Fatalf("count suffix should not fire at threshold 2, got %q", got)
	}
}

func TestCompressToolResult_StripsCarriageReturnSpinners(t *testing.T) {
	// A typical "download progress" sequence that survives when stdout is
	// piped without TERM=dumb: each frame is separated by \r.
	in := "downloading...\rdownloading...\rdownloading...\rdone\n"
	got := compressToolResult(in)
	if strings.Count(got, "downloading") > 1 {
		t.Fatalf("spinner frames should collapse: %q", got)
	}
	if !strings.Contains(got, "done") {
		t.Fatalf("final status was dropped: %q", got)
	}
}

func TestCompressToolResult_CollapsesBlankLineRuns(t *testing.T) {
	in := "header\n\n\n\n\nbody\n"
	got := compressToolResult(in)
	// One blank separator max.
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("blank runs survived: %q", got)
	}
	if !strings.Contains(got, "header\n\nbody") {
		t.Fatalf("expected single blank separator, got %q", got)
	}
}

func TestCompressToolResult_Idempotent(t *testing.T) {
	in := "\x1b[32mok\x1b[0m\nprog\nprog\nprog\ndone"
	first := compressToolResult(in)
	second := compressToolResult(first)
	if first != second {
		t.Fatalf("compression not idempotent:\nfirst:  %q\nsecond: %q", first, second)
	}
}

// TestPublishNativeToolResult_IncludesCompressionStats verifies that the
// tool:result event carries RTK-style savings (raw size, payload size,
// saved bytes, ratio) so the TUI stats panel can show compression wins.
func TestPublishNativeToolResult_IncludesCompressionStats(t *testing.T) {
	eng := &Engine{EventBus: NewEventBus()}
	ch := eng.EventBus.Subscribe("tool:result")
	defer eng.EventBus.Unsubscribe("tool:result", ch)

	raw := strings.Repeat("a", 100)     // pretend-output: 100 chars
	payload := strings.Repeat("b", 40)  // model-bound payload: 40 chars
	trace := nativeToolTrace{
		Call:       provider.ToolCall{ID: "c1", Name: "read_file"},
		Result:     tools.Result{Output: raw},
		Provider:   "stub",
		Model:      "stub-model",
		Step:       1,
		OccurredAt: time.Now(),
	}
	eng.publishNativeToolResultWithPayload(trace, payload)

	select {
	case ev := <-ch:
		p, _ := ev.Payload.(map[string]any)
		if got, _ := p["output_chars"].(int); got != 100 {
			t.Fatalf("output_chars should mirror raw length, got %v", p["output_chars"])
		}
		if got, _ := p["payload_chars"].(int); got != 40 {
			t.Fatalf("payload_chars should mirror modelPayload length, got %v", p["payload_chars"])
		}
		if got, _ := p["compression_saved_chars"].(int); got != 60 {
			t.Fatalf("expected 60 chars saved, got %v", p["compression_saved_chars"])
		}
		ratio, _ := p["compression_ratio"].(float64)
		if ratio < 0.39 || ratio > 0.41 {
			t.Fatalf("expected compression_ratio ≈ 0.40, got %v", ratio)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("tool:result event was not published in time")
	}
}

// TestPublishNativeToolResult_SkipsStatsWhenPayloadMissing guards the legacy
// path: callers that don't know the model payload length (empty string) get
// the plain event shape, no compression fields, no zero-division artifacts.
func TestPublishNativeToolResult_SkipsStatsWhenPayloadMissing(t *testing.T) {
	eng := &Engine{EventBus: NewEventBus()}
	ch := eng.EventBus.Subscribe("tool:result")
	defer eng.EventBus.Unsubscribe("tool:result", ch)

	trace := nativeToolTrace{
		Call:   provider.ToolCall{ID: "c2", Name: "read_file"},
		Result: tools.Result{Output: "hi"},
	}
	eng.publishNativeToolResultWithPayload(trace, "")

	select {
	case ev := <-ch:
		p, _ := ev.Payload.(map[string]any)
		if _, ok := p["payload_chars"]; ok {
			t.Fatalf("payload_chars should be absent when no payload passed, got %+v", p)
		}
		if _, ok := p["compression_ratio"]; ok {
			t.Fatalf("compression_ratio should be absent without payload, got %+v", p)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("tool:result event was not published in time")
	}
}

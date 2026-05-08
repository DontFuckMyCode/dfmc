package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// TestFormatProbeDurationTagsSlowProbes — Phase I item 1: durations
// under 1500ms render as the bare unit; anything slower picks up
// `(slow)` so the user sees the cost without parsing milliseconds.
// The threshold is small-enough-to-feel-instant but big-enough to not
// flag normal cold starts.
func TestFormatProbeDurationTagsSlowProbes(t *testing.T) {
	cases := []struct {
		ms      int
		want    string
		message string
	}{
		{0, "0ms", "zero -> zero"},
		{42, "42ms", "sub-second renders as ms"},
		{900, "900ms", "still fast at 900ms"},
		{1499, "1.4s", "just under threshold"},
		{1500, "1.5s (slow)", "threshold tags slow"},
		{3000, "3s (slow)", "round seconds skip the decimal"},
	}
	for _, c := range cases {
		if got := formatProbeDuration(c.ms); got != c.want {
			t.Errorf("%s: formatProbeDuration(%d) = %q; want %q", c.message, c.ms, got, c.want)
		}
	}
}

// TestProviderProbeChipReflectsState — chip shows the current state of
// each row's probe: empty before any probe, "probing…" while in flight,
// "probe ok …" on success, "probe failed" on error.
func TestProviderProbeChipReflectsState(t *testing.T) {
	m := Model{}
	m.diagnosticPanelsState = newDiagnosticPanelsState()

	if chip := m.providerProbeChip("anthropic"); chip != "" {
		t.Fatalf("no probe yet should yield empty chip, got %q", chip)
	}

	m.providers.probing = map[string]bool{"anthropic": true}
	if chip := stripANSI(m.providerProbeChip("anthropic")); !strings.Contains(chip, "probing") {
		t.Fatalf("in-flight probe should render probing chip, got %q", chip)
	}

	m.providers.probing = map[string]bool{}
	m.providers.probeResults = map[string]engine.ProviderProbeResult{
		"anthropic": {Provider: "anthropic", OK: true, DurationMs: 240, At: time.Now()},
	}
	chip := stripANSI(m.providerProbeChip("anthropic"))
	if !strings.Contains(chip, "probe ok") || !strings.Contains(chip, "240ms") {
		t.Fatalf("ok probe chip should show duration, got %q", chip)
	}

	m.providers.probeResults = map[string]engine.ProviderProbeResult{
		"anthropic": {Provider: "anthropic", OK: false, Err: "401 unauthorized", At: time.Now()},
	}
	chip = stripANSI(m.providerProbeChip("anthropic"))
	if !strings.Contains(chip, "probe failed") {
		t.Fatalf("failed probe chip should advertise failure, got %q", chip)
	}
}

// TestStartProviderProbeRejectsEmptyName — guard against a stray T
// keystroke when the panel has no rows. The handler returns a guidance
// notice rather than dispatching a probe against the empty string.
func TestStartProviderProbeRejectsEmptyName(t *testing.T) {
	m := Model{}
	m.diagnosticPanelsState = newDiagnosticPanelsState()
	out, cmd := m.startProviderProbe("")
	if cmd != nil {
		t.Fatalf("empty name should not dispatch a probe, got cmd=%#v", cmd)
	}
	if !strings.Contains(out.notice, "Select a provider row") {
		t.Fatalf("expected guidance notice, got %q", out.notice)
	}
}

// TestStartProviderProbeRejectsNilEngine — engine-not-ready surface:
// the user sees a per-name notice ("cannot probe X yet") rather than
// silent no-op or panic. Mirrors the same pattern the other engine-
// dependent handlers use.
func TestStartProviderProbeRejectsNilEngine(t *testing.T) {
	m := Model{}
	m.diagnosticPanelsState = newDiagnosticPanelsState()
	out, cmd := m.startProviderProbe("anthropic")
	if cmd != nil {
		t.Fatalf("nil engine should not dispatch a probe, got cmd=%#v", cmd)
	}
	if !strings.Contains(out.notice, "cannot probe anthropic") {
		t.Fatalf("expected per-name notice, got %q", out.notice)
	}
}

// TestRecordProviderUsageBoundsRing — Phase I item 2: per-provider
// history is capped at providerUsageHistoryCap so steady-state memory
// stays flat across long sessions. After cap+5 events, only the most
// recent cap entries should remain (oldest evicted from the front).
func TestRecordProviderUsageBoundsRing(t *testing.T) {
	m := Model{}
	m.diagnosticPanelsState = newDiagnosticPanelsState()
	for i := 0; i < providerUsageHistoryCap+5; i++ {
		m = m.recordProviderUsage(providerUsageEntry{
			At: time.Now().Add(time.Duration(i) * time.Second), Provider: "anthropic",
			Model: "claude-opus-4-7", InputTokens: 100, OutputTokens: 50, TotalTokens: 150,
		})
	}
	hist := m.providers.usageHistory["anthropic"]
	if len(hist) != providerUsageHistoryCap {
		t.Fatalf("expected ring capped at %d, got %d", providerUsageHistoryCap, len(hist))
	}
	// Oldest 5 should have been evicted; newest stays at the end.
	if hist[len(hist)-1].At.Before(hist[0].At) {
		t.Fatalf("ring should preserve newest-at-end ordering")
	}
}

// TestProviderUsageStripFormatsNewestFirst — the strip renders the
// most-recent completions first, includes input/output/total breakdown
// when available, and falls back to the model fallback on an empty
// model string. Empty when no history exists for the provider.
func TestProviderUsageStripFormatsNewestFirst(t *testing.T) {
	m := Model{}
	m.diagnosticPanelsState = newDiagnosticPanelsState()
	if got := m.providerUsageStrip("anthropic", 5); got != nil {
		t.Fatalf("no-history provider should yield nil strip, got %#v", got)
	}

	now := time.Now()
	m = m.recordProviderUsage(providerUsageEntry{
		At: now.Add(-2 * time.Minute), Provider: "anthropic",
		Model: "claude-opus-4-7", InputTokens: 100, OutputTokens: 50, TotalTokens: 150,
	})
	m = m.recordProviderUsage(providerUsageEntry{
		At: now.Add(-30 * time.Second), Provider: "anthropic",
		Model: "claude-opus-4-7", InputTokens: 200, OutputTokens: 80, TotalTokens: 280,
	})
	out := m.providerUsageStrip("anthropic", 5)
	if len(out) != 2 {
		t.Fatalf("expected 2 strip lines, got %d (%#v)", len(out), out)
	}
	// Newest first — the 30s-ago entry leads.
	if !strings.Contains(out[0], "in 200") || !strings.Contains(out[0], "out 80") || !strings.Contains(out[0], "total 280") {
		t.Fatalf("first line should hold newest entry breakdown, got %q", out[0])
	}
	if !strings.Contains(out[1], "in 100") {
		t.Fatalf("second line should hold older entry, got %q", out[1])
	}
}

// TestRecordProviderUsageIgnoresEmptyName — defensive: a malformed
// provider:complete event without a provider name shouldn't bucket
// entries under the empty key (would clutter the strip with cross-
// provider entries on lookup misses).
func TestRecordProviderUsageIgnoresEmptyName(t *testing.T) {
	m := Model{}
	m.diagnosticPanelsState = newDiagnosticPanelsState()
	m = m.recordProviderUsage(providerUsageEntry{Provider: "", Model: "x"})
	if len(m.providers.usageHistory) != 0 {
		t.Fatalf("empty provider name should not be recorded, got %#v", m.providers.usageHistory)
	}
	m = m.recordProviderUsage(providerUsageEntry{Provider: "   ", Model: "x"})
	if len(m.providers.usageHistory) != 0 {
		t.Fatalf("whitespace-only name should not be recorded, got %#v", m.providers.usageHistory)
	}
}

// TestHandleProviderProbeMsgCachesResult — the reducer caches the
// result, clears the in-flight flag, and updates the notice line so
// the chip strip + footer agree on the outcome.
func TestHandleProviderProbeMsgCachesResult(t *testing.T) {
	m := Model{}
	m.diagnosticPanelsState = newDiagnosticPanelsState()
	m.providers.probing = map[string]bool{"anthropic": true}
	out, _ := m.handleProviderProbeMsg(providerProbeMsg{
		name: "anthropic",
		result: engine.ProviderProbeResult{
			Provider: "anthropic", OK: true, DurationMs: 312, At: time.Now(),
		},
	})
	if out.providers.probing["anthropic"] {
		t.Fatalf("in-flight flag should clear after probe completes")
	}
	if got, ok := out.providers.probeResults["anthropic"]; !ok || !got.OK || got.DurationMs != 312 {
		t.Fatalf("probe result not cached correctly: %+v", got)
	}
	if !strings.Contains(out.notice, "Probe anthropic ok") {
		t.Fatalf("expected ok notice, got %q", out.notice)
	}
}

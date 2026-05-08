package tui

// provider_panel_probe.go — async test-connection probe for the
// Providers panel (Phase I item 1). `T` on a highlighted row dispatches
// `probeProviderCmd` which runs `engine.TestProviderConnection` with a
// hard timeout off the UI goroutine, then returns a `providerProbeMsg`
// that the update reducer caches in `m.providers.probeResults` so the
// per-row chip can render the latency / error / OK marker.

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// providerProbeTimeout is the hard ceiling for a single test-connection
// round trip. Long enough that a healthy slow-cold-start network blip
// passes, short enough that an unreachable upstream doesn't lock up the
// chip strip for half a minute.
const providerProbeTimeout = 8 * time.Second

// providerProbeMsg carries the outcome of an async probe back into the
// update loop. The full `engine.ProviderProbeResult` is preserved so
// the panel can render duration / error verbatim without a second
// lookup.
type providerProbeMsg struct {
	name   string
	result engine.ProviderProbeResult
}

// probeProviderCmd is the off-UI bubbletea Cmd that calls the engine's
// TestProviderConnection and packages the result. Returns a no-op
// message when the engine isn't ready so the panel notice stays the
// authoritative "couldn't probe" surface.
func probeProviderCmd(eng *engine.Engine, name string) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return providerProbeMsg{name: name, result: engine.ProviderProbeResult{
				Provider: name,
				Err:      "engine not initialised",
				At:       time.Now(),
			}}
		}
		return providerProbeMsg{name: name, result: eng.TestProviderConnection(name, providerProbeTimeout)}
	}
}

// startProviderProbe marks the row as in-flight (so the chip can render
// a "probing…" state) and dispatches the async cmd. Engine-nil short
// circuits to a notice so the user understands why nothing happened.
func (m Model) startProviderProbe(name string) (Model, tea.Cmd) {
	if name == "" {
		m.notice = "Select a provider row first — j/k highlights, then T probes."
		return m, nil
	}
	if m.eng == nil {
		m.notice = "Engine not ready — cannot probe " + name + " yet."
		return m, nil
	}
	if m.providers.probing == nil {
		m.providers.probing = map[string]bool{}
	}
	m.providers.probing[name] = true
	m.notice = "Probing " + name + "…"
	return m, probeProviderCmd(m.eng, name)
}

// handleProviderProbeMsg caches the probe result and clears the
// in-flight flag. The chip strip reads `m.providers.probeResults`
// directly so a successful probe shows up on the next render tick.
func (m Model) handleProviderProbeMsg(msg providerProbeMsg) (Model, tea.Cmd) {
	if m.providers.probeResults == nil {
		m.providers.probeResults = map[string]engine.ProviderProbeResult{}
	}
	m.providers.probeResults[msg.name] = msg.result
	if m.providers.probing != nil {
		delete(m.providers.probing, msg.name)
	}
	if msg.result.OK {
		m.notice = "Probe " + msg.name + " ok · " + formatProbeDuration(msg.result.DurationMs)
	} else {
		m.notice = "Probe " + msg.name + " failed: " + truncateSingleLine(msg.result.Err, 80)
	}
	return m, nil
}

// formatProbeDuration is a thin wrapper for the per-row chip + the
// notice line so they stay in sync. Sub-second probes show as "ms",
// anything slower includes a "(slow)" tag because that's actionable.
func formatProbeDuration(ms int) string {
	switch {
	case ms <= 0:
		return "0ms"
	case ms < 1500:
		return formatMillis(ms)
	default:
		return formatMillis(ms) + " (slow)"
	}
}

func formatMillis(ms int) string {
	if ms < 1000 {
		return intMs(ms) + "ms"
	}
	// Render seconds with one decimal place when ≥ 1s.
	whole := ms / 1000
	tenths := (ms % 1000) / 100
	if tenths == 0 {
		return intMs(whole) + "s"
	}
	return intMs(whole) + "." + intMs(tenths) + "s"
}

func intMs(n int) string {
	// tiny strconv-free helper to keep the panel free of odd allocations
	// during steady-state rendering. Negative inputs already filtered by
	// formatProbeDuration; this fast path is fine for "ms" / "s" labels.
	if n == 0 {
		return "0"
	}
	const digits = "0123456789"
	buf := [20]byte{}
	i := len(buf)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	if intMsSuffix {
		// suppress the suffix here — formatMillis appends "ms"/"s" itself.
	}
	return string(buf[i:])
}

// intMsSuffix is a compile-time tweak hook (kept false) so the helper
// can grow a unit suffix later without touching every call site.
const intMsSuffix = false

// recordProviderUsage appends a single completion event to the per-
// provider history ring (Phase I item 2). Lookup key is the provider
// name normalised to lowercase so router casing differences don't
// fragment the buffer. Older entries beyond providerUsageHistoryCap
// are evicted from the front so steady-state memory stays flat.
func (m Model) recordProviderUsage(entry providerUsageEntry) Model {
	name := strings.ToLower(strings.TrimSpace(entry.Provider))
	if name == "" {
		return m
	}
	if m.providers.usageHistory == nil {
		m.providers.usageHistory = map[string][]providerUsageEntry{}
	}
	hist := append(m.providers.usageHistory[name], entry)
	if len(hist) > providerUsageHistoryCap {
		// Drop the oldest entries from the front. Copy into a fresh
		// slice so the underlying array can be reclaimed by GC.
		drop := len(hist) - providerUsageHistoryCap
		hist = append([]providerUsageEntry(nil), hist[drop:]...)
	}
	m.providers.usageHistory[name] = hist
	return m
}

// providerUsageStrip returns up to `limit` formatted history lines for
// the named provider, newest first. Empty when there's no history yet.
// Used by the Providers panel detail view to render the last-N
// completions under the model picker.
func (m Model) providerUsageStrip(name string, limit int) []string {
	key := strings.ToLower(strings.TrimSpace(name))
	hist := m.providers.usageHistory[key]
	if len(hist) == 0 {
		return nil
	}
	if limit <= 0 || limit > len(hist) {
		limit = len(hist)
	}
	out := make([]string, 0, limit)
	for i := len(hist) - 1; i >= 0 && len(out) < limit; i-- {
		e := hist[i]
		when := formatRelativeTime(e.At)
		tokens := compactMetric(e.TotalTokens)
		if e.InputTokens > 0 || e.OutputTokens > 0 {
			tokens = fmt.Sprintf("in %s | out %s | total %s",
				compactMetric(e.InputTokens),
				compactMetric(e.OutputTokens),
				compactMetric(e.TotalTokens))
		}
		out = append(out, fmt.Sprintf("%s · %s · %s", when, blankFallback(e.Model, "(no model)"), tokens))
	}
	return out
}

// providerProbeChip returns the styled chip text for the probe state
// of `name`, or "" when there's no probe data and no in-flight call.
// Used by the Providers panel renderer to slot the chip after the
// status badge for each row.
func (m Model) providerProbeChip(name string) string {
	if m.providers.probing[name] {
		return infoStyle.Render("· probing…")
	}
	res, ok := m.providers.probeResults[name]
	if !ok {
		return ""
	}
	if res.OK {
		return okStyle.Render("· probe ok " + formatProbeDuration(res.DurationMs))
	}
	return failStyle.Render("· probe failed")
}

package tui

// engine_events_loop_headroom.go — pre-compact context-headroom
// threshold notifier. The thinking-event handler calls
// maybeNotifyHeadroomThreshold once per round; this file owns the
// 70/85/95 band table and the per-turn bitmask dedupe. Sibling to
// engine_events_loop.go which keeps the agent:loop:* dispatcher.

import "fmt"

// headroomThresholds defines the (pct, bit, severity, hint) bands the
// pre-compact warning notifies on. bit indices match the bitmask in
// agentLoopState.headroomThresholdsHit so each band fires at most once
// per turn — a long loop that ticks 71→72→73 over many rounds only
// shows the 70% notification ONCE.
var headroomThresholds = []struct {
	pct      int
	bit      uint8
	status   string // chat-event Status (warn, error)
	headline string
	hint     string
}{
	// Hint copy reflects what actually reduces ENGINE context (not the
	// TUI-only /compact slash command, which only collapses visible
	// transcript lines without affecting the running loop's working
	// set). Auto-compact fires reactively at 0.7 ratio; /chat new
	// rotates to a fresh conversation; narrowing @file mentions and
	// dropping pinned files reduces the per-Ask context payload.
	{pct: 70, bit: 1 << 0, status: "warn", headline: "context 70% full", hint: "auto-compact will fire next round — or narrow @files / drop pins"},
	{pct: 85, bit: 1 << 1, status: "warn", headline: "context 85% full", hint: "tighten scope: drop @files, fewer pins, or /conv new"},
	{pct: 95, bit: 1 << 2, status: "error", headline: "context 95% full", hint: "next turn may park on budget — /conv new for a fresh window"},
}

// maybeNotifyHeadroomThreshold pushes a chat-event line when the live
// loop tokens cross a 70/85/95 band for the first time this turn.
// Uses the live loop budget when available (max_tool_tokens), falling
// back to MaxContext from the live context snapshot when not — that
// way a non-loop Ask still gets a warning if the request itself fills
// the window. Caller is the agent:loop:thinking handler so this fires
// once per round at most; the bitmask dedupes within the turn.
func (m Model) maybeNotifyHeadroomThreshold() Model {
	used := m.agentLoop.liveLoopTokens
	cap := m.agentLoop.liveLoopBudgetCap
	if cap <= 0 {
		// Fall back to provider context window when the loop didn't
		// report a budget — better than nothing for non-tool-loop asks.
		if live := m.liveContextSnapshot(); live.ok && live.maxContext > 0 {
			cap = live.maxContext
		}
	}
	if used <= 0 || cap <= 0 {
		return m
	}
	pct := int((int64(used) * 100) / int64(cap))
	for _, band := range headroomThresholds {
		if pct < band.pct {
			continue
		}
		if m.agentLoop.headroomThresholdsHit&band.bit != 0 {
			continue // already fired this turn
		}
		m.agentLoop.headroomThresholdsHit |= band.bit
		m.upsertStreamingChatEvent(chatEventLine{
			Key:    fmt.Sprintf("context:headroom:%d", band.pct),
			Kind:   "context",
			Status: band.status,
			Title:  band.headline,
			Detail: fmt.Sprintf("%d / %d tokens · %s", used, cap, band.hint),
		})
	}
	return m
}

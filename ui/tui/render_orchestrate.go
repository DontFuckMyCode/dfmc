// render_orchestrate.go — the Orchestrate tab. A single screen that
// shows the entire orchestration hierarchy at a glance:
//
//   - Main agent (provider/model/phase/loop tokens/turn duration)
//   - Subagents (active, with provider+model and current task)
//   - TODOs (done/doing/pending split)
//   - Drive run (active TODO ladder with per-todo provider tag)
//   - Tokens (context budget, loop budget, session totals, compacts)
//   - Recent activity (last few high-signal event-bus lines)
//
// Lives next to render_workflow.go (Drive cockpit) but answers a
// different question — Workflow says "what's the Drive run doing
// step-by-step", Orchestrate says "what's the WHOLE system doing
// right now". Bound to Alt+R from any tab via update.go.
//
// File layout: this file owns renderOrchestrateView dispatcher + the
// TOKENS / RECENT ACTIVITY sections + subagentLimitFromConfig.
// Agent-centric sections (MAIN AGENT, SUBAGENTS) live in
// render_orchestrate_agents.go; work-tracking sections (TODOS, TASK
// STORE, DRIVE RUN) live in render_orchestrate_work.go.

package tui

import (
	"fmt"
	"strings"
)

// Section indices for the Orchestrate overlay's cursor. Keep contiguous
// so up/down arithmetic stays simple; orchestrateSectionCount is the
// clamp bound used by handleOrchestrateKey.
const (
	orchestrateSectionMain      = 0
	orchestrateSectionSubagents = 1
	orchestrateSectionTodos     = 2
	orchestrateSectionTaskStore = 3
	orchestrateSectionDrive     = 4
	orchestrateSectionTokens    = 5
	orchestrateSectionRecent    = 6
	orchestrateSectionCount     = 7
)

// orchestrateSectionMarker mirrors contextsSectionMarker — the selected
// section title gets a "▶ " accent, everything else is padded by two
// spaces so widths line up. Lipgloss adds ANSI escapes around "▶" so
// tests look for the literal glyph rather than a specific style.
func orchestrateSectionMarker(selected bool) string {
	if selected {
		return accentStyle.Bold(true).Render("▶ ")
	}
	return "  "
}

func (m Model) renderOrchestrateView(width int) string {
	inner := width
	if inner > 110 {
		inner = 110
	}
	if inner < 40 {
		inner = 40
	}
	sel := m.orchestrate.selectedSection

	parts := []string{
		sectionHeader("◬", "Orchestrate"),
		subtleStyle.Render("↑↓ section · → / enter action menu · j/k/pgup/pgdn scroll · alt+r jumps here"),
		renderDivider(inner),
		"",
	}

	parts = append(parts, m.orchestrateMainAgentSection(inner, sel == orchestrateSectionMain)...)
	parts = append(parts, "")
	parts = append(parts, m.orchestrateSubagentsSection(inner, sel == orchestrateSectionSubagents)...)
	parts = append(parts, "")
	parts = append(parts, m.orchestrateTodosSection(inner, sel == orchestrateSectionTodos)...)
	parts = append(parts, "")
	parts = append(parts, m.orchestrateTaskStoreSection(inner, sel == orchestrateSectionTaskStore)...)
	parts = append(parts, "")
	parts = append(parts, m.orchestrateDriveSection(inner, sel == orchestrateSectionDrive)...)
	parts = append(parts, "")
	parts = append(parts, m.orchestrateTokensSection(inner, sel == orchestrateSectionTokens)...)
	parts = append(parts, "")
	parts = append(parts, m.orchestrateRecentSection(inner, sel == orchestrateSectionRecent)...)

	return strings.Join(parts, "\n")
}

// orchestrateRecentSection — last few high-signal events from the
// activity log so the user gets a chat-history-style "what just
// happened" feed inside the panel. Uses the same activityLog that
// feeds the bottom-of-chat notice line; capped at the last 8 lines
// so the panel doesn't grow unbounded.
func (m Model) orchestrateRecentSection(width int, selected bool) []string {
	header := orchestrateSectionMarker(selected) + accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("RECENT ACTIVITY")
	log := m.activityLog
	if len(log) == 0 {
		return []string{
			header + "  " + subtleStyle.Render("(empty)"),
			"  " + subtleStyle.Render("event firehose builds up as the agent works · F7 for full feed"),
		}
	}
	out := []string{header + "  " + subtleStyle.Render(fmt.Sprintf("(last %d of %d)", min(len(log), 8), len(log)))}
	start := 0
	if len(log) > 8 {
		start = len(log) - 8
	}
	for _, line := range log[start:] {
		out = append(out, "  · "+truncateSingleLine(strings.TrimSpace(line), width-6))
	}
	return out
}

// orchestrateTokensSection — context budget + session totals + the
// per-turn pressure metrics. One block where the user can read the
// whole "are we running close to the budget?" story.
func (m Model) orchestrateTokensSection(_ int, selected bool) []string {
	out := []string{orchestrateSectionMarker(selected) + accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("TOKENS")}

	info := m.statsPanelInfo()
	if info.MaxContext > 0 {
		used := info.ContextTokens
		pct := 0
		if info.MaxContext > 0 {
			pct = (used * 100) / info.MaxContext
		}
		out = append(out, fmt.Sprintf("  Context:    %s / %s  (%d%%)",
			compactMetric(used), compactMetric(info.MaxContext), pct))
	}
	if m.agentLoop.liveLoopBudgetCap > 0 {
		used := m.agentLoop.liveLoopTokens
		pct := 0
		if m.agentLoop.liveLoopBudgetCap > 0 {
			pct = (used * 100) / m.agentLoop.liveLoopBudgetCap
		}
		out = append(out, fmt.Sprintf("  Loop turn:  %s / %s  (%d%%)",
			compactMetric(used), compactMetric(m.agentLoop.liveLoopBudgetCap), pct))
	}

	sessionLine := fmt.Sprintf("  Session:    in %s · out %s · total %s",
		compactMetric(info.SessionInputTokens),
		compactMetric(info.SessionOutputTokens),
		compactMetric(info.SessionTotalTokens))
	if info.CostPer1kTokens > 0 && info.SessionTotalTokens > 0 {
		cost := (float64(info.SessionTotalTokens) / 1000) * info.CostPer1kTokens
		sessionLine += "  ·  " + formatUSDCost(cost)
	}
	out = append(out, sessionLine)

	pressure := []string{}
	if m.agentLoop.compactsThisTurn > 0 {
		label := fmt.Sprintf("compacts ×%d", m.agentLoop.compactsThisTurn)
		if m.agentLoop.compactReclaimedTurn > 0 {
			label += fmt.Sprintf(" (-%s)", compactMetric(m.agentLoop.compactReclaimedTurn))
		}
		pressure = append(pressure, label)
	}
	if m.agentLoop.cacheHitsThisTurn > 0 {
		pressure = append(pressure, fmt.Sprintf("cache ×%d", m.agentLoop.cacheHitsThisTurn))
	}
	if m.agentLoop.cumulativeSteps > 0 && m.agentLoop.stepCeiling > 0 {
		pct := (m.agentLoop.cumulativeSteps * 100) / m.agentLoop.stepCeiling
		pressure = append(pressure, fmt.Sprintf("ceiling %d/%d steps (%d%%)",
			m.agentLoop.cumulativeSteps, m.agentLoop.stepCeiling, pct))
	}
	if len(pressure) > 0 {
		out = append(out, "  Pressure:   "+strings.Join(pressure, "  ·  "))
	}
	return out
}

// subagentLimitFromConfig returns the engine-reported subagent
// concurrency limit. Reads from m.status.SubagentsLimit (the same
// surface stats panel uses) so the orchestrate view stays in sync
// with whatever number the rest of the UI shows. Returns 0 when
// the status hasn't been populated yet (tests, fresh boot) and the
// caller suppresses the "/ N limit" hint.
func (m Model) subagentLimitFromConfig() int {
	return m.status.SubagentsLimit
}

package tui

// render_orchestrate_agents.go — agent-centric sections of the
// Orchestrate tab: the MAIN AGENT card (provider/model/phase/step/
// loop tokens/momentum) and the SUBAGENTS list (every active helper
// with its own provider+model and current task). Sibling files:
// render_orchestrate.go (entry point + tokens / recent activity),
// render_orchestrate_work.go (todos / task store / drive surfaces).

import (
	"fmt"
	"sort"
	"strings"
)

// orchestrateMainAgentSection — top-level "what's the main agent
// loop doing right now" card. Shows provider+model the user is
// paying for, current phase, step counter, live loop tokens, and
// the per-turn momentum badges.
func (m Model) orchestrateMainAgentSection(width int, selected bool) []string {
	out := []string{orchestrateSectionMarker(selected) + accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("MAIN AGENT")}
	provider := strings.TrimSpace(m.agentLoop.provider)
	model := strings.TrimSpace(m.agentLoop.model)
	if provider == "" {
		provider = "(provider?)"
	}
	if model == "" {
		model = "(model?)"
	}
	out = append(out, "  Provider:    "+provider+" / "+model)

	phase := humanizeAgentPhase(m.agentLoop.phase)
	if !m.agentLoop.active {
		phase = "idle (waiting on user)"
	}
	out = append(out, "  Phase:       "+phase)

	if m.agentLoop.maxToolStep > 0 {
		out = append(out, fmt.Sprintf("  Step:        %d / %d  ·  rounds %d",
			m.agentLoop.step, m.agentLoop.maxToolStep, m.agentLoop.toolRounds))
	} else if m.agentLoop.step > 0 || m.agentLoop.toolRounds > 0 {
		out = append(out, fmt.Sprintf("  Step:        %d  ·  rounds %d", m.agentLoop.step, m.agentLoop.toolRounds))
	}

	if m.agentLoop.liveLoopTokens > 0 {
		if m.agentLoop.liveLoopBudgetCap > 0 {
			pct := (m.agentLoop.liveLoopTokens * 100) / m.agentLoop.liveLoopBudgetCap
			out = append(out, fmt.Sprintf("  Loop tokens: %s / %s  (%d%%)",
				compactMetric(m.agentLoop.liveLoopTokens),
				compactMetric(m.agentLoop.liveLoopBudgetCap),
				pct))
		} else {
			out = append(out, "  Loop tokens: "+compactMetric(m.agentLoop.liveLoopTokens))
		}
	}

	momentum := []string{}
	if m.agentLoop.active && !m.agentLoop.turnStartedAt.IsZero() {
		s := agentLoopState{active: true, turnStartedAt: m.agentLoop.turnStartedAt}
		if elapsed := computeTurnElapsedSec(s); elapsed > 0 {
			momentum = append(momentum, "running "+formatTurnElapsed(elapsed))
		}
	}
	if files := len(m.agentLoop.turnEditedFiles); files > 0 {
		word := "files"
		if files == 1 {
			word = "file"
		}
		momentum = append(momentum, fmt.Sprintf("edits ×%d %s", files, word))
	}
	if m.agentLoop.compactsThisTurn > 0 {
		label := fmt.Sprintf("compacts ×%d", m.agentLoop.compactsThisTurn)
		if m.agentLoop.compactReclaimedTurn > 0 {
			label += fmt.Sprintf(" · -%s reclaimed", compactMetric(m.agentLoop.compactReclaimedTurn))
		}
		momentum = append(momentum, label)
	}
	if m.agentLoop.cacheHitsThisTurn > 0 {
		momentum = append(momentum, fmt.Sprintf("cache ×%d", m.agentLoop.cacheHitsThisTurn))
	}
	if m.agentLoop.toolErrorsThisTurn > 0 {
		momentum = append(momentum, fmt.Sprintf("errs ×%d", m.agentLoop.toolErrorsThisTurn))
	}
	if len(momentum) > 0 {
		out = append(out, "  Momentum:    "+strings.Join(momentum, "  ·  "))
	}

	if reason := strings.TrimSpace(m.agentLoop.lastToolReason); reason != "" {
		out = append(out, "  Reasoning:   "+truncateSingleLine(reason, width-15))
	}
	if stuck := strings.TrimSpace(m.agentLoop.stuckTool); stuck != "" && m.agentLoop.stuckCount > 0 {
		badge := fmt.Sprintf("stalled · %s ×%d", stuck, m.agentLoop.stuckCount)
		if cls := strings.TrimSpace(m.agentLoop.stuckErrClass); cls != "" {
			badge += "  ·  " + cls
		}
		out = append(out, "  "+warnStyle.Render(badge))
	}
	return out
}

// orchestrateSubagentsSection lists every active subagent with its
// provider+model+task. The user explicitly wanted to see "which
// model is doing which job" — this is the exact answer.
func (m Model) orchestrateSubagentsSection(width int, selected bool) []string {
	count := m.telemetry.activeSubagentCount
	limitHint := ""
	if cfg := m.subagentLimitFromConfig(); cfg > 0 {
		limitHint = fmt.Sprintf(" / %d limit", cfg)
	}
	header := orchestrateSectionMarker(selected) + accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("SUBAGENTS")
	header += "  " + subtleStyle.Render(fmt.Sprintf("(%d active%s)", count, limitHint))
	out := []string{header}

	if len(m.telemetry.subagents) == 0 {
		out = append(out, "  "+subtleStyle.Render("none running — main agent works solo until it spawns helpers"))
		return out
	}

	keys := make([]string, 0, len(m.telemetry.subagents))
	for k := range m.telemetry.subagents {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		item := m.telemetry.subagents[k]
		role := strings.TrimSpace(item.Role)
		if role == "" {
			role = "subagent"
		}
		provider := strings.TrimSpace(item.Provider)
		model := strings.TrimSpace(item.Model)
		identity := provider + "/" + model
		identity = strings.Trim(identity, "/")
		if identity == "" {
			identity = "(provider?)"
		}
		status := strings.TrimSpace(item.Status)
		statusGlyph := orchestrateSubagentGlyph(status)
		task := truncateSingleLine(item.Task, width-50)
		runtime := orchestrateSubagentRuntime(item)
		line := fmt.Sprintf("  %s %-18s %-32s  %s",
			statusGlyph,
			truncateSingleLine(role, 18),
			truncateSingleLine(identity, 32),
			task)
		if runtime != "" {
			line += "  " + subtleStyle.Render(runtime)
		}
		out = append(out, line)
	}
	return out
}

// orchestrateSubagentGlyph maps the runtime status set by
// subagent_runtime.go ("running"/"fallback"/"failed"/"parked"/
// "done") to a status glyph. The "subagent-*" chip statuses live
// on the chip ribbon, not the runtime item — different vocabulary,
// matched here so a fallback in flight reads as ↻ and a finished
// helper as ✓ instead of the generic running ▶.
func orchestrateSubagentGlyph(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed":
		return failStyle.Render("✗")
	case "fallback":
		return warnStyle.Render("↻")
	case "parked":
		return warnStyle.Render("⏸")
	case "done":
		return doneStyle.Render("✓")
	default:
		return accentStyle.Render("▶")
	}
}

func orchestrateSubagentRuntime(item subagentRuntimeItem) string {
	parts := []string{}
	if item.Rounds > 0 {
		parts = append(parts, fmt.Sprintf("%d rounds", item.Rounds))
	}
	if item.Attempts > 1 {
		parts = append(parts, fmt.Sprintf("attempt %d", item.Attempts))
	}
	if item.Fallback {
		parts = append(parts, "fallback")
	}
	if item.DurationMs > 0 {
		parts = append(parts, fmt.Sprintf("%dms", item.DurationMs))
	}
	return strings.Join(parts, " · ")
}

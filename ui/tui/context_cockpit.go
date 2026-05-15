package tui

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/skills"
	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
)

// renderContextCockpitBlock is the canonical TUI answer to:
// "what exactly enters the next provider request?" It renders from the same
// ContextPayloadSnapshot used by the stats panel and runtime strip.
func (m Model) renderContextCockpitBlock(width int) []string {
	vm := m.runtimeViewModel()
	payload := vm.ContextPayload
	width = clampInt(width, 40, 1000)

	lines := []string{
		sectionHeader("CTX", "Next Provider Request"),
	}
	identity := payload.Identity()
	if strings.TrimSpace(identity) == "" {
		identity = blankFallback(strings.TrimSpace(vm.Provider+" / "+vm.Model), "provider/model unknown")
	}
	if payload.LimitSource != "" {
		lines = append(lines, subtleStyle.Render("request: ")+boldStyle.Render(identity)+"  "+subtleStyle.Render("limit "+payload.LimitSource))
	} else {
		lines = append(lines, subtleStyle.Render("request: ")+boldStyle.Render(identity))
	}
	lines = append(lines, theme.RenderContextBarFrame(payload.WindowTokens, payload.MaxContext, 18, vm.SpinnerFrame))
	lines = append(lines, contextCockpitUsageLine(payload))
	lines = append(lines, "")

	lines = append(lines, subtleStyle.Render("payload sections"))
	lines = append(lines, m.contextCockpitPayloadRows(payload, vm)...)

	lines = append(lines, "")
	lines = append(lines, subtleStyle.Render("reserves / headroom"))
	lines = append(lines, contextCockpitReserveRows(payload)...)

	if len(vm.ContextTopFiles) > 0 || len(vm.ContextReasons) > 0 {
		lines = append(lines, "")
		lines = append(lines, subtleStyle.Render("evidence source"))
		if len(vm.ContextTopFiles) > 0 {
			lines = append(lines, "  files: "+truncateForLine(strings.Join(vm.ContextTopFiles, ", "), max(width-9, 24)))
		}
		if len(vm.ContextReasons) > 0 {
			lines = append(lines, "  why: "+truncateForLine(vm.ContextReasons[0], max(width-7, 24)))
		}
	}

	lines = append(lines, "")
	lines = append(lines, subtleStyle.Render("manage: enter preview · m message manager · a active evidence · e query · esc close"))
	return lines
}

func contextCockpitUsageLine(payload theme.ContextPayloadSnapshot) string {
	if payload.MaxContext <= 0 {
		return subtleStyle.Render("window: unknown")
	}
	pct := 0
	if payload.WindowTokens > 0 {
		pct = int((int64(payload.WindowTokens) * 100) / int64(payload.MaxContext))
	}
	free := payload.FreeTokens
	if free == 0 {
		free = payload.MaxContext - payload.WindowTokens
	}
	return fmt.Sprintf(
		"window: %s/%s (%d%%) | free %s",
		boldStyle.Render(theme.CompactTokens(payload.WindowTokens)),
		theme.CompactTokens(payload.MaxContext),
		pct,
		theme.CompactTokens(max(0, free)),
	)
}

func (m Model) contextCockpitPayloadRows(payload theme.ContextPayloadSnapshot, vm runtimeViewModel) []string {
	rows := []string{
		contextCockpitRow("system", payload.SystemTokens, "system prompt + runtime directives"),
		contextCockpitRow("tools", payload.ToolReserve, fmt.Sprintf("%d registered tool schemas / reserve", vm.ToolCount)),
		contextCockpitRow("history", payload.MessageTokens, fmt.Sprintf("%d messages, %d tool calls", payload.MessageCount, payload.ToolCallCount)),
		contextCockpitRow("evidence", payload.EvidenceTokens, contextCockpitEvidenceLabel(payload, vm)),
	}
	rows = append(rows, contextCockpitMemoryRow(m)...)
	rows = append(rows, contextCockpitSkillsRow(m)...)
	return rows
}

func contextCockpitReserveRows(payload theme.ContextPayloadSnapshot) []string {
	return []string{
		contextCockpitRow("response", payload.ResponseReserve, "assistant output headroom"),
		contextCockpitRow("tool", payload.ToolReserve, "tool-call/result headroom"),
		contextCockpitRow("history cap", payload.HistoryReserve, "conversation retention cap"),
		contextCockpitRow("empty", max(0, payload.FreeTokens), "uncommitted provider window"),
	}
}

func contextCockpitRow(label string, tokens int, note string) string {
	return fmt.Sprintf("  %-11s %7s  %s", label, theme.CompactTokens(tokens), subtleStyle.Render(note))
}

func contextCockpitEvidenceLabel(payload theme.ContextPayloadSnapshot, vm runtimeViewModel) string {
	if payload.WorkspaceEvidenceOff {
		return "workspace evidence off; conversation-only unless requested"
	}
	if vm.ContextFileCount > 0 {
		return fmt.Sprintf("%d/%d files, budget %s", vm.ContextFileCount, vm.ContextMaxFiles, theme.CompactTokens(payload.EvidenceBudgetTokens))
	}
	if payload.EvidenceBudgetTokens > 0 {
		return "budget " + theme.CompactTokens(payload.EvidenceBudgetTokens)
	}
	return "not built yet"
}

func contextCockpitMemoryRow(m Model) []string {
	if m.status.MemoryDegraded {
		reason := strings.TrimSpace(m.status.MemoryLoadErr)
		if reason == "" {
			reason = "load failed"
		}
		return []string{contextCockpitRow("memory", 0, "degraded: "+truncateForLine(reason, 54))}
	}
	if m.eng == nil || m.eng.Memory == nil {
		return []string{contextCockpitRow("memory", 0, "store unavailable; no recall injected")}
	}
	loaded := len(m.memory.entries)
	if loaded > 0 {
		return []string{contextCockpitRow("memory", 0, fmt.Sprintf("store ready; %d loaded in Memory panel, not separately metered", loaded))}
	}
	return []string{contextCockpitRow("memory", 0, "store ready; no separate token meter reported")}
}

func contextCockpitSkillsRow(m Model) []string {
	query := strings.TrimSpace(m.contextPanel.query)
	if query == "" {
		query = strings.TrimSpace(m.chat.input)
	}
	if query == "" {
		return []string{contextCockpitRow("skills", 0, "none selected for next input")}
	}
	sel := skills.ResolveForQuery(m.projectRoot(), query, "")
	if len(sel.Skills) == 0 {
		return []string{contextCockpitRow("skills", 0, "none resolved from current query")}
	}
	names := make([]string, 0, len(sel.Skills))
	toks := 0
	for _, skill := range sel.Skills {
		names = append(names, skill.Name)
		toks += estimatedChatTokens(skills.RenderSystemText(skill))
	}
	return []string{contextCockpitRow("skills", toks, truncateForLine(strings.Join(names, ", "), 56))}
}

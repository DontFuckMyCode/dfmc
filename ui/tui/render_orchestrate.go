// render_orchestrate.go — the Orchestrate tab. A single screen that
// shows the entire orchestration hierarchy at a glance:
//
//   - Main agent (provider/model/phase/loop tokens/turn duration)
//   - Subagents (active, with provider+model and current task)
//   - TODOs (done/doing/pending split)
//   - Drive run (active TODO ladder with per-todo provider tag)
//   - Tokens (context budget, loop budget, session totals, compacts)
//   - Headroom (auto-compact / handoff thresholds, ceiling proximity)
//
// Lives next to render_workflow.go (Drive cockpit) but answers a
// different question — Workflow says "what's the Drive run doing
// step-by-step", Orchestrate says "what's the WHOLE system doing
// right now". Bound to Alt+R from any tab via update.go.

package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

func (m Model) renderOrchestrateView(width int) string {
	inner := width
	if inner > 110 {
		inner = 110
	}
	if inner < 40 {
		inner = 40
	}

	parts := []string{
		sectionHeader("◬", "Orchestrate"),
		subtleStyle.Render("alt+r jumps here · live hierarchy of agents · todos · drive · tokens"),
		renderDivider(inner),
		"",
	}

	parts = append(parts, m.orchestrateMainAgentSection(inner)...)
	parts = append(parts, "")
	parts = append(parts, m.orchestrateSubagentsSection(inner)...)
	parts = append(parts, "")
	parts = append(parts, m.orchestrateTodosSection(inner)...)
	parts = append(parts, "")
	parts = append(parts, m.orchestrateTaskStoreSection(inner)...)
	parts = append(parts, "")
	parts = append(parts, m.orchestrateDriveSection(inner)...)
	parts = append(parts, "")
	parts = append(parts, m.orchestrateTokensSection(inner)...)
	parts = append(parts, "")
	parts = append(parts, m.orchestrateRecentSection(inner)...)

	return strings.Join(parts, "\n")
}

// orchestrateRecentSection — last few high-signal events from the
// activity log so the user gets a chat-history-style "what just
// happened" feed inside the panel. Uses the same activityLog that
// feeds the bottom-of-chat notice line; capped at the last 8 lines
// so the panel doesn't grow unbounded.
func (m Model) orchestrateRecentSection(width int) []string {
	header := accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("RECENT ACTIVITY")
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

// orchestrateMainAgentSection — top-level "what's the main agent
// loop doing right now" card. Shows provider+model the user is
// paying for, current phase, step counter, live loop tokens, and
// the per-turn momentum badges.
func (m Model) orchestrateMainAgentSection(width int) []string {
	out := []string{accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("MAIN AGENT")}
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
func (m Model) orchestrateSubagentsSection(width int) []string {
	count := m.telemetry.activeSubagentCount
	limitHint := ""
	if cfg := m.subagentLimitFromConfig(); cfg > 0 {
		limitHint = fmt.Sprintf(" / %d limit", cfg)
	}
	header := accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("SUBAGENTS")
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

// orchestrateTodosSection — the active TODO ladder with done/doing/
// pending counts and the active form of the in-flight item.
func (m Model) orchestrateTodosSection(width int) []string {
	todos := m.workflowTodos()
	total, pending, doing, done := summarizeWorkflowTodos(todos)
	header := accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("TODOS")
	if total == 0 {
		header += "  " + subtleStyle.Render("(none)")
		return []string{
			header,
			"  " + subtleStyle.Render("no shared todo list yet · agent uses todo_write to shard work"),
		}
	}
	header += "  " + subtleStyle.Render(fmt.Sprintf("(%d total · %d done · %d doing · %d pending)",
		total, done, doing, pending))
	out := []string{header}
	for _, item := range todos {
		st := strings.ToLower(strings.TrimSpace(item.Status))
		label := strings.TrimSpace(item.Content)
		if label == "" {
			label = "(untitled)"
		}
		var glyph, body string
		switch st {
		case "completed", "done":
			glyph = doneStyle.Render("✓")
			body = subtleStyle.Render(label)
		case "in_progress", "active", "doing":
			glyph = accentStyle.Render("▶")
			active := strings.TrimSpace(item.ActiveForm)
			if active == "" {
				active = label
			}
			body = active + "  " + subtleStyle.Render("← active")
		default:
			glyph = subtleStyle.Render("⏳")
			body = label
		}
		out = append(out, "  "+glyph+" "+truncateSingleLine(body, width-6))
	}
	return out
}

// orchestrateTaskStoreSection — supervisor task tree from the task
// store. Distinct from the TODOs surface (todo_write) and the DRIVE
// surface (drive runs): tasks here come from /split, orchestrate,
// or delegate_task. Renders the hierarchical tree as already
// formatted by statsPanelInfo so root → leaf indentation matches
// the stats panel.
func (m Model) orchestrateTaskStoreSection(width int) []string {
	header := accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("TASK STORE")
	info := m.statsPanelInfo()
	lines := info.TaskTreeLines

	// Plan-only state (no store entries yet but a /split plan exists).
	if len(lines) == 0 {
		if info.PlanSubtasks > 0 {
			mode := "serial"
			if info.PlanParallel {
				mode = "parallel"
			}
			out := []string{
				header + "  " + subtleStyle.Render(fmt.Sprintf("(plan only · %d subtasks · %s)", info.PlanSubtasks, mode)),
			}
			for _, line := range info.TaskLines {
				out = append(out, "  "+truncateSingleLine(line, width-4))
			}
			return out
		}
		return []string{
			header + "  " + subtleStyle.Render("(empty)"),
			"  " + subtleStyle.Render("populated by /split, orchestrate, delegate_task · F-keys can't reach this surface yet, but it's live"),
		}
	}

	out := []string{header + "  " + subtleStyle.Render(fmt.Sprintf("(%d entries)", len(lines)))}
	cap := 12
	if len(lines) <= cap {
		for _, line := range lines {
			out = append(out, "  "+truncateSingleLine(line, width-4))
		}
		return out
	}
	for _, line := range lines[:cap] {
		out = append(out, "  "+truncateSingleLine(line, width-4))
	}
	out = append(out, "  "+subtleStyle.Render(fmt.Sprintf("... %d more in store · open the stats panel (alt+d) for the full tree", len(lines)-cap)))
	return out
}

// orchestrateDriveSection — the active drive run + its TODO ladder
// with the routed provider tag per TODO so the user can see "which
// model is doing T3 vs T4".
func (m Model) orchestrateDriveSection(width int) []string {
	header := accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("DRIVE RUN")
	run := m.selectedRunForWorkflow()
	if run == nil || strings.TrimSpace(string(run.Status)) == "" {
		return []string{
			header + "  " + subtleStyle.Render("(idle)"),
			"  " + subtleStyle.Render("/drive <task> in chat to start an autonomous run · F5 for cockpit"),
		}
	}
	done, blocked, skipped, pending := run.Counts()
	running := 0
	for _, t := range run.Todos {
		if t.Status == drive.TodoRunning {
			running++
		}
	}
	header += "  " + subtleStyle.Render(fmt.Sprintf("(%s · %s)", truncateForLine(run.ID, 8), strings.ToLower(string(run.Status))))
	out := []string{header}
	out = append(out, "  Task:     "+truncateSingleLine(run.Task, width-13))
	out = append(out, fmt.Sprintf("  Progress: %d done · %d running · %d pending · %d blocked · %d skipped",
		done, running, pending, blocked, skipped))
	if len(run.Todos) == 0 {
		return out
	}
	out = append(out, "")
	for _, todo := range run.Todos {
		glyph := orchestrateDriveTodoGlyph(todo.Status)
		title := strings.TrimSpace(todo.Title)
		if title == "" {
			title = strings.TrimSpace(todo.ID)
		}
		tag := strings.TrimSpace(todo.ProviderTag)
		tagHint := ""
		if tag != "" {
			tagHint = "  " + subtleStyle.Render("["+tag+"]")
		}
		idHint := ""
		if id := strings.TrimSpace(todo.ID); id != "" {
			idHint = subtleStyle.Render(id) + " "
		}
		line := fmt.Sprintf("  %s %s%s%s", glyph, idHint, truncateSingleLine(title, width-30), tagHint)
		out = append(out, line)
	}
	return out
}

func orchestrateDriveTodoGlyph(status drive.TodoStatus) string {
	switch status {
	case drive.TodoDone:
		return doneStyle.Render("✓")
	case drive.TodoRunning:
		return accentStyle.Render("▶")
	case drive.TodoBlocked:
		return failStyle.Render("✗")
	case drive.TodoSkipped:
		return subtleStyle.Render("↷")
	default:
		return subtleStyle.Render("⏳")
	}
}

// orchestrateTokensSection — context budget + session totals + the
// per-turn pressure metrics. One block where the user can read the
// whole "are we running close to the budget?" story.
func (m Model) orchestrateTokensSection(_ int) []string {
	out := []string{accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("TOKENS")}

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

package tui

// describe_workflow.go — workflow/stats chat cards: the read-only
// snapshots that /stats, /workflow, /todos, /subagents, /queue show
// directly in the transcript.
//
// Split out of describe.go so the "what is the agent currently up to"
// surface lives in one focused file. Every function here is a pure
// read over Model + Engine state returning a single multi-line string
// suitable for appendSystemMessage. Shared helpers (workflowTodos,
// summarizeWorkflowTodos, formatWorkflowTodoLines, recentWorkflow*,
// latestWorkflowPlanSummary) are kept alongside their callers because
// nothing outside the workflow-describe surface uses them.
//
// Health/hooks/approval describe helpers live in describe_health.go;
// transcript export + compaction stay in describe.go.

import (
	"fmt"
	"strings"
	"time"

	toolruntime "github.com/dontfuckmycode/dfmc/internal/tools"
)

func (m Model) describeStats() string {
	lines := []string{"▸ Session stats"}

	elapsed := time.Duration(0)
	if !m.sessionStart.IsZero() {
		elapsed = time.Since(m.sessionStart).Round(time.Second)
	}
	lines = append(lines, fmt.Sprintf("  elapsed:     %s", elapsed))
	lines = append(lines, fmt.Sprintf("  messages:    %d transcript line(s)", len(m.chat.transcript)))

	// Token budget. ContextIn carries the last computed budget if a turn
	// has run; otherwise fall back to the provider's MaxContext only.
	tokens, maxCtx := 0, 0
	if m.status.ContextIn != nil {
		tokens = m.status.ContextIn.TokenCount
		maxCtx = m.status.ContextIn.ProviderMaxContext
	}
	if maxCtx == 0 {
		maxCtx = m.status.ProviderProfile.MaxContext
	}
	if maxCtx > 0 {
		pct := 0
		if tokens > 0 {
			pct = int(float64(tokens) / float64(maxCtx) * 100)
		}
		lines = append(lines, fmt.Sprintf("  context in:  %s / %s tokens (%d%% of window)",
			formatThousands(tokens), formatThousands(maxCtx), pct))
	} else {
		lines = append(lines, "  context in:  (no provider window info yet)")
	}

	// Agent loop progress (cumulative across turns).
	if m.agentLoop.toolRounds > 0 || m.agentLoop.step > 0 {
		phase := strings.TrimSpace(m.agentLoop.phase)
		if phase == "" {
			phase = "idle"
		}
		if m.agentLoop.maxToolStep > 0 {
			lines = append(lines, fmt.Sprintf("  agent:       %s · step %d/%d · %d tool round(s)",
				phase, m.agentLoop.step, m.agentLoop.maxToolStep, m.agentLoop.toolRounds))
		} else {
			lines = append(lines, fmt.Sprintf("  agent:       %s · step %d · %d tool round(s)",
				phase, m.agentLoop.step, m.agentLoop.toolRounds))
		}
		if last := strings.TrimSpace(m.agentLoop.lastTool); last != "" {
			lines = append(lines, fmt.Sprintf("  last tool:   %s (%s, %dms)",
				last, blankFallback(m.agentLoop.lastStatus, "?"), m.agentLoop.lastDuration))
		}
	} else {
		lines = append(lines, "  agent:       no tool rounds this session yet")
	}

	// Fan-out live counters.
	if m.telemetry.activeToolCount > 0 || m.telemetry.activeSubagentCount > 0 {
		lines = append(lines, fmt.Sprintf("  in-flight:   %d tool(s), %d subagent(s)", m.telemetry.activeToolCount, m.telemetry.activeSubagentCount))
	}

	// RTK-style compression wins — the headline token-miser metric.
	if m.telemetry.compressionRawChars > 0 {
		saved := m.telemetry.compressionSavedChars
		raw := m.telemetry.compressionRawChars
		pct := 0
		if raw > 0 {
			pct = int(float64(saved) / float64(raw) * 100)
		}
		lines = append(lines, fmt.Sprintf("  rtk savings: %s chars dropped (%d%% of %s raw output)",
			formatThousands(saved), pct, formatThousands(raw)))
	} else {
		lines = append(lines, "  rtk savings: (no tool output yet to compress)")
	}

	// Recent denials — short summary, full list lives in /approve.
	if m.eng != nil {
		if denials := m.eng.RecentDenials(); len(denials) > 0 {
			lines = append(lines, fmt.Sprintf("  denials:     %d blocked agent tool call(s) — see /approve", len(denials)))
		}

		// Prompt cache split — how much of the rendered system prompt
		// Anthropic can cache. Only visible when a sensible breakdown is
		// available; otherwise silent to keep the card tight.
		lastQuery := ""
		for i := len(m.chat.transcript) - 1; i >= 0; i-- {
			if m.chat.transcript[i].Role.Eq(chatRoleUser) {
				lastQuery = strings.TrimSpace(m.chat.transcript[i].Content)
				break
			}
		}
		rec := m.eng.PromptRecommendation(lastQuery)
		if rec.CacheableTokens+rec.DynamicTokens > 0 {
			lines = append(lines, fmt.Sprintf("  cache split: %d%% stable · %s cacheable / %s dynamic",
				rec.CacheablePercent,
				formatThousands(rec.CacheableTokens),
				formatThousands(rec.DynamicTokens)))
		}
	}

	return strings.Join(lines, "\n")
}

// describeWorkflow renders the high-level autonomous-workflow snapshot:
// todo list counts, active subagent fan-out, drive progress, and the
// latest available plan summary.
func (m Model) describeWorkflow() string {
	lines := []string{"▸ Workflow snapshot"}

	todos := m.workflowTodos()
	total, pending, doing, done := summarizeWorkflowTodos(todos)
	switch {
	case total == 0:
		lines = append(lines, "  todos:      no shared todo list yet (this session may still be on a single-step ask)")
	default:
		lines = append(lines, fmt.Sprintf("  todos:      %d total · %d pending · %d doing · %d done", total, pending, doing, done))
		for i, line := range formatWorkflowTodoLines(todos, 5) {
			prefix := "             "
			if i == 0 {
				prefix = "  now:        "
			}
			lines = append(lines, prefix+line)
		}
	}

	if m.telemetry.activeSubagentCount > 0 {
		lines = append(lines, fmt.Sprintf("  subagents:  %d active", m.telemetry.activeSubagentCount))
	} else {
		lines = append(lines, "  subagents:  idle")
	}
	for i, line := range m.recentWorkflowActivity("agent:subagent:", 3) {
		prefix := "             "
		if i == 0 {
			prefix = "  recent:     "
		}
		lines = append(lines, prefix+line)
	}

	if runID := strings.TrimSpace(m.telemetry.driveRunID); runID != "" {
		lines = append(lines, fmt.Sprintf("  drive:      %s Â· %d/%d done Â· %d blocked", runID, m.telemetry.driveDone, m.telemetry.driveTotal, m.telemetry.driveBlocked))
	} else {
		lines = append(lines, "  drive:      idle")
	}

	if summary := strings.TrimSpace(m.latestWorkflowPlanSummary()); summary != "" {
		lines = append(lines, "  plan:       "+summary)
	} else {
		lines = append(lines, "  plan:       no recent split/autonomy plan recorded")
	}

	lines = append(lines,
		"",
		"Shortcuts:",
		"  /todos shows the shared todo list",
		"  /subagents shows recent subagent fan-out",
		"  ctrl+y jumps to Plans Â· ctrl+g jumps to Activity",
	)
	return strings.Join(lines, "\n")
}

// describeTodos prints the current shared todo_write state directly into the
// chat transcript so the user can inspect the agent's checklist in-place.
func (m Model) describeTodos() string {
	lines := []string{"▸ Shared todo list"}
	todos := m.workflowTodos()
	total, pending, doing, done := summarizeWorkflowTodos(todos)
	if total == 0 {
		lines = append(lines,
			"  no todo list is active right now.",
			"  The autonomy preflight seeds this automatically for multi-step asks; /split can also preview a plan.",
		)
		return strings.Join(lines, "\n")
	}
	lines = append(lines, fmt.Sprintf("  total:      %d Â· %d pending Â· %d doing Â· %d done", total, pending, doing, done))
	for i, line := range formatWorkflowTodoLines(todos, 12) {
		lines = append(lines, fmt.Sprintf("  %2d. %s", i+1, line))
	}
	if len(todos) > 12 {
		lines = append(lines, fmt.Sprintf("  … %d more item(s) not shown here", len(todos)-12))
	}
	return strings.Join(lines, "\n")
}

// describeSubagents shows current fan-out plus the most recent subagent
// events mirrored into the Activity feed.
func (m Model) describeSubagents() string {
	lines := []string{"▸ Subagent activity"}
	if m.telemetry.activeSubagentCount > 0 {
		lines = append(lines, fmt.Sprintf("  active:     %d subagent(s) currently running", m.telemetry.activeSubagentCount))
	} else {
		lines = append(lines, "  active:     no subagents currently running")
	}

	recent := m.recentWorkflowActivity("agent:subagent:", 6)
	if len(recent) == 0 {
		lines = append(lines,
			"  recent:     no subagent events recorded this session",
			"  Tip: multi-step tasks can fan out through autonomy preflight, /split, orchestrate, or delegate_task.",
		)
		return strings.Join(lines, "\n")
	}
	for i, line := range recent {
		prefix := "             "
		if i == 0 {
			prefix = "  recent:     "
		}
		lines = append(lines, prefix+line)
	}
	lines = append(lines, "  jump:       ctrl+g opens Activity for the full event stream")
	return strings.Join(lines, "\n")
}

func (m Model) describePendingQueue() string {
	lines := []string{"▸ Pending chat queue"}
	if len(m.chat.pendingQueue) == 0 {
		lines = append(lines,
			"  state:      empty",
			"  note:       while a turn is streaming, normal follow-up prompts queue here",
			"  commands:   /queue clear · /queue drop N",
		)
		return strings.Join(lines, "\n")
	}
	lines = append(lines,
		fmt.Sprintf("  count:      %d queued message(s)", len(m.chat.pendingQueue)),
		"  commands:   /queue clear · /queue drop N",
	)
	for i, item := range m.chat.pendingQueue {
		lines = append(lines, fmt.Sprintf("  %2d. %s", i+1, truncateSingleLine(item, 120)))
	}
	return strings.Join(lines, "\n")
}

func (m Model) workflowTodos() []toolruntime.TodoItem {
	if m.eng == nil || m.eng.Tools == nil {
		return nil
	}
	return m.eng.Tools.TodoSnapshot()
}

func summarizeWorkflowTodos(todos []toolruntime.TodoItem) (total, pending, doing, done int) {
	total = len(todos)
	for _, it := range todos {
		switch strings.ToLower(strings.TrimSpace(it.Status)) {
		case "completed", "done":
			done++
		case "in_progress", "active", "doing":
			doing++
		default:
			pending++
		}
	}
	return total, pending, doing, done
}

func formatWorkflowTodoLines(todos []toolruntime.TodoItem, limit int) []string {
	if len(todos) == 0 || limit <= 0 {
		return nil
	}
	if limit > len(todos) {
		limit = len(todos)
	}
	out := make([]string, 0, limit)
	for _, it := range todos[:limit] {
		label := strings.TrimSpace(it.Content)
		if label == "" {
			label = "(untitled)"
		}
		switch strings.ToLower(strings.TrimSpace(it.Status)) {
		case "completed", "done":
			label = "[done] " + label
		case "in_progress", "active", "doing":
			active := strings.TrimSpace(it.ActiveForm)
			if active == "" {
				active = label
			}
			label = "[doing] " + active
		default:
			label = "[todo] " + label
		}
		out = append(out, truncateSingleLine(label, 100))
	}
	return out
}

func (m Model) recentWorkflowActivity(prefix string, limit int) []string {
	if limit <= 0 || len(m.activity.entries) == 0 {
		return nil
	}
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	out := make([]string, 0, limit)
	for i := len(m.activity.entries) - 1; i >= 0 && len(out) < limit; i-- {
		entry := m.activity.entries[i]
		eventID := strings.ToLower(strings.TrimSpace(entry.EventID))
		if prefix != "" && !strings.HasPrefix(eventID, prefix) {
			continue
		}
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		out = append(out, truncateSingleLine(text, 100))
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (m Model) recentWorkflowTimeline(limit int) []string {
	if limit <= 0 || len(m.activity.entries) == 0 {
		return nil
	}
	out := make([]string, 0, limit)
	now := time.Now()
	for i := len(m.activity.entries) - 1; i >= 0 && len(out) < limit; i-- {
		entry := m.activity.entries[i]
		eventID := strings.ToLower(strings.TrimSpace(entry.EventID))
		switch {
		case strings.HasPrefix(eventID, "tool:"),
			strings.HasPrefix(eventID, "drive:"),
			strings.HasPrefix(eventID, "agent:subagent:"),
			strings.HasPrefix(eventID, "agent:autonomy:"),
			strings.HasPrefix(eventID, "agent:loop:"),
			strings.HasPrefix(eventID, "provider:throttle:retry"):
		default:
			continue
		}
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		age := ""
		if !entry.At.IsZero() {
			age = formatSessionDuration(now.Sub(entry.At))
		}
		if age != "" {
			text = age + " ago · " + text
		}
		out = append(out, truncateSingleLine(text, 120))
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (m Model) latestWorkflowPlanSummary() string {
	if m.plans.plan != nil && len(m.plans.plan.Subtasks) > 0 {
		mode := "sequential"
		if m.plans.plan.Parallel {
			mode = "parallel"
		}
		return fmt.Sprintf("%d subtasks Â· %s Â· confidence %.2f", len(m.plans.plan.Subtasks), mode, m.plans.plan.Confidence)
	}
	for i := len(m.activity.entries) - 1; i >= 0; i-- {
		entry := m.activity.entries[i]
		if strings.EqualFold(strings.TrimSpace(entry.EventID), "agent:autonomy:plan") {
			return strings.TrimSpace(entry.Text)
		}
	}
	return ""
}

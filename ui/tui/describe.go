package tui

// describe.go — diagnostic & overview helpers that turn Model + Engine
// state into human-readable cards for the chat surface and slash
// commands (/stats, /doctor, /hooks, /approve, /export, /compact).
//
// Lifted out of the 10K-line tui.go god file (REPORT.md C1) so the
// "what does the system look like right now" surface lives in one
// obvious place. None of these mutate Engine state — they're pure
// reads — and most return a single multi-line string suitable for
// appendSystemMessage. compactTranscript is a transcript transform
// that only touches local view state.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/hooks"
	toolruntime "github.com/dontfuckmycode/dfmc/internal/tools"
)

func (m Model) exportTranscript(target string) (string, error) {
	if len(m.chat.transcript) == 0 {
		return "", fmt.Errorf("transcript is empty")
	}
	projectRoot := strings.TrimSpace(m.projectRoot())
	if projectRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve working directory: %w", err)
		}
		projectRoot = cwd
	}
	if target == "" {
		stamp := time.Now().Format("20060102-150405")
		target = filepath.Join(".dfmc", "exports", "transcript-"+stamp+".md")
	}
	// Resolve against project root when relative.
	if !filepath.IsAbs(target) {
		target = filepath.Join(projectRoot, target)
	}
	// Make sure the parent directory exists. MkdirAll is a no-op when it
	// already does, so safe to call unconditionally.
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("create export directory: %w", err)
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "# DFMC transcript — %s\n\n", time.Now().Format(time.RFC3339))
	if provider := strings.TrimSpace(m.status.Provider); provider != "" {
		fmt.Fprintf(&buf, "_provider:_ `%s`", provider)
		if model := strings.TrimSpace(m.status.Model); model != "" {
			fmt.Fprintf(&buf, " · _model:_ `%s`", model)
		}
		buf.WriteString("\n\n")
	}
	for _, line := range m.chat.transcript {
		role := strings.ToLower(strings.TrimSpace(string(line.Role)))
		content := strings.TrimRight(line.Content, "\n")
		if strings.TrimSpace(content) == "" {
			continue
		}
		switch role {
		case "user":
			fmt.Fprintf(&buf, "## user\n\n%s\n\n", content)
		case "assistant":
			fmt.Fprintf(&buf, "## assistant\n\n%s\n\n", content)
		case "tool":
			fmt.Fprintf(&buf, "### [tool] %s\n\n%s\n\n", strings.Join(line.ToolNames, ", "), content)
		case "system":
			fmt.Fprintf(&buf, "### [system]\n\n%s\n\n", content)
		default:
			fmt.Fprintf(&buf, "### [%s]\n\n%s\n\n", role, content)
		}
	}

	if err := os.WriteFile(target, []byte(buf.String()), 0o644); err != nil {
		return "", fmt.Errorf("write export file: %w", err)
	}
	return target, nil
}

// describeStats renders a one-card session-metrics snapshot: transcript
// size, agent loop progress, active tool fan-out, token budget fill,
// and RTK-style compression savings. Pure read over Model fields — no
// engine call, so it's cheap and safe to run mid-stream.
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
	lines := []string{"â–¸ Workflow snapshot"}

	todos := m.workflowTodos()
	total, pending, doing, done := summarizeWorkflowTodos(todos)
	switch {
	case total == 0:
		lines = append(lines, "  todos:      no shared todo list yet (this session may still be on a single-step ask)")
	default:
		lines = append(lines, fmt.Sprintf("  todos:      %d total Â· %d pending Â· %d doing Â· %d done", total, pending, doing, done))
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
	lines := []string{"â–¸ Shared todo list"}
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
		lines = append(lines, fmt.Sprintf("  â€¦ %d more item(s) not shown here", len(todos)-12))
	}
	return strings.Join(lines, "\n")
}

// describeSubagents shows current fan-out plus the most recent subagent
// events mirrored into the Activity feed.
func (m Model) describeSubagents() string {
	lines := []string{"â–¸ Subagent activity"}
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

// describeHealth renders a compact health snapshot: provider/model/AST
// readiness, tool surface, approval gate, hooks count, recent denials,
// memory store liveness. Intended as a "quick sanity check" the user
// runs from chat (/doctor or /health) without leaving the TUI. Full
// diagnostics still live in the `dfmc doctor` CLI (network, auto-fix).
func (m Model) describeHealth() string {
	lines := []string{"▸ DFMC health snapshot"}

	// Engine presence. If nil something is very wrong — but NewModel can
	// be passed nil in tests, so guard for it.
	if m.eng == nil {
		lines = append(lines, "  engine:   ✗ not initialized (no engine attached to model)")
		return strings.Join(lines, "\n")
	}

	// Provider profile. A misconfigured provider is the #1 reason users
	// report "agent isn't doing anything" — surface it first.
	provider := strings.TrimSpace(m.status.Provider)
	model := strings.TrimSpace(m.status.Model)
	providerLine := "?"
	switch {
	case provider == "":
		providerLine = "✗ no provider selected (run `dfmc config provider anthropic` or edit .dfmc/config.yaml)"
	case strings.EqualFold(provider, "offline") || strings.EqualFold(provider, "placeholder"):
		providerLine = fmt.Sprintf("◈ %s/%s — read-only (no mutating tool calls)", provider, blankFallback(model, "offline"))
	case !m.status.ProviderProfile.Configured:
		providerLine = fmt.Sprintf("⚠ %s/%s — profile not fully configured (missing API key or base_url?)", provider, blankFallback(model, "?"))
	default:
		providerLine = fmt.Sprintf("✓ %s/%s", provider, blankFallback(model, "?"))
	}
	lines = append(lines, "  provider: "+providerLine)

	// AST backend — regex is a warning because tree-sitter needs CGO.
	ast := strings.TrimSpace(m.status.ASTBackend)
	switch ast {
	case "":
		lines = append(lines, "  ast:      ⚠ backend unavailable")
	case "regex":
		lines = append(lines, "  ast:      ⚠ regex fallback (build with CGO_ENABLED=1 for tree-sitter)")
	default:
		lines = append(lines, "  ast:      ✓ "+ast)
	}

	// Tools surface.
	if m.eng.Tools == nil {
		lines = append(lines, "  tools:    ✗ engine.Tools is nil")
	} else {
		tools := m.eng.Tools.List()
		lines = append(lines, fmt.Sprintf("  tools:    ✓ %d registered", len(tools)))
	}

	// Memory store reachability. The degraded flag surfaces a failed
	// Memory.Load() from Init so the operator doesn't silently run on
	// an empty store when the bbolt file is corrupt or locked.
	switch {
	case m.eng.Memory == nil:
		lines = append(lines, "  memory:   ⚠ store not initialized")
	case m.status.MemoryDegraded:
		reason := strings.TrimSpace(m.status.MemoryLoadErr)
		if reason == "" {
			reason = "load failed"
		}
		lines = append(lines, "  memory:   ⚠ degraded — "+reason+" (running with empty store)")
	default:
		lines = append(lines, "  memory:   ✓ bbolt store open")
	}

	// Approval gate condensed to one line (/approve has the long form).
	gated := 0
	if m.eng.Config != nil {
		for _, s := range m.eng.Config.Tools.RequireApproval {
			if strings.TrimSpace(s) != "" {
				gated++
			}
		}
	}
	if gated == 0 {
		lines = append(lines, "  gate:     off — no tools require approval (/approve to learn more)")
	} else {
		lines = append(lines, fmt.Sprintf("  gate:     ON — %d tool(s) gated (/approve for details)", gated))
	}

	// Hooks count.
	hookTotal := 0
	for _, entries := range m.eng.Hooks.Inventory() {
		hookTotal += len(entries)
	}
	if hookTotal == 0 {
		lines = append(lines, "  hooks:    none registered (/hooks to see)")
	} else {
		lines = append(lines, fmt.Sprintf("  hooks:    %d registered (/hooks for details)", hookTotal))
	}

	// Recent denials — useful when user wonders why the agent "did
	// nothing" last turn.
	denials := m.eng.RecentDenials()
	if len(denials) > 0 {
		newest := denials[len(denials)-1]
		lines = append(lines, fmt.Sprintf("  denials:  %d this session — last: %s (%s ago)",
			len(denials), newest.Tool, time.Since(newest.At).Round(time.Second)))
	}

	// Project root — helps users verify DFMC is looking at the right tree.
	if root := strings.TrimSpace(m.projectRoot()); root != "" {
		lines = append(lines, "  project:  "+root)
	}

	return strings.Join(lines, "\n")
}

// describeHooks renders a snapshot of every lifecycle hook registered
// with the engine's dispatcher, grouped by event. Paired with /approve
// so the user can see the whole tool-lifecycle surface without digging
// through config.yaml. Returns a single multi-line string suitable for
// appendSystemMessage.
func (m Model) describeHooks() string {
	var dispatcher *hooks.Dispatcher
	if m.eng != nil {
		dispatcher = m.eng.Hooks
	}
	inventory := dispatcher.Inventory()
	lines := []string{"▸ Lifecycle hooks"}
	if len(inventory) == 0 {
		lines = append(lines,
			"  state:  none registered",
			"  enable: add entries under `hooks:` in .dfmc/config.yaml",
			"  events: user_prompt_submit, pre_tool, post_tool, session_start, session_end",
		)
		return strings.Join(lines, "\n")
	}
	// Render events in a stable order so repeated /hooks doesn't
	// reshuffle the output and confuse the reader.
	eventOrder := []hooks.Event{
		hooks.EventSessionStart,
		hooks.EventUserPromptSubmit,
		hooks.EventPreTool,
		hooks.EventPostTool,
		hooks.EventSessionEnd,
	}
	seen := make(map[hooks.Event]bool, len(eventOrder))
	for _, ev := range eventOrder {
		if entries, ok := inventory[ev]; ok {
			seen[ev] = true
			lines = append(lines, formatHookEvent(ev, entries)...)
		}
	}
	// Fold in any unknown events the dispatcher happened to register
	// (plugins, future additions) so nothing silently disappears.
	for ev, entries := range inventory {
		if seen[ev] {
			continue
		}
		lines = append(lines, formatHookEvent(ev, entries)...)
	}
	return strings.Join(lines, "\n")
}

// formatHookEvent emits a header line per event plus one line per hook.
// "cond=..." is only shown when the entry carries a condition expression
// — otherwise it adds noise.
func formatHookEvent(ev hooks.Event, entries []hooks.HookInventoryEntry) []string {
	out := make([]string, 0, 1+len(entries))
	out = append(out, fmt.Sprintf("  %s (%d)", ev, len(entries)))
	for _, h := range entries {
		name := strings.TrimSpace(h.Name)
		if name == "" {
			name = "(unnamed)"
		}
		cmd := truncateSingleLine(h.Command, 80)
		if cond := strings.TrimSpace(h.Condition); cond != "" {
			out = append(out, fmt.Sprintf("    · %s [cond: %s] → %s", name, cond, cmd))
		} else {
			out = append(out, fmt.Sprintf("    · %s → %s", name, cmd))
		}
	}
	return out
}

// describeApprovalGate returns a human-readable snapshot of the current
// tool-approval configuration for the /approve slash command. Lists the
// gated tools, whether a TUI approver is wired, and whether a prompt
// is currently pending. Read-only: editing the gate is a config change,
// not a slash action.
func (m Model) describeApprovalGate() string {
	var gated []string
	if m.eng != nil && m.eng.Config != nil {
		for _, raw := range m.eng.Config.Tools.RequireApproval {
			if s := strings.TrimSpace(raw); s != "" {
				gated = append(gated, s)
			}
		}
	}
	lines := []string{"▸ Tool approval gate"}
	if len(gated) == 0 {
		lines = append(lines,
			"  state:    off — no tools require approval (tools.require_approval is empty)",
			"  enable:   add tool names to .dfmc/config.yaml under tools.require_approval (or '*' for every tool)",
			"  bypass:   user-initiated /tool calls are never gated",
		)
	} else {
		lines = append(lines,
			"  state:    ON",
			"  gated:    "+strings.Join(gated, ", "),
			"  bypass:   user-initiated /tool calls are never gated; only agent/subagent calls prompt",
		)
	}
	if m.pendingApproval != nil {
		lines = append(lines, fmt.Sprintf("  pending:  %s (source=%s) — press y/enter to approve, n/esc to deny", m.pendingApproval.Req.Tool, m.pendingApproval.Req.Source))
	} else {
		lines = append(lines, "  pending:  none")
	}
	if m.eng != nil {
		denials := m.eng.RecentDenials()
		if len(denials) == 0 {
			lines = append(lines, "  recent:   no denials this session")
		} else {
			lines = append(lines, fmt.Sprintf("  recent:   %d denial(s) — newest first", len(denials)))
			// Walk oldest-first storage in reverse so the newest denial
			// is the first line the user reads.
			for i := len(denials) - 1; i >= 0; i-- {
				d := denials[i]
				age := time.Since(d.At).Round(time.Second)
				lines = append(lines, fmt.Sprintf("    · %s (%s, %s ago) — %s", d.Tool, d.Source, age, d.Reason))
			}
		}
	}
	return strings.Join(lines, "\n")
}

// compactTranscript collapses all transcript entries older than the last
// `keep` into a single system-role summary line so a long session stays
// scannable. Purely a view-layer operation — the engine's own memory and
// conversation store are untouched.
//
// Returns the new transcript, the number of lines that were collapsed,
// and ok=true iff there was actually something to compact. We compact
// only when there are older lines AND they include at least one user or
// assistant turn — summarising a tail of system/tool chatter gains
// nothing and just inflates the notice.
func compactTranscript(lines []chatLine, keep int) ([]chatLine, int, bool) {
	if keep <= 0 {
		keep = 1
	}
	if len(lines) <= keep {
		return lines, 0, false
	}
	head := lines[:len(lines)-keep]
	tail := lines[len(lines)-keep:]

	// Count by role so the summary carries a useful one-glance fingerprint
	// ("5 user turns, 5 assistant replies, 12 tool events, 2 system notes").
	users, assistants, tools, systems, other := 0, 0, 0, 0, 0
	for _, ln := range head {
		switch strings.ToLower(strings.TrimSpace(string(ln.Role))) {
		case "user":
			users++
		case "assistant":
			assistants++
		case "tool":
			tools++
		case "system":
			systems++
		default:
			other++
		}
	}
	if users == 0 && assistants == 0 && tools == 0 {
		// Only a run of system lines to collapse — not worth a summary.
		return lines, 0, false
	}
	fingerprint := make([]string, 0, 5)
	if users > 0 {
		fingerprint = append(fingerprint, fmt.Sprintf("%d user", users))
	}
	if assistants > 0 {
		fingerprint = append(fingerprint, fmt.Sprintf("%d assistant", assistants))
	}
	if tools > 0 {
		fingerprint = append(fingerprint, fmt.Sprintf("%d tool", tools))
	}
	if systems > 0 {
		fingerprint = append(fingerprint, fmt.Sprintf("%d system", systems))
	}
	if other > 0 {
		fingerprint = append(fingerprint, fmt.Sprintf("%d other", other))
	}
	summary := newChatLine(chatRoleSystem,
		fmt.Sprintf("▸ Transcript compacted — %s collapsed. Full history kept in Conversations panel.",
			strings.Join(fingerprint, ", ")))

	out := make([]chatLine, 0, 1+keep)
	out = append(out, summary)
	out = append(out, tail...)
	return out, len(head), true
}

// describeLastIntent prints the most recent intent-router decision in a
// dense chat-system block. Surfaces the rewrite, the routing reason, the
// classifier latency, and (when populated) the follow-up question. When
// no decision has fired yet (fresh session, intent layer disabled, or
// only fallbacks so far), the block explains why so the user can tell
// "the layer ran" from "the layer is off".
func (m Model) describeLastIntent() string {
	lines := []string{"▸ Intent layer"}
	if m.intent.lastDecisionAtMs == 0 {
		lines = append(lines,
			"  state:    no decisions yet this session",
			"  hint:     send a message; the engine will publish intent:decision events on every submit",
			"  toggles:  /intent (verbose), /intent show (this view)",
		)
		return strings.Join(lines, "\n")
	}
	when := time.UnixMilli(m.intent.lastDecisionAtMs)
	age := time.Since(when).Round(time.Second)
	lines = append(lines,
		fmt.Sprintf("  intent:   %s (source=%s, %s ago, %dms)",
			defaultStr(m.intent.lastIntent, "?"),
			defaultStr(m.intent.lastSource, "?"),
			age, m.intent.lastLatencyMs),
	)
	if m.intent.lastRaw != "" {
		lines = append(lines, "  raw:      "+truncateSingleLine(m.intent.lastRaw, 120))
	}
	if m.intent.lastEnriched != "" && m.intent.lastEnriched != m.intent.lastRaw {
		lines = append(lines, "  enriched: "+truncateSingleLine(m.intent.lastEnriched, 200))
	}
	if m.intent.lastReasoning != "" {
		lines = append(lines, "  reason:   "+truncateSingleLine(m.intent.lastReasoning, 200))
	}
	if m.intent.lastFollowUp != "" {
		lines = append(lines, "  follow:   "+truncateSingleLine(m.intent.lastFollowUp, 200))
	}
	verbose := "off"
	if m.intent.verbose {
		verbose = "on (transcript shows raw → enriched pairs)"
	}
	lines = append(lines, "  verbose:  "+verbose+" — toggle with /intent")
	return strings.Join(lines, "\n")
}

func defaultStr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

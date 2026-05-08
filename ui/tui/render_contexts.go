package tui

// render_contexts.go — Shift+F6 "Active Contexts" panel. A single
// dedicated screen surveying every concurrently-live agent context so
// the user can see at a glance:
//
//   - the main agent (provider/model + last status snapshot),
//   - any parked agent (token cumulative, last tool, parked-at age),
//   - the in-flight sub-agent count + concurrency cap,
//   - the active drive run with its currently-running TODO id,
//   - context-window pressure for whichever agent is foregrounded.
//
// Why a separate panel rather than a stats-panel mode: when several
// agents run together (main + a parked turn + multiple drive sub-
// agents) the chat composer tab can't grow tall enough to show them
// all without crowding the transcript. A dedicated overlay keeps the
// data dense and lets the user pop in/out without losing chat focus.
//
// Keyboard surface here is intentionally minimal — overlay scroll
// (j/k/g/G/pgup/pgdn) is shared via overlay_scroll_keys.go; esc closes.

import (
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

func (m Model) renderContextsView(width int) string {
	if width < 40 {
		width = 40
	}
	parts := []string{
		sectionHeader("◐", "Active Contexts"),
		subtleStyle.Render("shift+f6 jumps here · esc closes · every concurrently-live agent surfaces below"),
		renderDivider(min(width, 110)),
		"",
	}
	parts = append(parts, m.contextsMainSection(width)...)
	parts = append(parts, "")
	parts = append(parts, m.contextsParkedSection(width)...)
	parts = append(parts, "")
	parts = append(parts, m.contextsSubagentSection(width)...)
	parts = append(parts, "")
	parts = append(parts, m.contextsDriveSection(width)...)
	return strings.Join(parts, "\n")
}

func (m Model) contextsMainSection(width int) []string {
	out := []string{accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("MAIN AGENT")}
	st := m.status
	provider := blankFallback(st.Provider, "(none)")
	model := blankFallback(st.Model, "-")
	state := "idle"
	if m.chat.sending {
		state = "streaming"
	} else if m.agentLoop.active {
		state = "in tool loop"
	}
	rows := [][2]string{
		{"provider", provider},
		{"model", model},
		{"state", state},
	}
	if st.ContextIn != nil && st.ContextIn.ProviderMaxContext > 0 {
		// TokenCount is the most recent context-build footprint; pair
		// with ProviderMaxContext to give a percent so the user can
		// see how close to the ceiling each agent's last build ran.
		used := st.ContextIn.TokenCount
		maxCtx := st.ContextIn.ProviderMaxContext
		pct := 0
		if maxCtx > 0 {
			pct = int(int64(used) * 100 / int64(maxCtx))
		}
		rows = append(rows, [2]string{"window", fmt.Sprintf("%s / %s (%d%%)", compactMetric(used), compactMetric(maxCtx), pct)})
	}
	if m.agentLoop.active {
		if step := m.agentLoop.step; step > 0 {
			rows = append(rows, [2]string{"step", fmt.Sprintf("%d", step)})
		}
		if phase := strings.TrimSpace(m.agentLoop.phase); phase != "" {
			rows = append(rows, [2]string{"phase", phase})
		}
	}
	out = append(out, renderContextRows(rows, width)...)
	return out
}

func (m Model) contextsParkedSection(width int) []string {
	out := []string{accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("PARKED AGENT")}
	if m.eng == nil || !m.eng.HasParkedAgent() {
		out = append(out, "  "+subtleStyle.Render("(no parked agent — /continue would have nothing to resume)"))
		return out
	}
	d, ok := m.eng.ParkedAgentDetails()
	if !ok || d == nil {
		out = append(out, "  "+subtleStyle.Render("(parked but no details — engine internal state out of sync)"))
		return out
	}
	q := truncateForLine(d.Question, max(width-12, 40))
	rows := [][2]string{
		{"question", q},
		{"step", fmt.Sprintf("%d (cumulative %d)", d.Step, d.CumulativeSteps)},
		{"tokens", fmt.Sprintf("%s (cumulative %s)", compactMetric(d.TotalTokens), compactMetric(d.CumulativeTokens))},
		{"context", compactMetric(d.ContextTokens)},
		{"provider/model", strings.TrimSpace(d.LastProvider+" / "+d.LastModel)},
	}
	if d.LastToolName != "" {
		rows = append(rows, [2]string{"last tool", d.LastToolName})
	}
	if !d.ParkedAt.IsZero() {
		rows = append(rows, [2]string{"parked", humaniseAge(time.Since(d.ParkedAt)) + " ago"})
	}
	out = append(out, renderContextRows(rows, width)...)
	out = append(out, "  "+subtleStyle.Render("/continue resumes from here · /cancel discards"))
	return out
}

func (m Model) contextsSubagentSection(width int) []string {
	_ = width
	out := []string{accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("SUB-AGENTS")}
	if m.eng == nil {
		out = append(out, "  "+subtleStyle.Render("(engine offline)"))
		return out
	}
	active := m.eng.SubagentInFlight()
	limit := m.eng.SubagentConcurrencyLimit()
	if active == 0 {
		out = append(out, "  "+subtleStyle.Render(fmt.Sprintf("none in flight · concurrency cap %d", limit)))
		return out
	}
	out = append(out, "  "+accentStyle.Render(fmt.Sprintf("%d in flight", active))+
		subtleStyle.Render(fmt.Sprintf(" / cap %d", limit)))
	out = append(out, "  "+subtleStyle.Render("Drive routes its TODOs through here. Per-subagent details land in Activity (F5) and Workflow (F4)."))
	return out
}

func (m Model) contextsDriveSection(width int) []string {
	_ = width
	out := []string{accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("DRIVE")}
	var active *drive.Run
	for _, r := range m.workflow.runs {
		if r == nil {
			continue
		}
		if r.Status == drive.RunRunning || r.Status == drive.RunPlanning {
			active = r
			break
		}
	}
	if active == nil {
		out = append(out, "  "+subtleStyle.Render("(no active drive run — start one with /drive <task>)"))
		return out
	}
	done, blocked, skipped, pending := active.Counts()
	running := 0
	for _, t := range active.Todos {
		if t.Status == drive.TodoRunning {
			running++
		}
	}
	rows := [][2]string{
		{"id", active.ID},
		{"task", truncateForLine(active.Task, 80)},
		{"status", string(active.Status)},
		{"todos", fmt.Sprintf("%d done · %d running · %d pending · %d blocked · %d skipped", done, running, pending, blocked, skipped)},
	}
	for _, t := range active.Todos {
		if t.Status == drive.TodoRunning {
			rows = append(rows, [2]string{"running todo", fmt.Sprintf("%s — %s", t.ID, truncateForLine(t.Title, 70))})
		}
	}
	out = append(out, renderContextRows(rows, width)...)
	out = append(out, "  "+subtleStyle.Render("F4 Workflow has the full TODO tree · /drive stop "+active.ID))
	return out
}

func renderContextRows(rows [][2]string, width int) []string {
	_ = width
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, fmt.Sprintf("    %-16s %s",
			subtleStyle.Render(r[0]),
			r[1]))
	}
	return out
}

func humaniseAge(d time.Duration) string {
	if d < time.Second {
		return "just now"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

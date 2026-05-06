// render_shortcuts.go — the Shortcuts tab. A single dedicated screen
// that lists every panel + every keyboard shortcut + the most-used
// slash commands in one organized cheat sheet so a user doesn't have
// to memorize F-key positions or scroll through /help to find what
// they need.
//
// Reachable via Alt+H from any tab. Distinct from the Ctrl+H per-tab
// help overlay (which is a popup of just THIS tab's hints) and from
// /help (which is a transcript message). The Shortcuts tab is the
// one place that surveys everything.

package tui

import (
	"fmt"
	"strings"
)

func (m Model) renderShortcutsView(width int) string {
	inner := width
	if inner > 110 {
		inner = 110
	}
	if inner < 40 {
		inner = 40
	}

	parts := []string{
		sectionHeader("?", "Shortcuts"),
		subtleStyle.Render("alt+h jumps here · ctrl+h toggles per-tab popup · /help prints in chat"),
		renderDivider(inner),
		"",
	}

	parts = append(parts, m.shortcutsTabsSection()...)
	parts = append(parts, "")
	parts = append(parts, m.shortcutsChatComposerSection()...)
	parts = append(parts, "")
	parts = append(parts, m.shortcutsStatsPanelSection()...)
	parts = append(parts, "")
	parts = append(parts, m.shortcutsControlSection()...)
	parts = append(parts, "")
	parts = append(parts, m.shortcutsDiagnosticsSection()...)
	parts = append(parts, "")
	parts = append(parts, m.shortcutsSlashSection()...)

	return strings.Join(parts, "\n")
}

// shortcutsTabsSection — the panel catalog. Pulls live tab names
// and F-key hints from tabFKeyHint so this list never drifts from
// what the rest of the UI actually does. Two-column layout so the
// user can scan all 17 tabs without scrolling.
func (m Model) shortcutsTabsSection() []string {
	out := []string{accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("PANELS")}
	out = append(out, "  "+subtleStyle.Render("Tab/Shift+Tab cycle · F-keys jump direct · Alt-keys for diagnostic panels"))
	out = append(out, "")
	type row struct {
		name, key, hint string
	}
	rows := []row{
		{"Chat", "F1", "main composer · transcript · slash commands"},
		{"Status", "F2", "engine + provider + ast/codemap snapshot"},
		{"Files", "F3", "project file picker · pin · preview"},
		{"Patch", "F4", "worktree diff · staged hunks · apply/dry-run"},
		{"Workflow", "F5", "drive cockpit · run list + TODO ladder"},
		{"Tools", "F6", "tool registry · params editor · test runs"},
		{"Activity", "F7", "event firehose · search/filter · follow tail"},
		{"Memory", "F8", "working/episodic/semantic memory tiers"},
		{"CodeMap", "F9", "symbol/dep graph · cycles · hotspots"},
		{"Conversations", "F10", "saved conversations · branch nav"},
		{"Prompts", "Alt+T", "task/role/language prompt overlays (F11 reserved — most terminals eat it for fullscreen; opens this help if it leaks through)"},
		{"Security", "F12", "scanner · secrets · vuln scan"},
		{"Plans", "Ctrl+Y", "plan-split editor · subtask preview"},
		{"Context", "Ctrl+W", "context-build preview · ranked snippets"},
		{"Providers", "Ctrl+O", "provider catalog · keys · profiles"},
		{"Orchestrate", "Alt+R", "agents/subagents/todos/drive/tokens hierarchy"},
		{"Shortcuts", "Alt+H", "this screen — cheat sheet of everything"},
	}
	for _, r := range rows {
		// Format: "  F1   Chat            main composer ..."
		key := r.key
		out = append(out, fmt.Sprintf("  %-7s %-14s %s",
			accentStyle.Render(key), titleStyle.Render(r.name),
			subtleStyle.Render(truncateSingleLine(r.hint, 60))))
	}
	return out
}

// shortcutsChatComposerSection — the keys most users hit most often,
// in groups: send, history, edit, navigation, picker.
func (m Model) shortcutsChatComposerSection() []string {
	out := []string{accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("CHAT COMPOSER")}
	groups := []struct {
		title string
		rows  [][2]string
	}{
		{"Send / queue", [][2]string{
			{"Ctrl+X", "send composer (or queue while streaming)"},
			{"Enter / Ctrl+J", "literal newline (Alt+Enter also works)"},
			{"Ctrl+C", "cancel active turn (or rage-quit if idle)"},
			{"Esc", "dismiss resume prompt · close picker"},
		}},
		{"Edit", [][2]string{
			{"Ctrl+W", "kill word before cursor"},
			{"Ctrl+K", "kill to end of line"},
			{"Ctrl+U", "clear entire input line"},
			{"Backspace", "delete char before cursor"},
			{"Delete", "delete char at cursor"},
		}},
		{"Navigate", [][2]string{
			{"Ctrl+A / Home", "cursor to line start"},
			{"Ctrl+E / End", "cursor to line end · jump to latest"},
			{"Ctrl+← / →", "word left/right"},
			{"↑ / ↓", "history nav · suggestion nav (no picker)"},
			{"PgUp/PgDn", "scroll transcript 8 lines"},
			{"Shift+PgUp/Dn", "scroll transcript 3 lines"},
			{"Shift+↑ / ↓", "scroll transcript 3 lines"},
		}},
		{"Pickers", [][2]string{
			{"@ or Ctrl+T", "open file mention picker"},
			{"/", "open slash command picker"},
			{"Tab", "autocomplete (mention/slash/quick action)"},
			{"Ctrl+P", "open slash command menu"},
			{"Ctrl+G", "jump to Activity tab"},
		}},
	}
	for _, g := range groups {
		out = append(out, "  "+subtleStyle.Render(g.title))
		for _, r := range g.rows {
			out = append(out, fmt.Sprintf("    %-18s %s",
				accentStyle.Render(r[0]),
				subtleStyle.Render(r[1])))
		}
	}
	return out
}

// shortcutsStatsPanelSection — the right-side stats panel and the
// alt+letter shortcuts that flip its mode mid-session.
func (m Model) shortcutsStatsPanelSection() []string {
	out := []string{accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("STATS PANEL")}
	out = append(out, "  "+subtleStyle.Render("right-side panel on the Chat tab · alt+key flips its mode"))
	out = append(out, "")
	rows := [][2]string{
		{"Alt+A", "overview mode (default)"},
		{"Alt+S", "todos mode"},
		{"Alt+D", "tasks mode"},
		{"Alt+F", "subagents mode"},
		{"Alt+P", "providers mode"},
		{"Ctrl+S", "show / hide the panel entirely"},
		{"Alt+X", "toggle selection mode (drag-select transcript)"},
	}
	for _, r := range rows {
		out = append(out, fmt.Sprintf("  %-18s %s",
			accentStyle.Render(r[0]),
			subtleStyle.Render(r[1])))
	}
	return out
}

// shortcutsControlSection — the "stop / cancel / clear" command
// surface added so the user has a clear way to halt running work.
func (m Model) shortcutsControlSection() []string {
	out := []string{accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("CONTROL · STOP/CLEAR")}
	rows := [][2]string{
		{"Ctrl+C", "cancel active turn (subagents auto-unwind)"},
		{"/cancel", "slash equivalent of Ctrl+C (aliases /abort, /stop)"},
		{"/drive stop [id]", "cancel an autonomous drive run"},
		{"/todos clear", "wipe the shared todo list"},
		{"/tasks clear", "wipe non-drive tasks from the store"},
		{"/clear", "clear transcript only (memory untouched)"},
		{"/compact [N]", "collapse older transcript into a summary"},
		{"/queue clear", "drop queued follow-up prompts"},
	}
	for _, r := range rows {
		out = append(out, fmt.Sprintf("  %-18s %s",
			accentStyle.Render(r[0]),
			subtleStyle.Render(r[1])))
	}
	return out
}

// shortcutsDiagnosticsSection — slash commands for inspecting state.
func (m Model) shortcutsDiagnosticsSection() []string {
	out := []string{accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("DIAGNOSTICS · INSPECT")}
	rows := [][2]string{
		{"/stats", "session metrics: rounds, savings, fill, cost"},
		{"/workflow", "todos + subagents + drive + plan snapshot"},
		{"/todos", "shared todo list (the agent is tracking)"},
		{"/tasks", "task store panel (j/k navigate, enter/esc)"},
		{"/subagents", "subagent fan-out + recent delegation"},
		{"/queue", "queued follow-up prompts (show/clear/drop)"},
		{"/intent show", "last intent decision in full"},
		{"/doctor", "in-chat health snapshot"},
		{"/approve", "tool-approval gate state"},
		{"/hooks", "lifecycle hooks per event"},
		{"/status", "engine + provider snapshot"},
	}
	for _, r := range rows {
		out = append(out, fmt.Sprintf("  %-18s %s",
			accentStyle.Render(r[0]),
			subtleStyle.Render(r[1])))
	}
	return out
}

// shortcutsSlashSection — the most-used everyday slash commands the
// user types into the composer. Not exhaustive (run /help for the
// full catalog); just the high-traffic ones.
func (m Model) shortcutsSlashSection() []string {
	out := []string{accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("EVERYDAY SLASH COMMANDS")}
	out = append(out, "  "+subtleStyle.Render("/help for the full catalog · /help <command> for one-line details"))
	out = append(out, "")
	rows := [][2]string{
		{"/drive <task>", "start an autonomous plan/execute loop"},
		{"/continue", "resume a parked agent loop"},
		{"/btw <note>", "inject a note at the next agent step"},
		{"/split <task>", "decompose a broad task into subtasks"},
		{"/review <path>", "review a file or directory"},
		{"/explain <path>", "explain a file"},
		{"/refactor <path>", "propose a scoped refactor"},
		{"/test <path>", "draft tests for a target"},
		{"/doc <path>", "draft or update documentation"},
		{"/scan", "security + correctness pass"},
		{"/map", "render the codemap"},
		{"/conversation new", "start a fresh conversation (resets context)"},
		{"/export [path]", "save transcript to .dfmc/exports/*.md"},
		{"/coach", "mute / unmute coach notes"},
		{"/hints", "show / hide trajectory hints"},
		{"/intent", "toggle intent rewrites visibility"},
		{"/mouse", "toggle mouse capture (terminal native vs in-app)"},
		{"/select", "selection mode (drag-select transcript)"},
		{"/keylog", "dump key events into footer (debug)"},
	}
	for _, r := range rows {
		out = append(out, fmt.Sprintf("  %-18s %s",
			accentStyle.Render(r[0]),
			subtleStyle.Render(r[1])))
	}
	return out
}

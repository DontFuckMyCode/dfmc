// render_shortcuts.go — the Shortcuts tab. A single dedicated screen
// that lists every panel + every keyboard shortcut + the most-used
// slash commands in one organized cheat sheet so a user doesn't have
// to memorize F-key positions or scroll through /help to find what
// they need.
//
// Reachable via Shift+F5 from any tab. Distinct from the Ctrl+H per-tab
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
		subtleStyle.Render("shift+f5 jumps here · ctrl+h toggles per-tab popup · /help prints in chat"),
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
// user can scan every panel without scrolling.
func (m Model) shortcutsTabsSection() []string {
	out := []string{accentStyle.Bold(true).Render("▣") + " " + sectionTitleStyle.Render("PANELS")}
	out = append(out, "  "+subtleStyle.Render("Use F1 to return to Chat. Ctrl+B opens every panel. F1..F12 plus Shift+F1..F8 cover the direct map."))
	out = append(out, "")
	type row struct {
		name, key, hint string
	}
	rows := []row{
		// F1..F8 step through the 8 first-class tabs in tab-strip order.
		// Alt+1..Alt+8 mirror them for terminals that swallow F-keys.
		{"Chat", "F1", "main composer · transcript · slash commands"},
		{"Files", "F2", "project file picker · pin · preview"},
		{"Patch", "F3", "worktree diff · staged hunks · apply/dry-run"},
		{"Workflow", "F4", "drive cockpit · run list + TODO ladder"},
		{"Activity", "F5", "event firehose · search/filter · follow tail"},
		{"Memory", "F6", "working/episodic/semantic memory tiers"},
		{"Conversations", "F7", "saved conversations · branch nav"},
		{"Providers", "F8", "provider catalog · keys · profiles"},
		// F9..F12 reach the four most-trafficked demoted overlays.
		{"Status", "F9", "engine + provider + ast/codemap snapshot"},
		{"CodeMap", "F10", "symbol/dep graph · cycles · hotspots"},
		{"Tools", "F11", "tool registry · params editor · test runs"},
		{"Security", "F12", "scanner · secrets · vuln scan"},
		// Shift+F1..Shift+F8 reach the rest. Most terminals emit F13..F20
		// codes for these — both shapes are bound where the terminal sends them.
		{"Prompts", "Shift+F1", "task/role/language prompt overlays"},
		{"Plans", "Shift+F2", "plan-split editor · subtask preview"},
		{"Context", "Shift+F3", "context-build preview · ranked snippets"},
		{"Orchestrate", "Shift+F4", "agents/subagents/todos/drive/tokens hierarchy"},
		{"Shortcuts", "Shift+F5", "this screen — cheat sheet of everything"},
		{"Contexts", "Shift+F6", "live agents — main · parked · subagents · drive run"},
		{"ProviderLog", "Shift+F7", "provider call log · prompts/replies/tokens"},
		{"Telegram", "Shift+F8", "telegram bot messages · connection status"},
		{"ToolStatus", "Ctrl+Shift+T", "tool call history · enter toggles expanded body (full params/result/error)"},
	}
	for _, r := range rows {
		// Format: "  F1       Chat            main composer ..."
		key := r.key
		out = append(out, fmt.Sprintf("  %-9s %-14s %s",
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
			{"Enter", "send composer"},
			{"Alt+Enter", "literal newline"},
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
			{"Shift+↑ / ↓", "scroll transcript 3 lines (fine)"},
			{"Ctrl+Home/End", "jump to top / latest of transcript"},
			{"Wheel", "mouse-wheel scroll (Shift+wheel: page step)"},
		}},
		{"Pickers", [][2]string{
			{"@ or Ctrl+T", "open file mention picker"},
			{"/", "open slash command picker"},
			{"Tab", "autocomplete (mention/slash/quick action)"},
			{"Ctrl+P", "open slash command menu"},
			{"Ctrl+G", "jump to Activity tab"},
		}},
		{"Yank / find (composer empty)", [][2]string{
			{"Ctrl+Y", "copy last assistant response (OSC 52)"},
			{"/history search", "scan transcript for a substring"},
			{"/next /prev", "step between matched rows"},
			{"/jump N", "scroll to assistant turn #N"},
			{"/expand N|all", "open a collapsed long assistant turn"},
			{"/toolshow N", "dump full tool event detail inline"},
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

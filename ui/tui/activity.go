package tui

// activity.go - the Activity panel is the TUI's mission-control surface:
// a searchable, filterable event timeline with a detail inspector. Other
// panels summarize state; this one answers "what just happened?" without
// making the user leave the terminal.

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// maxActivityEntries caps memory use; at ~300 bytes per entry this still lands
// comfortably under a megabyte while keeping plenty of live history.
const maxActivityEntries = 2000

const activityDefaultRenderHeight = 24

type activityKind string
type activityViewMode string
type activityActionTarget string

const (
	activityKindInfo   activityKind = "info"
	activityKindAgent  activityKind = "agent"
	activityKindTool   activityKind = "tool"
	activityKindStream activityKind = "stream"
	activityKindError  activityKind = "error"
	activityKindCtx    activityKind = "context"
	activityKindIndex  activityKind = "index"
)

const (
	activityViewAll      activityViewMode = "all"
	activityViewTools    activityViewMode = "tools"
	activityViewAgents   activityViewMode = "agents"
	activityViewErrors   activityViewMode = "errors"
	activityViewWorkflow activityViewMode = "workflow"
	activityViewContext  activityViewMode = "context"
)

var activityViewModes = []activityViewMode{
	activityViewAll,
	activityViewTools,
	activityViewAgents,
	activityViewErrors,
	activityViewWorkflow,
	activityViewContext,
}

const (
	activityTargetStatus    activityActionTarget = "status"
	activityTargetFiles     activityActionTarget = "files"
	activityTargetPatch     activityActionTarget = "patch"
	activityTargetTools     activityActionTarget = "tools"
	activityTargetPlans     activityActionTarget = "plans"
	activityTargetContext   activityActionTarget = "context"
	activityTargetCodeMap   activityActionTarget = "codemap"
	activityTargetSecurity  activityActionTarget = "security"
	activityTargetProviders activityActionTarget = "providers"
)

type activityEntry struct {
	At       time.Time
	Kind     activityKind
	EventID  string
	Source   string
	Tool     string
	Path     string
	Provider string
	Query    string
	Text     string
	Details  []string
	Count    int
}

func (m *Model) recordActivityEvent(ev engine.Event) {
	prevVisible := 0
	if !m.activity.follow {
		prevVisible = len(m.filteredActivityEntries())
	}
	kind, text := classifyActivity(ev)
	if text == "" {
		text = strings.TrimSpace(ev.Type)
	}
	if text == "" {
		return
	}
	payload, _ := toStringAnyMap(ev.Payload)
	at := ev.Timestamp
	if at.IsZero() {
		at = time.Now()
	}
	entry := activityEntry{
		At:       at,
		Kind:     kind,
		EventID:  strings.TrimSpace(ev.Type),
		Source:   strings.TrimSpace(ev.Source),
		Tool:     payloadString(payload, "tool", ""),
		Path:     extractActivityPath(ev),
		Provider: extractActivityProvider(ev),
		Query:    extractActivityQuery(ev, text),
		Text:     truncateActivityText(text, 200),
		Details:  buildActivityDetailLines(ev, text),
		Count:    1,
	}

	if n := len(m.activity.entries); n > 0 {
		last := &m.activity.entries[n-1]
		if last.EventID == entry.EventID && last.Text == entry.Text {
			last.Count++
			last.At = entry.At
			if last.Source == "" {
				last.Source = entry.Source
			}
			if len(last.Details) == 0 {
				last.Details = entry.Details
			}
			return
		}
	}

	m.activity.entries = append(m.activity.entries, entry)
	if len(m.activity.entries) > maxActivityEntries {
		drop := len(m.activity.entries) - maxActivityEntries
		m.activity.entries = m.activity.entries[drop:]
	}
	if m.activity.follow {
		m.activity.scroll = 0
	} else {
		// Hold the user's selected event in place only when the active
		// filter/query actually gained visible rows.
		if nextVisible := len(m.filteredActivityEntries()); nextVisible > prevVisible {
			m.activity.scroll += nextVisible - prevVisible
		}
		m.activity.scroll = clampActivityOffset(m.activity.scroll, len(m.filteredActivityEntries()))
	}
}

// classifyActivity maps an engine event onto a short display line +
// coloring category. Unknown events fall through as info/typename.
func classifyActivity(ev engine.Event) (activityKind, string) {
	kind := activityKindInfo
	t := strings.ToLower(strings.TrimSpace(ev.Type))
	payload, _ := toStringAnyMap(ev.Payload)

	switch {
	case strings.HasPrefix(t, "agent:"):
		kind = activityKindAgent
	case strings.HasPrefix(t, "tool:"):
		kind = activityKindTool
	case strings.HasPrefix(t, "stream:"):
		kind = activityKindStream
	case strings.HasPrefix(t, "context:"), strings.HasPrefix(t, "ctx:"):
		kind = activityKindCtx
	case strings.HasPrefix(t, "index:"):
		kind = activityKindIndex
	case strings.Contains(t, "error"), strings.Contains(t, "fail"):
		kind = activityKindError
	}

	text := t
	switch t {
	case "tool:call":
		name := payloadString(payload, "tool", "tool")
		step := payloadInt(payload, "step", 0)
		if step > 0 {
			text = fmt.Sprintf("tool call - %s (step %d)", name, step)
		} else {
			text = "tool call - " + name
		}
	case "tool:result":
		name := payloadString(payload, "tool", "tool")
		dur := payloadIntAny(payload, 0, "duration_ms", "durationMs")
		text = fmt.Sprintf("tool done - %s (%dms)", name, dur)
	case "tool:error":
		name := payloadString(payload, "tool", "tool")
		err := payloadString(payload, "error", "")
		text = fmt.Sprintf("tool failed - %s %s", name, err)
		kind = activityKindError
	case "agent:loop:start":
		prov := payloadString(payload, "provider", "")
		model := payloadString(payload, "model", "")
		protocol := payloadString(payload, "protocol", "")
		baseURL := payloadString(payload, "base_url", "")
		host := ""
		if parsed, err := url.Parse(baseURL); err == nil {
			host = strings.TrimSpace(parsed.Host)
		}
		max := payloadInt(payload, "max_tool_steps", 0)
		text = fmt.Sprintf("agent start - %s/%s", prov, model)
		if protocol != "" {
			text += " " + protocol
		}
		if host != "" {
			text += " " + host
		}
		text += fmt.Sprintf(" max=%d", max)
	case "agent:loop:thinking":
		step := payloadInt(payload, "step", 0)
		max := payloadInt(payload, "max_tool_steps", 0)
		text = fmt.Sprintf("agent thinking - %d/%d", step, max)
	case "agent:autonomy:plan":
		count := payloadInt(payload, "subtask_count", 0)
		confidence := 0.0
		if raw, ok := payload["confidence"].(float64); ok {
			confidence = raw
		}
		mode := "sequential"
		if payloadBool(payload, "parallel", false) {
			mode = "parallel"
		}
		scope := payloadString(payload, "scope", "")
		text = fmt.Sprintf("autonomy preflight - %d subtasks %s %.2f", count, mode, confidence)
		if scope != "" && scope != "top_level" {
			text = fmt.Sprintf("autonomy preflight [%s] - %d subtasks %s %.2f", scope, count, mode, confidence)
		}
	case "agent:autonomy:kickoff":
		toolName := payloadString(payload, "tool", "orchestrate")
		count := payloadInt(payload, "subtask_count", 0)
		confidence := 0.0
		if raw, ok := payload["confidence"].(float64); ok {
			confidence = raw
		}
		text = fmt.Sprintf("autonomy kickoff - %s %d subtasks %.2f", toolName, count, confidence)
	case "agent:loop:end":
		reason := payloadString(payload, "reason", "done")
		text = "agent end - " + reason
	case "agent:loop:error":
		text = "agent error - " + payloadString(payload, "error", "")
		kind = activityKindError
	case "provider:throttle:retry":
		prov := payloadString(payload, "provider", "?")
		attempt := payloadInt(payload, "attempt", 0)
		waitMs := payloadInt(payload, "wait_ms", 0)
		mode := "request"
		if payloadBool(payload, "stream", false) {
			mode = "stream"
		}
		text = fmt.Sprintf("provider throttled - %s %s retry #%d in %dms", prov, mode, attempt, waitMs)
		kind = activityKindError
	case "config:reload:auto":
		path := payloadString(payload, "path", "")
		text = "config auto-reloaded"
		if path != "" {
			text += " - " + truncateSingleLine(path, 96)
		}
	case "config:reload:auto_failed":
		errText := payloadString(payload, "error", "")
		text = "config auto-reload failed"
		if errText != "" {
			text += " - " + truncateSingleLine(errText, 120)
		}
		kind = activityKindError
	case "context:lifecycle:compacted":
		before := payloadIntAny(payload, 0, "before_tokens", "tokens_before")
		after := payloadIntAny(payload, 0, "after_tokens", "tokens_after")
		text = fmt.Sprintf("context compacted - %d -> %d tok", before, after)
	case "context:lifecycle:handoff":
		text = "context handoff"
	case "index:start":
		text = "index start"
	case "index:done":
		files := payloadInt(payload, "files", 0)
		text = fmt.Sprintf("index done - %d files", files)
	case "index:error":
		text = "index error - " + payloadString(payload, "error", "")
		kind = activityKindError
	case "engine:initializing", "engine:ready", "engine:serving", "engine:shutdown", "engine:stopped":
		text = strings.TrimPrefix(t, "engine:")
	case "stream:delta":
		text = "stream delta"
	case "stream:start":
		text = "stream start"
	case "stream:done":
		text = "stream done"
	default:
		if s, ok := ev.Payload.(string); ok && s != "" {
			text = t + " - " + s
		}
	}
	return kind, text
}


func (m Model) filteredActivityEntries() []activityEntry {
	mode := m.activity.mode
	if mode == "" {
		mode = activityViewAll
	}
	query := strings.TrimSpace(m.activity.query)
	filtered := make([]activityEntry, 0, len(m.activity.entries))
	for _, entry := range m.activity.entries {
		if !activityMatchesMode(entry, mode) || !activityMatchesQuery(entry, query) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func clampActivityOffset(scroll, total int) int {
	if total <= 0 {
		return 0
	}
	if scroll < 0 {
		return 0
	}
	if scroll >= total {
		return total - 1
	}
	return scroll
}

func activitySelectedIndex(total, scroll int) int {
	if total <= 0 {
		return -1
	}
	scroll = clampActivityOffset(scroll, total)
	return total - 1 - scroll
}

func formatActivityLine(entry activityEntry, width int, selected bool) string {
	ts := entry.At.Format("15:04:05")
	icon := kindIcon(entry.Kind)
	count := ""
	if entry.Count > 1 {
		count = subtleStyle.Render(fmt.Sprintf(" x%d", entry.Count))
	}
	prefix := "  "
	if selected {
		prefix = accentStyle.Render("› ")
	}
	line := prefix + subtleStyle.Render(ts) + " " + icon + " " + entry.Text + count
	line = truncateSingleLine(line, width)
	if selected {
		line = lipgloss.NewStyle().
			Foreground(colorTitleFg).
			Background(colorAccent).
			Bold(true).
			Render(line)
	}
	return line
}

func renderActivityPane(title string, body []string, width, height int) string {
	if height < 3 {
		height = 3
	}
	lines := []string{
		accentStyle.Bold(true).Render(title),
		renderDivider(max(width-1, 1)),
	}
	lines = append(lines, body...)
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lipgloss.NewStyle().Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

func activityTargetForEntry(entry activityEntry) activityActionTarget {
	eventID := strings.ToLower(strings.TrimSpace(entry.EventID))
	text := strings.ToLower(strings.TrimSpace(entry.Text))
	switch {
	case strings.HasPrefix(eventID, "provider:"):
		return activityTargetProviders
	case strings.HasPrefix(eventID, "drive:"),
		strings.HasPrefix(eventID, "agent:autonomy:"),
		strings.HasPrefix(eventID, "agent:subagent:"):
		return activityTargetPlans
	case strings.HasPrefix(eventID, "security:"),
		strings.Contains(eventID, "secret"),
		strings.Contains(eventID, "vuln"),
		strings.Contains(text, "secret"),
		strings.Contains(text, "vulnerability"):
		return activityTargetSecurity
	case strings.HasPrefix(eventID, "context:"),
		strings.HasPrefix(eventID, "ctx:"):
		return activityTargetContext
	case strings.HasPrefix(eventID, "index:"):
		return activityTargetCodeMap
	case strings.HasPrefix(eventID, "tool:") && isMutationTool(entry.Tool):
		return activityTargetPatch
	case strings.HasPrefix(eventID, "tool:") && strings.TrimSpace(entry.Path) != "":
		return activityTargetFiles
	case strings.HasPrefix(eventID, "tool:"):
		return activityTargetTools
	case strings.HasPrefix(eventID, "config:"),
		strings.HasPrefix(eventID, "engine:"):
		return activityTargetStatus
	case strings.TrimSpace(entry.Path) != "":
		return activityTargetFiles
	default:
		return activityTargetStatus
	}
}

func activityTargetLabel(target activityActionTarget) string {
	switch target {
	case activityTargetFiles:
		return "Files"
	case activityTargetPatch:
		return "Patch"
	case activityTargetTools:
		return "Tools"
	case activityTargetPlans:
		return "Plans"
	case activityTargetContext:
		return "Context"
	case activityTargetCodeMap:
		return "CodeMap"
	case activityTargetSecurity:
		return "Security"
	case activityTargetProviders:
		return "Providers"
	default:
		return "Status"
	}
}

func activityTargetSupportsRefresh(target activityActionTarget) bool {
	switch target {
	case activityTargetStatus,
		activityTargetPatch,
		activityTargetPlans,
		activityTargetContext,
		activityTargetCodeMap,
		activityTargetSecurity,
		activityTargetProviders:
		return true
	default:
		return false
	}
}

func renderActivityInspector(entry activityEntry, width, height int) string {
	target := activityTargetForEntry(entry)
	body := []string{
		boldStyle.Render(truncateSingleLine(entry.Text, width-2)),
		subtleStyle.Render("event: " + blankFallback(strings.TrimSpace(entry.EventID), "(unknown)")),
		subtleStyle.Render("kind: " + string(entry.Kind)),
		subtleStyle.Render("time: " + entry.At.Format("15:04:05")),
		subtleStyle.Render("open: enter/o -> " + activityTargetLabel(target)),
	}
	if source := strings.TrimSpace(entry.Source); source != "" {
		body = append(body, subtleStyle.Render("source: "+source))
	}
	if provider := strings.TrimSpace(entry.Provider); provider != "" {
		body = append(body, subtleStyle.Render("provider: "+provider))
	}
	if path := strings.TrimSpace(entry.Path); path != "" {
		body = append(body, subtleStyle.Render("file: f -> "+truncateSingleLine(path, width-14)))
	}
	if activityTargetSupportsRefresh(target) {
		body = append(body, subtleStyle.Render("refresh: r -> reopen target with fresh data"))
	}
	body = append(body, subtleStyle.Render("copy: y -> snapshot details to clipboard"))
	if entry.Count > 1 {
		body = append(body, subtleStyle.Render(fmt.Sprintf("repeats: %d consecutive", entry.Count)))
	}
	body = append(body, "")
	for _, line := range entry.Details {
		body = append(body, truncateSingleLine(line, width-2))
	}
	return renderActivityPane("INSPECTOR", body, width, height)
}

func renderActivityTimeline(entries []activityEntry, selected, width, height int) string {
	if height < 4 {
		height = 4
	}
	rowsHeight := height - 3
	if rowsHeight < 1 {
		rowsHeight = 1
	}
	if len(entries) == 0 {
		return renderActivityPane("TIMELINE", []string{subtleStyle.Render("No matching events.")}, width, height)
	}
	end := selected + 1
	if end < 1 {
		end = 1
	}
	if end > len(entries) {
		end = len(entries)
	}
	start := end - rowsHeight
	if start < 0 {
		start = 0
	}
	hiddenOlder := start
	hiddenNewer := len(entries) - end

	body := make([]string, 0, rowsHeight+1)
	body = append(body, subtleStyle.Render(fmt.Sprintf(
		"%d shown · selected %d/%d · older %d · newer %d",
		len(entries), selected+1, len(entries), hiddenOlder, hiddenNewer,
	)))
	for idx := start; idx < end; idx++ {
		body = append(body, formatActivityLine(entries[idx], width-2, idx == selected))
	}
	return renderActivityPane("TIMELINE", body, width, height)
}

func activityKindCounts(entries []activityEntry) map[activityKind]int {
	counts := map[activityKind]int{}
	for _, entry := range entries {
		counts[entry.Kind] += entry.Count
	}
	return counts
}

func (m Model) renderActivityView(width int) string {
	return m.renderActivityViewSized(width, activityDefaultRenderHeight)
}

func (m Model) renderActivityViewSized(width int, height int) string {
	width = clampInt(width, 24, 1000)
	height = clampInt(height, 10, 1000)

	mode := m.activity.mode
	if mode == "" {
		mode = activityViewAll
	}
	query := strings.TrimSpace(m.activity.query)
	allCounts := activityKindCounts(m.activity.entries)
	filtered := m.filteredActivityEntries()
	scroll := clampActivityOffset(m.activity.scroll, len(filtered))
	selected := activitySelectedIndex(len(filtered), scroll)
	followState := okStyle.Render("live")
	if !m.activity.follow {
		followState = warnStyle.Render("paused")
	}

	hint := "j/k older-newer · pgup/pgdn page · enter/o open · r refresh · f file · y copy · 1-6 filter"
	if m.activity.searchActive {
		hint = "typing search · enter commit · esc stop · backspace delete"
	}
	queryLine := subtleStyle.Render("view: ") +
		accentStyle.Render(activityModeLabel(mode)) +
		subtleStyle.Render(" ["+activityModeShortcut(mode)+"] · query: ")
	if query != "" {
		queryLine += boldStyle.Render(query)
	} else {
		queryLine += subtleStyle.Render("(none)")
	}
	queryLine += subtleStyle.Render(" · follow: ") + followState

	summary := fmt.Sprintf(
		"%d total · %d shown · tool %d · agent %d · err %d · ctx %d",
		len(m.activity.entries),
		len(filtered),
		allCounts[activityKindTool],
		allCounts[activityKindAgent],
		allCounts[activityKindError],
		allCounts[activityKindCtx]+allCounts[activityKindIndex],
	)

	lines := []string{
		sectionHeader("✦", "Activity"),
		subtleStyle.Render(hint),
		queryLine,
		subtleStyle.Render(summary),
		renderDivider(width - 2),
	}

	if len(m.activity.entries) == 0 {
		lines = append(lines,
			"",
			subtleStyle.Render("No events yet."),
			subtleStyle.Render("Tool calls, subagent fan-out, drive progress, provider retries, and context lifecycle stream here live."),
		)
		return strings.Join(lines, "\n")
	}
	if len(filtered) == 0 {
		lines = append(lines,
			"",
			warnStyle.Render("No events match this filter/query."),
			subtleStyle.Render("Press c to clear the query or v / 1-6 to change the view."),
		)
		return strings.Join(lines, "\n")
	}

	remainingHeight := height - len(lines)
	if remainingHeight < 4 {
		remainingHeight = 4
	}

	selectedEntry := filtered[selected]
	if width >= 110 && remainingHeight >= 8 {
		leftWidth := int(float64(width-2) * 0.58)
		if leftWidth < 42 {
			leftWidth = 42
		}
		rightWidth := width - 2 - leftWidth - 2
		if rightWidth < 28 {
			rightWidth = 28
			leftWidth = width - 2 - rightWidth - 2
		}
		timeline := renderActivityTimeline(filtered, selected, leftWidth, remainingHeight)
		inspector := renderActivityInspector(selectedEntry, rightWidth, remainingHeight)
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, timeline, "  ", inspector))
	} else {
		timelineHeight := remainingHeight / 2
		if timelineHeight < 5 {
			timelineHeight = 5
		}
		inspectorHeight := remainingHeight - timelineHeight - 1
		if inspectorHeight < 4 {
			inspectorHeight = 4
		}
		lines = append(lines, renderActivityTimeline(filtered, selected, width-2, timelineHeight))
		lines = append(lines, renderDivider(width-2))
		lines = append(lines, renderActivityInspector(selectedEntry, width-2, inspectorHeight))
	}

	if !m.activity.follow {
		lines = append(lines, warnStyle.Render("paused - press G to jump to tail and resume follow"))
	}
	return strings.Join(lines, "\n")
}

func (m Model) activitySelectedEntry() (activityEntry, bool) {
	filtered := m.filteredActivityEntries()
	if len(filtered) == 0 {
		return activityEntry{}, false
	}
	scroll := clampActivityOffset(m.activity.scroll, len(filtered))
	selected := activitySelectedIndex(len(filtered), scroll)
	if selected < 0 || selected >= len(filtered) {
		return activityEntry{}, false
	}
	return filtered[selected], true
}

func activityCopyPayload(entry activityEntry) string {
	lines := []string{
		"summary: " + strings.TrimSpace(entry.Text),
		"event: " + strings.TrimSpace(entry.EventID),
		"kind: " + string(entry.Kind),
		"time: " + entry.At.Format(time.RFC3339),
	}
	if source := strings.TrimSpace(entry.Source); source != "" {
		lines = append(lines, "source: "+source)
	}
	if provider := strings.TrimSpace(entry.Provider); provider != "" {
		lines = append(lines, "provider: "+provider)
	}
	if path := strings.TrimSpace(entry.Path); path != "" {
		lines = append(lines, "path: "+path)
	}
	if entry.Count > 1 {
		lines = append(lines, fmt.Sprintf("repeats: %d", entry.Count))
	}
	lines = append(lines, entry.Details...)
	return strings.Join(lines, "\n")
}

func (m Model) activityTabIndex(name string) int {
	for i, tab := range m.tabs {
		if strings.EqualFold(strings.TrimSpace(tab), name) {
			return i
		}
	}
	return m.activeTab
}

func (m Model) activityOpenFile(path string) (tea.Model, tea.Cmd) {
	path = strings.TrimSpace(path)
	if path == "" {
		m.notice = "Selected activity has no file path."
		return m, nil
	}
	if idx := indexOfString(m.filesView.entries, path); idx >= 0 {
		m.filesView.index = idx
	} else {
		m.filesView.entries = append(m.filesView.entries, path)
		m.filesView.index = len(m.filesView.entries) - 1
	}
	m.filesView.path = path
	m.activeTab = m.activityTabIndex("Files")
	m.notice = "Focused file from Activity: " + path
	return m, loadFilePreviewCmd(m.eng, path)
}

func activityPlanOrContextQuery(entry activityEntry) string {
	if query := strings.TrimSpace(entry.Query); query != "" {
		return query
	}
	return ""
}

func (m Model) activityFocusPatchPath(path string) Model {
	path = strings.TrimSpace(path)
	if path == "" {
		return m
	}
	for i, section := range m.patchView.set {
		if strings.EqualFold(strings.TrimSpace(section.Path), path) {
			m.patchView.index = i
			m.patchView.hunk = 0
			return m
		}
	}
	return m
}

func (m Model) activityCopySelection() (tea.Model, tea.Cmd) {
	entry, ok := m.activitySelectedEntry()
	if !ok {
		m.notice = "No activity event selected."
		return m, nil
	}
	cmd, res := copyToClipboardCmd(activityCopyPayload(entry))
	m.notice = copyNotice("activity event", res)
	return m, cmd
}

func (m Model) activityOpenSelection(refresh bool) (tea.Model, tea.Cmd) {
	entry, ok := m.activitySelectedEntry()
	if !ok {
		m.notice = "No activity event selected."
		return m, nil
	}
	target := activityTargetForEntry(entry)
	switch target {
	case activityTargetFiles:
		return m.activityOpenFile(entry.Path)
	case activityTargetPatch:
		m = m.activityFocusPatchPath(entry.Path)
		m.activeTab = m.activityTabIndex("Patch")
		m.notice = "Opened Patch from Activity."
		if refresh {
			return m, tea.Batch(loadWorkspaceCmd(m.eng), loadLatestPatchCmd(m.eng))
		}
		return m, nil
	case activityTargetTools:
		m.activeTab = m.activityTabIndex("Tools")
		m.notice = "Opened Tools from Activity."
		return m, nil
	case activityTargetPlans:
		m = m.activatePlansPanel(activityPlanOrContextQuery(entry), refresh)
		m.notice = "Opened Plans from Activity."
		return m, nil
	case activityTargetContext:
		m = m.activateContextPanel(activityPlanOrContextQuery(entry), refresh)
		m.notice = "Opened Context from Activity."
		return m, nil
	case activityTargetCodeMap:
		m.activeTab = m.activityTabIndex("CodeMap")
		m.notice = "Opened CodeMap from Activity."
		if refresh {
			return m, loadCodemapCmd(m.eng)
		}
		return m, nil
	case activityTargetSecurity:
		m.activeTab = m.activityTabIndex("Security")
		m.notice = "Opened Security from Activity."
		if refresh {
			return m, loadSecurityCmd(m.eng)
		}
		return m, nil
	case activityTargetProviders:
		m = m.activateProvidersPanel(entry.Provider, refresh)
		m.notice = "Opened Providers from Activity."
		return m, nil
	default:
		m.activeTab = m.activityTabIndex("Status")
		m.notice = "Opened Status from Activity."
		if refresh {
			return m, loadStatusCmd(m.eng)
		}
		return m, nil
	}
}

func (m Model) activityFocusSelectionFile() (tea.Model, tea.Cmd) {
	entry, ok := m.activitySelectedEntry()
	if !ok {
		m.notice = "No activity event selected."
		return m, nil
	}
	return m.activityOpenFile(entry.Path)
}

func (m Model) handleActivityKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.activity.searchActive {
		return m.handleActivitySearchKey(msg)
	}
	total := len(m.filteredActivityEntries())
	step := 1
	pageStep := 10
	switch msg.String() {
	case "enter", "o":
		return m.activityOpenSelection(false)
	case "r":
		return m.activityOpenSelection(true)
	case "f":
		return m.activityFocusSelectionFile()
	case "y":
		return m.activityCopySelection()
	case "j", "down":
		if m.activity.scroll >= step {
			m.activity.scroll -= step
		} else {
			m.activity.scroll = 0
		}
		m.activity.follow = m.activity.scroll == 0
	case "k", "up":
		if m.activity.scroll+step < total {
			m.activity.scroll += step
			m.activity.follow = false
		}
	case "pgdown":
		if m.activity.scroll >= pageStep {
			m.activity.scroll -= pageStep
		} else {
			m.activity.scroll = 0
		}
		m.activity.follow = m.activity.scroll == 0
	case "pgup":
		if m.activity.scroll+pageStep <= total {
			m.activity.scroll += pageStep
		} else if total > 0 {
			m.activity.scroll = total - 1
		}
		m.activity.follow = false
	case "g", "home":
		if total > 0 {
			m.activity.scroll = total - 1
		}
		m.activity.follow = false
	case "G", "end":
		m.activity.scroll = 0
		m.activity.follow = true
	case "1":
		m.activity.mode = activityViewAll
		m.activity.scroll = 0
		m.activity.follow = true
	case "2":
		m.activity.mode = activityViewTools
		m.activity.scroll = 0
		m.activity.follow = true
	case "3":
		m.activity.mode = activityViewAgents
		m.activity.scroll = 0
		m.activity.follow = true
	case "4":
		m.activity.mode = activityViewErrors
		m.activity.scroll = 0
		m.activity.follow = true
	case "5":
		m.activity.mode = activityViewWorkflow
		m.activity.scroll = 0
		m.activity.follow = true
	case "6":
		m.activity.mode = activityViewContext
		m.activity.scroll = 0
		m.activity.follow = true
	case "v":
		m.activity.mode = nextActivityMode(m.activity.mode)
		m.activity.scroll = 0
		m.activity.follow = true
	case "/":
		m.activity.searchActive = true
	case "c":
		if strings.TrimSpace(m.activity.query) != "" {
			m.activity.query = ""
			m.activity.scroll = 0
			m.activity.follow = true
			break
		}
		m.activity.entries = nil
		m.activity.scroll = 0
		m.activity.follow = true
	case "p":
		m.activity.follow = !m.activity.follow
		if m.activity.follow {
			m.activity.scroll = 0
		}
	}
	m.activity.scroll = clampActivityOffset(m.activity.scroll, len(m.filteredActivityEntries()))
	return m, nil
}

func (m Model) handleActivitySearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.activity.searchActive = false
		m.activity.scroll = 0
		m.activity.follow = true
		return m, nil
	case tea.KeyEsc:
		m.activity.searchActive = false
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.activity.query); len(r) > 0 {
			m.activity.query = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.activity.query += msg.String()
		return m, nil
	}
	return m, nil
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

var _ = lipgloss.NewStyle

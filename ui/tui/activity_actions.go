// Activity panel action handlers: when the user hits enter on a row
// these route the entry to the most relevant panel (Files, Patch,
// Plans, Context, CodeMap, Security, Providers, Status) and copy the
// payload to clipboard. Extracted from activity.go so the main file
// stays focused on the event-recording/rendering loop.

package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

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

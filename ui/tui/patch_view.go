package tui

// patch_view.go — Patch Lab panel and the patch-related Model
// accessors that drive it.
//
// Lifted out of the 10K-line tui.go god file (REPORT.md C1) so the
// "what changed and what's about to change" surface lives in one
// obvious place. Pure parsing & git-apply helpers stayed in
// patch_parse.go; this file is the layer above — Model state for the
// currently-focused section/hunk, the bubbletea commands that run
// `git apply`, and the renderer that paints the side-by-side view.
//
// All Model methods are kept verbatim — no behaviour change, no new
// abstractions. handleChatKey / handlePatchKey still call them.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (m Model) patchCommandSummary() string {
	parts := []string{
		"Patch files: " + strings.Join(m.patchFilesOrNone(), ", "),
		"Patch target: " + m.patchTargetSummary(),
		"Hunk target: " + m.patchHunkSummary(),
	}
	if hints := m.patchReviewHints(); len(hints) > 0 {
		parts = append(parts, "Review cues: "+strings.Join(hints, " | "))
	}
	return strings.Join(parts, "\n")
}

func (m Model) renderPatchView(width int) string {
	// Worktree diff and the assistant's pending hunk both render as
	// side-by-side (before | after) so the eye can visually pair a
	// removed line with the line that replaced it. The unified-text
	// stack we used before forced the user to scroll between `-` and
	// `+` halves of the same change. 18-row cap each pane mirrors
	// the previous truncateForPanel budget.
	diffSide := renderDiffSideBySide(strings.TrimSpace(m.diff), width, 18)
	patchSide := renderDiffSideBySide(m.patchPreviewText(), width, 18)
	if strings.TrimSpace(m.patchPreviewText()) == "" {
		patchSide = subtleStyle.Render("No assistant patch yet. Ask DFMC to refactor, fix, or rewrite a file in Chat — the generated diff lands here.")
	}

	changed := "(none)"
	if len(m.changed) > 0 {
		changed = strings.Join(m.changed, ", ")
	}
	parts := []string{
		sectionHeader("◈", "Patch Lab"),
		subtleStyle.Render("a apply · u undo · c check · ctrl+h keys · side-by-side: red=removed, green=added"),
		renderDivider(min(width, 100)),
		"",
		"Changed:      " + truncateForPanel(changed, width),
		"Patch files:  " + truncateForPanel(strings.Join(m.patchFilesOrNone(), ", "), width),
		"Focus file:   " + truncateForPanel(m.patchTargetSummary(), width),
		"Focus hunk:   " + truncateForPanel(m.patchHunkSummary(), width),
		"",
		sectionHeader("⇄", "Worktree Diff"),
		diffSide,
		"",
		sectionHeader("◇", "Current Hunk"),
		patchSide,
	}
	if info := m.patchFocusSummary(); info != "" {
		parts = append(parts, "", subtleStyle.Render(info))
	}
	if hints := m.patchReviewHints(); len(hints) > 0 {
		parts = append(parts, "", subtleStyle.Render("Review cues: "+strings.Join(hints, " | ")))
	}
	if note := strings.TrimSpace(m.notice); note != "" {
		parts = append(parts, "", subtleStyle.Render(note))
	}
	return strings.Join(parts, "\n")
}

func loadLatestPatchCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return latestPatchLoadedMsg{}
		}
		return latestPatchLoadedMsg{patch: latestAssistantUnifiedDiff(eng.ConversationActive())}
	}
}

func applyPatchCmd(eng *engine.Engine, patch string, checkOnly bool) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return patchApplyMsg{err: fmt.Errorf("engine is nil"), checkOnly: checkOnly}
		}
		if strings.TrimSpace(patch) == "" {
			return patchApplyMsg{err: fmt.Errorf("no assistant patch loaded"), checkOnly: checkOnly}
		}
		root := strings.TrimSpace(eng.Status().ProjectRoot)
		if root == "" {
			root = "."
		}
		if err := applyUnifiedDiff(root, patch, checkOnly); err != nil {
			return patchApplyMsg{err: err, checkOnly: checkOnly}
		}
		if checkOnly {
			return patchApplyMsg{checkOnly: true}
		}
		changed, err := gitChangedFiles(root, 12)
		return patchApplyMsg{checkOnly: false, changed: changed, err: err}
	}
}

func (m Model) focusPatchFile() (tea.Model, tea.Cmd) {
	target := strings.TrimSpace(m.currentPatchPath())
	if target == "" {
		target = strings.TrimSpace(m.bestPatchFileTarget())
	}
	if target == "" {
		m.notice = "No patched file to focus."
		return m, nil
	}
	for i, path := range m.files {
		if strings.EqualFold(strings.TrimSpace(path), target) {
			m.fileIndex = i
			m.activeTab = 2
			m.notice = "Focused patched file " + target
			return m, loadFilePreviewCmd(m.eng, target)
		}
	}
	m.notice = "Patched file not present in file index: " + target
	return m, nil
}

func (m Model) shiftPatchTarget(delta int) (tea.Model, tea.Cmd) {
	if len(m.patchSet) == 0 {
		m.notice = "No patched file to navigate."
		return m, nil
	}
	m.patchIndex = (m.patchIndex + delta + len(m.patchSet)) % len(m.patchSet)
	m.patchHunk = 0
	m.notice = "Viewing patch for " + m.currentPatchPath()
	return m, nil
}

func (m Model) shiftPatchHunk(delta int) (tea.Model, tea.Cmd) {
	section := m.currentPatchSection()
	if section == nil || len(section.Hunks) == 0 {
		m.notice = "No patch hunk to navigate."
		return m, nil
	}
	m.patchHunk = (m.patchHunk + delta + len(section.Hunks)) % len(section.Hunks)
	m.notice = "Viewing hunk " + m.patchHunkSummary()
	return m, nil
}

func (m Model) patchFilesOrNone() []string {
	if len(m.patchFiles) == 0 {
		return []string{"(none)"}
	}
	return append([]string(nil), m.patchFiles...)
}

func (m Model) patchFocusSummary() string {
	parts := make([]string, 0, 2)
	if current := strings.TrimSpace(m.currentPatchPath()); current != "" {
		parts = append(parts, "Viewing "+current+".")
	}
	if pinned := strings.TrimSpace(m.pinnedFile); pinned != "" && containsStringFold(m.patchFiles, pinned) {
		parts = append(parts, "Pinned file is touched by latest patch.")
	}
	if selected := strings.TrimSpace(m.selectedFile()); selected != "" && containsStringFold(m.patchFiles, selected) {
		parts = append(parts, "Selected file is touched by latest patch.")
	}
	return strings.Join(parts, " ")
}

func (m Model) bestPatchFileTarget() string {
	if len(m.patchFiles) == 0 {
		return ""
	}
	if pinned := strings.TrimSpace(m.pinnedFile); pinned != "" && containsStringFold(m.patchFiles, pinned) {
		return pinned
	}
	if selected := strings.TrimSpace(m.selectedFile()); selected != "" && containsStringFold(m.patchFiles, selected) {
		return selected
	}
	return strings.TrimSpace(m.patchFiles[0])
}

func (m Model) bestPatchIndex() int {
	if len(m.patchSet) == 0 {
		return 0
	}
	candidates := []string{
		m.currentPatchPath(),
		strings.TrimSpace(m.pinnedFile),
		strings.TrimSpace(m.selectedFile()),
	}
	for _, target := range candidates {
		if target == "" {
			continue
		}
		for i, item := range m.patchSet {
			if strings.EqualFold(strings.TrimSpace(item.Path), target) {
				return i
			}
		}
	}
	return 0
}

func (m Model) currentPatchPath() string {
	section := m.currentPatchSection()
	if section == nil {
		return ""
	}
	return strings.TrimSpace(section.Path)
}

func (m Model) currentPatchSection() *patchSection {
	if m.patchIndex < 0 || m.patchIndex >= len(m.patchSet) {
		return nil
	}
	return &m.patchSet[m.patchIndex]
}

func (m Model) patchTargetSummary() string {
	section := m.currentPatchSection()
	if section == nil {
		return "(none)"
	}
	return fmt.Sprintf("%s (%d/%d, hunks=%d)", section.Path, m.patchIndex+1, len(m.patchSet), section.HunkCount)
}

func (m Model) patchHunkSummary() string {
	section := m.currentPatchSection()
	if section == nil || len(section.Hunks) == 0 {
		return "(none)"
	}
	index := m.patchHunk
	if index < 0 || index >= len(section.Hunks) {
		index = 0
	}
	header := strings.TrimSpace(section.Hunks[index].Header)
	if header == "" {
		header = "@@"
	}
	return fmt.Sprintf("%s (%d/%d)", header, index+1, len(section.Hunks))
}

func (m Model) patchPreviewText() string {
	section := m.currentPatchSection()
	if section == nil {
		return strings.TrimSpace(m.latestPatch)
	}
	if len(section.Hunks) == 0 {
		return strings.TrimSpace(section.Content)
	}
	index := m.patchHunk
	if index < 0 || index >= len(section.Hunks) {
		index = 0
	}
	return strings.TrimSpace(section.Hunks[index].Content)
}

func (m Model) patchReviewHints() []string {
	section := m.currentPatchSection()
	if section == nil {
		return nil
	}
	text := m.patchPreviewText()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	hints := make([]string, 0, 4)
	additions, deletions := patchLineCounts(text)
	if additions > 0 || deletions > 0 {
		hints = append(hints, fmt.Sprintf("+%d/-%d lines", additions, deletions))
	}
	path := strings.ToLower(strings.TrimSpace(section.Path))
	if path != "" && !strings.Contains(path, "_test.") && !strings.Contains(path, "/test") {
		hints = append(hints, "consider test coverage")
	}
	if strings.Contains(text, "TODO") || strings.Contains(text, "FIXME") {
		hints = append(hints, "contains TODO/FIXME")
	}
	if strings.Contains(text, "panic(") || strings.Contains(text, "fmt.Println(") || strings.Contains(text, "console.log(") {
		hints = append(hints, "check debug or panic statements")
	}
	return hints
}

func (m Model) chatPatchSummary(item chatLine) string {
	if len(item.PatchFiles) == 0 && item.PatchHunks == 0 && item.ToolCalls == 0 {
		return ""
	}
	parts := make([]string, 0, 6)
	if len(item.PatchFiles) > 0 {
		parts = append(parts, fmt.Sprintf("patch: %s", strings.Join(item.PatchFiles, ", ")))
	}
	if item.PatchHunks > 0 {
		parts = append(parts, fmt.Sprintf("hunks=%d", item.PatchHunks))
	}
	if item.IsLatestPatch {
		parts = append(parts, "latest")
	}
	if current := strings.TrimSpace(m.currentPatchPath()); current != "" && containsStringFold(item.PatchFiles, current) {
		parts = append(parts, "current target")
	}
	if item.ToolCalls > 0 {
		toolSummary := fmt.Sprintf("tools=%d", item.ToolCalls)
		if len(item.ToolNames) > 0 {
			toolSummary = fmt.Sprintf("%s [%s]", toolSummary, strings.Join(item.ToolNames, ", "))
		}
		parts = append(parts, toolSummary)
	}
	if item.ToolFailures > 0 {
		parts = append(parts, fmt.Sprintf("failures=%d", item.ToolFailures))
	}
	return strings.Join(parts, " | ")
}

func (m *Model) annotateAssistantPatch(index int) {
	if index < 0 || index >= len(m.transcript) {
		return
	}
	if m.transcript[index].Role != "assistant" {
		return
	}
	sections := parseUnifiedDiffSections(m.transcript[index].Content)
	m.transcript[index].PatchFiles = patchSectionPaths(sections)
	m.transcript[index].PatchHunks = totalPatchHunks(sections)
}

func (m *Model) annotateAssistantToolUsage(index int) {
	if index < 0 || index >= len(m.transcript) {
		return
	}
	if m.transcript[index].Role != "assistant" || m.eng == nil || m.eng.Conversation == nil {
		return
	}
	msg, ok := m.matchAssistantConversationMessage(m.transcript[index].Content)
	if !ok {
		return
	}
	m.transcript[index].ToolCalls = len(msg.ToolCalls)
	m.transcript[index].ToolFailures = 0
	if len(msg.ToolCalls) == 0 && len(msg.Results) == 0 {
		return
	}
	names := make([]string, 0, len(msg.ToolCalls))
	seen := map[string]struct{}{}
	for _, call := range msg.ToolCalls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for _, result := range msg.Results {
		if !result.Success {
			m.transcript[index].ToolFailures++
		}
		if name := strings.TrimSpace(result.Name); name != "" {
			if _, ok := seen[name]; !ok {
				seen[name] = struct{}{}
				names = append(names, name)
			}
		}
	}
	m.transcript[index].ToolNames = names
}

func (m Model) matchAssistantConversationMessage(content string) (types.Message, bool) {
	if m.eng == nil || m.eng.Conversation == nil {
		return types.Message{}, false
	}
	active := m.eng.Conversation.Active()
	if active == nil {
		return types.Message{}, false
	}
	want := strings.TrimSpace(content)
	messages := active.Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != types.RoleAssistant {
			continue
		}
		if strings.TrimSpace(msg.Content) == want {
			return msg, true
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != types.RoleAssistant {
			continue
		}
		if len(msg.ToolCalls) > 0 || len(msg.Results) > 0 {
			return msg, true
		}
	}
	return types.Message{}, false
}

func (m *Model) markLatestPatchInTranscript(patch string) {
	for i := range m.transcript {
		m.transcript[i].IsLatestPatch = false
	}
	patch = strings.TrimSpace(strings.ReplaceAll(patch, "\r\n", "\n"))
	if patch == "" {
		return
	}
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].Role != "assistant" {
			continue
		}
		if strings.TrimSpace(extractUnifiedDiff(m.transcript[i].Content)) == patch {
			m.transcript[i].IsLatestPatch = true
			if len(m.transcript[i].PatchFiles) == 0 {
				m.annotateAssistantPatch(i)
			}
			return
		}
	}
}

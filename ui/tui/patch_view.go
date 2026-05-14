package tui

// patch_view.go — Patch Lab panel: the read-only summary/render
// surface and the section/hunk accessors that back it. Sibling of
// patch_view_actions.go which owns the action menu (apply / check /
// undo / next-prev file or hunk / focus-in-Files / reload diff or
// patch) plus the bubbletea commands and Model navigators those
// actions dispatch to.
//
// Lifted out of the 10K-line tui.go god file (REPORT.md C1) so the
// "what changed and what's about to change" surface lives in one
// obvious place. Pure parsing & git-apply helpers stayed in
// patch_parse.go; this file is the layer above for the read side,
// patch_view_actions.go is the layer above for the write side.

import (
	"fmt"
	"strings"
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

// renderPatchView delegates to the rebuilt 3-pane Patch Lab in
// render_patch.go. The legacy stack-rendering implementation lives in
// git history; the V2 renderer is the active F4 panel.
func (m Model) renderPatchView(width int) string {
	out := m.renderPatchViewV2(width)
	if m.actionMenu.open && m.actionMenu.owner == "Patch" {
		out += "\n\n" + m.renderActionMenu(width)
	}
	return out
}

// openPatchActionMenu + applyPatchCmd + loadLatestPatchCmd +
// focusPatchFile + shiftPatchTarget + shiftPatchHunk live in
// patch_view_actions.go.

func (m Model) patchFilesOrNone() []string {
	if len(m.patchView.files) == 0 {
		return []string{"(none)"}
	}
	return append([]string(nil), m.patchView.files...)
}

func (m Model) patchFocusSummary() string {
	parts := make([]string, 0, 2)
	if current := strings.TrimSpace(m.currentPatchPath()); current != "" {
		parts = append(parts, "Viewing "+current+".")
	}
	if pinned := strings.TrimSpace(m.filesView.pinned); pinned != "" && containsStringFold(m.patchView.files, pinned) {
		parts = append(parts, "Pinned file is touched by latest patch.")
	}
	if selected := strings.TrimSpace(m.selectedFile()); selected != "" && containsStringFold(m.patchView.files, selected) {
		parts = append(parts, "Selected file is touched by latest patch.")
	}
	return strings.Join(parts, " ")
}

func (m Model) bestPatchFileTarget() string {
	if len(m.patchView.files) == 0 {
		return ""
	}
	if pinned := strings.TrimSpace(m.filesView.pinned); pinned != "" && containsStringFold(m.patchView.files, pinned) {
		return pinned
	}
	if selected := strings.TrimSpace(m.selectedFile()); selected != "" && containsStringFold(m.patchView.files, selected) {
		return selected
	}
	return strings.TrimSpace(m.patchView.files[0])
}

func (m Model) bestPatchIndex() int {
	if len(m.patchView.set) == 0 {
		return 0
	}
	candidates := []string{
		m.currentPatchPath(),
		strings.TrimSpace(m.filesView.pinned),
		strings.TrimSpace(m.selectedFile()),
	}
	for _, target := range candidates {
		if target == "" {
			continue
		}
		for i, item := range m.patchView.set {
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
	if m.patchView.index < 0 || m.patchView.index >= len(m.patchView.set) {
		return nil
	}
	return &m.patchView.set[m.patchView.index]
}

func (m Model) patchTargetSummary() string {
	section := m.currentPatchSection()
	if section == nil {
		return "(none)"
	}
	return fmt.Sprintf("%s (%d/%d, hunks=%d)", section.Path, m.patchView.index+1, len(m.patchView.set), section.HunkCount)
}

func (m Model) patchHunkSummary() string {
	section := m.currentPatchSection()
	if section == nil || len(section.Hunks) == 0 {
		return "(none)"
	}
	index := m.patchView.hunk
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
		return strings.TrimSpace(m.patchView.latestPatch)
	}
	if len(section.Hunks) == 0 {
		return strings.TrimSpace(section.Content)
	}
	index := m.patchView.hunk
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

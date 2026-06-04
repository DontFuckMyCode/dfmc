package tui

// patch_view_actions.go — Patch panel action menu + the bubbletea
// commands and Model navigators it dispatches to. Sibling of
// patch_view.go which keeps the read-only summary/render side and
// the section/hunk accessors.
//
// Splitting the action surface out keeps patch_view.go scoped to
// "what does the current patch look like" while this file owns
// "what can the user do to it" — apply / dry-run check / undo
// turn / next-prev file or hunk / focus file in Files tab /
// reload worktree diff or latest assistant patch. Every handler is
// wired through the arrow-only action menu (no per-keystroke
// shortcut; accelerators live alongside the labels for the badge).

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// openPatchActionMenu builds the contextual action list for the
// Patch panel — every operation that previously lived behind
// a/c/u/n/b/j/k/f/d/l is now reachable via arrows + enter.
func (m Model) openPatchActionMenu() Model {
	actions := []panelAction{
		{Label: "Apply patch (modifies the worktree)", Accel: "a",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m, applyPatchCmd(m.eng, m.patchView.latestPatch, false)
			}},
		{Label: "Apply current hunk only (selective)", Accel: "A",
			Handler: func(m Model) (Model, tea.Cmd) {
				frag, ok := m.currentHunkPatchFragment()
				if !ok {
					m.notice = "No hunk selected — use j/k to highlight a hunk first."
					return m, nil
				}
				return m, applyPatchCmd(m.eng, frag, false)
			}},
		{Label: "Check current hunk only (dry-run)", Accel: "C",
			Handler: func(m Model) (Model, tea.Cmd) {
				frag, ok := m.currentHunkPatchFragment()
				if !ok {
					m.notice = "No hunk selected — use j/k to highlight a hunk first."
					return m, nil
				}
				return m, applyPatchCmd(m.eng, frag, true)
			}},
		{Label: "Check patch (dry-run apply)", Accel: "c",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m, applyPatchCmd(m.eng, m.patchView.latestPatch, true)
			}},
		{Label: "Undo last conversation turn", Accel: "u",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m, undoConversationCmd(m.eng)
			}},
		{Label: "Next file in patch", Accel: "n",
			Handler: func(m Model) (Model, tea.Cmd) {
				nm, cmd := m.shiftPatchTarget(1)
				if mm, ok := nm.(Model); ok {
					return mm, cmd
				}
				return m, cmd
			}},
		{Label: "Previous file in patch", Accel: "b",
			Handler: func(m Model) (Model, tea.Cmd) {
				nm, cmd := m.shiftPatchTarget(-1)
				if mm, ok := nm.(Model); ok {
					return mm, cmd
				}
				return m, cmd
			}},
		{Label: "Next hunk",
			Handler: func(m Model) (Model, tea.Cmd) {
				nm, cmd := m.shiftPatchHunk(1)
				if mm, ok := nm.(Model); ok {
					return mm, cmd
				}
				return m, cmd
			}},
		{Label: "Previous hunk",
			Handler: func(m Model) (Model, tea.Cmd) {
				nm, cmd := m.shiftPatchHunk(-1)
				if mm, ok := nm.(Model); ok {
					return mm, cmd
				}
				return m, cmd
			}},
		{Label: "Focus current file in Files tab", Accel: "f",
			Handler: func(m Model) (Model, tea.Cmd) {
				nm, cmd := m.focusPatchFile()
				if mm, ok := nm.(Model); ok {
					return mm, cmd
				}
				return m, cmd
			}},
		{Label: "Reload worktree diff", Accel: "d",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m, loadWorkspaceCmd(m.eng)
			}},
		{Label: "Reload latest assistant patch", Accel: "alt+l",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m, loadLatestPatchCmd(m.eng)
			}},
	}
	return m.openActionMenu("Patch", "Patch actions", actions)
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
	for i, path := range m.filesView.entries {
		if strings.EqualFold(strings.TrimSpace(path), target) {
			m.filesView.index = i
			m.activeTab = m.activityTabIndex("Files")
			m.notice = "Focused patched file " + target
			return m, loadFilePreviewCmd(m.eng, target)
		}
	}
	m.notice = "Patched file not present in file index: " + target
	return m, nil
}

func (m Model) shiftPatchTarget(delta int) (tea.Model, tea.Cmd) {
	if len(m.patchView.set) == 0 {
		m.notice = "No patched file to navigate."
		return m, nil
	}
	m.patchView.index = (m.patchView.index + delta + len(m.patchView.set)) % len(m.patchView.set)
	m.patchView.hunk = 0
	m.notice = "Viewing patch for " + m.currentPatchPath()
	return m, nil
}

// currentHunkPatchFragment — Phase F item 1. Builds a self-contained
// unified-diff fragment for the highlighted hunk so the user can apply
// it in isolation without dragging the rest of the section along. The
// fragment carries the original file preamble (`diff --git`, `index`,
// `---`, `+++`) followed by only the selected `@@` block, which is
// already what `extractPatchHunks` produces per hunk — each hunk's
// Content is built as `preamble + @@ + body` precisely so a single-hunk
// extract is a one-liner here.
//
// Returns ok=false when no section/hunk is highlighted (panel guard so
// the caller can surface a notice instead of dispatching an empty
// patch through the apply path).
func (m Model) currentHunkPatchFragment() (string, bool) {
	section := m.currentPatchSection()
	if section == nil {
		return "", false
	}
	if m.patchView.hunk < 0 || m.patchView.hunk >= len(section.Hunks) {
		return "", false
	}
	body := strings.TrimSpace(section.Hunks[m.patchView.hunk].Content)
	if body == "" {
		return "", false
	}
	// applyUnifiedDiff (git apply) tolerates either trailing newline
	// or no newline; normalise to one trailing newline so the patch
	// looks identical to what git diff itself emits, regardless of how
	// the source assistant rendered it.
	return body + "\n", true
}

func (m Model) shiftPatchHunk(delta int) (tea.Model, tea.Cmd) {
	section := m.currentPatchSection()
	if section == nil || len(section.Hunks) == 0 {
		m.notice = "No patch hunk to navigate."
		return m, nil
	}
	m.patchView.hunk = (m.patchView.hunk + delta + len(section.Hunks)) % len(section.Hunks)
	m.notice = "Viewing hunk " + m.patchHunkSummary()
	return m, nil
}

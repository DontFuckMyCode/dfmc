package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/commands"
)

func isMutationTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "write_file", "edit_file", "apply_patch":
		return true
	default:
		return false
	}
}

func (m Model) togglePinnedFile() (tea.Model, tea.Cmd) {
	target := strings.TrimSpace(m.selectedFile())
	if target == "" {
		m.notice = "No file selected."
		return m, nil
	}
	if strings.EqualFold(strings.TrimSpace(m.filesView.pinned), target) {
		m.filesView.pinned = ""
		m.notice = "Cleared pinned file."
		return m, nil
	}
	m.filesView.pinned = target
	m.notice = "Pinned " + target + " for chat context."
	return m, nil
}

func (m Model) focusChangedFiles(changed []string) Model {
	if len(changed) == 0 {
		return m
	}
	target := strings.TrimSpace(m.filesView.pinned)
	if target == "" || !containsStringFold(changed, target) {
		target = strings.TrimSpace(changed[0])
	}
	if target == "" {
		return m
	}
	for i, path := range m.filesView.entries {
		if strings.EqualFold(strings.TrimSpace(path), target) {
			m.filesView.index = i
			return m
		}
	}
	return m
}

func (m Model) refreshToolMutationState(path string) Model {
	if m.eng == nil {
		return m
	}
	root := strings.TrimSpace(m.eng.Status().ProjectRoot)
	if root == "" {
		root = "."
	}
	if files, err := listProjectFiles(root, 500); err == nil {
		m.filesView.entries = files
	}
	if diff, err := gitWorkingDiff(root, 120_000); err == nil {
		m.patchView.diff = diff
	}
	if changed, err := gitChangedFiles(root, 12); err == nil {
		m.patchView.changed = changed
		m = m.focusChangedFiles(changed)
	}
	path = strings.TrimSpace(path)
	if path != "" {
		m.filesView.path = path
		if idx := indexOfString(m.filesView.entries, path); idx >= 0 {
			m.filesView.index = idx
		}
		if content, size, err := readProjectFile(root, path, 32_000); err == nil {
			m.filesView.preview = content
			m.filesView.size = size
		}
	}
	m.activeTab = 3
	if len(m.patchView.changed) > 0 {
		m.notice = "Tool updated workspace: " + strings.Join(m.patchView.changed, ", ")
	} else {
		m.notice = "Tool updated workspace."
	}
	return m
}

// isKnownChatCommandToken reports whether a bare word (without the leading /)
// matches a registered canonical command or alias in the shared registry, or
// one of the TUI-only slash utilities. Used by the input parser to classify
// tokens as commands vs. ordinary chat text.
func isKnownChatCommandToken(token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return false
	}
	switch token {
	case "reload", "providers", "models", "tools", "ls", "read", "grep", "run", "diff", "patch", "apply", "undo",
		"continue", "resume", "btw", "quit", "exit", "q", "clear", "coach", "hints", "queue", "select",
		"workflow", "todos", "subagents", "stats":
		return true
	}
	if _, ok := commands.DefaultRegistry().Lookup(token); ok {
		return true
	}
	return false
}

func isImmediateChatSlashCommand(token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	switch token {
	case "", "help", "tools", "stats", "workflow", "todos", "subagents", "queue",
		"coach", "hints", "keylog", "mouse", "select", "status", "providers", "models",
		"hooks", "approve", "doctor", "health", "map", "version", "magicdoc", "magic",
		"conversation", "conv", "memory", "prompt", "skill":
		return true
	default:
		return false
	}
}

func (m Model) chatPrompt() string {
	question := strings.TrimSpace(expandAtFileMentionsWithRecent(m.chat.input, m.filesView.entries, m.engineRecentFiles()))
	if pinned := strings.TrimSpace(m.filesView.pinned); pinned != "" {
		question = composeChatPrompt(question, fileMarker(pinned))
	}
	return strings.TrimSpace(question)
}

// update_data.go — handlers for "data arrived" tea.Msg types: panel
// loaders (status, workspace, files, memory, codemap, conversations,
// prompts, security), the patch-apply / undo / tool-run / sync-models
// completion notifications, and the gitInfo refresh. Each handler is a
// small (Model -> Model + tea.Cmd) reducer; Update in update.go just
// forwards the typed message to the matching method.

package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleStatusLoadedMsg(msg statusLoadedMsg) (tea.Model, tea.Cmd) {
	m.status = msg.status
	m = m.hydrateStatusProviderFromConfig()
	return m, nil
}

func (m Model) handleWorkspaceLoadedMsg(msg workspaceLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.notice = "workspace: " + msg.err.Error()
		return m, nil
	}
	m.patchView.diff = msg.diff
	m.patchView.changed = msg.changed
	if strings.TrimSpace(msg.diff) == "" {
		m.notice = "Working tree is clean."
	} else if len(msg.changed) > 0 {
		m.notice = "Changed files: " + strings.Join(msg.changed, ", ")
	}
	return m, nil
}

func (m Model) handleLatestPatchLoadedMsg(msg latestPatchLoadedMsg) (tea.Model, tea.Cmd) {
	m.patchView.latestPatch = msg.patch
	m.patchView.set = parseUnifiedDiffSections(msg.patch)
	m.patchView.files = patchSectionPaths(m.patchView.set)
	if len(m.patchView.files) == 0 {
		m.patchView.files = extractPatchedFiles(msg.patch)
	}
	m.patchView.index = m.bestPatchIndex()
	m.patchView.hunk = 0
	m.markLatestPatchInTranscript(msg.patch)
	if strings.TrimSpace(msg.patch) == "" {
		m.notice = "No assistant patch found yet."
	} else {
		m.notice = "Loaded latest assistant patch."
	}
	return m, nil
}

func (m Model) handleFilesLoadedMsg(msg filesLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.notice = "files: " + msg.err.Error()
		return m, nil
	}
	m.filesView.entries = msg.files
	if len(m.filesView.entries) == 0 {
		m.filesView.index = 0
		m.filesView.path = ""
		m.filesView.preview = ""
		m.filesView.size = 0
		m.notice = "No project files found."
		return m, nil
	}
	selected := m.selectedFile()
	nextIndex := 0
	if selected != "" {
		for i, path := range m.filesView.entries {
			if path == selected {
				nextIndex = i
				break
			}
		}
	}
	m.filesView.index = nextIndex
	return m, loadFilePreviewCmd(m.eng, m.selectedFile())
}

func (m Model) handleFilePreviewLoadedMsg(msg filePreviewLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.notice = "preview: " + msg.err.Error()
		return m, nil
	}
	m.filesView.path = msg.path
	m.filesView.preview = msg.content
	m.filesView.size = msg.size
	if strings.TrimSpace(msg.path) != "" {
		m.notice = fmt.Sprintf("Previewing %s (%d bytes)", msg.path, msg.size)
	}
	return m, nil
}

func (m Model) handleMemoryLoadedMsg(msg memoryLoadedMsg) (tea.Model, tea.Cmd) {
	m.memory.loading = false
	if msg.err != nil {
		m.memory.err = msg.err.Error()
		return m, nil
	}
	m.memory.err = ""
	m.memory.entries = msg.entries
	if msg.tier != "" {
		m.memory.tier = msg.tier
	}
	if m.memory.scroll >= len(m.memory.entries) {
		m.memory.scroll = 0
	}
	return m, nil
}

func (m Model) handleCodemapLoadedMsg(msg codemapLoadedMsg) (tea.Model, tea.Cmd) {
	m.codemap.loading = false
	m.codemap.loaded = true
	if msg.err != nil {
		m.codemap.err = msg.err.Error()
		return m, nil
	}
	m.codemap.err = ""
	m.codemap.snap = msg.snap
	if m.codemap.scroll >= codemapViewRowCount(m.codemap.view, m.codemap.snap) {
		m.codemap.scroll = 0
	}
	return m, nil
}

func (m Model) handleConversationsLoadedMsg(msg conversationsLoadedMsg) (tea.Model, tea.Cmd) {
	m.conversations.loading = false
	m.conversations.loaded = true
	if msg.err != nil {
		m.conversations.err = msg.err.Error()
		return m, nil
	}
	m.conversations.err = ""
	m.conversations.entries = msg.entries
	// Sync deep-search state with the result that landed: an empty
	// deepSearchQuery means a plain List() (drop the deep-search flag);
	// a non-empty one means the search came back (keep both). The
	// render path uses these to choose between "filtered" and "search
	// for: <q>" framing in the title bar.
	if msg.deepSearchQuery != "" {
		m.conversations.deepSearchActive = true
		m.conversations.deepSearchQuery = msg.deepSearchQuery
	} else {
		m.conversations.deepSearchActive = false
		m.conversations.deepSearchQuery = ""
	}
	if m.conversations.scroll >= len(m.conversations.entries) {
		m.conversations.scroll = 0
	}
	return m, nil
}

func (m Model) handleConversationPreviewMsg(msg conversationPreviewMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.notice = "conversations: " + msg.err.Error()
		return m, nil
	}
	m.conversations.previewID = msg.id
	m.conversations.preview = msg.msgs
	m.conversations.previewBranches = msg.branches
	m.conversations.previewActiveBranch = msg.activeBranch
	// LoadReadOnly does NOT change the active conversation — Chat
	// keeps whatever was running. The notice has to make that
	// explicit so users don't assume f1 will jump them into the
	// previewed history.
	m.notice = fmt.Sprintf("Previewed conversation %s (%d messages) — read-only; Chat keeps the current session.", msg.id, len(msg.msgs))
	return m, nil
}

func (m Model) handlePromptsLoadedMsg(msg promptsLoadedMsg) (tea.Model, tea.Cmd) {
	m.prompts.loading = false
	m.prompts.loaded = true
	if msg.err != nil {
		m.prompts.err = msg.err.Error()
		return m, nil
	}
	m.prompts.err = ""
	m.prompts.templates = msg.templates
	if m.prompts.scroll >= len(m.prompts.templates) {
		m.prompts.scroll = 0
	}
	return m, nil
}

func (m Model) handleSecurityLoadedMsg(msg securityLoadedMsg) (tea.Model, tea.Cmd) {
	m.security.loading = false
	m.security.loaded = true
	if msg.err != nil {
		m.security.err = msg.err.Error()
		return m, nil
	}
	m.security.err = ""
	m.security.report = msg.report
	m.security.scroll = 0
	// Phase J item 1 — hydrate the ignore set from disk on the first
	// successful load. Subsequent reruns keep the in-memory map (which
	// already reflects toggle-time writes), so we only read once per
	// TUI session. Read errors surface via the notice but don't block
	// the panel — fingerprints just stay in memory until the next save.
	if m.security.ignored == nil {
		path := m.securityIgnoresPath()
		loaded, err := loadSecurityIgnoresFromDisk(path)
		if err != nil {
			m.notice = "security: ignore file unreadable — " + err.Error()
		}
		m.security.ignored = loaded
	}
	return m, nil
}

func (m Model) handleSyncModelsDevMsg(msg syncModelsDevMsg) (tea.Model, tea.Cmd) {
	m.providers.syncing = false
	if msg.err != nil {
		m.notice = "sync failed: " + msg.err.Error()
		return m, nil
	}
	m.providers.lastSyncedAt = time.Now()
	m = m.refreshProvidersRows()
	m.status = m.eng.Status()
	m.notice = fmt.Sprintf("synced %d changes → %s", len(msg.changes), msg.path)
	return m, nil
}

func (m Model) handlePatchApplyMsg(msg patchApplyMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.notice = "patch: " + msg.err.Error()
		return m, nil
	}
	if msg.checkOnly {
		m.notice = "Patch check passed."
		return m, nil
	}
	m = m.focusChangedFiles(msg.changed)
	// Phase F item 2 — post-apply validation prompt. After a successful
	// apply, suggest the project-aware build/test command in the same
	// notice line so the user has a single keystroke (`/run <cmd>`) to
	// verify nothing regressed before committing. Detection rides off
	// the first changed file's extension; falls back to a generic
	// "/run go test ./..." style hint when nothing matches. Empty
	// `changed` (e.g. comment-only patch) skips the suggestion.
	base := "Patch applied"
	if len(msg.changed) > 0 {
		base += ": " + strings.Join(msg.changed, ", ")
	}
	if hint := postApplyValidationHint(msg.changed); hint != "" {
		base += "  ·  " + hint
	}
	m.notice = base
	cmds := []tea.Cmd{loadWorkspaceCmd(m.eng)}
	if target := m.selectedFile(); target != "" {
		cmds = append(cmds, loadFilePreviewCmd(m.eng, target))
	}
	return m, tea.Batch(cmds...)
}

// postApplyValidationHint returns a short "validate with X" suffix
// for the patch-applied notice, picked from the first changed file's
// extension. When the language is unknown we return "" so the notice
// stays clean rather than showing a useless generic hint.
func postApplyValidationHint(changed []string) string {
	for _, path := range changed {
		switch {
		case strings.HasSuffix(path, ".go"):
			return "validate: /run go build ./...  (or /run go test ./...)"
		case strings.HasSuffix(path, ".py"):
			return "validate: /run pytest  (or /run python -m mypy)"
		case strings.HasSuffix(path, ".ts"), strings.HasSuffix(path, ".tsx"):
			return "validate: /run npm test  (or /run tsc --noEmit)"
		case strings.HasSuffix(path, ".js"), strings.HasSuffix(path, ".jsx"):
			return "validate: /run npm test"
		case strings.HasSuffix(path, ".rs"):
			return "validate: /run cargo build  (or /run cargo test)"
		case strings.HasSuffix(path, ".java"):
			return "validate: /run mvn test  (or /run gradle test)"
		case strings.HasSuffix(path, ".rb"):
			return "validate: /run bundle exec rspec"
		}
	}
	return ""
}

func (m Model) handleConversationUndoMsg(msg conversationUndoMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.notice = "undo: " + msg.err.Error()
		return m, nil
	}
	m.notice = fmt.Sprintf("Undone messages: %d", msg.removed)
	return m, loadLatestPatchCmd(m.eng)
}

func (m Model) handleToolRunMsg(msg toolRunMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.notice = "tool: " + msg.err.Error()
		m.toolView.output = formatToolErrorForPanel(msg.name, msg.params, msg.result, msg.err)
		if m.chat.toolPending && strings.EqualFold(strings.TrimSpace(msg.name), strings.TrimSpace(m.chat.toolName)) {
			m = m.appendSystemMessage(formatToolResultForChat(msg.name, msg.params, msg.result, msg.err))
			m.chat.toolPending = false
			m.chat.toolName = ""
		}
		if toolResultWorkspaceChanged(msg.result) {
			m = m.refreshToolMutationState("")
		}
		return m, nil
	}
	m.toolView.output = formatToolResultForPanel(msg.name, msg.params, msg.result)
	m.notice = fmt.Sprintf("Tool ran: %s (%dms)", msg.name, msg.result.DurationMs)
	if warnings := toolResultWarnings(msg.name, msg.result); len(warnings) > 0 {
		m.notice = warnings[0]
	}
	if m.chat.toolPending && strings.EqualFold(strings.TrimSpace(msg.name), strings.TrimSpace(m.chat.toolName)) {
		m = m.appendSystemMessage(formatToolResultForChat(msg.name, msg.params, msg.result, nil))
		m.chat.toolPending = false
		m.chat.toolName = ""
	}
	if path := toolResultRelativePath(m.eng, msg.result); path != "" {
		m.filesView.path = path
		if idx := indexOfString(m.filesView.entries, path); idx >= 0 {
			m.filesView.index = idx
		}
		if msg.name == "read_file" {
			m.filesView.preview = msg.result.Output
			m.filesView.size = len([]byte(msg.result.Output))
		}
		if isMutationTool(msg.name) || toolResultWorkspaceChanged(msg.result) {
			m = m.refreshToolMutationState(path)
		}
	} else if isMutationTool(msg.name) || toolResultWorkspaceChanged(msg.result) {
		m = m.refreshToolMutationState("")
	}
	return m, nil
}

func (m Model) handleGitInfoLoadedMsg(msg gitInfoLoadedMsg) (tea.Model, tea.Cmd) {
	m.gitInfo = msg.info
	return m, nil
}

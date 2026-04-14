package tui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type Options struct {
	AltScreen bool
}

type chatLine struct {
	Role    string
	Content string
}

type Model struct {
	ctx context.Context
	eng *engine.Engine

	width  int
	height int

	tabs      []string
	activeTab int

	status engine.Status

	transcript     []chatLine
	input          string
	sending        bool
	streamIndex    int
	streamMessages <-chan tea.Msg

	diff        string
	changed     []string
	latestPatch string
	files       []string
	fileIndex   int
	filePreview string
	filePath    string
	fileSize    int

	notice string
}

type statusLoadedMsg struct {
	status engine.Status
}

type workspaceLoadedMsg struct {
	diff    string
	changed []string
	err     error
}

type latestPatchLoadedMsg struct {
	patch string
}

type filesLoadedMsg struct {
	files []string
	err   error
}

type filePreviewLoadedMsg struct {
	path    string
	content string
	size    int
	err     error
}

type patchApplyMsg struct {
	checkOnly bool
	changed   []string
	err       error
}

type conversationUndoMsg struct {
	removed int
	err     error
}

type chatDeltaMsg struct {
	delta string
}

type chatDoneMsg struct{}

type chatErrMsg struct {
	err error
}

type streamClosedMsg struct{}

var (
	docStyle = lipgloss.NewStyle().
			Padding(1, 2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#355070"))

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F25F5C")).
			Bold(true)

	subtleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#94A3B8"))

	tabActiveStyle = lipgloss.NewStyle().
			Padding(0, 2).
			Background(lipgloss.Color("#1E293B")).
			Foreground(lipgloss.Color("#F8FAFC")).
			Bold(true)

	tabInactiveStyle = lipgloss.NewStyle().
				Padding(0, 2).
				Foreground(lipgloss.Color("#94A3B8"))

	statusBarStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(lipgloss.Color("#E2E8F0")).
			Background(lipgloss.Color("#0F172A"))

	userLineStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#93C5FD"))
	assistantLineStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#A7F3D0"))
	systemLineStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FDE68A"))
)

func NewModel(ctx context.Context, eng *engine.Engine) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	return Model{
		ctx:         ctx,
		eng:         eng,
		tabs:        []string{"Chat", "Status", "Files", "Patch"},
		streamIndex: -1,
		notice:      "F1-F4 or Tab to switch panels. Files: j/k move, enter reload. Patch: d diff, l load, c check, a apply, u undo.",
	}
}

func Run(ctx context.Context, eng *engine.Engine, opts Options) error {
	model := NewModel(ctx, eng)
	programOpts := []tea.ProgramOption{}
	if opts.AltScreen {
		programOpts = append(programOpts, tea.WithAltScreen())
	}
	p := tea.NewProgram(model, programOpts...)
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		loadStatusCmd(m.eng),
		loadWorkspaceCmd(m.eng),
		loadLatestPatchCmd(m.eng),
		loadFilesCmd(m.eng),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case statusLoadedMsg:
		m.status = msg.status
		return m, nil

	case workspaceLoadedMsg:
		if msg.err != nil {
			m.notice = "workspace: " + msg.err.Error()
			return m, nil
		}
		m.diff = msg.diff
		m.changed = msg.changed
		if strings.TrimSpace(msg.diff) == "" {
			m.notice = "Working tree is clean."
		} else if len(msg.changed) > 0 {
			m.notice = "Changed files: " + strings.Join(msg.changed, ", ")
		}
		return m, nil

	case latestPatchLoadedMsg:
		m.latestPatch = msg.patch
		if strings.TrimSpace(msg.patch) == "" {
			m.notice = "No assistant patch found yet."
		} else {
			m.notice = "Loaded latest assistant patch."
		}
		return m, nil

	case filesLoadedMsg:
		if msg.err != nil {
			m.notice = "files: " + msg.err.Error()
			return m, nil
		}
		m.files = msg.files
		if len(m.files) == 0 {
			m.fileIndex = 0
			m.filePath = ""
			m.filePreview = ""
			m.fileSize = 0
			m.notice = "No project files found."
			return m, nil
		}
		selected := m.selectedFile()
		nextIndex := 0
		if selected != "" {
			for i, path := range m.files {
				if path == selected {
					nextIndex = i
					break
				}
			}
		}
		m.fileIndex = nextIndex
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())

	case filePreviewLoadedMsg:
		if msg.err != nil {
			m.notice = "preview: " + msg.err.Error()
			return m, nil
		}
		m.filePath = msg.path
		m.filePreview = msg.content
		m.fileSize = msg.size
		if strings.TrimSpace(msg.path) != "" {
			m.notice = fmt.Sprintf("Previewing %s (%d bytes)", msg.path, msg.size)
		}
		return m, nil

	case patchApplyMsg:
		if msg.err != nil {
			m.notice = "patch: " + msg.err.Error()
			return m, nil
		}
		if msg.checkOnly {
			m.notice = "Patch check passed."
			return m, nil
		}
		if len(msg.changed) > 0 {
			m.notice = "Patch applied: " + strings.Join(msg.changed, ", ")
		} else {
			m.notice = "Patch applied."
		}
		return m, loadWorkspaceCmd(m.eng)

	case conversationUndoMsg:
		if msg.err != nil {
			m.notice = "undo: " + msg.err.Error()
			return m, nil
		}
		m.notice = fmt.Sprintf("Undone messages: %d", msg.removed)
		return m, loadLatestPatchCmd(m.eng)

	case chatDeltaMsg:
		if m.streamIndex >= 0 && m.streamIndex < len(m.transcript) {
			m.transcript[m.streamIndex].Content += msg.delta
		}
		return m, waitForStreamMsg(m.streamMessages)

	case chatDoneMsg:
		m.sending = false
		m.streamMessages = nil
		m.streamIndex = -1
		m.input = ""
		m.notice = "Chat response completed."
		return m, tea.Batch(loadStatusCmd(m.eng), loadLatestPatchCmd(m.eng))

	case chatErrMsg:
		m.sending = false
		m.streamMessages = nil
		m.streamIndex = -1
		m.notice = "chat: " + msg.err.Error()
		return m, nil

	case streamClosedMsg:
		m.sending = false
		m.streamMessages = nil
		m.streamIndex = -1
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab":
			m.activeTab = (m.activeTab + 1) % len(m.tabs)
			return m, nil
		case "shift+tab":
			m.activeTab--
			if m.activeTab < 0 {
				m.activeTab = len(m.tabs) - 1
			}
			return m, nil
		case "f1":
			m.activeTab = 0
			return m, nil
		case "f2":
			m.activeTab = 1
			return m, nil
		case "f3":
			m.activeTab = 2
			return m, nil
		case "f4":
			m.activeTab = 3
			return m, nil
		}

		switch m.tabs[m.activeTab] {
		case "Chat":
			return m.handleChatKey(msg)
		case "Status":
			if msg.String() == "r" {
				return m, loadStatusCmd(m.eng)
			}
		case "Files":
			return m.handleFilesKey(msg)
		case "Patch":
			switch msg.String() {
			case "d":
				return m, loadWorkspaceCmd(m.eng)
			case "l":
				return m, loadLatestPatchCmd(m.eng)
			case "c":
				return m, applyPatchCmd(m.eng, m.latestPatch, true)
			case "a":
				return m, applyPatchCmd(m.eng, m.latestPatch, false)
			case "u":
				return m, undoConversationCmd(m.eng)
			}
		}
	}
	return m, nil
}

func (m Model) handleChatKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyRunes:
		if !m.sending {
			m.input += string(msg.Runes)
		}
		return m, nil
	case tea.KeySpace:
		if !m.sending {
			m.input += " "
		}
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		if !m.sending && len(m.input) > 0 {
			m.input = string([]rune(m.input)[:len([]rune(m.input))-1])
		}
		return m, nil
	case tea.KeyEnter:
		question := strings.TrimSpace(m.input)
		if m.sending || question == "" {
			return m, nil
		}
		m.transcript = append(m.transcript,
			chatLine{Role: "user", Content: question},
			chatLine{Role: "assistant", Content: ""},
		)
		m.streamIndex = len(m.transcript) - 1
		m.sending = true
		m.notice = "Streaming answer..."
		m.streamMessages = startChatStream(m.ctx, m.eng, question)
		return m, waitForStreamMsg(m.streamMessages)
	}
	return m, nil
}

func (m Model) handleFilesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		return m, loadFilesCmd(m.eng)
	case "down", "j":
		if len(m.files) == 0 {
			return m, nil
		}
		if m.fileIndex < len(m.files)-1 {
			m.fileIndex++
		}
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())
	case "up", "k":
		if len(m.files) == 0 {
			return m, nil
		}
		if m.fileIndex > 0 {
			m.fileIndex--
		}
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())
	case "enter":
		if len(m.files) == 0 {
			return m, nil
		}
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())
	}
	return m, nil
}

func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	bodyWidth := width - 4
	if bodyWidth < 20 {
		bodyWidth = width
	}

	tabs := make([]string, 0, len(m.tabs))
	for i, tab := range m.tabs {
		if i == m.activeTab {
			tabs = append(tabs, tabActiveStyle.Render(tab))
		} else {
			tabs = append(tabs, tabInactiveStyle.Render(tab))
		}
	}

	header := titleStyle.Render("DFMC TUI") + "\n" + strings.Join(tabs, " ")
	body := m.renderActiveView(bodyWidth)
	footer := statusBarStyle.Width(width).Render(m.renderFooter())

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m Model) renderActiveView(width int) string {
	switch m.tabs[m.activeTab] {
	case "Status":
		return docStyle.Width(width).Render(m.renderStatusView(width - 6))
	case "Files":
		return docStyle.Width(width).Render(m.renderFilesView(width - 6))
	case "Patch":
		return docStyle.Width(width).Render(m.renderPatchView(width - 6))
	default:
		return docStyle.Width(width).Render(m.renderChatView(width - 6))
	}
}

func (m Model) renderChatView(width int) string {
	lines := []string{
		titleStyle.Render("Chat"),
		subtleStyle.Render("Enter to send. Ctrl+C or q quits. Tab switches panels."),
		"",
	}
	start := 0
	if len(m.transcript) > 10 {
		start = len(m.transcript) - 10
	}
	for _, item := range m.transcript[start:] {
		role := "[" + strings.ToUpper(item.Role) + "] "
		content := truncateForPanel(item.Content, width)
		switch item.Role {
		case "user":
			lines = append(lines, userLineStyle.Render(role+content))
		case "assistant":
			lines = append(lines, assistantLineStyle.Render(role+content))
		default:
			lines = append(lines, systemLineStyle.Render(role+content))
		}
	}
	if len(m.transcript) == 0 {
		lines = append(lines, subtleStyle.Render("No messages yet. Ask for a review, explanation, or refactor plan."))
	}
	lines = append(lines, "", subtleStyle.Render("Input:"), "> "+m.input)
	if m.sending {
		lines = append(lines, subtleStyle.Render("Streaming in progress..."))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderStatusView(width int) string {
	parts := []string{
		titleStyle.Render("Status"),
		subtleStyle.Render("Press r to refresh."),
		"",
		fmt.Sprintf("Project:   %s", blankFallback(m.status.ProjectRoot, "(none)")),
		fmt.Sprintf("Provider:  %s / %s", blankFallback(m.status.Provider, "-"), blankFallback(m.status.Model, "-")),
		fmt.Sprintf("AST:       %s", blankFallback(m.status.ASTBackend, "-")),
		fmt.Sprintf("Languages: %s", truncateForPanel(formatASTLanguageSummaryTUI(m.status.ASTLanguages), width)),
		fmt.Sprintf("AST Metrics: %s", truncateForPanel(formatASTMetricsSummaryTUI(m.status.ASTMetrics), width)),
		fmt.Sprintf("CodeMap:   %s", truncateForPanel(formatCodeMapMetricsSummaryTUI(m.status.CodeMap), width)),
		"",
		subtleStyle.Render(m.notice),
	}
	return strings.Join(parts, "\n")
}

func (m Model) renderFilesView(width int) string {
	listWidth := width / 3
	if listWidth < 28 {
		listWidth = 28
	}
	if listWidth > width-24 {
		listWidth = width / 2
	}
	previewWidth := width - listWidth - 3
	if previewWidth < 20 {
		previewWidth = 20
	}

	listLines := []string{
		titleStyle.Render("Files"),
		subtleStyle.Render("Keys: j/k or arrows move | enter reload | r refresh"),
		"",
	}
	if len(m.files) == 0 {
		listLines = append(listLines, subtleStyle.Render("No indexed project files yet."))
	} else {
		start := 0
		if m.fileIndex > 6 {
			start = m.fileIndex - 6
		}
		end := start + 14
		if end > len(m.files) {
			end = len(m.files)
		}
		if end-start < 14 && start > 0 {
			start = end - 14
			if start < 0 {
				start = 0
			}
		}
		for i := start; i < end; i++ {
			prefix := "  "
			label := truncateSingleLine(m.files[i], listWidth-4)
			if i == m.fileIndex {
				prefix = "> "
				label = titleStyle.Render(label)
			}
			listLines = append(listLines, prefix+label)
		}
		listLines = append(listLines, "", subtleStyle.Render(fmt.Sprintf("%d files | selected %d/%d", len(m.files), m.fileIndex+1, len(m.files))))
	}

	previewLines := []string{
		titleStyle.Render("Preview"),
		subtleStyle.Render(blankFallback(m.filePath, "Select a file")),
		"",
	}
	content := truncateForPanel(m.filePreview, previewWidth)
	if content == "" {
		content = subtleStyle.Render("No preview loaded.")
	}
	previewLines = append(previewLines, content)
	if m.fileSize > 0 {
		previewLines = append(previewLines, "", subtleStyle.Render(fmt.Sprintf("size=%d bytes", m.fileSize)))
	}

	left := lipgloss.NewStyle().Width(listWidth).Render(strings.Join(listLines, "\n"))
	right := lipgloss.NewStyle().Width(previewWidth).Render(strings.Join(previewLines, "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "   ", right)
}

func (m Model) renderPatchView(width int) string {
	diffPreview := truncateForPanel(strings.TrimSpace(m.diff), width)
	if diffPreview == "" {
		diffPreview = "Working tree is clean."
	}
	patchPreview := truncateForPanel(strings.TrimSpace(m.latestPatch), width)
	if patchPreview == "" {
		patchPreview = "No assistant patch available."
	}
	changed := "(none)"
	if len(m.changed) > 0 {
		changed = strings.Join(m.changed, ", ")
	}
	parts := []string{
		titleStyle.Render("Patch Lab"),
		subtleStyle.Render("Keys: d refresh diff | l load latest patch | c check | a apply | u undo"),
		"",
		"Changed: " + truncateForPanel(changed, width),
		"",
		subtleStyle.Render("Worktree Diff"),
		diffPreview,
		"",
		subtleStyle.Render("Latest Assistant Patch"),
		patchPreview,
		"",
		subtleStyle.Render(m.notice),
	}
	return strings.Join(parts, "\n")
}

func (m Model) renderFooter() string {
	tab := m.tabs[m.activeTab]
	providerName := blankFallback(m.status.Provider, "-")
	modelName := blankFallback(m.status.Model, "-")
	return fmt.Sprintf("%s | %s / %s | %s", tab, providerName, modelName, m.notice)
}

func loadStatusCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return statusLoadedMsg{}
		}
		return statusLoadedMsg{status: eng.Status()}
	}
}

func loadWorkspaceCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return workspaceLoadedMsg{}
		}
		root := strings.TrimSpace(eng.Status().ProjectRoot)
		if root == "" {
			root = "."
		}
		diff, err := gitWorkingDiff(root, 120_000)
		if err != nil {
			return workspaceLoadedMsg{err: err}
		}
		changed, err := gitChangedFiles(root, 12)
		if err != nil {
			return workspaceLoadedMsg{err: err}
		}
		return workspaceLoadedMsg{diff: diff, changed: changed}
	}
}

func loadLatestPatchCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return latestPatchLoadedMsg{}
		}
		return latestPatchLoadedMsg{patch: latestAssistantUnifiedDiff(eng.ConversationActive())}
	}
}

func loadFilesCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return filesLoadedMsg{}
		}
		root := strings.TrimSpace(eng.Status().ProjectRoot)
		if root == "" {
			root = "."
		}
		files, err := listProjectFiles(root, 500)
		return filesLoadedMsg{files: files, err: err}
	}
}

func loadFilePreviewCmd(eng *engine.Engine, rel string) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return filePreviewLoadedMsg{}
		}
		root := strings.TrimSpace(eng.Status().ProjectRoot)
		if root == "" {
			root = "."
		}
		content, size, err := readProjectFile(root, rel, 32_000)
		return filePreviewLoadedMsg{path: rel, content: content, size: size, err: err}
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

func undoConversationCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return conversationUndoMsg{err: fmt.Errorf("engine is nil")}
		}
		removed, err := eng.ConversationUndoLast()
		return conversationUndoMsg{removed: removed, err: err}
	}
}

func startChatStream(ctx context.Context, eng *engine.Engine, question string) <-chan tea.Msg {
	out := make(chan tea.Msg, 64)
	go func() {
		defer close(out)
		if eng == nil {
			out <- chatErrMsg{err: fmt.Errorf("engine is nil")}
			return
		}
		stream, err := eng.StreamAsk(ctx, question)
		if err != nil {
			out <- chatErrMsg{err: err}
			return
		}
		for ev := range stream {
			switch ev.Type {
			case provider.StreamDelta:
				out <- chatDeltaMsg{delta: ev.Delta}
			case provider.StreamError:
				if ev.Err != nil {
					out <- chatErrMsg{err: ev.Err}
				} else {
					out <- chatErrMsg{err: fmt.Errorf("stream error")}
				}
				return
			case provider.StreamDone:
				out <- chatDoneMsg{}
				return
			}
		}
		out <- streamClosedMsg{}
	}()
	return out
}

func waitForStreamMsg(ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return streamClosedMsg{}
		}
		return msg
	}
}

func formatASTLanguageSummaryTUI(items []ast.BackendLanguageStatus) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Language) == "" || strings.TrimSpace(item.Active) == "" {
			continue
		}
		parts = append(parts, item.Language+"="+item.Active)
	}
	return strings.Join(parts, ", ")
}

func formatASTMetricsSummaryTUI(metrics ast.ParseMetrics) string {
	parts := make([]string, 0, 6)
	if metrics.Requests > 0 {
		parts = append(parts, fmt.Sprintf("requests=%d", metrics.Requests))
	}
	if metrics.Parsed > 0 {
		parts = append(parts, fmt.Sprintf("parsed=%d", metrics.Parsed))
	}
	if metrics.CacheHits > 0 || metrics.CacheMisses > 0 {
		parts = append(parts, fmt.Sprintf("cache=%d/%d", metrics.CacheHits, metrics.CacheMisses))
	}
	if metrics.AvgParseDurationMs > 0 {
		parts = append(parts, fmt.Sprintf("avg=%.1fms", metrics.AvgParseDurationMs))
	}
	if metrics.LastLanguage != "" || metrics.LastBackend != "" {
		parts = append(parts, fmt.Sprintf("last=%s/%s", blankFallback(metrics.LastLanguage, "-"), blankFallback(metrics.LastBackend, "-")))
	}
	if len(metrics.ByBackend) > 0 {
		parts = append(parts, "backend["+formatMetricMap(metrics.ByBackend)+"]")
	}
	if len(parts) == 0 {
		return "no parse activity"
	}
	return strings.Join(parts, " ")
}

func formatCodeMapMetricsSummaryTUI(metrics codemap.BuildMetrics) string {
	parts := make([]string, 0, 8)
	if metrics.Builds > 0 {
		parts = append(parts, fmt.Sprintf("builds=%d", metrics.Builds))
	}
	if metrics.FilesRequested > 0 || metrics.FilesProcessed > 0 {
		parts = append(parts, fmt.Sprintf("files=%d/%d", metrics.FilesProcessed, metrics.FilesRequested))
	}
	if metrics.LastDurationMs > 0 {
		parts = append(parts, fmt.Sprintf("last=%dms", metrics.LastDurationMs))
	}
	if metrics.LastGraphNodes > 0 || metrics.LastGraphEdges > 0 {
		parts = append(parts, fmt.Sprintf("graph=%dN/%dE", metrics.LastGraphNodes, metrics.LastGraphEdges))
	}
	if metrics.RecentBuilds > 1 {
		parts = append(parts, fmt.Sprintf("trend=%druns", metrics.RecentBuilds))
	}
	if len(metrics.RecentLanguages) > 0 {
		parts = append(parts, "langs["+formatMetricMap(metrics.RecentLanguages)+"]")
	}
	if len(parts) == 0 {
		return "no codemap activity"
	}
	return strings.Join(parts, " ")
}

func formatMetricMap(items map[string]int64) string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, items[key]))
	}
	return strings.Join(parts, ",")
}

func truncateForPanel(text string, width int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > 18 {
		lines = append(lines[:18], "... [truncated]")
	}
	for i, line := range lines {
		if width > 0 && len([]rune(line)) > width {
			runes := []rune(line)
			trimTo := width - 14
			if trimTo < 0 {
				trimTo = 0
			}
			lines[i] = string(runes[:trimTo]) + "... [trimmed]"
		}
	}
	return strings.Join(lines, "\n")
}

func blankFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (m Model) selectedFile() string {
	if len(m.files) == 0 {
		return ""
	}
	if m.fileIndex < 0 {
		return m.files[0]
	}
	if m.fileIndex >= len(m.files) {
		return m.files[len(m.files)-1]
	}
	return m.files[m.fileIndex]
}

func truncateSingleLine(text string, width int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if width <= 0 {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= width {
		return trimmed
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

func gitWorkingDiff(projectRoot string, maxBytes int64) (string, error) {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		root = "."
	}
	cmd := exec.Command("git", "-C", root, "diff", "--")
	out, err := cmd.Output()
	if err != nil {
		if ee := (&exec.ExitError{}); errors.As(err, &ee) {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	if maxBytes > 0 && int64(len(out)) > maxBytes {
		out = out[:maxBytes]
		return string(out) + "\n... [truncated]\n", nil
	}
	return string(out), nil
}

func latestAssistantUnifiedDiff(active *conversation.Conversation) string {
	if active == nil {
		return ""
	}
	msgs := active.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != types.RoleAssistant {
			continue
		}
		if patch := extractUnifiedDiff(msgs[i].Content); strings.TrimSpace(patch) != "" {
			return patch
		}
	}
	return ""
}

func extractUnifiedDiff(in string) string {
	text := strings.TrimSpace(strings.ReplaceAll(in, "\r\n", "\n"))
	if text == "" {
		return ""
	}
	for _, marker := range []string{"```diff", "```patch", "```"} {
		idx := 0
		for {
			start := strings.Index(text[idx:], marker)
			if start < 0 {
				break
			}
			start += idx
			blockStart := strings.Index(text[start:], "\n")
			if blockStart < 0 {
				break
			}
			blockStart += start + 1
			end := strings.Index(text[blockStart:], "\n```")
			if end < 0 {
				break
			}
			end += blockStart
			block := strings.TrimSpace(text[blockStart:end])
			if looksLikeUnifiedDiff(block) {
				return block
			}
			idx = end + 4
		}
	}
	if looksLikeUnifiedDiff(text) {
		return text
	}
	return ""
}

func looksLikeUnifiedDiff(diff string) bool {
	d := "\n" + strings.TrimSpace(diff) + "\n"
	if strings.Contains(d, "\ndiff --git ") {
		return true
	}
	return strings.Contains(d, "\n--- ") && strings.Contains(d, "\n+++ ") && strings.Contains(d, "\n@@ ")
}

func applyUnifiedDiff(projectRoot, patch string, checkOnly bool) error {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		root = "."
	}
	patch = strings.ReplaceAll(patch, "\r\n", "\n")
	if patch != "" && !strings.HasSuffix(patch, "\n") {
		patch += "\n"
	}
	args := []string{"-C", root, "apply", "--whitespace=nowarn", "--recount"}
	if checkOnly {
		args = append(args, "--check")
	}
	cmd := exec.Command("git", args...)
	cmd.Stdin = strings.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func gitChangedFiles(projectRoot string, limit int) ([]string, error) {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		root = "."
	}
	cmd := exec.Command("git", "-C", root, "status", "--short", "--")
	out, err := cmd.Output()
	if err != nil {
		if ee := (&exec.ExitError{}); errors.As(err, &ee) {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	text := strings.ReplaceAll(string(out), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	files := make([]string, 0, len(lines))
	for _, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if len(raw) > 3 {
			files = append(files, strings.TrimSpace(raw[3:]))
		} else {
			files = append(files, strings.TrimSpace(raw))
		}
		if limit > 0 && len(files) >= limit {
			break
		}
	}
	return files, nil
}

func listProjectFiles(root string, limit int) ([]string, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	out := make([]string, 0, limit)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".dfmc", "node_modules", "vendor", "dist", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		if limit > 0 && len(out) >= limit {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return nil, err
	}
	return out, nil
}

func readProjectFile(root, rel string, maxBytes int) (string, int, error) {
	if strings.TrimSpace(rel) == "" {
		return "", 0, nil
	}
	target, err := resolvePathWithinRoot(root, rel)
	if err != nil {
		return "", 0, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", 0, err
	}
	if info.IsDir() {
		return "", 0, fmt.Errorf("path is a directory: %s", rel)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", 0, err
	}
	size := len(data)
	if maxBytes > 0 && size > maxBytes {
		data = append(data[:maxBytes], []byte("\n... [truncated]\n")...)
	}
	return string(data), size, nil
}

func resolvePathWithinRoot(root, rel string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := rel
	if !filepath.IsAbs(target) {
		target = filepath.Join(absRoot, rel)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rootWithSep := absRoot
	if !strings.HasSuffix(rootWithSep, string(filepath.Separator)) {
		rootWithSep += string(filepath.Separator)
	}
	if absTarget != absRoot && !strings.HasPrefix(absTarget, rootWithSep) {
		return "", fmt.Errorf("path escapes project root: %s", rel)
	}
	return absTarget, nil
}

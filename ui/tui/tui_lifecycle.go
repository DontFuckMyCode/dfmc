// tui_lifecycle.go — Model constructor, ensureDiagnostics defaults,
// Init command batch, projectRoot accessor, and the top-level View
// renderer. Sibling of tui.go which keeps the package doc-comment,
// Options, the Model struct itself, and the mouse/scroll/queue
// constants. Lifecycle entry (Run + panic guard) lives in tui_run.go;
// small types and helpers live in tui_types.go.
//
// Splitting these out keeps tui.go scoped to "what is a Model and
// what knobs does it expose" while this file owns "how does a Model
// get built and rendered".

package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

const brandHeader = " DON'T FUCK MY CODE (DFMC) · dfmc.dev "

func NewModel(ctx context.Context, eng *engine.Engine) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	m := Model{
		ctx: ctx,
		eng: eng,
		// First-class tabs: only the eight surfaces a user touches in the
		// primary daily flow. The other nine (Status, Tools, CodeMap,
		// Prompts, Security, Plans, Context, Orchestrate, Shortcuts)
		// were demoted in tui.md Phase A — they remain reachable via
		// slash commands and F-keys but render as panelOverlayKind
		// overlays rather than dedicated tabs.
		tabs:                  []string{"Chat", "Files", "Patch", "Workflow", "Activity", "Memory", "Conversations", "Providers"},
		activity:              activityPanelState{follow: true},
		diagnosticPanelsState: newDiagnosticPanelsState(),
		chat:                  chatState{streamIndex: -1},
		inputHistory:          inputHistoryState{index: -1},
		toolView:              toolViewState{overrides: map[string]string{}},
		// The chat body shows the welcome + starters on first paint; don't
		// park a duplicate banner in the footer notice slot (signal density).
		sessionStart: time.Now(),
		ui: uiToggles{
			showStatsPanel:    eng == nil || eng.Config == nil || eng.Config.TUI.ShowStatsPanel,
			statsPanelMode:    statsPanelModeOverview,
			keyLogEnabled:     os.Getenv("DFMC_KEYLOG") == "1",
			toolStripExpanded: eng != nil && eng.Config != nil && eng.Config.TUI.ToolStripExpanded,
		},
		viewCache: &viewCacheState{},
	}
	// Seed status synchronously so the chat header renders with real
	// provider info on the first paint, before the async loadStatusCmd
	// delivers. Without this the header shows "⚠ no provider" until the
	// message loop processes statusLoadedMsg.
	if eng != nil {
		m.status = eng.Status()
		m = m.hydrateStatusProviderFromConfig()
		m.bindTelegramBotToPanel(eng.TelegramBot)
	}
	// Surface a primary-conflict notice on startup so the user doesn't
	// have to navigate to the Providers panel to discover that their
	// user-home `primary` is being shadowed by a project override.
	// This was the root cause of "her girişte neden /provider minimax
	// yazıyorum" — saves landed correctly but were invisible behind
	// project's hard-coded primary.
	if eng != nil {
		if userP, projP, conflict := m.detectProviderConfigConflict(); conflict {
			m.notice = fmt.Sprintf("⚠ provider conflict — user: %s, project: %s wins · panel saves now go to project", userP, projP)
		}
	}
	return m
}

func (m *Model) ensureDiagnostics() {
	if m == nil {
		return
	}
	if m.diagnosticPanelsState == nil {
		m.diagnosticPanelsState = newDiagnosticPanelsState()
		return
	}
	m.diagnosticPanelsState.applyDefaults()
}

func (m Model) Init() tea.Cmd {
	m.ensureDiagnostics()
	cmds := []tea.Cmd{
		tea.EnableBracketedPaste,
		loadStatusCmd(m.eng),
		telegramMessageCmd(m.telegram.events),
	}
	if !m.eventRelayExternal {
		cmds = append(cmds, subscribeEventsCmd(m.eng))
	}
	return tea.Batch(cmds...)
}

func (m Model) projectRoot() string {
	if m.eng == nil {
		return ""
	}
	return m.eng.ProjectRoot
}

func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height
	if height <= 0 {
		height = 30
	}
	if m.chat.suppressPasteRender && m.viewCache != nil && m.viewCache.value != "" &&
		m.viewCache.width == width && m.viewCache.height == height && m.viewCache.activeTab == m.activeTab {
		return m.viewCache.value
	}
	m.ensureDiagnostics()

	// 1. Global Branding Header (New)
	branding := brandHeader
	versionInfo := " dev "
	if m.eng != nil && m.eng.Version != "" {
		versionInfo = " " + m.eng.Version + " "
	}

	updateBadge := ""
	if m.eng != nil {
		if update := m.eng.LatestUpdate(); update.UpdateAvailable {
			updateBadge = okStyle.Bold(true).Render(" NEW ") + " "
		}
	}

	headerLine := headerStyle.Width(width).Render(
		lipgloss.JoinHorizontal(lipgloss.Center,
			branding,
			strings.Repeat(" ", max(0, width-lipgloss.Width(branding)-lipgloss.Width(versionInfo)-lipgloss.Width(updateBadge)-2)),
			updateBadge,
			versionInfo,
		),
	)

	bodyWidth := width - 4
	if bodyWidth < 20 {
		bodyWidth = width
	}

	planMode := m.ui.planMode
	tabName := ""
	if m.activeTab >= 0 && m.activeTab < len(m.tabs) {
		tabName = m.tabs[m.activeTab]
	}
	pal := paletteForTab(tabName, planMode)
	strip := renderTopTabStrip(m.tabs, m.activeTab, planMode, width)

	projLabel := ""
	if root := m.projectRoot(); root != "" {
		projLabel = filepath.Base(root)
	}
	parts := []string{"DFMC WORKBENCH · " + tabName}
	if projLabel != "" {
		parts = append(parts, projLabel)
	}
	brandLine := strings.Join(parts, " · ")
	brandTag := subtleStyle.Render(brandLine)

	tabArea := strip + "\n" + brandTag
	footer := statusBarStyle.Width(width).Render(m.renderFooter(width))

	bodyHeight := height - lipgloss.Height(headerLine) - lipgloss.Height(tabArea) - lipgloss.Height(footer)
	if bodyHeight < 6 {
		bodyHeight = 6
	}
	body := m.renderActiveView(bodyWidth, bodyHeight, pal)

	out := lipgloss.JoinVertical(lipgloss.Left, headerLine, tabArea, body, footer)
	if m.viewCache != nil {
		m.viewCache.width = width
		m.viewCache.height = height
		m.viewCache.activeTab = m.activeTab
		m.viewCache.value = out
	}
	return out
}

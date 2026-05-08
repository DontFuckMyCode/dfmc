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
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func NewModel(ctx context.Context, eng *engine.Engine) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	m := Model{
		ctx:                   ctx,
		eng:                   eng,
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
			showStatsPanel:    true,
			statsPanelMode:    statsPanelModeOverview,
			keyLogEnabled:     os.Getenv("DFMC_KEYLOG") == "1",
			toolStripExpanded: true, // expanded by default; /tools toggles to collapsed
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
	cmds := []tea.Cmd{
		tea.EnableBracketedPaste,
		loadStatusCmd(m.eng),
		loadWorkspaceCmd(m.eng),
		loadLatestPatchCmd(m.eng),
		loadFilesCmd(m.eng),
		loadGitInfoCmd(m.projectRoot()),
		heartbeatTickCmd(),
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
	bodyWidth := width - 4
	if bodyWidth < 20 {
		bodyWidth = width
	}

	// New header: a single dense strip with brand on the left, the
	// active tab badge centered between its prev/next neighbours, and
	// a navigation hint on the right. Replaces the old two-line
	// banner + 15-tab row that wrapped on narrow terminals and made
	// the active tab hard to spot. The "DFMC WORKBENCH" text is kept
	// for tests and for users who grep their terminal scrollback.
	planMode := m.ui.planMode
	tabName := ""
	if m.activeTab >= 0 && m.activeTab < len(m.tabs) {
		tabName = m.tabs[m.activeTab]
	}
	pal := paletteForTab(tabName, planMode)
	strip := renderTopTabStrip(m.tabs, m.activeTab, planMode, width)
	// Phase B (single source of truth): runtime/agent state lives in the
	// footer (`footerRuntimeSegment`) and the stats panel — repeating it
	// on the brand line was the highest-traffic offender from Section
	// 1.2's "same info three places" table. The brand string itself
	// stays for scrollback grep / branding parity.
	brandLine := "DFMC WORKBENCH · " + tabName
	brandTag := subtleStyle.Render(brandLine)
	header := strip + "\n" + brandTag
	footer := statusBarStyle.Width(width).Render(m.renderFooter(width))
	bodyHeight := height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyHeight < 6 {
		bodyHeight = 6
	}
	body := m.renderActiveView(bodyWidth, bodyHeight, pal)

	out := lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
	if m.viewCache != nil {
		m.viewCache.width = width
		m.viewCache.height = height
		m.viewCache.activeTab = m.activeTab
		m.viewCache.value = out
	}
	return out
}

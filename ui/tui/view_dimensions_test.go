package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// TestViewFillsExactScreenBox is the resize-robustness contract: whatever the
// terminal shape, View() must (1) emit exactly `height` rows and (2) keep every
// row at most `width` cells wide. Violating either is the user-visible defect
// class — a too-wide row wraps and shifts the whole frame ("kayma"), and a
// row-count mismatch scrolls the terminal. lipgloss .Width()/.Height() only pad,
// they never trim overflow, so this invariant is enforced by clipBlock at each
// frame and normalizeScreen on the final composition; this test locks it in.
func TestViewFillsExactScreenBox(t *testing.T) {
	base := newCoverageModel(t)
	overlays := []string{"", "status", "tools", "codemap", "security", "shortcuts", "telegram", "contexts", "orchestrate", "providerlog", "toolstatus"}
	widths := []int{1, 2, 3, 5, 8, 13, 20, 40, 60, 80, 100, 120, 160, 200}
	heights := []int{1, 2, 3, 5, 8, 12, 24, 40, 60}

	for ti := range base.tabs {
		for _, k := range overlays {
			for _, w := range widths {
				for _, h := range heights {
					m := base
					m.activeTab = ti
					m.ui.panelOverlayKind = k
					m.width = w
					m.height = h
					out := m.View()
					lines := strings.Split(out, "\n")
					if len(lines) != h {
						t.Fatalf("height mismatch tab=%s overlay=%q %dx%d: got %d rows, want %d",
							base.tabs[ti], k, w, h, len(lines), h)
					}
					for i, ln := range lines {
						if cw := ansi.StringWidth(ln); cw > w {
							t.Fatalf("row too wide tab=%s overlay=%q %dx%d row %d: width %d > %d (%q)",
								base.tabs[ti], k, w, h, i, cw, w, ansiStripForTest(ln))
						}
					}
				}
			}
		}
	}
}

// TestViewSelectionModeFillsScreenBox covers the frameless copy/selection path,
// which has no border to contain overflow and is composed separately.
func TestViewSelectionModeFillsScreenBox(t *testing.T) {
	base := newCoverageModel(t)
	base.ui.selectionModeActive = true
	for _, w := range []int{1, 10, 40, 80, 120} {
		for _, h := range []int{1, 3, 8, 24, 50} {
			m := base
			m.width = w
			m.height = h
			out := m.View()
			lines := strings.Split(out, "\n")
			if len(lines) != h {
				t.Fatalf("selection-mode height mismatch %dx%d: got %d rows, want %d", w, h, len(lines), h)
			}
			for i, ln := range lines {
				if cw := ansi.StringWidth(ln); cw > w {
					t.Fatalf("selection-mode row too wide %dx%d row %d: %d > %d", w, h, i, cw, w)
				}
			}
		}
	}
}

// TestChatBodyRespectsWidthBudget locks the horizontal-split contract: the
// chat body block must never render wider than the width it was handed, or it
// shoves the stats panel right and clips the panel's border. Static hint lines
// (empty-state prompt, composer key legend) used to overrun a narrow chatWidth.
func TestChatBodyRespectsWidthBudget(t *testing.T) {
	m := newCoverageModel(t)
	for _, cw := range []int{20, 30, 40, 50, 60, 80, 120} {
		parts := m.renderChatViewParts(cw, true)
		body := fitChatBodyWithScrollbar(parts.Head, parts.Tail, 22, 0, cw)
		body = clipBlock(body, cw, 22)
		for i, ln := range strings.Split(body, "\n") {
			if w := ansi.StringWidth(ln); w > cw {
				t.Fatalf("chat body row %d width %d > budget %d", i, w, cw)
			}
		}
	}
}

// TestViewScreenBoxAcrossInteractiveStates extends the exact-box contract to
// dynamic/modal states — plan mode, selection mode, the floating tasks panel,
// the help overlay, the panel switcher, an open action menu, the routing
// editor, and an active search. "No shift, whatever happens" means these must
// fill the box too, not just the static panels.
func TestViewScreenBoxAcrossInteractiveStates(t *testing.T) {
	base := newCoverageModel(t)
	states := []struct {
		name string
		tab  int
		mut  func(Model) Model
	}{
		{"planMode", 0, func(m Model) Model { m.ui.planMode = true; return m }},
		{"selection", 0, func(m Model) Model { m.ui.selectionModeActive = true; return m }},
		{"tasksPanel", 0, func(m Model) Model { m.ui.showTasksPanel = true; return m }},
		{"helpOverlay", 1, func(m Model) Model { m.ui.showHelpOverlay = true; return m }},
		{"panelSwitcher", 0, func(m Model) Model { m.panelSwitcher.active = true; return m }},
		{"actionMenuStatus", 0, func(m Model) Model { return m.openStatusActionMenu() }},
		{"actionMenuWorkflow", 3, func(m Model) Model { return m.openWorkflowActionMenu() }},
		{"routingEditor", 3, func(m Model) Model { m.workflow.showRoutingEditor = true; return m }},
		{"searchActive", 1, func(m Model) Model {
			m.filesView.searchActive = true
			m.filesView.query = "a-long-query-string-that-could-overrun"
			return m
		}},
		{"slashMenu", 0, func(m Model) Model { m.chat.input = "/"; return m }},
		{"mentionMenu", 0, func(m Model) Model {
			m.chat.input = "@"
			m.filesView.entries = []string{"main.go", "util.go", "api.go", "db.go", "cmd.go"}
			return m
		}},
		{"commandPicker", 0, func(m Model) Model { return m.startCommandPicker("provider", "", false) }},
	}
	widths := []int{20, 40, 80, 100, 160}
	heights := []int{6, 20, 30, 50}
	for _, st := range states {
		for _, w := range widths {
			for _, h := range heights {
				m := base
				m.activeTab = st.tab
				m = st.mut(m)
				m.width, m.height = w, h
				out := m.View()
				lines := strings.Split(out, "\n")
				if len(lines) != h {
					t.Fatalf("state %s %dx%d: got %d rows, want %d", st.name, w, h, len(lines), h)
				}
				for i, ln := range lines {
					if cw := ansi.StringWidth(ln); cw > w {
						t.Fatalf("state %s %dx%d row %d: width %d > %d", st.name, w, h, i, cw, w)
					}
				}
			}
		}
	}
}

// TestActionMenuFitsTheFrame guards the reported defect: opening a panel's
// action menu (right arrow) overran the frame, leaving only the title or first
// row on screen. The menu is now composited centrally and height-bounded; at
// any normal terminal size the View must stay an exact box (no overflow, no
// scroll) and the menu must actually render its chrome + the selected row.
func TestActionMenuFitsTheFrame(t *testing.T) {
	base := newCoverageModel(t)
	cases := []struct {
		name string
		tab  int
		ov   string
		open func(Model) Model
	}{
		{"Files", 1, "", func(m Model) Model {
			m.filesView.entries = []string{"a.go", "b.go", "c.go"}
			return m.openFilesActionMenu()
		}},
		{"Status", 0, "status", func(m Model) Model { return m.openStatusActionMenu() }},
		{"Security", 0, "security", func(m Model) Model { return m.openSecurityActionMenu() }},
		{"CodeMap", 0, "codemap", func(m Model) Model { return m.openCodemapActionMenu() }},
	}
	sizes := [][2]int{{120, 40}, {100, 30}, {90, 24}, {80, 16}}
	for _, c := range cases {
		for _, sz := range sizes {
			m := base
			m.activeTab = c.tab
			m.ui.panelOverlayKind = c.ov
			m = c.open(m)
			n := len(m.actionMenu.actions)
			if n == 0 {
				continue
			}
			m.actionMenu.selected = n - 1 // last row must stay visible
			m.width, m.height = sz[0], sz[1]
			out := m.View()
			lines := strings.Split(out, "\n")
			if len(lines) != sz[1] {
				t.Fatalf("%s menu %dx%d: %d rows, want %d", c.name, sz[0], sz[1], len(lines), sz[1])
			}
			for i, ln := range lines {
				if w := ansi.StringWidth(ln); w > sz[0] {
					t.Fatalf("%s menu %dx%d row %d width %d > %d", c.name, sz[0], sz[1], i, w, sz[0])
				}
			}
			body := ansiStripForTest(out)
			if !strings.Contains(body, "◇ ") {
				t.Errorf("%s menu %dx%d: menu header not rendered", c.name, sz[0], sz[1])
			}
			if !strings.Contains(body, "▶") {
				t.Errorf("%s menu %dx%d: selected-row cursor not visible", c.name, sz[0], sz[1])
			}
		}
	}
}

// TestResizeSweepKeepsScreenBox simulates dragging the terminal corner: it
// carries model state (scroll offsets, an open action menu, active search)
// across a jagged sweep of sizes — grow, shrink, 1x1, extreme aspect ratios —
// feeding each through the real WindowSizeMsg handler. Stale per-panel scroll
// offsets left over from a larger size must not produce an over-wide row or a
// row-count mismatch at the new size. This is the closest test to the actual
// "resize must never shift the frame" requirement.
func TestResizeSweepKeepsScreenBox(t *testing.T) {
	base := newCoverageModel(t)
	base.ui.statsPanelScroll = 30
	base.helpOverlay.scroll = 40
	base.orchestrate.scroll = 40
	base.shortcuts.scroll = 40
	base.chat.scrollback = 80

	sizes := [][2]int{
		{200, 60}, {120, 40}, {80, 24}, {40, 12}, {20, 6}, {200, 8},
		{8, 50}, {300, 30}, {1, 1}, {100, 30}, {35, 35}, {250, 5},
	}
	mutators := map[string]func(Model) Model{
		"plain":  func(m Model) Model { return m },
		"menu":   func(m Model) Model { return m.openStatusActionMenu() },
		"search": func(m Model) Model { m.filesView.searchActive = true; m.filesView.query = "xyz"; return m },
	}
	for _, ov := range []string{"", "status", "orchestrate", "shortcuts"} {
		for name, mut := range mutators {
			m := mut(base)
			m.ui.panelOverlayKind = ov
			for _, sz := range sizes {
				upd, _ := m.handleWindowSizeMsg(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
				m = upd.(Model)
				lines := strings.Split(m.View(), "\n")
				if len(lines) != sz[1] {
					t.Fatalf("[%s ov=%s] %dx%d: %d rows, want %d", name, ov, sz[0], sz[1], len(lines), sz[1])
				}
				for i, ln := range lines {
					if w := ansi.StringWidth(ln); w > sz[0] {
						t.Fatalf("[%s ov=%s] %dx%d row %d width %d > %d", name, ov, sz[0], sz[1], i, w, sz[0])
					}
				}
			}
		}
	}
}

// TestTasksPanelOverlayFitsColumn locks the tiled-pane border math for the
// floating tasks panel: the side-by-side composite must be exactly contentWidth
// wide and innerHeight tall, or the outer clip eats the panel's right/bottom
// border (the same lipgloss border-+2 seam fixed on the stats panel).
func TestTasksPanelOverlayFitsColumn(t *testing.T) {
	m := newCoverageModel(t)
	for _, cw := range []int{80, 100, 140, 200} {
		for _, ih := range []int{12, 20, 30} {
			out := m.renderTasksPanelOverlay("chat body line", cw, ih)
			if w := lipgloss.Width(out); w > cw {
				t.Fatalf("tasks overlay %dx%d width %d > %d", cw, ih, w, cw)
			}
			if h := lipgloss.Height(out); h > ih {
				t.Fatalf("tasks overlay %dx%d height %d > %d", cw, ih, h, ih)
			}
		}
	}
}

// TestOverlayTitleReflectsActivePanel guards the "stuck title" defect: when a
// demoted-panel overlay (F9+: Status, CodeMap, Security, ...) opens over a tab,
// the top brand line and the footer chip must name the overlay, not the
// underlying tab they happen to cover.
func TestOverlayTitleReflectsActivePanel(t *testing.T) {
	cases := map[string]string{
		"codemap":     "CODEMAP",
		"security":    "SECURITY",
		"providerlog": "PROVIDER LOG",
		"toolstatus":  "TOOL STATUS",
		"shortcuts":   "SHORTCUTS",
	}
	for kind, want := range cases {
		m := newCoverageModel(t)
		m.width, m.height = 100, 30
		m.activeTab = 0 // Chat underneath
		m.ui.panelOverlayKind = kind
		lines := strings.Split(m.View(), "\n")
		brand := ansiStripForTest(lines[2])
		if !strings.Contains(brand, want) {
			t.Errorf("overlay %q brand line = %q, want it to contain %q", kind, brand, want)
		}
		if strings.Contains(brand, "Chat") {
			t.Errorf("overlay %q brand line still shows underlying tab: %q", kind, brand)
		}
	}
}

// TestPanelCardGridFitsWidth locks the card-grid budget so a 2-column status
// grid never overruns its content width (which clipped the right card border).
func TestPanelCardGridFitsWidth(t *testing.T) {
	m := newCoverageModel(t)
	for _, cw := range []int{40, 54, 74, 94, 134, 194} {
		body := m.renderStatusViewSized(cw, 26)
		for i, ln := range strings.Split(body, "\n") {
			if w := ansi.StringWidth(ln); w > cw {
				t.Fatalf("status row %d width %d > content width %d", i, w, cw)
			}
		}
	}
}

// TestClipBlockTrimsBothAxes is a focused unit check on the helper the contract
// relies on: it must drop overflow rows and ANSI-safely truncate wide lines
// while leaving content that already fits untouched.
func TestClipBlockTrimsBothAxes(t *testing.T) {
	in := "abcdef\nghijkl\nmnopqr"
	if got := clipBlock(in, 3, 2); got != "abc\nghi" {
		t.Fatalf("clipBlock width+height: got %q", got)
	}
	if got := clipBlock(in, 0, 2); got != "abcdef\nghijkl" {
		t.Fatalf("clipBlock height-only: got %q", got)
	}
	short := "hi\nyo"
	if got := clipBlock(short, 10, 10); got != short {
		t.Fatalf("clipBlock no-op on fitting content: got %q", got)
	}
	styled := "\x1b[31mredtext\x1b[0m"
	got := clipBlock(styled, 3, 0)
	if ansi.StringWidth(got) != 3 {
		t.Fatalf("clipBlock ANSI width: got visible width %d for %q", ansi.StringWidth(got), got)
	}
}

// TestNormalizeScreenPadsAndClips checks the final whole-screen pass squares the
// output to an exact height (padding short, clipping tall) and width.
func TestNormalizeScreenPadsAndClips(t *testing.T) {
	cases := []struct {
		in            string
		w, h          int
		wantRows      int
		wantMaxColumn int
	}{
		{"a\nb", 5, 4, 4, 5},          // pad up to 4 rows
		{"a\nb\nc\nd\ne", 5, 3, 3, 5}, // clip down to 3 rows
		{"abcdefgh", 4, 1, 1, 4},      // clip wide line
	}
	for _, c := range cases {
		out := normalizeScreen(c.in, c.w, c.h)
		lines := strings.Split(out, "\n")
		if len(lines) != c.wantRows {
			t.Fatalf("normalizeScreen(%q,%d,%d) rows=%d want %d", c.in, c.w, c.h, len(lines), c.wantRows)
		}
		for _, ln := range lines {
			if ansi.StringWidth(ln) > c.wantMaxColumn {
				t.Fatalf("normalizeScreen left a %d-wide row (limit %d)", ansi.StringWidth(ln), c.wantMaxColumn)
			}
		}
	}
}

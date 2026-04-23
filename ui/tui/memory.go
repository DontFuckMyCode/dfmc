package tui

// memory.go — the Memory panel surfaces what DFMC remembers across
// sessions: episodic interactions (question/answer pairs scoped to a
// project) and semantic facts (long-lived knowledge). The backing Store
// lives in internal/memory; this panel is a read-only view on top with
// tier filtering and substring search.
//
// Shape: a list of types.MemoryEntry, a tier filter (all | episodic |
// semantic), a search query, and a scroll offset. Refresh is manual —
// the memory store doesn't publish mutation events, so `r` re-runs the
// list query and tab-switch triggers an initial load.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

const (
	// memoryListLimit caps how many entries the panel fetches per tier.
	// The backing Store.List orders by UpdatedAt desc, so this window is
	// "the 200 most-recently-touched entries" — enough for a TUI without
	// dragging in years of history.
	memoryListLimit = 200

	// memoryTierAll is a synthetic filter: the panel merges both tiers.
	// Kept as a string so it parks in the same slot as real MemoryTier
	// values without polluting pkg/types.
	memoryTierAll = "all"
)

type memoryLoadedMsg struct {
	entries []types.MemoryEntry
	tier    string
	err     error
}

// loadMemoryCmd fetches memory entries for the given tier. Tier
// "all" merges both persisted tiers (episodic + semantic).
func loadMemoryCmd(eng *engine.Engine, tier string) tea.Cmd {
	return func() tea.Msg {
		if eng == nil || eng.Memory == nil {
			return memoryLoadedMsg{tier: tier}
		}
		var (
			entries []types.MemoryEntry
			err     error
		)
		switch tier {
		case string(types.MemoryEpisodic), string(types.MemorySemantic):
			entries, err = eng.Memory.List(types.MemoryTier(tier), memoryListLimit)
		default:
			tier = memoryTierAll
			ep, e1 := eng.Memory.List(types.MemoryEpisodic, memoryListLimit)
			if e1 != nil {
				return memoryLoadedMsg{tier: tier, err: e1}
			}
			sem, e2 := eng.Memory.List(types.MemorySemantic, memoryListLimit)
			if e2 != nil {
				return memoryLoadedMsg{tier: tier, err: e2}
			}
			entries = append(ep, sem...)
		}
		return memoryLoadedMsg{entries: entries, tier: tier, err: err}
	}
}

// filteredMemoryEntries applies the in-panel search query over the
// loaded entries. The filter matches Category / Key / Value / ID.
func filteredMemoryEntries(entries []types.MemoryEntry, query string) []types.MemoryEntry {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return entries
	}
	out := entries[:0:0]
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Category), q) ||
			strings.Contains(strings.ToLower(e.Key), q) ||
			strings.Contains(strings.ToLower(e.Value), q) ||
			strings.Contains(strings.ToLower(e.ID), q) {
			out = append(out, e)
		}
	}
	return out
}

// formatMemoryRow renders one entry as a single line, clipped to width.
// Shape: `[tier] category · key — value`. When Category/Key are blank
// (bare episodic interaction) we fall back to the value on its own.
func formatMemoryRow(e types.MemoryEntry, width int) string {
	tierLabel := strings.ToUpper(string(e.Tier))
	if tierLabel == "" {
		tierLabel = "MEM"
	}
	head := subtleStyle.Render("[" + tierLabel + "]")
	var body strings.Builder
	cat := strings.TrimSpace(e.Category)
	key := strings.TrimSpace(e.Key)
	val := strings.TrimSpace(e.Value)
	if cat != "" {
		body.WriteString(accentStyle.Render(cat))
		if key != "" {
			body.WriteString(" · ")
			body.WriteString(key)
		}
	} else if key != "" {
		body.WriteString(key)
	}
	if val != "" {
		if body.Len() > 0 {
			body.WriteString(" — ")
		}
		body.WriteString(val)
	}
	line := head + " " + oneLine(body.String())
	if width > 0 {
		return truncateSingleLine(line, width)
	}
	return line
}

// oneLine collapses internal whitespace so the panel stays aligned
// even when entries carry embedded newlines or tabs.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

func (m Model) renderMemoryView(width int) string {
	width = clampInt(width, 24, 1000)
	tier := m.memory.tier
	if tier == "" {
		tier = memoryTierAll
	}
	hint := subtleStyle.Render("j/k scroll · t toggle tier · / search · r refresh · c clear search")
	header := sectionHeader("◈", "Memory")
	tierLine := subtleStyle.Render("tier: ") + accentStyle.Render(tier)
	if strings.TrimSpace(m.memory.query) != "" {
		tierLine += subtleStyle.Render("  query: ") + m.memory.query
	}
	lines := []string{header, hint, tierLine, renderDivider(width - 2)}

	if m.memory.err != "" {
		lines = append(lines, "", warnStyle.Render("error · "+m.memory.err))
		return strings.Join(lines, "\n")
	}
	if m.memory.loading {
		lines = append(lines, "", subtleStyle.Render("loading..."))
		return strings.Join(lines, "\n")
	}

	filtered := filteredMemoryEntries(m.memory.entries, m.memory.query)
	if len(filtered) == 0 {
		lines = append(lines, "")
		if len(m.memory.entries) == 0 {
			lines = append(lines,
				subtleStyle.Render("No memory entries in this view."),
				subtleStyle.Render("Use `dfmc memory add <text>` or ask the agent to remember something."),
			)
		} else {
			lines = append(lines,
				warnStyle.Render(fmt.Sprintf("No matches for %q in %d memory entries.", m.memory.query, len(m.memory.entries))),
				subtleStyle.Render("Press c to clear the query, or / to edit it."),
			)
		}
		return strings.Join(lines, "\n")
	}

	// Scroll window: clamp offset into range, then show up to the rest.
	scroll := m.memory.scroll
	if scroll < 0 {
		scroll = 0
	}
	if scroll >= len(filtered) {
		scroll = len(filtered) - 1
	}
	for _, e := range filtered[scroll:] {
		lines = append(lines, formatMemoryRow(e, width-2))
	}

	lines = append(lines, "", subtleStyle.Render(fmt.Sprintf(
		"%d shown · %d loaded · tier=%s",
		len(filtered), len(m.memory.entries), tier,
	)))
	return strings.Join(lines, "\n")
}

// handleMemoryKey drives the Memory panel. The search input mode owns
// the keyboard while active; returning Cmd(nil) keeps the model as-is.
func (m Model) handleMemoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.memory.searchActive {
		return m.handleMemorySearchKey(msg)
	}
	total := len(filteredMemoryEntries(m.memory.entries, m.memory.query))
	step := 1
	pageStep := 10
	switch msg.String() {
	case "j", "down":
		if m.memory.scroll+step < total {
			m.memory.scroll += step
		}
	case "k", "up":
		if m.memory.scroll >= step {
			m.memory.scroll -= step
		} else {
			m.memory.scroll = 0
		}
	case "pgdown":
		if m.memory.scroll+pageStep < total {
			m.memory.scroll += pageStep
		} else if total > 0 {
			m.memory.scroll = total - 1
		}
	case "pgup":
		if m.memory.scroll >= pageStep {
			m.memory.scroll -= pageStep
		} else {
			m.memory.scroll = 0
		}
	case "g":
		m.memory.scroll = 0
	case "G":
		if total > 0 {
			m.memory.scroll = total - 1
		}
	case "t":
		// Cycle all → episodic → semantic → all.
		m.memory.tier = nextMemoryTier(m.memory.tier)
		m.memory.scroll = 0
		m.memory.loading = true
		m.memory.err = ""
		return m, loadMemoryCmd(m.eng, m.memory.tier)
	case "r":
		m.memory.loading = true
		m.memory.err = ""
		return m, loadMemoryCmd(m.eng, m.memory.tier)
	case "/":
		m.memory.searchActive = true
		// Preserve previous query so the user can refine rather than
		// starting blank each time.
		return m, nil
	case "c":
		m.memory.query = ""
		m.memory.scroll = 0
	}
	return m, nil
}

// handleMemorySearchKey is active while `/`-search input mode is on.
// Enter confirms the query, Esc cancels and keeps the previous query,
// Backspace trims one rune, printable runes append. We don't need full
// textinput state — memory search is short and ephemeral.
func (m Model) handleMemorySearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.memory.searchActive = false
		m.memory.scroll = 0
		return m, nil
	case tea.KeyEsc:
		m.memory.searchActive = false
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.memory.query); len(r) > 0 {
			m.memory.query = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.memory.query += msg.String()
		return m, nil
	}
	return m, nil
}

func nextMemoryTier(current string) string {
	switch current {
	case "", memoryTierAll:
		return string(types.MemoryEpisodic)
	case string(types.MemoryEpisodic):
		return string(types.MemorySemantic)
	default:
		return memoryTierAll
	}
}

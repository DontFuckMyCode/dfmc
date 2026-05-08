package tui

// memory.go — the Memory panel surfaces what DFMC remembers across
// sessions: episodic interactions (question/answer pairs scoped to a
// project) and semantic facts (long-lived knowledge). The backing Store
// lives in internal/memory; this panel is a read-only view on top with
// tier filtering and substring search.
//
// This file owns the load command, key dispatch, action menu, and
// tier-cycle helper. Rendering (filteredMemoryEntries, formatMemoryRow,
// oneLine, renderMemoryView, memoryTopBanner) lives in memory_render.go.
//
// Shape: a list of types.MemoryEntry, a tier filter (all | episodic |
// semantic), a search query, and a scroll offset. Refresh is manual —
// the memory store doesn't publish mutation events, so `r` re-runs the
// list query and tab-switch triggers an initial load.

import (
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
			entries, err = eng.MemoryList(types.MemoryTier(tier), memoryListLimit)
		default:
			tier = memoryTierAll
			ep, e1 := eng.MemoryList(types.MemoryEpisodic, memoryListLimit)
			if e1 != nil {
				return memoryLoadedMsg{tier: tier, err: e1}
			}
			sem, e2 := eng.MemoryList(types.MemorySemantic, memoryListLimit)
			if e2 != nil {
				return memoryLoadedMsg{tier: tier, err: e2}
			}
			entries = append(ep, sem...)
		}
		return memoryLoadedMsg{entries: entries, tier: tier, err: err}
	}
}


// openMemoryActionMenu builds the contextual action list for the
// Memory panel. Mirrors the F2-F6 ACTIONS card content but routes
// every entry through the shared arrow-driven menu.
func (m Model) openMemoryActionMenu() Model {
	actions := []panelAction{
		{Label: "Cycle tier (all → episodic → semantic)", Accel: "t",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.memory.tier = nextMemoryTier(m.memory.tier)
				m.memory.scroll = 0
				m.memory.loading = true
				m.memory.err = ""
				return m, loadMemoryCmd(m.eng, m.memory.tier)
			}},
		{Label: "Refresh from store", Accel: "r",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.memory.loading = true
				m.memory.err = ""
				return m, loadMemoryCmd(m.eng, m.memory.tier)
			}},
		{Label: "Search…", Accel: "/",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.memory.searchActive = true
				return m, nil
			}},
		{Label: "Clear search query", Accel: "c",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.memory.query = ""
				m.memory.scroll = 0
				return m, nil
			}},
		// Phase H item 1 — mutation actions on the highlighted entry.
		// Delete is the destructive path (engine.MemoryDelete walks both
		// tiers and is idempotent); promote graduates an episodic entry
		// into the semantic tier (no-op when already semantic). Both
		// queue a reload so the panel reflects the new state without a
		// manual refresh.
		{Label: "Delete the highlighted entry", Accel: "d",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m.deleteSelectedMemoryEntry()
			}},
		{Label: "Promote highlighted entry to semantic tier", Accel: "p",
			Handler: func(m Model) (Model, tea.Cmd) {
				return m.promoteSelectedMemoryEntry()
			}},
	}
	return m.openActionMenu("Memory", "Memory actions", actions)
}

// selectedMemoryEntry returns the entry under the current scroll row
// of the filtered view, or (zero, false) when the panel has no
// highlight. Both delete and promote use this to resolve the target
// without the caller having to re-walk the filter.
func (m Model) selectedMemoryEntry() (types.MemoryEntry, bool) {
	filtered := filteredMemoryEntries(m.memory.entries, m.memory.query)
	if len(filtered) == 0 || m.memory.scroll < 0 || m.memory.scroll >= len(filtered) {
		return types.MemoryEntry{}, false
	}
	return filtered[m.memory.scroll], true
}

// deleteSelectedMemoryEntry — Phase H item 1 surface for `d`. Looks up
// the highlighted entry, calls engine.MemoryDelete, drops the row from
// the in-memory list immediately so the panel updates without waiting
// for the reload, and queues a refresh as belt-and-braces.
func (m Model) deleteSelectedMemoryEntry() (Model, tea.Cmd) {
	entry, ok := m.selectedMemoryEntry()
	if !ok {
		m.notice = "No memory entry selected — j/k highlights a row, then d deletes it."
		return m, nil
	}
	if m.eng == nil {
		m.notice = "Engine not ready — cannot delete memory entries yet."
		return m, nil
	}
	if err := m.eng.MemoryDelete(entry.ID); err != nil {
		m.notice = "Delete failed: " + err.Error()
		return m, nil
	}
	// Drop locally so the row vanishes immediately; the reload below
	// reconciles in case anything else mutated the store concurrently.
	m.memory.entries = removeMemoryEntryByID(m.memory.entries, entry.ID)
	if m.memory.scroll >= len(filteredMemoryEntries(m.memory.entries, m.memory.query)) && m.memory.scroll > 0 {
		m.memory.scroll--
	}
	if m.memory.expandedID == entry.ID {
		m.memory.expandedID = ""
	}
	m.notice = "Deleted memory entry — store updated."
	m.memory.loading = true
	return m, loadMemoryCmd(m.eng, m.memory.tier)
}

// promoteSelectedMemoryEntry — Phase H item 1 surface for `p`. Moves
// the highlighted entry from episodic to semantic via engine.MemoryPromote
// and triggers a reload so the row migrates between tiers visually.
// Already-semantic entries return a friendlier "nothing to promote"
// notice rather than calling the engine.
func (m Model) promoteSelectedMemoryEntry() (Model, tea.Cmd) {
	entry, ok := m.selectedMemoryEntry()
	if !ok {
		m.notice = "No memory entry selected — j/k highlights a row, then p promotes it."
		return m, nil
	}
	// Already-semantic check is engine-independent — answer it first so
	// a user pressing `p` on a semantic row gets the friendly notice
	// even when the engine isn't available (e.g. degraded-storage start).
	if entry.Tier == types.MemorySemantic {
		m.notice = "Entry is already semantic — nothing to promote."
		return m, nil
	}
	if m.eng == nil {
		m.notice = "Engine not ready — cannot promote memory entries yet."
		return m, nil
	}
	if err := m.eng.MemoryPromote(entry.ID); err != nil {
		m.notice = "Promote failed: " + err.Error()
		return m, nil
	}
	m.notice = "Promoted episodic entry → semantic tier."
	m.memory.loading = true
	return m, loadMemoryCmd(m.eng, m.memory.tier)
}

// removeMemoryEntryByID drops the entry whose ID matches `id` from the
// slice, preserving order. Used by deleteSelectedMemoryEntry to update
// the panel optimistically before the reload arrives.
func removeMemoryEntryByID(entries []types.MemoryEntry, id string) []types.MemoryEntry {
	out := make([]types.MemoryEntry, 0, len(entries))
	for _, e := range entries {
		if e.ID == id {
			continue
		}
		out = append(out, e)
	}
	return out
}

// handleMemoryKey drives the Memory panel. The search input mode owns
// the keyboard while active; returning Cmd(nil) keeps the model as-is.
func (m Model) handleMemoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.memory.searchActive {
		return m.handleMemorySearchKey(msg)
	}
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	// Right / l opens the action menu (cycle tier · refresh · search ·
	// clear). Enter is reserved for the per-entry expand toggle (Phase
	// H item 3) — that lives in the switch below so each row's full
	// value can be inspected without leaving the panel.
	if s := msg.String(); s == "right" || s == "l" {
		return m.openMemoryActionMenu(), nil
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
	case "enter":
		// Phase H item 3: enter expands the currently-highlighted entry
		// to its full multi-line value, or collapses it back to a single
		// row if it was already expanded. Useful for episodic memory
		// where the body is a paragraph and gets clipped at the panel
		// width otherwise.
		filtered := filteredMemoryEntries(m.memory.entries, m.memory.query)
		if len(filtered) == 0 || m.memory.scroll < 0 || m.memory.scroll >= len(filtered) {
			return m, nil
		}
		id := filtered[m.memory.scroll].ID
		if m.memory.expandedID == id {
			m.memory.expandedID = ""
		} else {
			m.memory.expandedID = id
		}
		return m, nil
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
	case "d":
		// Phase H item 1 — delete the highlighted entry. Calls through
		// engine.MemoryDelete (idempotent across both tiers), drops the
		// row locally, and reloads to reconcile with the store.
		nm, cmd := m.deleteSelectedMemoryEntry()
		return nm, cmd
	case "p":
		// Phase H item 1 — promote the highlighted episodic entry into
		// the semantic tier. No-op when already semantic (handler shows
		// a friendly "nothing to promote" notice instead of erroring).
		nm, cmd := m.promoteSelectedMemoryEntry()
		return nm, cmd
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
	// Phase H item 2: cycle order is working → episodic → semantic →
	// all → working. Working is the default landing (recent
	// scratchpad), the others step through the tiers, and `all` is
	// the merged-everything view at the end.
	switch current {
	case "", string(types.MemoryWorking):
		return string(types.MemoryEpisodic)
	case string(types.MemoryEpisodic):
		return string(types.MemorySemantic)
	case string(types.MemorySemantic):
		return memoryTierAll
	default:
		return string(types.MemoryWorking)
	}
}

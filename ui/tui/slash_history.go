package tui

// slash_history.go — `/history` and `/jump` slash commands for the
// chat transcript.
//
//   /history search <q>   case-insensitive substring scan across every
//                         transcript row, printed as #N · role · 1-line
//                         snippet entries so the user can pick a turn
//                         to dig into.
//   /history list         enumerate assistant turns with their first
//                         line preview (think "table of contents").
//   /jump N               scroll the transcript so assistant turn #N is
//                         visible. Cheap counterpart to /copy N — finds
//                         what /history search told you to look at.
//
// Search is intentionally dumb: just `strings.Contains` after lowercasing
// both sides. No regex flag — when users need that they can dump the
// transcript via /export and grep externally. The handler reaches into
// existing transcript state, never mutates content.

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleHistorySlash(args []string) (tea.Model, tea.Cmd, bool) {
	if len(args) == 0 {
		return m.historyUsage(), nil, true
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	rest := args[1:]
	switch sub {
	case "search", "find", "grep":
		query := strings.TrimSpace(strings.Join(rest, " "))
		if query == "" {
			m.notice = "/history search: pass a query string."
			return m.appendSystemMessage("Usage: /history search <q> — substring scan, case-insensitive."), nil, true
		}
		return m.runHistorySearch(query), nil, true
	case "list", "ls", "toc":
		return m.runHistoryList(), nil, true
	}
	return m.historyUsage(), nil, true
}

func (m Model) historyUsage() Model {
	return m.appendSystemMessage("Usage: /history search <q> | /history list. Pair with /jump N once you've spotted the turn.")
}

func (m Model) runHistorySearch(query string) Model {
	needle := strings.ToLower(query)
	// Stash the query so the transcript renderer can highlight matches
	// on the next paint. Clear when the search returns zero hits so we
	// don't leave a stale highlight from a query the user already
	// abandoned.
	m.chat.lastSearchQuery = query
	type hit struct {
		idx      int
		role     string
		turn     int
		snippet  string
	}
	hits := make([]hit, 0, 16)
	assistantSeen := 0
	for i, line := range m.chat.transcript {
		role := strings.ToLower(string(line.Role))
		if role == string(chatRoleAssistant) {
			assistantSeen++
		}
		body := strings.ToLower(line.Content)
		if body == "" || !strings.Contains(body, needle) {
			continue
		}
		// First matched line gives the user enough context to recognise
		// the turn; truncate hard so the chat panel doesn't blow up on
		// long assistant answers.
		snippet := firstMatchingLine(line.Content, needle)
		hits = append(hits, hit{
			idx:     i,
			role:    role,
			turn:    assistantSeen,
			snippet: snippet,
		})
	}
	if len(hits) == 0 {
		// No hits means nothing to highlight either; drop the stash so
		// the next paint doesn't keep painting a dead query.
		m.chat.lastSearchQuery = ""
		m.notice = fmt.Sprintf("/history search %q — no matches.", query)
		return m.appendSystemMessage(fmt.Sprintf("/history search %q: 0 matches across %d transcript rows.", query, len(m.chat.transcript)))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "/history search %q → %d match(es):\n", query, len(hits))
	const maxRows = 12
	for i, h := range hits {
		if i == maxRows {
			fmt.Fprintf(&b, "  ... %d more hidden — refine the query for full list.\n", len(hits)-maxRows)
			break
		}
		anchor := ""
		if h.role == string(chatRoleAssistant) && h.turn > 0 {
			anchor = fmt.Sprintf("  ↳ /jump %d", h.turn)
		}
		fmt.Fprintf(&b, "  row %d · %s · %s%s\n", h.idx, h.role, h.snippet, anchor)
	}
	m.notice = fmt.Sprintf("/history search %q — %d match(es). /next /prev to step.", query, len(hits))
	return m.appendSystemMessage(strings.TrimRight(b.String(), "\n"))
}

func (m Model) runHistoryList() Model {
	idxs := m.assistantIndices()
	if len(idxs) == 0 {
		m.notice = "/history list — no assistant turns yet."
		return m.appendSystemMessage("/history list: 0 assistant responses recorded.")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "/history list → %d assistant turn(s):\n", len(idxs))
	const maxRows = 20
	for i, idx := range idxs {
		if i == maxRows {
			fmt.Fprintf(&b, "  ... %d more — use /history search <q> to filter.\n", len(idxs)-maxRows)
			break
		}
		line := m.chat.transcript[idx]
		snippet := firstNonEmptyLine(line.Content)
		if w := []rune(snippet); len(w) > 80 {
			snippet = string(w[:77]) + "..."
		}
		stamp := ""
		if !line.Timestamp.IsZero() {
			stamp = line.Timestamp.Format("15:04:05") + " · "
		}
		fmt.Fprintf(&b, "  #%d · %s%s\n", i+1, stamp, snippet)
	}
	m.notice = fmt.Sprintf("/history list — %d turns.", len(idxs))
	return m.appendSystemMessage(strings.TrimRight(b.String(), "\n"))
}

// handleNextHitSlash and handlePrevHitSlash jump between rows that
// matched the most recent /history search. Without an active query
// the commands report cleanly so users learn the pairing. Direction:
// +1 walks newer-than-current, -1 walks older. The "current" hit is
// derived from the current scrollback position so repeated taps
// step through the list in either direction.
func (m Model) handleNextHitSlash(_ []string) (tea.Model, tea.Cmd, bool) {
	return m.stepSearchHit(+1), nil, true
}

func (m Model) handlePrevHitSlash(_ []string) (tea.Model, tea.Cmd, bool) {
	return m.stepSearchHit(-1), nil, true
}

func (m Model) stepSearchHit(dir int) Model {
	q := strings.TrimSpace(m.chat.lastSearchQuery)
	if q == "" {
		m.notice = "/next: no active search. Run /history search <q> first."
		return m
	}
	hits := m.searchHitIndices(q)
	if len(hits) == 0 {
		m.notice = fmt.Sprintf("/next: 0 matches for %q (query was stale).", q)
		m.chat.lastSearchQuery = ""
		return m
	}
	target := pickNextHit(hits, m.currentTranscriptAnchor(), dir)
	m.chat.scrollback = scrollbackToReach(m.chat.transcript, target)
	maxBack := estimateTranscriptLines(m.chat.transcript)
	if m.chat.scrollback > maxBack {
		m.chat.scrollback = maxBack
	}
	// 1-based hit number for the notice so the user can match it to
	// the /history search printout.
	pos := 1
	for i, idx := range hits {
		if idx == target {
			pos = i + 1
			break
		}
	}
	m.notice = fmt.Sprintf("/%s: hit %d / %d (row %d, query %q).", verbFor(dir), pos, len(hits), target, q)
	return m
}

func verbFor(dir int) string {
	if dir < 0 {
		return "prev"
	}
	return "next"
}

func (m Model) searchHitIndices(query string) []int {
	needle := strings.ToLower(query)
	out := make([]int, 0, 8)
	for i, line := range m.chat.transcript {
		if needle == "" || strings.Contains(strings.ToLower(line.Content), needle) {
			out = append(out, i)
		}
	}
	return out
}

// currentTranscriptAnchor returns the transcript row index currently
// closest to the top of the viewport. Mirrors the math used by
// /jump so scroll position stays linked to a "row anchor" rather
// than a raw line offset.
func (m Model) currentTranscriptAnchor() int {
	if m.chat.scrollback == 0 {
		return len(m.chat.transcript) - 1
	}
	// Walk from the end accumulating lines until we cross scrollback.
	acc := 0
	for i := len(m.chat.transcript) - 1; i >= 0; i-- {
		acc += 6 + strings.Count(m.chat.transcript[i].Content, "\n")
		if acc >= m.chat.scrollback {
			return i
		}
	}
	return 0
}

func scrollbackToReach(transcript []chatLine, target int) int {
	lines := 0
	for i := target; i < len(transcript); i++ {
		lines += 6 + strings.Count(transcript[i].Content, "\n")
	}
	return lines
}

// pickNextHit returns the hit row index that follows `anchor` in the
// requested direction. Wraps around at either end so a user mashing
// /next at the bottom of the list cycles back to the first hit
// instead of getting a "no more matches" dead-end.
func pickNextHit(hits []int, anchor, dir int) int {
	if len(hits) == 0 {
		return 0
	}
	if dir > 0 {
		for _, h := range hits {
			if h > anchor {
				return h
			}
		}
		return hits[0]
	}
	for i := len(hits) - 1; i >= 0; i-- {
		if hits[i] < anchor {
			return hits[i]
		}
	}
	return hits[len(hits)-1]
}

func (m Model) handleJumpSlash(args []string) (tea.Model, tea.Cmd, bool) {
	if len(args) == 0 {
		m.notice = "/jump: pass an assistant turn number."
		return m.appendSystemMessage("Usage: /jump N — scrolls the transcript so assistant turn #N is visible. Pair with /history search to find N."), nil, true
	}
	n, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil || n <= 0 {
		m.notice = "/jump: positive integer turn number required."
		return m.appendSystemMessage("/jump expects a positive integer (the #N chip in an assistant header)."), nil, true
	}
	idxs := m.assistantIndices()
	if n > len(idxs) {
		m.notice = fmt.Sprintf("/jump %d — only %d assistant turn(s).", n, len(idxs))
		return m.appendSystemMessage(fmt.Sprintf("/jump %d: out of range (only %d assistant turn(s)).", n, len(idxs))), nil, true
	}
	target := idxs[n-1]
	// Count user turns at or before the target so scrollback can land
	// just above that turn. Each user-turn separator is one logical
	// "unit" the scroll math operates on; estimateTranscriptLines gives
	// us the upper bound to clamp against.
	turnsAbove := 0
	for i := target; i < len(m.chat.transcript); i++ {
		if m.chat.transcript[i].Role.Eq(chatRoleUser) {
			turnsAbove++
		}
	}
	// Use the line-count scroll metric: estimate lines from target → end.
	lines := 0
	for i := target; i < len(m.chat.transcript); i++ {
		lines += 6 + strings.Count(m.chat.transcript[i].Content, "\n")
	}
	m.chat.scrollback = lines
	maxBack := estimateTranscriptLines(m.chat.transcript)
	if m.chat.scrollback > maxBack {
		m.chat.scrollback = maxBack
	}
	m.notice = fmt.Sprintf("/jump %d — scrolled to assistant turn #%d (%d turn(s) below current view).", n, n, turnsAbove)
	return m, nil, true
}

func firstMatchingLine(body, needleLower string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(strings.ToLower(trimmed), needleLower) {
			return truncSnippet(trimmed, 100)
		}
	}
	return truncSnippet(strings.TrimSpace(body), 100)
}

func firstNonEmptyLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			return t
		}
	}
	return ""
}

func truncSnippet(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-3]) + "..."
}

package tui

import tea "github.com/charmbracelet/bubbletea"

func scrollIndexDown(current, total, step int) int {
	if step <= 0 {
		step = 1
	}
	if total <= 0 {
		return 0
	}
	next := current + step
	if next >= total {
		return total - 1
	}
	return next
}

func scrollIndexUp(current, step int) int {
	if step <= 0 {
		step = 1
	}
	if current < step {
		return 0
	}
	return current - step
}

func lastScrollIndex(total int) int {
	if total <= 0 {
		return 0
	}
	return total - 1
}

func applyInlineSearchTextKey(query string, msg tea.KeyMsg) (string, bool) {
	switch msg.Type {
	case tea.KeyBackspace:
		if r := []rune(query); len(r) > 0 {
			return string(r[:len(r)-1]), true
		}
		return query, true
	case tea.KeyRunes, tea.KeySpace:
		return query + msg.String(), true
	default:
		return query, false
	}
}

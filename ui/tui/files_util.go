package tui

import "strings"

func (m Model) visibleFilesEntries() []string {
	return filteredFilesEntries(m.filesView.entries, m.filesView.query)
}

func filteredFilesEntries(entries []string, q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return entries
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e), q) {
			out = append(out, e)
		}
	}
	return out
}

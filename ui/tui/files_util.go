package tui

import "strings"

func filteredFilesEntries(entries []string, q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return entries
	}
	out := entries[:0]
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e), q) {
			out = append(out, e)
		}
	}
	return out
}
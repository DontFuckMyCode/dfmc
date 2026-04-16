package tui

// mention.go — @-mention ranker used by the chat composer.
//
// Ranking prioritizes matches in this order:
//
//   1. exact basename / path
//   2. basename prefix
//   3. path prefix
//   4. basename substring
//   5. path substring
//   6. subsequence (all query chars appear in order somewhere in the path)
//
// A recency bonus from the engine's working memory lifts files the user has
// recently touched so they surface before equally-scoring strangers. Shorter
// paths tiebreak ahead of longer ones.

import (
	"path/filepath"
	"sort"
	"strings"
)

// mentionRanker scores a pool of project-relative file paths against a query
// and returns the best matches up to limit. Passing an empty query returns
// the recency-sorted list so the menu is immediately useful after typing `@`.
type mentionRanker struct {
	files       []string
	recent      []string
	recentIndex map[string]int
}

func newMentionRanker(files []string, recent []string) mentionRanker {
	idx := make(map[string]int, len(recent))
	for i, p := range recent {
		idx[filepath.ToSlash(strings.TrimSpace(p))] = i
	}
	return mentionRanker{files: files, recent: recent, recentIndex: idx}
}

type mentionCandidate struct {
	path  string
	score int
}

func (r mentionRanker) rank(query string, limit int) []mentionCandidate {
	if limit <= 0 {
		limit = 8
	}
	q := strings.ToLower(strings.TrimSpace(query))
	candidates := make([]mentionCandidate, 0, len(r.files))
	for _, raw := range r.files {
		path := filepath.ToSlash(strings.TrimSpace(raw))
		if path == "" {
			continue
		}
		score := r.scoreOne(path, q)
		if score <= 0 {
			continue
		}
		candidates = append(candidates, mentionCandidate{path: path, score: score})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if len(candidates[i].path) != len(candidates[j].path) {
			return len(candidates[i].path) < len(candidates[j].path)
		}
		return candidates[i].path < candidates[j].path
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

func (r mentionRanker) scoreOne(path, q string) int {
	lowerPath := strings.ToLower(path)
	base := strings.ToLower(filepath.Base(path))
	base = strings.TrimSuffix(base, filepath.Ext(base))

	recency := r.recencyBonus(path)

	if q == "" {
		// Empty query still gets surfaced, recency-first; we tiebreak by length later.
		return 1 + recency
	}

	switch {
	case base == q || lowerPath == q:
		return 1000 + recency
	case strings.HasPrefix(base, q):
		return 800 - lenPenalty(path) + recency
	case strings.HasPrefix(lowerPath, q):
		return 700 - lenPenalty(path) + recency
	case strings.Contains(base, q):
		return 500 - lenPenalty(path) + recency
	case strings.Contains(lowerPath, q):
		return 400 - lenPenalty(path) + recency
	}
	if gap, ok := subsequenceGap(lowerPath, q); ok {
		return 200 - gap - lenPenalty(path) + recency
	}
	return 0
}

func (r mentionRanker) recencyBonus(path string) int {
	if len(r.recentIndex) == 0 {
		return 0
	}
	rank, ok := r.recentIndex[path]
	if !ok {
		return 0
	}
	// Top recent file: +100, decays by 2 per slot and clamps at 0.
	bonus := 100 - rank*2
	if bonus < 0 {
		return 0
	}
	return bonus
}

func lenPenalty(path string) int {
	p := len(path) / 4
	if p > 50 {
		return 50
	}
	return p
}

// subsequenceGap reports whether every character of q appears in s in order.
// The returned gap is the number of skipped characters — lower is tighter.
func subsequenceGap(s, q string) (int, bool) {
	if q == "" {
		return 0, true
	}
	gap := 0
	qi := 0
	for i := 0; i < len(s) && qi < len(q); i++ {
		if s[i] == q[qi] {
			qi++
			continue
		}
		gap++
	}
	if qi != len(q) {
		return 0, false
	}
	return gap, true
}

// engineRecentFiles extracts the working-memory recent-files list, handling
// nil engines (tests) without panicking.
func (m Model) engineRecentFiles() []string {
	if m.eng == nil {
		return nil
	}
	w := m.eng.MemoryWorking()
	return w.RecentFiles
}

// resolveMentionQuery picks the best match for an ambiguous @token during
// submit. Returns ("", false) when no match is confident enough — callers
// should leave the original text alone in that case so the LLM can still
// interpret the literal @name itself.
func resolveMentionQuery(files, recent []string, query string) (string, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", false
	}
	ranker := newMentionRanker(files, recent)
	best := ranker.rank(query, 1)
	if len(best) == 0 {
		return "", false
	}
	// Demand at least a substring-level match before silently rewriting.
	if best[0].score < 400 {
		return "", false
	}
	return best[0].path, true
}

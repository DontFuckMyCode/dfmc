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
//
// Users can append a line range to the typed @mention (`@auth.go:10-50`,
// `@auth.go#L10`, `@auth.go#L10-L50`). The range is kept verbatim through the
// picker and attached to the inserted `[[file:...#L10-L50]]` marker so it
// flows through to the context manager.
//
// The picker hides common binary/asset extensions by default so the first
// eight rows stay useful even on projects that ship a lot of images or
// fixtures. The filter relaxes automatically when the user explicitly types
// one of those extensions into the query.

import (
	"path/filepath"
	"regexp"
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
	path   string
	score  int
	recent bool
}

func (r mentionRanker) rank(query string, limit int) []mentionCandidate {
	if limit <= 0 {
		limit = 8
	}
	q := strings.ToLower(strings.TrimSpace(query))
	showBinary := queryTargetsBinary(q)
	candidates := make([]mentionCandidate, 0, len(r.files))
	for _, raw := range r.files {
		path := filepath.ToSlash(strings.TrimSpace(raw))
		if path == "" {
			continue
		}
		if !showBinary && mentionPickerSkip(path) {
			continue
		}
		score := r.scoreOne(path, q)
		if score <= 0 {
			continue
		}
		_, recent := r.recentIndex[path]
		candidates = append(candidates, mentionCandidate{path: path, score: score, recent: recent})
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
	// Empty query: show more files (up to 20) so the picker is immediately
	// useful as a file browser, not a sparse teaser. Binary skip still applies.
	if q == "" && limit < 20 {
		limit = 20
	}
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

func (r mentionRanker) scoreOne(path, q string) int {
	lowerPath := strings.ToLower(path)
	baseExt := strings.ToLower(filepath.Base(path))
	base := strings.TrimSuffix(baseExt, filepath.Ext(baseExt))

	recency := r.recencyBonus(path)

	if q == "" {
		// Empty query — no substring/basename filter applied.
		// Return recency-sorted list so the picker is immediately useful
		// after typing bare "@". Score of 1 keeps them ranked by recency
		// tiebreak (shorter paths first).
		return 1 + recency
	}

	// Queries with a ".ext" tail should also match the basename-with-extension
	// form — otherwise `@token.go` scores below threshold against
	// `internal/auth/token.go` because the raw substring only appears in the
	// full path (tier 5, penalized) rather than against a basename tier.
	switch {
	case base == q || baseExt == q || lowerPath == q:
		return 1000 + recency
	case strings.HasPrefix(base, q) || strings.HasPrefix(baseExt, q):
		return 800 - lenPenalty(path) + recency
	case strings.HasPrefix(lowerPath, q):
		return 700 - lenPenalty(path) + recency
	case strings.Contains(base, q) || strings.Contains(baseExt, q):
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

// mentionBinaryExts enumerates extensions that rarely help the model and
// would dilute the top of the picker (images, binaries, archives, fonts,
// media, compiled classes, database files). The filter is a sensible default,
// not a veto — if the query explicitly names one of these extensions the
// filter relaxes so power users can still pick them.
var mentionBinaryExts = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".bmp": {}, ".tif": {}, ".tiff": {}, ".ico": {}, ".webp": {}, ".svg": {},
	".pdf": {}, ".zip": {}, ".tar": {}, ".gz": {}, ".tgz": {}, ".bz2": {}, ".7z": {}, ".rar": {},
	".exe": {}, ".dll": {}, ".so": {}, ".dylib": {}, ".bin": {}, ".o": {}, ".obj": {}, ".a": {}, ".class": {}, ".jar": {},
	".mp3": {}, ".mp4": {}, ".mov": {}, ".wav": {}, ".ogg": {}, ".webm": {}, ".mkv": {}, ".flac": {}, ".m4a": {},
	".woff": {}, ".woff2": {}, ".ttf": {}, ".otf": {}, ".eot": {},
	".db": {}, ".sqlite": {}, ".sqlite3": {}, ".bolt": {},
}

// mentionPickerSkip returns true when a path's extension is in the default
// binary-skip set. The filter is bypassed when the typed query matches the
// extension (see queryTargetsBinary).
func mentionPickerSkip(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return false
	}
	_, skip := mentionBinaryExts[ext]
	return skip
}

// queryTargetsBinary reports whether a query looks like the user is
// explicitly asking for a binary extension (e.g. "logo.png", "favicon.ico").
// When true, the picker skips its default filter. The check is anchored on
// the dotted extension itself so "logo" does not match ".o" and similar
// short extensions.
func queryTargetsBinary(q string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return false
	}
	for ext := range mentionBinaryExts {
		if strings.Contains(q, ext) {
			return true
		}
	}
	return false
}

// mentionRangeRE matches the suffix after the filename: either `:N[-M]` or
// `#L N[-L?M]`. Accepts both bare digits after `:` and the `#L` form; always
// normalizes the emitted suffix to `#L<start>[-L<end>]`.
var mentionRangeRE = regexp.MustCompile(`(?i)(:|#L)(\d+)(?:[-:]L?(\d+))?$`)

// splitMentionToken splits a typed token like `auth.go:10-50` or
// `auth.go#L10-L50` into (fileQuery, normalizedRangeSuffix). When no range is
// present, the suffix is empty. A malformed range is left alone (the whole
// token is treated as the file query).
func splitMentionToken(token string) (string, string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", ""
	}
	m := mentionRangeRE.FindStringSubmatchIndex(token)
	if m == nil {
		return token, ""
	}
	prefix := token[:m[0]]
	start := token[m[4]:m[5]]
	end := ""
	if m[6] != -1 {
		end = token[m[6]:m[7]]
	}
	suffix := "#L" + start
	if end != "" {
		suffix += "-L" + end
	}
	return prefix, suffix
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

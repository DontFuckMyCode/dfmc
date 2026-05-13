package tools

// glob.go — GlobTool: shell-style pattern matching over the project
// tree with doublestar (`**`) support. Walks subdirectories skipping
// the standard ignore set (.git, .dfmc, node_modules, vendor, bin,
// dist, .venv) and uses filepath.Match against forward-slash-normalized
// relative paths so a Windows source tree behaves like POSIX. Companion
// sibling: todo_write.go owns the unrelated TodoWriteTool + ThinkTool
// that historically lived alongside this file.

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// GlobTool performs fast shell-style glob matching anywhere under the project
// root. Supports doublestar (`**`) by walking the subtree when the pattern
// contains "**", otherwise defers to filepath.Match for each candidate.
type GlobTool struct{}

func NewGlobTool() *GlobTool            { return &GlobTool{} }
func (t *GlobTool) Name() string        { return "glob" }
func (t *GlobTool) Description() string { return "Match file paths against a glob pattern." }

func (t *GlobTool) Execute(ctx context.Context, req Request) (Result, error) {
	pattern := strings.TrimSpace(asString(req.Params, "pattern", ""))
	if pattern == "" {
		hint := ""
		if p := strings.TrimSpace(asString(req.Params, "path", "")); valueLooksLikePath(p) {
			hint = fmt.Sprintf(`Looks like you put the directory %q in "path" but forgot the glob. glob matches files BY NAME — pass a glob like "**/*.go" as "pattern" and (optionally) keep "path" to restrict the search root. To list everything in a directory use list_dir; to search content use grep_codebase.`, p)
		}
		return Result{}, missingParamError("glob", "pattern", req.Params,
			`{"pattern":"**/*.go"} or {"pattern":"*_test.go","path":"internal"}`,
			hint)
	}
	root := strings.TrimSpace(asString(req.Params, "path", ""))
	base := req.ProjectRoot
	if root != "" {
		p, err := EnsureWithinRoot(req.ProjectRoot, root)
		if err != nil {
			return Result{}, err
		}
		base = p
	}
	limit := asInt(req.Params, "max_results", 200)
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}

	// Normalize path separators to match filepath.Match on Windows.
	normalizedPattern := filepath.ToSlash(pattern)
	doublestar := strings.Contains(normalizedPattern, "**")

	var matches []string
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if defaultWalkSkipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(req.ProjectRoot, path)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if globMatch(normalizedPattern, relSlash, doublestar) {
			matches = append(matches, relSlash)
			if len(matches) >= limit {
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return Result{}, err
	}
	sort.Strings(matches)

	return Result{
		Output: strings.Join(matches, "\n"),
		Data: map[string]any{
			"pattern": pattern,
			"count":   len(matches),
			// `matches` was duplicated here AND in Output. The native loop
			// re-encodes Data into the model's tool_result, doubling
			// every glob hit on the wire. Output is the canonical
			// surface; Data keeps just metadata. `root` was the absolute
			// project root — same FS-leak pattern fixed in builtin.go.
		},
		Truncated: len(matches) >= limit,
	}, nil
}

// globMatch handles `**` by matching the literal pattern against all
// progressively-stripped prefixes of the path. For non-doublestar patterns,
// falls back to filepath.Match on the forward-slash-normalized path.
func globMatch(pattern, path string, doublestar bool) bool {
	if !doublestar {
		if ok, err := filepath.Match(pattern, path); err == nil && ok {
			return true
		}
		// Also try matching just the basename — mirrors the common "*.go"
		// usage where the user expects recursive match.
		if ok, err := filepath.Match(pattern, filepath.Base(path)); err == nil && ok {
			return true
		}
		return false
	}
	return doublestarMatch(pattern, path)
}

// doublestarMatch implements a small subset of doublestar matching: `**`
// matches zero or more path segments, `*` matches within a segment, `?`
// matches a single character. Sufficient for typical developer use.
func doublestarMatch(pattern, name string) bool {
	pIdx, nIdx := 0, 0
	pParts := strings.Split(pattern, "/")
	nParts := strings.Split(name, "/")
	return matchSegments(pParts, pIdx, nParts, nIdx)
}

func matchSegments(pattern []string, pi int, name []string, ni int) bool {
	for pi < len(pattern) {
		p := pattern[pi]
		if p == "**" {
			if pi == len(pattern)-1 {
				return true
			}
			for ni <= len(name) {
				if matchSegments(pattern, pi+1, name, ni) {
					return true
				}
				ni++
			}
			return false
		}
		if ni >= len(name) {
			return false
		}
		ok, _ := filepath.Match(p, name[ni])
		if !ok {
			return false
		}
		pi++
		ni++
	}
	return ni == len(name)
}

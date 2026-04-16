package tools

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

func NewGlobTool() *GlobTool             { return &GlobTool{} }
func (t *GlobTool) Name() string         { return "glob" }
func (t *GlobTool) Description() string  { return "Match file paths against a glob pattern." }

func (t *GlobTool) Execute(_ context.Context, req Request) (Result, error) {
	pattern := strings.TrimSpace(asString(req.Params, "pattern", ""))
	if pattern == "" {
		return Result{}, fmt.Errorf("pattern is required")
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
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".dfmc", "node_modules", "vendor", "bin", "dist", ".venv":
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
			"root":    req.ProjectRoot,
			"count":   len(matches),
			"matches": matches,
		},
		Truncated: len(matches) >= limit,
	}, nil
}

// globMatch handles `**` by matching the literal pattern against all
// progressively-stripped prefixes of the path. For non-doublestar patterns,
// falls back to filepath.Match on the forward-slash-normalized path.
func globMatch(pattern, path string, doublestar bool) bool {
	if !doublestar {
		ok, _ := filepath.Match(pattern, path)
		if ok {
			return true
		}
		// Also try matching just the basename — mirrors the common "*.go"
		// usage where the user expects recursive match.
		ok, _ = filepath.Match(pattern, filepath.Base(path))
		return ok
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

// ThinkTool is a structured reasoning scratch-pad. It never touches state; it
// exists so the LLM can emit an explicit thought, have it logged in the trace,
// and proceed. No output back — the thought is recorded via Result.Data.
type ThinkTool struct{}

func NewThinkTool() *ThinkTool            { return &ThinkTool{} }
func (t *ThinkTool) Name() string         { return "think" }
func (t *ThinkTool) Description() string  { return "Record a reasoning step for the trace." }

func (t *ThinkTool) Execute(_ context.Context, req Request) (Result, error) {
	thought := strings.TrimSpace(asString(req.Params, "thought", ""))
	if thought == "" {
		return Result{}, fmt.Errorf("thought is required")
	}
	// Truncate very long thoughts so the trace stays readable. The LLM can
	// break its reasoning into multiple think calls if needed.
	if len(thought) > 2000 {
		thought = thought[:2000] + "…"
	}
	return Result{
		Output: "noted",
		Data: map[string]any{
			"thought": thought,
			"chars":   len(thought),
		},
	}, nil
}

// TodoWriteTool maintains a per-engine, in-memory todo list. The LLM calls it
// to plan multi-step work. State is intentionally ephemeral: it does not
// persist across process restarts — by design, so stale plans don't leak
// across sessions.
type TodoWriteTool struct {
	mu    struct{} // placeholder
	state *todoState
}

type todoState struct {
	items []todoItem
}

type todoItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"active_form,omitempty"`
}

func NewTodoWriteTool() *TodoWriteTool {
	return &TodoWriteTool{state: &todoState{}}
}
func (t *TodoWriteTool) Name() string        { return "todo_write" }
func (t *TodoWriteTool) Description() string { return "Plan or update the session todo list." }

func (t *TodoWriteTool) Execute(_ context.Context, req Request) (Result, error) {
	action := strings.ToLower(strings.TrimSpace(asString(req.Params, "action", "set")))
	switch action {
	case "list", "show":
		return t.render(), nil
	case "clear", "reset":
		t.state.items = t.state.items[:0]
		return Result{Output: "todos cleared", Data: map[string]any{"count": 0}}, nil
	case "set", "replace", "update":
		raw, ok := req.Params["todos"]
		if !ok || raw == nil {
			return Result{}, fmt.Errorf("todos is required when action=set")
		}
		items, err := parseTodoList(raw)
		if err != nil {
			return Result{}, err
		}
		t.state.items = items
		return t.render(), nil
	default:
		return Result{}, fmt.Errorf("unknown action %q (want set|list|clear)", action)
	}
}

func (t *TodoWriteTool) render() Result {
	if len(t.state.items) == 0 {
		return Result{Output: "(no todos)", Data: map[string]any{"count": 0, "items": []any{}}}
	}
	var lines []string
	var pending, active, done int
	for i, it := range t.state.items {
		mark := "[ ]"
		switch strings.ToLower(it.Status) {
		case "in_progress", "active", "doing":
			mark = "[~]"
			active++
		case "completed", "done":
			mark = "[x]"
			done++
		default:
			pending++
		}
		lines = append(lines, fmt.Sprintf("%s %d. %s", mark, i+1, it.Content))
	}
	out := strings.Join(lines, "\n")
	return Result{
		Output: out,
		Data: map[string]any{
			"count":       len(t.state.items),
			"pending":     pending,
			"in_progress": active,
			"completed":   done,
			"items":       t.state.items,
		},
	}
}

func parseTodoList(raw any) ([]todoItem, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("todos must be an array")
	}
	out := make([]todoItem, 0, len(arr))
	for i, entry := range arr {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("todos[%d] must be an object with {content,status}", i)
		}
		content := strings.TrimSpace(asString(m, "content", ""))
		if content == "" {
			return nil, fmt.Errorf("todos[%d].content is required", i)
		}
		status := strings.ToLower(strings.TrimSpace(asString(m, "status", "pending")))
		if status == "" {
			status = "pending"
		}
		out = append(out, todoItem{
			Content:    content,
			Status:     status,
			ActiveForm: strings.TrimSpace(asString(m, "active_form", "")),
		})
	}
	return out, nil
}


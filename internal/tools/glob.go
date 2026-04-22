package tools

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

// GlobTool performs fast shell-style glob matching anywhere under the project
// root. Supports doublestar (`**`) by walking the subtree when the pattern
// contains "**", otherwise defers to filepath.Match for each candidate.
type GlobTool struct{}

func NewGlobTool() *GlobTool            { return &GlobTool{} }
func (t *GlobTool) Name() string        { return "glob" }
func (t *GlobTool) Description() string { return "Match file paths against a glob pattern." }

func (t *GlobTool) Execute(_ context.Context, req Request) (Result, error) {
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

func NewThinkTool() *ThinkTool           { return &ThinkTool{} }
func (t *ThinkTool) Name() string        { return "think" }
func (t *ThinkTool) Description() string { return "Record a reasoning step for the trace." }

func (t *ThinkTool) Execute(_ context.Context, req Request) (Result, error) {
	thought := strings.TrimSpace(asString(req.Params, "thought", ""))
	if thought == "" {
		return Result{}, missingParamError("think", "thought", req.Params,
			`{"thought":"plan: read engine.go, then patch run loop"}`,
			`think is a scratch-pad — pass your reasoning step as the "thought" string. No side effects, just trace logging.`)
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
// to plan multi-step work. When a task store is injected via SetStore, state
// is persisted to bbolt so todos survive process restarts. Without a store the
// tool falls back to the original in-memory behaviour.
type TodoWriteTool struct {
	state *todoState
	store *taskstore.Store
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

func (t *TodoWriteTool) SetStore(store *taskstore.Store) {
	t.store = store
}

func (t *TodoWriteTool) Execute(_ context.Context, req Request) (Result, error) {
	action := strings.ToLower(strings.TrimSpace(asString(req.Params, "action", "set")))
	switch action {
	case "list", "show":
		return t.renderStore(), nil
	case "clear", "reset":
		if t.store != nil {
			tasks, _ := t.store.ListTasks(taskstore.ListOptions{})
			for _, task := range tasks {
				if task.RunID == "" {
					_ = t.store.DeleteTask(task.ID)
				}
			}
		} else {
			t.state.items = t.state.items[:0]
		}
		return Result{Output: "todos cleared", Data: map[string]any{"count": 0}}, nil
	case "set", "replace", "update":
		raw, ok := req.Params["todos"]
		if !ok || raw == nil {
			return Result{}, missingParamError("todo_write", "todos", req.Params,
				`{"action":"set","todos":[{"content":"Read engine.go","status":"pending"},{"content":"Patch run loop","status":"in_progress"}]}`,
				`todos must be an array of {content, status} objects. Use status "pending" | "in_progress" | "completed".`)
		}
		items, err := parseTodoList(raw)
		if err != nil {
			return Result{}, err
		}
		if t.store != nil {
			if err := t.syncToStore(items); err != nil {
				return Result{}, fmt.Errorf("todo_write: persist failed: %w", err)
			}
		} else {
			t.state.items = items
		}
		return t.renderStore(), nil
	default:
		return Result{}, fmt.Errorf("todo_write: unknown action %q. Allowed: set | list | clear (aliases: replace/update for set; show for list; reset for clear). Example: {\"action\":\"set\",\"todos\":[...]}", action)
	}
}

// renderStore renders todos from the task store when available, falling back
// to the in-memory state.
func (t *TodoWriteTool) renderStore() Result {
	if t.store != nil {
		tasks, err := t.store.ListTasks(taskstore.ListOptions{})
		if err == nil && len(tasks) == 0 {
			return Result{Output: "(no todos)", Data: map[string]any{"count": 0, "items": []any{}}}
		}
		if err == nil {
			return t.renderTasksAsResult(tasks)
		}
	}
	return t.renderMem()
}

// syncToStore clears session-only tasks (those with no RunID) and replaces
// them with the items parsed from the model payload.
func (t *TodoWriteTool) syncToStore(items []todoItem) error {
	existing, err := t.store.ListTasks(taskstore.ListOptions{})
	if err != nil {
		return err
	}
	for _, task := range existing {
		if task.RunID == "" {
			_ = t.store.DeleteTask(task.ID)
		}
	}
	for _, item := range items {
		task := supervisor.Task{
			ID:     taskstore.NewTaskID(),
			Title:  item.Content,
			State:  supervisor.TaskState(strings.ToLower(item.Status)),
			Origin: "todo_write",
		}
		if task.State == "" {
			task.State = supervisor.TaskPending
		}
		if err := t.store.SaveTask(&task); err != nil {
			return err
		}
	}
	return nil
}

func (t *TodoWriteTool) renderTasksAsResult(tasks []*supervisor.Task) Result {
	if len(tasks) == 0 {
		return Result{Output: "(no todos)", Data: map[string]any{"count": 0, "items": []any{}}}
	}
	var lines []string
	var pending, active, done int
	for i, task := range tasks {
		mark := "[ ]"
		switch strings.ToLower(string(task.State)) {
		case "running", "in_progress", "active", "doing":
			mark = "[~]"
			active++
		case "done", "completed":
			mark = "[x]"
			done++
		default:
			pending++
		}
		content := task.Title
		if task.Detail != "" {
			content = task.Detail
		}
		lines = append(lines, fmt.Sprintf("%s %d. %s", mark, i+1, content))
	}
	out := strings.Join(lines, "\n")
	items := make([]todoItem, len(tasks))
	for i, task := range tasks {
		items[i] = todoItem{Content: task.Title, Status: string(task.State)}
	}
	return Result{
		Output: out,
		Data: map[string]any{
			"count":       len(tasks),
			"pending":     pending,
			"in_progress": active,
			"completed":   done,
			"items":       items,
		},
	}
}

func (t *TodoWriteTool) renderMem() Result {
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

// TodoItem is the exported projection of a single todo entry for consumers
// outside the tools package (agent loop, handoff brief, TUI).
type TodoItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"active_form,omitempty"`
}

// Snapshot returns a defensive copy of the current todo list so callers
// can read it without racing with a concurrent todo_write call. Reads from
// the task store when available, falls back to in-memory state.
func (t *TodoWriteTool) Snapshot() []TodoItem {
	if t == nil {
		return nil
	}
	if t.store != nil {
		tasks, err := t.store.ListTasks(taskstore.ListOptions{})
		if err == nil {
			out := make([]TodoItem, len(tasks))
			for i, task := range tasks {
				out[i] = TodoItem{Content: task.Title, Status: string(task.State)}
			}
			return out
		}
	}
	if t.state == nil {
		return nil
	}
	out := make([]TodoItem, len(t.state.items))
	for i, it := range t.state.items {
		out[i] = TodoItem(it)
	}
	return out
}

func parseTodoList(raw any) ([]todoItem, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf(`todos must be a JSON array of {content, status} objects, got %T. Example: [{"content":"Read engine.go","status":"pending"}]`, raw)
	}
	out := make([]todoItem, 0, len(arr))
	for i, entry := range arr {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf(`todos[%d] must be an object like {"content":"...","status":"pending"}, got %T`, i, entry)
		}
		content := strings.TrimSpace(asString(m, "content", ""))
		if content == "" {
			return nil, fmt.Errorf(`todos[%d].content is required (a non-empty string describing the task). Example entry: {"content":"Read engine.go","status":"pending"}`, i)
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

// MCP handler for task store operations. Provides a dfmc_task_* virtual tool
// family that mirrors the HTTP /api/v1/tasks endpoints. Synthetic — not
// registered in engine.Tools so the LLM loop never dispatches a task operation
// to itself.
//
// Tool surface:
//   dfmc_task_create  {title, parent_id?, origin?, state?, worker_class?,
//                       depends_on?, file_scope?, labels?, ...}  -> Task
//   dfmc_task_get     {id}                                      -> Task
//   dfmc_task_list    {parent_id?, run_id?, state?, label?,
//                       limit?, offset?}                          -> [Task, ...]
//   dfmc_task_update  {id, title?, state?, summary?, confidence?} -> Task
//   dfmc_task_delete  {id}                                      -> {deleted}

package cli

import (
	"context"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/mcp"
	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

type taskMCPHandler struct {
	eng *engine.Engine
}

const taskToolPrefix = "dfmc_task_"

func (h *taskMCPHandler) Handles(name string) bool {
	return strings.HasPrefix(name, taskToolPrefix)
}

func (h *taskMCPHandler) Tools() []mcp.ToolDescriptor {
	return []mcp.ToolDescriptor{
		{
			Name:        "dfmc_task_create",
			Description: "Create a new task in the persistent task store. Tasks can be hierarchical (parent_id) and carry metadata like worker_class, file_scope, and verification policy.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":        map[string]any{"type": "string", "description": "Task title (required)"},
					"parent_id":    map[string]any{"type": "string", "description": "Parent task ID for hierarchical tasks"},
					"origin":       map[string]any{"type": "string", "description": "Origin hint: 'todo_write', 'planner', or 'supervisor'"},
					"state":        map[string]any{"type": "string", "description": "Initial state: pending (default), running, done, blocked, skipped, waiting, external_review"},
					"worker_class": map[string]any{"type": "string", "description": "Planner/coder/reviewer/tester/security/synthesizer"},
					"depends_on":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Task IDs this task depends on"},
					"file_scope":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "File paths this task operates on"},
					"labels":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Arbitrary string tags"},
					"verification": map[string]any{"type": "string", "description": "none/light/required/deep"},
					"confidence":   map[string]any{"type": "number", "description": "Confidence 0-1"},
					"summary":      map[string]any{"type": "string", "description": "Brief summary of outcome"},
				},
				"required":             []string{"title"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_task_get",
			Description: "Fetch a single task by its ID.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Task ID (e.g. tsk-a1b2c3)"},
				},
				"required":             []string{"id"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_task_list",
			Description: "List tasks from the persistent store, with optional filters. Returns newest-first.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"parent_id": map[string]any{"type": "string", "description": "Filter to children of this parent ID"},
					"run_id":    map[string]any{"type": "string", "description": "Filter to tasks from a specific drive run"},
					"state":     map[string]any{"type": "string", "description": "Filter by state"},
					"label":     map[string]any{"type": "string", "description": "Filter by label tag"},
					"limit":     map[string]any{"type": "integer", "description": "Max results (default 25)"},
					"offset":    map[string]any{"type": "integer", "description": "Skip N results for pagination"},
				},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_task_update",
			Description: "Partially update a task: change its state, title, summary, confidence, or blocked_reason.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":             map[string]any{"type": "string"},
					"title":          map[string]any{"type": "string"},
					"state":          map[string]any{"type": "string"},
					"summary":        map[string]any{"type": "string"},
					"confidence":     map[string]any{"type": "number"},
					"blocked_reason": map[string]any{"type": "string"},
				},
				"required":             []string{"id"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_task_delete",
			Description: "Delete a task from the store by ID.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
				"required":             []string{"id"},
				"additionalProperties": false,
			},
		},
	}
}

func (h *taskMCPHandler) Call(ctx context.Context, name string, rawArgs []byte) (mcp.CallToolResult, error) {
	switch name {
	case "dfmc_task_create":
		return h.callCreate(ctx, rawArgs)
	case "dfmc_task_get":
		return h.callGet(rawArgs)
	case "dfmc_task_list":
		return h.callList(rawArgs)
	case "dfmc_task_update":
		return h.callUpdate(rawArgs)
	case "dfmc_task_delete":
		return h.callDelete(rawArgs)
	default:
		return errResult("unknown tool: " + name)
	}
}

type taskCreateArgs struct {
	Title        string   `json:"title"`
	ParentID     string   `json:"parent_id,omitempty"`
	Origin       string   `json:"origin,omitempty"`
	State        string   `json:"state,omitempty"`
	WorkerClass  string   `json:"worker_class,omitempty"`
	DependsOn    []string `json:"depends_on,omitempty"`
	FileScope    []string `json:"file_scope,omitempty"`
	Labels       []string `json:"labels,omitempty"`
	Verification string   `json:"verification,omitempty"`
	Confidence   float64  `json:"confidence,omitempty"`
	Summary      string   `json:"summary,omitempty"`
}

func (h *taskMCPHandler) callCreate(_ context.Context, rawArgs []byte) (mcp.CallToolResult, error) {
	var args taskCreateArgs
	if err := decodeOrEmpty(rawArgs, &args); err != nil {
		return errResult("decode: " + err.Error())
	}
	if strings.TrimSpace(args.Title) == "" {
		return errResult("title is required")
	}
	store := h.eng.Tools.TaskStore()
	if store == nil {
		return errResult("task store unavailable")
	}
	task := supervisor.Task{
		ID:           taskstore.NewTaskID(),
		ParentID:     strings.TrimSpace(args.ParentID),
		Origin:       strings.TrimSpace(args.Origin),
		Title:        strings.TrimSpace(args.Title),
		State:        supervisor.TaskState(strings.TrimSpace(args.State)),
		DependsOn:    append([]string(nil), args.DependsOn...),
		FileScope:    append([]string(nil), args.FileScope...),
		WorkerClass:  supervisor.WorkerClass(strings.TrimSpace(args.WorkerClass)),
		Labels:       append([]string(nil), args.Labels...),
		Verification: supervisor.VerificationStatus(strings.TrimSpace(args.Verification)),
		Confidence:   args.Confidence,
		Summary:      strings.TrimSpace(args.Summary),
	}
	if task.State == "" {
		task.State = supervisor.TaskPending
	}
	if err := store.SaveTask(&task); err != nil {
		return errResult("save: " + err.Error())
	}
	return okResult(task)
}

type taskGetArgs struct {
	ID string `json:"id"`
}

func (h *taskMCPHandler) callGet(rawArgs []byte) (mcp.CallToolResult, error) {
	var args taskGetArgs
	if err := decodeOrEmpty(rawArgs, &args); err != nil {
		return errResult("decode: " + err.Error())
	}
	id := strings.TrimSpace(args.ID)
	if id == "" {
		return errResult("id is required")
	}
	store := h.eng.Tools.TaskStore()
	if store == nil {
		return errResult("task store unavailable")
	}
	task, err := store.LoadTask(id)
	if err != nil {
		return errResult("load: " + err.Error())
	}
	if task == nil {
		return errResult("task " + id + " not found")
	}
	return okResult(task)
}

type taskListArgs struct {
	ParentID string `json:"parent_id,omitempty"`
	RunID    string `json:"run_id,omitempty"`
	State    string `json:"state,omitempty"`
	Label    string `json:"label,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
}

func (h *taskMCPHandler) callList(rawArgs []byte) (mcp.CallToolResult, error) {
	var args taskListArgs
	if err := decodeOrEmpty(rawArgs, &args); err != nil {
		return errResult("decode: " + err.Error())
	}
	store := h.eng.Tools.TaskStore()
	if store == nil {
		return errResult("task store unavailable")
	}
	opts := taskstore.ListOptions{
		ParentID: strings.TrimSpace(args.ParentID),
		RunID:    strings.TrimSpace(args.RunID),
		State:    strings.TrimSpace(args.State),
		Label:    strings.TrimSpace(args.Label),
		Limit:    args.Limit,
		Offset:   args.Offset,
	}
	if opts.Limit == 0 {
		opts.Limit = 25
	}
	tasks, err := store.ListTasks(opts)
	if err != nil {
		return errResult("list: " + err.Error())
	}
	if tasks == nil {
		tasks = []*supervisor.Task{}
	}
	return okResult(tasks)
}

type taskUpdateArgs struct {
	ID            string  `json:"id"`
	Title         string  `json:"title,omitempty"`
	State         string  `json:"state,omitempty"`
	Summary       string  `json:"summary,omitempty"`
	Confidence    float64 `json:"confidence,omitempty"`
	BlockedReason string  `json:"blocked_reason,omitempty"`
}

func (h *taskMCPHandler) callUpdate(rawArgs []byte) (mcp.CallToolResult, error) {
	var args taskUpdateArgs
	if err := decodeOrEmpty(rawArgs, &args); err != nil {
		return errResult("decode: " + err.Error())
	}
	id := strings.TrimSpace(args.ID)
	if id == "" {
		return errResult("id is required")
	}
	store := h.eng.Tools.TaskStore()
	if store == nil {
		return errResult("task store unavailable")
	}
	err := store.UpdateTask(id, func(t *supervisor.Task) error {
		if args.Title != "" {
			t.Title = strings.TrimSpace(args.Title)
		}
		if args.State != "" {
			t.State = supervisor.TaskState(strings.TrimSpace(args.State))
		}
		if args.Summary != "" {
			t.Summary = strings.TrimSpace(args.Summary)
		}
		if args.BlockedReason != "" {
			t.BlockedReason = strings.TrimSpace(args.BlockedReason)
		}
		if args.Confidence > 0 {
			t.Confidence = args.Confidence
		}
		return nil
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return errResult("task " + id + " not found")
		}
		return errResult("update: " + err.Error())
	}
	updated, _ := store.LoadTask(id)
	return okResult(updated)
}

type taskDeleteArgs struct {
	ID string `json:"id"`
}

func (h *taskMCPHandler) callDelete(rawArgs []byte) (mcp.CallToolResult, error) {
	var args taskDeleteArgs
	if err := decodeOrEmpty(rawArgs, &args); err != nil {
		return errResult("decode: " + err.Error())
	}
	id := strings.TrimSpace(args.ID)
	if id == "" {
		return errResult("id is required")
	}
	store := h.eng.Tools.TaskStore()
	if store == nil {
		return errResult("task store unavailable")
	}
	if err := store.DeleteTask(id); err != nil {
		return errResult("delete: " + err.Error())
	}
	return okResult(map[string]any{"deleted": id})
}

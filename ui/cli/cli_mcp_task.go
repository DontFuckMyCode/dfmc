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
	"errors"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/mcp"
	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

type taskMCPHandler struct {
	eng *engine.Engine
}

func (h *taskMCPHandler) Handles(name string) bool {
	return strings.HasPrefix(name, taskToolPrefix)
}

// Tools() (the JSON-Schema descriptors) and the per-call argument
// types (taskCreateArgs, taskGetArgs, taskListArgs, taskUpdateArgs,
// taskDeleteArgs) live in cli_mcp_task_schema.go.

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
	mutator := func(t *supervisor.Task) error {
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
	}
	var err error
	if args.IfVersion != nil && *args.IfVersion >= 0 {
		err = store.UpdateTaskCAS(id, *args.IfVersion, mutator)
		if errors.Is(err, taskstore.ErrTaskVersionConflict) {
			// Surface a stable, parseable token so MCP clients can
			// retry-after-reread without text matching the rest of the
			// error string. Mirrors the HTTP 412 path in semantic intent.
			return errResult("version_conflict: stored version differs from if_version; reload the task and retry")
		}
	} else {
		err = store.UpdateTask(id, mutator)
	}
	if err != nil {
		if errors.Is(err, taskstore.ErrTaskNotFound) {
			return errResult("task " + id + " not found")
		}
		return errResult("update: " + err.Error())
	}
	updated, _ := store.LoadTask(id)
	return okResult(updated)
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

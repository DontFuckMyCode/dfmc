// MCP-side adapter for the autonomous drive loop. The IDE host (Claude
// Desktop, Cursor, VSCode) sees a small set of `dfmc_drive_*` virtual
// tools alongside the regular file/search/run-command tools, and can
// trigger or supervise drive runs without leaving its chat surface.
//
// These tools are NOT registered in engine.Tools — they're synthetic,
// resolved entirely inside this file and dispatched directly against
// the drive package. Keeping them out of the regular registry means
// DFMC's own agent loop never sees them: drive is for human/host-
// initiated autonomous work, not for an LLM step inside another LLM
// step (recursion that way leads to runaway token spend).
//
// Tool surface:
//
//   dfmc_drive_start   {task, max_parallel?, max_todos?, retries?,
//                       max_wall_time_ms?, planner_model?, routing?,
//                       auto_approve?}                  -> {run_id, started}
//   dfmc_drive_status  {run_id}                         -> full Run record
//   dfmc_drive_active  {}                               -> [{run_id, task}, ...]
//   dfmc_drive_list    {limit?}                         -> [Run, ...]
//   dfmc_drive_stop    {run_id}                         -> {run_id, cancelled}
//   dfmc_drive_resume  {run_id}                         -> {run_id, resumed}
//
// Mirrors the HTTP API shapes one-for-one so a host that already speaks
// to /api/v1/drive can flip to MCP without remapping fields.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/mcp"
)

// driveMCPHandler is the synthetic-tool dispatcher for drive operations.
// Stateless on purpose: every call rebuilds the store and runner from
// the engine, so a long-lived MCP session sees the latest engine state
// without holding stale references.
type driveMCPHandler struct {
	eng *engine.Engine
}

// driveToolPrefix gates Handles() — anything outside the prefix is left
// to the regular tool bridge.
const driveToolPrefix = "dfmc_drive_"

// Handles returns true when `name` is one of our virtual tools. Used by
// the parent bridge to route a tools/call before falling back to the
// engine.CallTool path.
func (h *driveMCPHandler) Handles(name string) bool {
	return strings.HasPrefix(name, driveToolPrefix)
}

// Tools returns the descriptors the bridge merges into tools/list. Order
// matters: hosts often render the list verbatim, so start/status/active
// come first as the most common entry points.
func (h *driveMCPHandler) Tools() []mcp.ToolDescriptor {
	return []mcp.ToolDescriptor{
		{
			Name:        "dfmc_drive_start",
			Description: "Start an autonomous DFMC drive run. The planner breaks the task into a TODO DAG, then DFMC executes them with sub-agents until done. Returns immediately with the run_id; subscribe to drive:* events or poll dfmc_drive_status for progress.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task":             map[string]any{"type": "string", "description": "What to do, in plain language. Example: 'add CSV export to /api/users with tests'"},
					"max_parallel":     map[string]any{"type": "integer", "description": "Max TODOs running concurrently (default 3)", "minimum": 1, "maximum": 20},
					"max_todos":        map[string]any{"type": "integer", "description": "Cap on TODOs the planner may emit (default 20)", "minimum": 1, "maximum": 200},
					"retries":          map[string]any{"type": "integer", "description": "Per-TODO retry count on failure (default 1)", "minimum": 0, "maximum": 10},
					"max_wall_time_ms": map[string]any{"type": "integer", "description": "Wall-clock budget in ms before the run is force-stopped (default 30min)", "minimum": 1000},
					"planner_model":    map[string]any{"type": "string", "description": "Provider profile name to use for the planner LLM call (defaults to engine primary)"},
					"routing":          map[string]any{"type": "object", "description": "Map provider_tag -> profile name. Tags planner emits: plan, code, review, test, research", "additionalProperties": map[string]any{"type": "string"}},
					"auto_approve":     map[string]any{"type": "array", "description": "Tool names to auto-approve for this run (use ['*'] for all)", "items": map[string]any{"type": "string"}},
				},
				"required":             []string{"task"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_drive_status",
			Description: "Fetch the full record of one drive run (status, planner output, every TODO with its brief/error). Use after dfmc_drive_start to follow progress.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id": map[string]any{"type": "string", "description": "Run ID returned by dfmc_drive_start"},
				},
				"required":             []string{"run_id"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_drive_active",
			Description: "List drive runs currently executing in this DFMC process. Useful before stopping or resuming.",
			InputSchema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_drive_list",
			Description: "List recently-persisted drive runs (newest first). Includes finished/failed/stopped runs that dfmc_drive_active no longer surfaces.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{"type": "integer", "description": "Cap on entries returned (default 25)", "minimum": 1, "maximum": 200},
				},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_drive_stop",
			Description: "Cancel an active drive run. The driver finalises gracefully (in-flight TODOs get a 2-second drain window) and emits drive:run:stopped. Returns 'not active' when the run is already terminal.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id": map[string]any{"type": "string", "description": "Run ID to cancel"},
				},
				"required":             []string{"run_id"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_drive_resume",
			Description: "Resume a stopped or paused drive run from its persisted state. Refuses runs that already reached done/failed (terminal). Returns immediately; poll dfmc_drive_status for progress.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run_id": map[string]any{"type": "string", "description": "Run ID to resume"},
				},
				"required":             []string{"run_id"},
				"additionalProperties": false,
			},
		},
	}
}

// Call dispatches one virtual-tool invocation. Any per-call resource
// (drive store, runner) is built fresh — the engine is the source of
// truth, not this handler.
func (h *driveMCPHandler) Call(ctx context.Context, name string, rawArgs []byte) (mcp.CallToolResult, error) {
	if h.eng == nil {
		return errResult("engine not initialized")
	}
	switch name {
	case "dfmc_drive_start":
		return h.callStart(ctx, rawArgs)
	case "dfmc_drive_status":
		return h.callStatus(rawArgs)
	case "dfmc_drive_active":
		return h.callActive()
	case "dfmc_drive_list":
		return h.callList(rawArgs)
	case "dfmc_drive_stop":
		return h.callStop(rawArgs)
	case "dfmc_drive_resume":
		return h.callResume(ctx, rawArgs)
	default:
		return mcp.CallToolResult{}, fmt.Errorf("drive handler: unknown tool %q", name)
	}
}

// driveStartArgs mirrors web.DriveStartRequest so the JSON shape is
// identical between MCP and HTTP. Keeps host-side logic portable.
type driveStartArgs struct {
	Task           string            `json:"task"`
	MaxTodos       int               `json:"max_todos,omitempty"`
	MaxFailedTodos int               `json:"max_failed_todos,omitempty"`
	MaxWallTimeMs  int64             `json:"max_wall_time_ms,omitempty"`
	Retries        int               `json:"retries,omitempty"`
	MaxParallel    int               `json:"max_parallel,omitempty"`
	PlannerModel   string            `json:"planner_model,omitempty"`
	Routing        map[string]string `json:"routing,omitempty"`
	AutoApprove    []string          `json:"auto_approve,omitempty"`
}

func (h *driveMCPHandler) callStart(_ context.Context, rawArgs []byte) (mcp.CallToolResult, error) {
	var args driveStartArgs
	if err := decodeOrEmpty(rawArgs, &args); err != nil {
		return errResult("decode arguments: " + err.Error())
	}
	task := strings.TrimSpace(args.Task)
	if task == "" {
		return errResult(`task is required. Example: {"task":"add CSV export to /api/users with tests"}`)
	}
	runner := h.eng.NewDriveRunner()
	if runner == nil {
		return errResult("drive runner unavailable (provider router missing)")
	}
	store, err := drive.NewStore(h.eng.Storage.DB())
	if err != nil {
		return errResult("drive store init: " + err.Error())
	}
	cfg := drive.Config{
		MaxTodos:       args.MaxTodos,
		MaxFailedTodos: args.MaxFailedTodos,
		MaxWallTime:    time.Duration(args.MaxWallTimeMs) * time.Millisecond,
		Retries:        args.Retries,
		MaxParallel:    args.MaxParallel,
		PlannerModel:   args.PlannerModel,
		Routing:        args.Routing,
		AutoApprove:    args.AutoApprove,
	}
	publisher := drive.Publisher(func(typ string, payload map[string]any) {
		h.eng.PublishDriveEvent(typ, payload)
	})
	driver := drive.NewDriver(runner, store, publisher, cfg)

	runIDCh := make(chan string, 1)
	h.eng.StartBackgroundTask("mcp.drive.run", func(ctx context.Context) {
		run, _ := driver.Run(ctx, task)
		if run != nil {
			select {
			case runIDCh <- run.ID:
			default:
			}
		} else {
			close(runIDCh)
		}
	})

	// We need to return a run_id to the host, but driver.Run doesn't
	// hand one out until the planner kicks off. Wait briefly — long
	// enough for the registry to record the run, short enough not to
	// block the MCP loop on a slow planner. If we time out, return a
	// hint that the run is in flight and the host should poll active.
	select {
	case id, ok := <-runIDCh:
		if !ok || id == "" {
			return okResult(map[string]any{
				"started": true,
				"hint":    "driver returned without a run id — check dfmc_drive_active",
			})
		}
		return okResult(map[string]any{
			"started": true,
			"run_id":  id,
			"hint":    "poll dfmc_drive_status with this run_id, or call dfmc_drive_stop to cancel",
		})
	case <-time.After(200 * time.Millisecond):
		// Planner hasn't returned yet; surface what we know.
		active := drive.ListActive()
		hint := "planner is still warming up — call dfmc_drive_active in a moment for the run_id"
		if len(active) > 0 {
			return okResult(map[string]any{
				"started":      true,
				"run_id":       active[len(active)-1].RunID,
				"hint":         hint,
				"active_count": len(active),
			})
		}
		return okResult(map[string]any{
			"started": true,
			"hint":    hint,
		})
	}
}

func (h *driveMCPHandler) callStatus(rawArgs []byte) (mcp.CallToolResult, error) {
	id, err := decodeRunIDArg(rawArgs)
	if err != nil {
		return errResult(err.Error())
	}
	store, err := drive.NewStore(h.eng.Storage.DB())
	if err != nil {
		return errResult("drive store init: " + err.Error())
	}
	run, err := store.Load(id)
	if err != nil {
		return errResult("load run: " + err.Error())
	}
	if run == nil {
		return errResult("run " + id + " not found")
	}
	return okResult(run)
}

func (h *driveMCPHandler) callActive() (mcp.CallToolResult, error) {
	active := drive.ListActive()
	if active == nil {
		active = []drive.ActiveRun{}
	}
	return okResult(active)
}

type driveListArgs struct {
	Limit int `json:"limit,omitempty"`
}

func (h *driveMCPHandler) callList(rawArgs []byte) (mcp.CallToolResult, error) {
	var args driveListArgs
	if err := decodeOrEmpty(rawArgs, &args); err != nil {
		return errResult("decode arguments: " + err.Error())
	}
	store, err := drive.NewStore(h.eng.Storage.DB())
	if err != nil {
		return errResult("drive store init: " + err.Error())
	}
	runs, err := store.List()
	if err != nil {
		return errResult("list runs: " + err.Error())
	}
	if runs == nil {
		runs = []*drive.Run{}
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 25
	}
	if len(runs) > limit {
		runs = runs[:limit]
	}
	return okResult(runs)
}

func (h *driveMCPHandler) callStop(rawArgs []byte) (mcp.CallToolResult, error) {
	id, err := decodeRunIDArg(rawArgs)
	if err != nil {
		return errResult(err.Error())
	}
	if !drive.IsActive(id) {
		return errResult("no active drive run with id " + id + " in this process. Call dfmc_drive_status for the persisted state.")
	}
	cancelled := drive.Cancel(id)
	return okResult(map[string]any{
		"run_id":    id,
		"cancelled": cancelled,
	})
}

func (h *driveMCPHandler) callResume(_ context.Context, rawArgs []byte) (mcp.CallToolResult, error) {
	id, err := decodeRunIDArg(rawArgs)
	if err != nil {
		return errResult(err.Error())
	}
	runner := h.eng.NewDriveRunner()
	if runner == nil {
		return errResult("drive runner unavailable")
	}
	store, err := drive.NewStore(h.eng.Storage.DB())
	if err != nil {
		return errResult("drive store init: " + err.Error())
	}
	existing, err := store.Load(id)
	if err != nil {
		return errResult("load run: " + err.Error())
	}
	if existing == nil {
		return errResult("run " + id + " not found")
	}
	if existing.Status == drive.RunDone || existing.Status == drive.RunFailed {
		return errResult("run " + id + " already terminal (" + string(existing.Status) + ") — cannot resume")
	}
	publisher := drive.Publisher(func(typ string, payload map[string]any) {
		h.eng.PublishDriveEvent(typ, payload)
	})
	driver := drive.NewDriver(runner, store, publisher, drive.Config{})
	h.eng.StartBackgroundTask("mcp.drive.resume", func(ctx context.Context) {
		_, _ = driver.Resume(ctx, id)
	})
	return okResult(map[string]any{
		"run_id":  id,
		"resumed": true,
		"hint":    "poll dfmc_drive_status for live progress",
	})
}

// decodeRunIDArg parses {"run_id":"..."} and validates the field.
// Centralised so every status/stop/resume returns the same shape on a
// missing/empty id — IDE hosts learn one error format.
func decodeRunIDArg(rawArgs []byte) (string, error) {
	var args struct {
		RunID string `json:"run_id"`
	}
	if err := decodeOrEmpty(rawArgs, &args); err != nil {
		return "", fmt.Errorf("decode arguments: %w", err)
	}
	id := strings.TrimSpace(args.RunID)
	if id == "" {
		return "", fmt.Errorf(`run_id is required. Example: {"run_id":"drv-abc123-def"}`)
	}
	return id, nil
}

// decodeOrEmpty unmarshals when rawArgs is non-empty. MCP allows tools
// with no required fields to be called with arguments=null/missing, so
// swallowing "no input" is correct.
func decodeOrEmpty(rawArgs []byte, dst any) error {
	if len(rawArgs) == 0 {
		return nil
	}
	trimmed := strings.TrimSpace(string(rawArgs))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	return json.Unmarshal(rawArgs, dst)
}

// okResult marshals `body` to JSON and wraps it as a single text block.
// MCP only emits text content today; structured payloads ride as JSON
// strings the host parses on its end.
func okResult(body any) (mcp.CallToolResult, error) {
	buf, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return errResult("encode result: " + err.Error())
	}
	return mcp.CallToolResult{
		Content: []mcp.ContentBlock{mcp.TextContent(string(buf))},
		IsError: false,
	}, nil
}

// errResult builds a tool-level error response (CallToolResult with
// IsError:true). Distinct from a transport error returned alongside —
// this signals "the tool ran but failed", which lets the host surface
// the message instead of treating it as a protocol fault.
func errResult(msg string) (mcp.CallToolResult, error) {
	return mcp.CallToolResult{
		Content: []mcp.ContentBlock{mcp.TextContent(msg)},
		IsError: true,
	}, nil
}

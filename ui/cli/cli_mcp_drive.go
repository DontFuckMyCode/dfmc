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
//
// File layout: this file owns the driveMCPHandler struct, Handles /
// Tools schema descriptors, and the Call dispatcher. Per-call handler
// methods (callStart, callStatus, callActive, callList, callStop,
// callResume) plus the shared decode/result helpers live in
// cli_mcp_drive_calls.go.

package cli

import (
	"context"
	"fmt"
	"strings"

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
					"task":              map[string]any{"type": "string", "description": "What to do, in plain language. Example: 'add CSV export to /api/users with tests'. Required unless from_spec is set."},
					"max_parallel":      map[string]any{"type": "integer", "description": "Max TODOs running concurrently (default 3)", "minimum": 1, "maximum": 20},
					"max_todos":         map[string]any{"type": "integer", "description": "Cap on TODOs the planner may emit (default 20)", "minimum": 1, "maximum": 200},
					"retries":           map[string]any{"type": "integer", "description": "Per-TODO retry count on failure (default 1)", "minimum": 0, "maximum": 10},
					"max_wall_time_ms":  map[string]any{"type": "integer", "description": "Wall-clock budget in ms before the run is force-stopped (default 30min)", "minimum": 1000},
					"planner_model":     map[string]any{"type": "string", "description": "Provider profile name to use for the planner LLM call (defaults to engine primary)"},
					"auto_survey":       map[string]any{"type": "boolean", "description": "Prepend a supervisor-generated discovery task after planning"},
					"auto_verify":       map[string]any{"type": "boolean", "description": "Append a supervisor-generated verification task after planning"},
					"routing":           map[string]any{"type": "object", "description": "Map provider_tag -> profile name. Tags planner emits: plan, code, review, test, research", "additionalProperties": map[string]any{"type": "string"}},
					"auto_approve":      map[string]any{"type": "array", "description": "Tool names to auto-approve for this run (use ['*'] for all)", "items": map[string]any{"type": "string"}},
					"from_spec":         map[string]any{"type": "string", "description": "Path to a markdown spec (e.g. '.project/PLAN.md'); skips the planner LLM and turns each '- [ ]' into one TODO. Mutually substitutable with task."},
					"spec_section":      map[string]any{"type": "string", "description": "When from_spec is set, only ingest TODOs from this heading anchor (lowercase slug)"},
					"spec_include_done": map[string]any{"type": "boolean", "description": "When from_spec is set, also load already-checked items as status=done TODOs"},
				},
				// task and from_spec are alternatives — neither is strictly
				// "required" alone, the handler validates that at least one
				// is present. The schema's required[] only carries fields a
				// caller MUST supply unconditionally.
				"required":             []string{},
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

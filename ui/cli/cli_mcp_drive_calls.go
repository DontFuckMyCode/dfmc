// cli_mcp_drive_calls.go — per-call handlers for the synthetic
// dfmc_drive_* MCP tools. Sibling of cli_mcp_drive.go, which owns the
// driveMCPHandler struct, Handles/Tools schema surface, and the Call
// dispatcher. This file holds the JSON arg shapes (driveStartArgs /
// driveListArgs), the six tool-call methods (callStart, callStatus,
// callActive, callList, callStop, callResume), and the small response
// helpers (okResult / errResult / decodeRunIDArg / decodeOrEmpty)
// shared across all of them.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/mcp"
)

// driveStartArgs mirrors web.DriveStartRequest so the JSON shape is
// identical between MCP and HTTP. Keeps host-side logic portable.
type driveStartArgs struct {
	Task           string            `json:"task"`
	MaxTodos       int               `json:"max_todos,omitempty"`
	MaxFailedTodos int               `json:"max_failed_todos,omitempty"`
	MaxWallTimeMs  int64             `json:"max_wall_time_ms,omitempty"`
	Retries        int               `json:"retries,omitempty"`
	MaxParallel    int               `json:"max_parallel,omitempty"`
	AutoSurvey     bool              `json:"auto_survey,omitempty"`
	AutoVerify     bool              `json:"auto_verify,omitempty"`
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
		AutoSurvey:     args.AutoSurvey,
		AutoVerify:     args.AutoVerify,
		PlannerModel:   args.PlannerModel,
		Routing:        args.Routing,
		AutoApprove:    args.AutoApprove,
	}
	publisher := drive.Publisher(func(typ string, payload map[string]any) {
		h.eng.PublishDriveEvent(typ, payload)
	})
	driver := drive.NewDriver(runner, store, publisher, cfg)
	driver.SetReportDir(h.eng.DriveReportDir())
	run, err := drive.NewRun(task)
	if err != nil {
		return errResult(err.Error())
	}
	if err := store.Save(run); err != nil {
		return errResult("persist run: " + err.Error())
	}
	h.eng.StartBackgroundTask("mcp.drive.run", func(ctx context.Context) {
		_, _ = driver.RunPrepared(ctx, run)
	})
	return okResult(map[string]any{
		"started": true,
		"run_id":  run.ID,
		"hint":    "poll dfmc_drive_status with this run_id, or call dfmc_drive_stop to cancel",
	})
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
	if drive.IsActive(id) {
		return errResult("run " + id + " is already active in this process - cannot resume it again")
	}
	if existing.Status == drive.RunDone || existing.Status == drive.RunFailed {
		return errResult("run " + id + " already terminal (" + string(existing.Status) + ") — cannot resume")
	}
	publisher := drive.Publisher(func(typ string, payload map[string]any) {
		h.eng.PublishDriveEvent(typ, payload)
	})
	driver := drive.NewDriver(runner, store, publisher, drive.Config{})
	driver.SetReportDir(h.eng.DriveReportDir())
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

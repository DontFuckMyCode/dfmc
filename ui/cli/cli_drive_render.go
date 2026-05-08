package cli

// cli_drive_render.go — text formatters used by `dfmc drive` to paint
// the streaming event log on stderr, the end-of-run / `drive show`
// summary on stdout, and the help screen. ASCII-only so output stays
// readable in CI logs and piped files. Companion siblings:
//
//   - cli_drive.go         runDrive subcommand dispatcher +
//                          executeDriveRun for new/resume runs +
//                          parseAutoApproveFlag + multiString +
//                          parseRouteFlags
//   - cli_drive_subcmds.go runDriveList / Show / Resume / Stop /
//                          Active / Delete subcommand handlers

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	supervisorbridge "github.com/dontfuckmycode/dfmc/internal/supervisor/bridge"
)

// renderDriveEventLine writes a single human-readable line per event.
// Compact format: `[run-id] event-type | payload-summary`. Goes to
// stderr so stdout remains parseable for --json runs.
func renderDriveEventLine(w *os.File, typ string, payload map[string]any) {
	runID, _ := payload["run_id"].(string)
	short := runID
	if len(short) > 12 {
		short = short[:12]
	}
	switch typ {
	case drive.EventRunStart:
		fmt.Fprintf(w, "[%s] %s task=%q\n", short, typ, payload["task"])
	case drive.EventPlanDone:
		fmt.Fprintf(w, "[%s] %s todo_count=%v\n", short, typ, payload["todo_count"])
	case drive.EventPlanAugment:
		fmt.Fprintf(w, "[%s] %s added=%v\n", short, typ, payload["added"])
	case drive.EventTodoStart:
		fmt.Fprintf(w, "[%s] %s id=%v title=%q attempt=%v\n",
			short, typ, payload["todo_id"], payload["title"], payload["attempt"])
	case drive.EventTodoDone:
		fmt.Fprintf(w, "[%s] %s id=%v dur=%vms tools=%v\n",
			short, typ, payload["todo_id"], payload["duration_ms"], payload["tool_calls"])
	case drive.EventTodoBlocked:
		fmt.Fprintf(w, "[%s] %s id=%v err=%q attempts=%v\n",
			short, typ, payload["todo_id"], payload["error"], payload["attempts"])
	case drive.EventTodoSkipped:
		fmt.Fprintf(w, "[%s] %s id=%v reason=%q\n",
			short, typ, payload["todo_id"], payload["reason"])
	case drive.EventTodoRetry:
		fmt.Fprintf(w, "[%s] %s id=%v attempt=%v\n", short, typ, payload["todo_id"], payload["attempt"])
	case drive.EventRunDone, drive.EventRunStopped, drive.EventRunFailed:
		fmt.Fprintf(w, "[%s] %s status=%v done=%v blocked=%v skipped=%v dur=%vms",
			short, typ, payload["status"], payload["done"], payload["blocked"],
			payload["skipped"], payload["duration_ms"])
		if reason, _ := payload["reason"].(string); reason != "" {
			fmt.Fprintf(w, " reason=%q", reason)
		}
		_, _ = fmt.Fprintln(w)
	default:
		fmt.Fprintf(w, "[%s] %s\n", short, typ)
	}
}

// renderDriveSummary prints a multi-line summary of the run with
// per-TODO status. Used both for the end-of-run report and `dfmc
// drive show <id>`.
func renderDriveSummary(w *os.File, run *drive.Run) {
	done, blocked, skipped, pending := run.Counts()
	var layerCount, rootCount, leafCount int
	if run.Plan != nil {
		layerCount = len(run.Plan.Layers)
		rootCount = len(run.Plan.Roots)
		leafCount = len(run.Plan.Leaves)
	} else {
		plan := supervisor.BuildExecutionPlan(supervisorbridge.RunFromDrive(run), supervisor.ExecutionOptions{})
		layerCount = len(plan.Layers)
		rootCount = len(plan.Roots)
		leafCount = len(plan.Leaves)
	}
	fmt.Fprintf(w, "\n=== drive run %s ===\n", run.ID)
	fmt.Fprintf(w, "task:   %s\n", run.Task)
	fmt.Fprintf(w, "status: %s", run.Status)
	if run.Reason != "" {
		fmt.Fprintf(w, " (%s)", run.Reason)
	}
	_, _ = fmt.Fprintln(w)
	if !run.EndedAt.IsZero() {
		fmt.Fprintf(w, "duration: %s\n", run.EndedAt.Sub(run.CreatedAt).Round(time.Millisecond))
	}
	if layerCount > 0 {
		fmt.Fprintf(w, "plan:   %d layers, %d roots, %d leaves\n", layerCount, rootCount, leafCount)
	}
	if run.Plan != nil && len(run.Plan.LaneCaps) > 0 {
		fmt.Fprintf(w, "lanes:  %s\n", formatLaneCaps(run.Plan.LaneOrder, run.Plan.LaneCaps))
	}
	fmt.Fprintf(w, "totals:  %d done, %d blocked, %d skipped, %d pending (of %d)\n",
		done, blocked, skipped, pending, len(run.Todos))
	fmt.Fprintln(w)
	for _, t := range run.Todos {
		marker := todoMarker(t.Status)
		deps := ""
		if len(t.DependsOn) > 0 {
			deps = "  ← " + strings.Join(t.DependsOn, ",")
		}
		meta := ""
		if strings.TrimSpace(t.Kind) != "" && !strings.EqualFold(t.Kind, "work") {
			meta = " [" + t.Kind + "]"
		}
		if strings.EqualFold(t.Origin, "supervisor") {
			meta += " [auto]"
		}
		fmt.Fprintf(w, "  %s %s  %s%s%s\n", marker, t.ID, t.Title, meta, deps)
		if t.Status == drive.TodoBlocked && t.Error != "" {
			fmt.Fprintf(w, "      err: %s\n", truncateLine(t.Error, 200))
		}
		if t.Status == drive.TodoSkipped && t.Error != "" {
			fmt.Fprintf(w, "      skip: %s\n", t.Error)
		}
		if t.Status == drive.TodoDone && t.Brief != "" {
			fmt.Fprintf(w, "      brief: %s\n", truncateLine(t.Brief, 200))
		}
	}
}

// todoMarker is a tiny ASCII glyph for status. Avoiding emoji because
// drive output frequently lands in CI logs / files where emoji render
// as boxes.
func todoMarker(s drive.TodoStatus) string {
	switch s {
	case drive.TodoDone:
		return "[x]"
	case drive.TodoBlocked:
		return "[!]"
	case drive.TodoSkipped:
		return "[-]"
	case drive.TodoRunning:
		return "[*]"
	default:
		return "[ ]"
	}
}

func formatLaneCaps(order []string, caps map[string]int) string {
	if len(caps) == 0 {
		return ""
	}
	parts := make([]string, 0, len(caps))
	seen := map[string]struct{}{}
	for _, lane := range order {
		lane = strings.TrimSpace(lane)
		if lane == "" {
			continue
		}
		cap, ok := caps[lane]
		if !ok {
			continue
		}
		seen[strings.ToLower(lane)] = struct{}{}
		parts = append(parts, fmt.Sprintf("%s=%d", lane, cap))
	}
	var extra []string
	for lane, cap := range caps {
		if _, ok := seen[strings.ToLower(strings.TrimSpace(lane))]; ok {
			continue
		}
		extra = append(extra, fmt.Sprintf("%s=%d", lane, cap))
	}
	sort.Strings(extra)
	parts = append(parts, extra...)
	return strings.Join(parts, ", ")
}

func printDriveHelp() {
	fmt.Println(`dfmc drive — autonomous plan/execute loop

Usage:
  dfmc drive "<task>"          plan and execute a new task
  dfmc drive list              list past drive runs
  dfmc drive active            list runs currently running in this process
  dfmc drive show <id>         show a run's TODOs and status
  dfmc drive resume <id>       resume a stopped or interrupted run
  dfmc drive stop [id]         cancel an active run (id optional if exactly one is active)
  dfmc drive delete <id>       remove a run record

Flags (for new runs):
  --max-todos N           cap planner output (default 20)
  --max-failed N          stop after N consecutive blocked TODOs (default 3)
  --max-wall-time DUR     hard wall-clock cap (default 30m)
  --retries N             per-TODO retry count (default 1)
  --planner NAME          override the planner provider/model
  --max-parallel N        max concurrent TODO executors (default 3); 1 = sequential
  --auto-survey           prepend a supervisor-generated discovery task
  --auto-verify           append a supervisor-generated verification TODO
  --route TAG=PROFILE     route TODOs with provider_tag=TAG to a specific
                          provider profile (repeatable)
  --auto-approve LIST     comma-separated tools to auto-approve during the run.
                          Use "*" to approve everything (truly unattended).
                          Without this, drive prompts for every gated tool.
  --from-spec PATH        load TODOs literally from a markdown spec
                          (e.g. .project/PLAN.md), skipping the planner LLM.
                          Each '- [ ]' becomes one TODO; classification is
                          keyword-based.
  --spec-section ANCHOR   filter --from-spec to one heading anchor
  --spec-include-done     also load already-checked items as status=done

Examples:
  dfmc drive "add rate limiting to /api/auth"
  dfmc drive --max-todos 10 --planner anthropic-opus "refactor X"
  dfmc drive --max-parallel 4 --route plan=opus --route code=sonnet --route test=haiku "ship feature"
  dfmc drive --auto-approve "edit_file,write_file,apply_patch" "do work unattended"
  dfmc drive --auto-approve "*" "fully autonomous run"
  dfmc drive --from-spec .project/PLAN.md --spec-section phase-1
  dfmc drive resume drv-67abcd-...

Drive runs use the engine's existing tool surface — every TODO becomes
a sub-agent with the same step/token budget, parking, and approvals
as a regular ` + "`dfmc ask`" + ` call.`)
}

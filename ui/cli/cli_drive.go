// Drive command — autonomous plan/execute loop.
//
// `dfmc drive "<task>"` plans the task into a DAG of TODOs, then walks
// them sequentially through sub-agents. Streams progress to stdout
// while running. Subcommands:
//
//   dfmc drive "task..."             — start a new run
//   dfmc drive list                  — list past runs (newest first)
//   dfmc drive show <id>             — pretty-print a run's todos
//   dfmc drive resume <id>           — re-enter a stopped/in-progress run
//   dfmc drive delete <id>           — remove a run record
//
// All event output goes to stderr so stdout stays parseable when the
// caller pipes the run summary somewhere. With --json the summary
// goes to stdout as a single JSON object.

package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func runDrive(ctx context.Context, eng *engine.Engine, args []string, asJSON bool) int {
	if len(args) == 0 {
		printDriveHelp()
		return 2
	}

	// Subcommand dispatch — recognized verbs first; everything else is
	// treated as the task string for a new run.
	switch args[0] {
	case "help", "-h", "--help":
		printDriveHelp()
		return 0
	case "list":
		return runDriveList(eng, asJSON)
	case "show":
		return runDriveShow(eng, args[1:], asJSON)
	case "resume":
		return runDriveResume(ctx, eng, args[1:], asJSON)
	case "delete":
		return runDriveDelete(eng, args[1:], asJSON)
	case "stop", "cancel":
		return runDriveStop(eng, args[1:], asJSON)
	case "active":
		return runDriveActive(eng, asJSON)
	}

	// Treat the whole arg list (joined) as the task. Supports both
	// `dfmc drive "build the thing"` and `dfmc drive build the thing`
	// forms — the latter without quotes works because we just join.
	fs := flag.NewFlagSet("drive", flag.ContinueOnError)
	maxTodos := fs.Int("max-todos", 0, "hard cap on TODO count (default 20)")
	maxFailed := fs.Int("max-failed", 0, "stop after N consecutive blocked TODOs (default 3)")
	maxWall := fs.Duration("max-wall-time", 0, "max total wall-clock duration (default 30m)")
	retries := fs.Int("retries", -1, "per-TODO retry count (default 1)")
	planner := fs.String("planner", "", "optional planner provider/model override")
	maxParallel := fs.Int("max-parallel", 0, "max concurrent TODO executors (default 3); 1 forces sequential")
	var routes multiString
	fs.Var(&routes, "route", "per-tag provider routing (repeatable): --route plan=opus --route code=sonnet")
	autoApprove := fs.String("auto-approve", "",
		`comma-separated list of tools to auto-approve during the run (use "*" to approve all). `+
			`Without this, drive prompts for every gated tool — usually not what you want unattended. `+
			`Recommended unattended preset: read_file,grep_codebase,glob,ast_query,find_symbol,list_dir,web_fetch,web_search,edit_file,write_file,apply_patch`)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "drive: %v\n", err)
		return 2
	}
	task := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if task == "" {
		fmt.Fprintln(os.Stderr, "drive: task is required (e.g. `dfmc drive \"add input validation to /api/users\"`)")
		return 2
	}
	routing, err := parseRouteFlags(routes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drive: %v\n", err)
		return 2
	}
	cfg := drive.Config{
		MaxTodos:       *maxTodos,
		MaxFailedTodos: *maxFailed,
		MaxWallTime:    *maxWall,
		Retries:        *retries,
		PlannerModel:   *planner,
		MaxParallel:    *maxParallel,
		Routing:        routing,
		AutoApprove:    parseAutoApproveFlag(*autoApprove),
	}
	return executeDriveRun(ctx, eng, task, cfg, asJSON, false, "")
}

// parseAutoApproveFlag splits the comma-separated --auto-approve flag
// into a tool list. Whitespace tokens and blanks are dropped (the
// driveAutoApprover ignores them anyway, but cleaning here keeps the
// driver event payload tidy if we ever surface AutoApprove there).
// Empty input returns nil so the driver knows to skip activation.
func parseAutoApproveFlag(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// multiString is a flag.Value that accumulates repeated --route uses
// into a slice. Standard library doesn't ship one, but the
// implementation is three lines.
type multiString []string

func (m *multiString) String() string     { return strings.Join(*m, ",") }
func (m *multiString) Set(v string) error { *m = append(*m, v); return nil }

// parseRouteFlags turns ["plan=opus","code=sonnet"] into a tag->profile
// map. Rejects malformed entries (no `=` or empty key/value) so the
// user notices early rather than getting silent default routing.
func parseRouteFlags(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for _, r := range raw {
		eq := strings.Index(r, "=")
		if eq <= 0 || eq == len(r)-1 {
			return nil, fmt.Errorf("--route %q must be tag=profile (e.g. --route code=anthropic-sonnet)", r)
		}
		tag := strings.TrimSpace(r[:eq])
		profile := strings.TrimSpace(r[eq+1:])
		if tag == "" || profile == "" {
			return nil, fmt.Errorf("--route %q has empty tag or profile", r)
		}
		out[tag] = profile
	}
	return out, nil
}

func executeDriveRun(ctx context.Context, eng *engine.Engine, task string, cfg drive.Config, asJSON, resume bool, resumeID string) int {
	runner := eng.NewDriveRunner()
	if runner == nil {
		fmt.Fprintln(os.Stderr, "drive: engine not initialized — run `dfmc doctor` to check provider config")
		return 1
	}
	store, err := drive.NewStore(eng.Storage.DB())
	if err != nil {
		fmt.Fprintf(os.Stderr, "drive: store init: %v\n", err)
		return 1
	}
	publisher := drive.Publisher(func(typ string, payload map[string]any) {
		eng.PublishDriveEvent(typ, payload)
		if !asJSON {
			renderDriveEventLine(os.Stderr, typ, payload)
		}
	})
	driver := drive.NewDriver(runner, store, publisher, cfg)

	var run *drive.Run
	if resume {
		run, err = driver.Resume(ctx, resumeID)
	} else {
		run, err = driver.Run(ctx, task)
	}
	if err != nil && run == nil {
		fmt.Fprintf(os.Stderr, "drive: %v\n", err)
		return 1
	}
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(run)
	} else {
		renderDriveSummary(os.Stdout, run)
	}
	if run.Status == drive.RunFailed {
		return 1
	}
	return 0
}

func runDriveList(eng *engine.Engine, asJSON bool) int {
	store, err := drive.NewStore(eng.Storage.DB())
	if err != nil {
		fmt.Fprintf(os.Stderr, "drive list: %v\n", err)
		return 1
	}
	runs, err := store.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "drive list: %v\n", err)
		return 1
	}
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(runs)
		return 0
	}
	if len(runs) == 0 {
		fmt.Println("(no drive runs yet)")
		return 0
	}
	for _, r := range runs {
		done, blocked, skipped, _ := r.Counts()
		fmt.Printf("%s  %s  %d todos (%d done, %d blocked, %d skipped)  %s\n",
			r.ID, r.Status, len(r.Todos), done, blocked, skipped,
			truncateLine(r.Task, 60))
	}
	return 0
}

func runDriveShow(eng *engine.Engine, args []string, asJSON bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "drive show: run ID required")
		return 2
	}
	store, err := drive.NewStore(eng.Storage.DB())
	if err != nil {
		fmt.Fprintf(os.Stderr, "drive show: %v\n", err)
		return 1
	}
	run, err := store.Load(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "drive show: %v\n", err)
		return 1
	}
	if run == nil {
		fmt.Fprintf(os.Stderr, "drive show: run %q not found\n", args[0])
		return 1
	}
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(run)
		return 0
	}
	renderDriveSummary(os.Stdout, run)
	return 0
}

func runDriveResume(ctx context.Context, eng *engine.Engine, args []string, asJSON bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "drive resume: run ID required")
		return 2
	}
	return executeDriveRun(ctx, eng, "", drive.Config{}, asJSON, true, args[0])
}

func runDriveStop(eng *engine.Engine, args []string, asJSON bool) int {
	if len(args) == 0 {
		// No ID given — if exactly one is active, stop that. Common
		// case for the typical "one drive at a time" workflow; the
		// user shouldn't have to copy the ID for an unambiguous stop.
		active := drive.ListActive()
		if len(active) == 0 {
			fmt.Fprintln(os.Stderr, "drive stop: no active drive runs in this process")
			return 1
		}
		if len(active) > 1 {
			fmt.Fprintln(os.Stderr, "drive stop: multiple active runs — pass an explicit run ID:")
			for _, a := range active {
				fmt.Fprintf(os.Stderr, "  %s  %s\n", a.RunID, truncateLine(a.Task, 60))
			}
			return 2
		}
		args = []string{active[0].RunID}
	}
	id := args[0]
	cancelled := drive.Cancel(id)
	// Refresh the persisted record so the user sees the post-cancel
	// status. The Driver's drainAndFinalize writes RunStopped before
	// returning, but the goroutine may need a beat to flush; we
	// don't block on that here — the next `drive show` will show it.
	store, _ := drive.NewStore(eng.Storage.DB())
	var run *drive.Run
	if store != nil {
		run, _ = store.Load(id)
	}
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"run_id":    id,
			"cancelled": cancelled,
			"run":       run,
		})
		return 0
	}
	if !cancelled {
		fmt.Printf("Drive %s: not active in this process (already done or wrong ID)\n", id)
		if run != nil {
			fmt.Printf("Persisted status: %s\n", run.Status)
		}
		return 1
	}
	fmt.Printf("Drive %s: cancellation signal sent. The loop will stop after the current TODO finishes.\n", id)
	return 0
}

func runDriveActive(eng *engine.Engine, asJSON bool) int {
	_ = eng
	active := drive.ListActive()
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(active)
		return 0
	}
	if len(active) == 0 {
		fmt.Println("(no active drive runs)")
		return 0
	}
	for _, a := range active {
		fmt.Printf("%s  %s\n", a.RunID, truncateLine(a.Task, 80))
	}
	return 0
}

func runDriveDelete(eng *engine.Engine, args []string, asJSON bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "drive delete: run ID required")
		return 2
	}
	store, err := drive.NewStore(eng.Storage.DB())
	if err != nil {
		fmt.Fprintf(os.Stderr, "drive delete: %v\n", err)
		return 1
	}
	if err := store.Delete(args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "drive delete: %v\n", err)
		return 1
	}
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"deleted": args[0]})
	} else {
		fmt.Printf("deleted %s\n", args[0])
	}
	return 0
}

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
		fmt.Fprintln(w)
	default:
		fmt.Fprintf(w, "[%s] %s\n", short, typ)
	}
}

// renderDriveSummary prints a multi-line summary of the run with
// per-TODO status. Used both for the end-of-run report and `dfmc
// drive show <id>`.
func renderDriveSummary(w *os.File, run *drive.Run) {
	done, blocked, skipped, pending := run.Counts()
	fmt.Fprintf(w, "\n=== drive run %s ===\n", run.ID)
	fmt.Fprintf(w, "task:   %s\n", run.Task)
	fmt.Fprintf(w, "status: %s", run.Status)
	if run.Reason != "" {
		fmt.Fprintf(w, " (%s)", run.Reason)
	}
	fmt.Fprintln(w)
	if !run.EndedAt.IsZero() {
		fmt.Fprintf(w, "duration: %s\n", run.EndedAt.Sub(run.CreatedAt).Round(time.Millisecond))
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
		fmt.Fprintf(w, "  %s %s  %s%s\n", marker, t.ID, t.Title, deps)
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
  --route TAG=PROFILE     route TODOs with provider_tag=TAG to a specific
                          provider profile (repeatable)
  --auto-approve LIST     comma-separated tools to auto-approve during the run.
                          Use "*" to approve everything (truly unattended).
                          Without this, drive prompts for every gated tool.

Examples:
  dfmc drive "add rate limiting to /api/auth"
  dfmc drive --max-todos 10 --planner anthropic-opus "refactor X"
  dfmc drive --max-parallel 4 --route plan=opus --route code=sonnet --route test=haiku "ship feature"
  dfmc drive --auto-approve "edit_file,write_file,apply_patch" "do work unattended"
  dfmc drive --auto-approve "*" "fully autonomous run"
  dfmc drive resume drv-67abcd-...

Drive runs use the engine's existing tool surface — every TODO becomes
a sub-agent with the same step/token budget, parking, and approvals
as a regular ` + "`dfmc ask`" + ` call.`)
}

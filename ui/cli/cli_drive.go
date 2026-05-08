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
//
// File layout: this file owns runDrive (the top-level dispatcher) +
// executeDriveRun (new + resume entry into the drive runner) +
// the route/auto-approve flag parsers. Subcommand handlers live in
// cli_drive_subcmds.go; render/format helpers live in
// cli_drive_render.go.

package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

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
	autoVerify := fs.Bool("auto-verify", false, "append a supervisor-generated verification TODO after planning")
	autoSurvey := fs.Bool("auto-survey", false, "prepend a supervisor-generated discovery/survey TODO when planning skipped it")
	var routes multiString
	fs.Var(&routes, "route", "per-tag provider routing (repeatable): --route plan=opus --route code=sonnet")
	autoApprove := fs.String("auto-approve", "",
		`comma-separated list of tools to auto-approve during the run (use "*" to approve all). `+
			`Without this, drive prompts for every gated tool — usually not what you want unattended. `+
			`Recommended unattended preset: read_file,grep_codebase,glob,ast_query,find_symbol,list_dir,web_fetch,web_search,edit_file,write_file,apply_patch`)
	fromSpec := fs.String("from-spec", "",
		`load TODOs literally from a markdown spec file (e.g. .project/PLAN.md), skipping the planner LLM. `+
			`Each '- [ ]' becomes one Drive TODO; classification is keyword-based (see spec_to_todo). `+
			`Pair with --spec-section to filter to a single anchor.`)
	specSection := fs.String("spec-section", "",
		`when used with --from-spec: only ingest TODOs from the named heading anchor (lowercase slug)`)
	specIncludeDone := fs.Bool("spec-include-done", false,
		`when used with --from-spec: also load already-checked items as status=done TODOs`)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "drive: %v\n", err)
		return 2
	}
	task := strings.TrimSpace(strings.Join(fs.Args(), " "))
	specPath := strings.TrimSpace(*fromSpec)
	if specPath == "" && task == "" {
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
		AutoSurvey:     *autoSurvey,
		AutoVerify:     *autoVerify,
	}
	if specPath != "" {
		return executeDriveRunFromSpec(ctx, eng, specPath, task, *specSection, *specIncludeDone, cfg, asJSON)
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
	driver.SetReportDir(eng.DriveReportDir())

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

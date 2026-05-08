// cli_drive_from_spec.go — `dfmc drive --from-spec <path>` path.
//
// Why a separate file: the regular `dfmc drive "task"` path goes
// through driver.Run() which calls the planner LLM. The
// from-spec path skips the planner entirely — spec_to_todo gives us
// the literal TODO list, we package it into a Run, and call
// driver.RunPrepared() so executeLoop runs against the preset plan.
// The two paths share executeDriveRun's setup boilerplate (runner,
// store, publisher, driver) but diverge at the `Run vs RunPrepared`
// fork, so a sibling file keeps both flows scannable.

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// executeDriveRunFromSpec runs spec_to_todo against the given path,
// converts the resulting items into drive.Todos, and starts a Drive
// run with those TODOs pre-loaded so the planner LLM is skipped.
//
// `taskOverride` is the optional human-readable label for the run;
// when blank we synthesise one from the spec path so `dfmc drive
// list` still shows something descriptive.
func executeDriveRunFromSpec(ctx context.Context, eng *engine.Engine, specPath, taskOverride, section string, includeDone bool, cfg drive.Config, asJSON bool) int {
	todos, task, err := todosFromSpecFile(ctx, eng, specPath, section, includeDone, taskOverride)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drive: %v\n", err)
		return 2
	}
	if len(todos) == 0 {
		fmt.Fprintln(os.Stderr, "drive: no TODOs found in spec (check the file has `- [ ]` entries; try --spec-include-done or drop --spec-section)")
		return 2
	}

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

	run, err := drive.NewRun(task)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drive: %v\n", err)
		return 1
	}
	run.Todos = todos
	run, err = driver.RunPrepared(ctx, run)
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

// todosFromSpecFile is the CLI's thin wrapper over
// engine.TodosFromSpecFile — the heavy lifting (spec_to_todo +
// drive.TodosFromSpec conversion) lives there so HTTP / MCP /
// CLI all use one path. CLI-specific work is the `task` label
// synthesis and the dropped-items stderr warning.
func todosFromSpecFile(ctx context.Context, eng *engine.Engine, specPath, section string, includeDone bool, taskOverride string) ([]drive.Todo, string, error) {
	todos, dropped, err := eng.TodosFromSpecFile(ctx, specPath, section, includeDone)
	if err != nil {
		return nil, "", err
	}
	if dropped > 0 {
		fmt.Fprintf(os.Stderr, "drive: spec ingest dropped %d item(s) without a title\n", dropped)
	}
	task := strings.TrimSpace(taskOverride)
	if task == "" {
		task = fmt.Sprintf("drive --from-spec %s", specPath)
		if section != "" {
			task += " (section: " + section + ")"
		}
	}
	return todos, task, nil
}

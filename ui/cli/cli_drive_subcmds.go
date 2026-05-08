package cli

// cli_drive_subcmds.go — list / show / resume / stop / active / delete
// subcommand handlers for `dfmc drive`. Pulled out of cli_drive.go so
// the file with the new-run dispatcher stays focused. Each handler
// loads the persisted run via drive.Store, optionally formats it via
// renderDriveSummary in cli_drive_render.go, and returns an exit code.
// Companion siblings:
//
//   - cli_drive.go         runDrive subcommand dispatcher +
//                          executeDriveRun for new/resume runs +
//                          parseAutoApproveFlag + multiString +
//                          parseRouteFlags
//   - cli_drive_render.go  renderDriveEventLine per-event stderr
//                          formatter + renderDriveSummary +
//                          todoMarker + formatLaneCaps +
//                          printDriveHelp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

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

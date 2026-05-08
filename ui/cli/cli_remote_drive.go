// Remote Drive client: `dfmc remote drive ...` subcommands that wrap
// the /api/v1/drive endpoints. Extracted from cli_remote.go.
// Subcommands mirror the local CLI: start / list / show / resume /
// delete / stop / active. Per-id read/control subcommands and the
// toAny list helper live in cli_remote_drive_runs.go; this file
// keeps the dispatcher and the start path (which carries the heavier
// flag surface that grows as new Drive runner knobs land).

package cli

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// runRemoteDrive is the `dfmc remote drive ...` client. Wraps the
// /api/v1/drive endpoints. Subcommands mirror the local CLI:
//
//	dfmc remote drive "task..."       start (POST /api/v1/drive)
//	dfmc remote drive list            GET  /api/v1/drive
//	dfmc remote drive show <id>       GET  /api/v1/drive/{id}
//	dfmc remote drive resume <id>     POST /api/v1/drive/{id}/resume
//	dfmc remote drive delete <id>     DELETE /api/v1/drive/{id}
//
// Live event stream on the remote: use `dfmc remote events --filter drive:`
// to tail drive:* events from the same process.
func runRemoteDrive(eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, `usage: dfmc remote drive ["<task>" | list | show <id> | resume <id> | delete <id>]`)
		return 2
	}
	defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)

	switch args[0] {
	case "list":
		return remoteDriveList(defaultURL, args[1:], jsonMode)
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc remote drive show <id>")
			return 2
		}
		return remoteDriveShow(defaultURL, args[1], args[2:], jsonMode)
	case "resume":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc remote drive resume <id>")
			return 2
		}
		return remoteDriveResume(defaultURL, args[1], args[2:], jsonMode)
	case "delete":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc remote drive delete <id>")
			return 2
		}
		return remoteDriveDelete(defaultURL, args[1], args[2:], jsonMode)
	case "stop", "cancel":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc remote drive stop <id>")
			return 2
		}
		return remoteDriveStop(defaultURL, args[1], args[2:], jsonMode)
	case "active":
		return remoteDriveActive(defaultURL, args[1:], jsonMode)
	}

	// Default: treat the whole arg list as the task and start a run.
	return remoteDriveStart(defaultURL, args, jsonMode)
}

func remoteDriveStart(defaultURL string, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("remote drive start", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 30*time.Second, "request timeout")
	maxTodos := fs.Int("max-todos", 0, "hard cap on TODO count")
	maxFailed := fs.Int("max-failed", 0, "stop after N consecutive blocks")
	maxWall := fs.Duration("max-wall-time", 0, "max total wall-clock duration")
	retries := fs.Int("retries", -1, "per-TODO retry count")
	maxParallel := fs.Int("max-parallel", 0, "max concurrent TODO executors")
	planner := fs.String("planner", "", "optional planner provider/model override")
	autoApprove := fs.String("auto-approve", "", `comma-separated tools to auto-approve (use "*" for all)`)
	var routes multiStringFlag
	fs.Var(&routes, "route", "per-tag provider routing tag=profile (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	task := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if task == "" {
		fmt.Fprintln(os.Stderr, `usage: dfmc remote drive "<task>"`)
		return 2
	}
	routing, err := parseKeyValueParams(routes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote drive: %v\n", err)
		return 2
	}
	routingStr := make(map[string]string, len(routing))
	for k, v := range routing {
		routingStr[k] = fmt.Sprint(v)
	}
	body := map[string]any{
		"task":             task,
		"max_todos":        *maxTodos,
		"max_failed_todos": *maxFailed,
		"max_wall_time_ms": maxWall.Milliseconds(),
		"retries":          *retries,
		"max_parallel":     *maxParallel,
		"planner_model":    *planner,
		"routing":          routingStr,
		"auto_approve":     parseAutoApproveFlag(*autoApprove),
	}
	payload, status, err := remoteJSONRequest(http.MethodPost,
		strings.TrimRight(*baseURL, "/")+"/api/v1/drive",
		*token, body, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote drive start: %v\n", err)
		return 1
	}
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "remote drive start failed (HTTP %d): %v\n", status, payload)
		return 1
	}
	if jsonMode {
		mustPrintJSON(payload)
	} else {
		fmt.Println("Drive started — subscribe with: dfmc remote events --filter drive:")
		mustPrintJSON(payload)
	}
	return 0
}

// Remote Drive client: `dfmc remote drive ...` subcommands that wrap
// the /api/v1/drive endpoints. Extracted from cli_remote.go.
// Subcommands mirror the local CLI: start / list / show / resume /
// delete / stop / active.

package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

func remoteDriveStop(defaultURL, id string, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("remote drive stop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	payload, status, err := remoteJSONRequest(http.MethodPost,
		strings.TrimRight(*baseURL, "/")+"/api/v1/drive/"+url.PathEscape(id)+"/stop",
		*token, nil, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote drive stop: %v\n", err)
		return 1
	}
	if status == http.StatusNotFound {
		fmt.Fprintf(os.Stderr, "remote drive stop: %q is not active on the remote\n", id)
		return 1
	}
	mustPrintJSON(payload)
	_ = jsonMode
	return 0
}

func remoteDriveActive(defaultURL string, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("remote drive active", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	endpoint := strings.TrimRight(*baseURL, "/") + "/api/v1/drive/active"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote drive active: %v\n", err)
		return 1
	}
	if tok := strings.TrimSpace(*token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: *timeout}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote drive active: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "remote drive active failed (HTTP %d): %s\n", resp.StatusCode, strings.TrimSpace(string(rawBody)))
		return 1
	}
	var active []map[string]any
	if err := json.Unmarshal(rawBody, &active); err != nil {
		fmt.Fprintf(os.Stderr, "remote drive active: invalid response: %v\n", err)
		return 1
	}
	if jsonMode {
		fmt.Println(string(rawBody))
		return 0
	}
	if len(active) == 0 {
		fmt.Println("(no active drive runs on remote)")
		return 0
	}
	for _, a := range active {
		fmt.Printf("%v  %s\n", a["RunID"], truncateLine(fmt.Sprint(a["Task"]), 80))
	}
	return 0
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

func remoteDriveList(defaultURL string, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("remote drive list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// /api/v1/drive returns a JSON array, not an object — bypass
	// remoteJSONRequest (which assumes objects) and decode raw.
	endpoint := strings.TrimRight(*baseURL, "/") + "/api/v1/drive"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote drive list: %v\n", err)
		return 1
	}
	if tok := strings.TrimSpace(*token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: *timeout}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote drive list: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "remote drive list failed (HTTP %d): %s\n", resp.StatusCode, strings.TrimSpace(string(rawBody)))
		return 1
	}
	var runs []map[string]any
	if err := json.Unmarshal(rawBody, &runs); err != nil {
		fmt.Fprintf(os.Stderr, "remote drive list: invalid response: %v\n", err)
		return 1
	}
	if jsonMode {
		fmt.Println(string(rawBody))
		return 0
	}
	if len(runs) == 0 {
		fmt.Println("(no drive runs on remote)")
		return 0
	}
	for _, m := range runs {
		fmt.Printf("%v  %v  %d todos  %s\n",
			m["id"], m["status"], len(toAny(m["todos"])), truncateLine(fmt.Sprint(m["task"]), 60))
	}
	return 0
}

func remoteDriveShow(defaultURL, id string, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("remote drive show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	payload, status, err := remoteJSONRequest(http.MethodGet,
		strings.TrimRight(*baseURL, "/")+"/api/v1/drive/"+url.PathEscape(id),
		*token, nil, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote drive show: %v\n", err)
		return 1
	}
	if status == http.StatusNotFound {
		fmt.Fprintf(os.Stderr, "remote drive show: run %q not found\n", id)
		return 1
	}
	mustPrintJSON(payload)
	_ = jsonMode
	return 0
}

func remoteDriveResume(defaultURL, id string, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("remote drive resume", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	payload, status, err := remoteJSONRequest(http.MethodPost,
		strings.TrimRight(*baseURL, "/")+"/api/v1/drive/"+url.PathEscape(id)+"/resume",
		*token, nil, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote drive resume: %v\n", err)
		return 1
	}
	if status == http.StatusConflict {
		fmt.Fprintf(os.Stderr, "remote drive resume: run %q is already terminal\n", id)
		return 1
	}
	if status == http.StatusNotFound {
		fmt.Fprintf(os.Stderr, "remote drive resume: run %q not found\n", id)
		return 1
	}
	mustPrintJSON(payload)
	_ = jsonMode
	return 0
}

func remoteDriveDelete(defaultURL, id string, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("remote drive delete", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	payload, _, err := remoteJSONRequest(http.MethodDelete,
		strings.TrimRight(*baseURL, "/")+"/api/v1/drive/"+url.PathEscape(id),
		*token, nil, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote drive delete: %v\n", err)
		return 1
	}
	mustPrintJSON(payload)
	_ = jsonMode
	return 0
}

// toAny is a tiny helper used by the list pretty-printer to safely
// count nested arrays in untyped JSON output.
func toAny(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

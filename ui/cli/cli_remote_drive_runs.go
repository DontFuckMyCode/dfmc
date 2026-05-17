// cli_remote_drive_runs.go — read/control subcommands for `dfmc
// remote drive`: list, active, show, resume, stop, delete plus the
// toAny helper used by the list pretty-printer. Sibling of
// cli_remote_drive.go which keeps the runRemoteDrive dispatcher and
// the heavier remoteDriveStart starter (planner / routing / auto-
// approve / per-TODO knobs) plus the package doc-comment.
//
// Splitting the per-id read/control surfaces out keeps
// cli_remote_drive.go scannable when adjusting the start-flag
// surface (which is the only one that grows over time as new Drive
// runner knobs land).

package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func remoteDriveStop(defaultURL, id string, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("remote drive stop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	payload, status, err := remoteJSONEndpoint(http.MethodPost, *baseURL, "/api/v1/drive/"+url.PathEscape(id)+"/stop", nil, *token, nil, *timeout)
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
	endpoint := remoteEndpoint(*baseURL, "/api/v1/drive/active", nil)
	req, err := remoteRequest(http.MethodGet, endpoint, *token, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote drive active: %v\n", err)
		return 1
	}
	client := &http.Client{Timeout: *timeout}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote drive active: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	rawBody, _ := readRemoteResponseBody(resp, 1<<20)
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
	endpoint := remoteEndpoint(*baseURL, "/api/v1/drive", nil)
	req, err := remoteRequest(http.MethodGet, endpoint, *token, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote drive list: %v\n", err)
		return 1
	}
	client := &http.Client{Timeout: *timeout}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote drive list: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	rawBody, _ := readRemoteResponseBody(resp, 2<<20)
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
	payload, status, err := remoteJSONEndpoint(http.MethodGet, *baseURL, "/api/v1/drive/"+url.PathEscape(id), nil, *token, nil, *timeout)
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
	payload, status, err := remoteJSONEndpoint(http.MethodPost, *baseURL, "/api/v1/drive/"+url.PathEscape(id)+"/resume", nil, *token, nil, *timeout)
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
	payload, _, err := remoteJSONEndpoint(http.MethodDelete, *baseURL, "/api/v1/drive/"+url.PathEscape(id), nil, *token, nil, *timeout)
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

// Remote and serve subcommands: serve (embedded HTTP+SSE server),
// remote (client against a running serve). Extracted from cli.go so the
// dispatcher stays focused. These commands share SSE parsing, bearer
// middleware, remote JSON-request helpers, and codemap payload decoding
// so they travel together.

package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/ui/web"
)

// addRemoteTokenFlag wires the standard `--token` flag onto fs with its
// default sourced from the DFMC_REMOTE_TOKEN env var. Used by every
// `dfmc remote *` client subcommand. addServeTokenFlag does the same
// for `dfmc serve` but reads DFMC_WEB_TOKEN instead so an operator can
// run a serve and a client side-by-side with separate creds without
// either subcommand stomping on the other's env.
//
// Centralising the flag declaration kills the H1 review finding —
// the line was duplicated 18 times across this file, so a future
// rename of the env var or the flag description had to be repeated
// 18 times or it would silently drift.
func addRemoteTokenFlag(fs *flag.FlagSet) *string {
	return fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
}

func addServeTokenFlag(fs *flag.FlagSet) *string {
	return fs.String("token", strings.TrimSpace(os.Getenv("DFMC_WEB_TOKEN")), "api token (for auth=token)")
}

func runServe(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	host := fs.String("host", eng.Config.Web.Host, "host")
	port := fs.Int("port", eng.Config.Web.Port, "port")
	auth := fs.String("auth", eng.Config.Web.Auth, "none|token")
	token := addServeTokenFlag(fs)
	openBrowser := fs.Bool("open-browser", eng.Config.Web.OpenBrowser, "open default browser")
	// --insecure is the explicit opt-out for the non-loopback-without-
	// auth guard below. Without it, we refuse to start a server that
	// exposes tool/file endpoints unauthenticated on a LAN or public
	// interface — a common foot-gun where a user flips --host 0.0.0.0
	// for sharing and forgets that --auth still defaults to "none".
	insecure := fs.Bool("insecure", false, "allow --auth=none on non-loopback hosts (exposes tools/files to the network)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	mode := strings.ToLower(strings.TrimSpace(*auth))
	if mode != "none" && mode != "token" {
		fmt.Fprintln(os.Stderr, "serve auth must be none|token")
		return 2
	}
	if mode == "token" && strings.TrimSpace(*token) == "" {
		fmt.Fprintln(os.Stderr, "serve token auth requires --token or DFMC_WEB_TOKEN")
		return 2
	}
	if mode == "none" && !isLoopbackBindHost(*host) && !*insecure {
		fmt.Fprintf(os.Stderr,
			"refusing to serve with --auth=none on non-loopback host %q: the web API exposes file/tool/shell endpoints. "+
				"Pass --auth=token (with --token or DFMC_WEB_TOKEN) to require a bearer token, or add --insecure to accept the risk explicitly.\n",
			*host)
		return 2
	}
	if mode == "none" && !isLoopbackBindHost(*host) && *insecure {
		fmt.Fprintf(os.Stderr,
			"WARNING: --auth=none on non-loopback host %q — all API endpoints (file read/write, tool invocation, shell) are reachable without authentication. Anyone on the network can drive this process.\n",
			*host)
	}

	if jsonMode {
		_ = printJSON(map[string]any{
			"status": "starting",
			"host":   *host,
			"port":   *port,
			"auth":   mode,
		})
	}

	srv := web.New(eng, *host, *port)
	srv.SetBearerToken(*token)
	handler := srv.Handler()
	if mode == "token" {
		handler = bearerTokenMiddleware(handler, *token)
	}

	addr := fmt.Sprintf("%s:%d", *host, *port)
	server := web.NewHTTPServer(addr, handler)
	fmt.Printf("DFMC Web API listening on http://%s\n", addr)
	if *openBrowser {
		target := "http://" + addr
		go func() {
			// Give server a small head-start before opening browser.
			time.Sleep(120 * time.Millisecond)
			_ = tryOpenBrowser(target)
		}()
	}
	if err := serveWithContext(ctx, server); err != nil {
		fmt.Fprintf(os.Stderr, "serve error: %v\n", err)
		return 1
	}
	return 0
}

func runRemote(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) == 0 {
		args = []string{"start"}
	}

	switch args[0] {
	case "status":
		fs := flag.NewFlagSet("remote status", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		live := fs.Bool("live", false, "query remote server status instead of local config")
		baseURL := fs.String("url", defaultURL, "remote base URL (for --live)")
		token := addRemoteTokenFlag(fs)
		timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		if !*live {
			payload := map[string]any{
				"enabled":   eng.Config.Remote.Enabled,
				"host":      eng.Config.Web.Host,
				"grpc_port": eng.Config.Remote.GRPCPort,
				"ws_port":   eng.Config.Remote.WSPort,
				"auth":      eng.Config.Remote.Auth,
			}
			if jsonMode {
				mustPrintJSON(payload)
				return 0
			}
			fmt.Printf("Remote enabled: %t\n", eng.Config.Remote.Enabled)
			fmt.Printf("Host:           %s\n", eng.Config.Web.Host)
			fmt.Printf("gRPC port:      %d\n", eng.Config.Remote.GRPCPort)
			fmt.Printf("WS/HTTP port:   %d\n", eng.Config.Remote.WSPort)
			fmt.Printf("Auth:           %s\n", eng.Config.Remote.Auth)
			return 0
		}

		statusURL := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/status"
		providersURL := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/providers"
		statusPayload, _, err := remoteJSONRequest(http.MethodGet, statusURL, *token, nil, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote status error: %v\n", err)
			return 1
		}
		providersPayload, _, err := remoteJSONRequest(http.MethodGet, providersURL, *token, nil, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote providers error: %v\n", err)
			return 1
		}
		out := map[string]any{
			"url":       *baseURL,
			"status":    statusPayload,
			"providers": providersPayload,
		}
		if jsonMode {
			mustPrintJSON(out)
			return 0
		}
		mustPrintJSON(out)
		return 0

	case "probe":
		fs := flag.NewFlagSet("remote probe", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := addRemoteTokenFlag(fs)
		timeout := fs.Duration("timeout", 3*time.Second, "request timeout")
		endpointsRaw := fs.String("endpoints", "/healthz,/api/v1/status,/api/v1/providers", "comma-separated endpoint paths")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		client := &http.Client{Timeout: *timeout}
		endpoints := parseEndpointList(*endpointsRaw)
		results := make([]remoteProbeResult, 0, len(endpoints))
		hasFailure := false
		for _, endpoint := range endpoints {
			res := probeRemoteEndpoint(client, *baseURL, endpoint, *token)
			results = append(results, res)
			if !res.OK {
				hasFailure = true
			}
		}

		if jsonMode {
			_ = printJSON(map[string]any{
				"url":       *baseURL,
				"timeout":   timeout.String(),
				"endpoints": endpoints,
				"results":   results,
			})
			if hasFailure {
				return 1
			}
			return 0
		}

		fmt.Printf("Remote probe: %s\n", *baseURL)
		for _, r := range results {
			status := "PASS"
			if !r.OK {
				status = "FAIL"
			}
			details := strings.TrimSpace(r.Error)
			if details == "" {
				details = strings.TrimSpace(r.Body)
			}
			if details == "" {
				details = "(empty)"
			}
			fmt.Printf("[%s] %s -> %d (%dms) %s\n", status, r.Endpoint, r.StatusCode, r.DurationMs, truncateLine(details, 160))
		}
		if hasFailure {
			return 1
		}
		return 0

	case "events":
		fs := flag.NewFlagSet("remote events", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := addRemoteTokenFlag(fs)
		eventType := fs.String("type", "*", "event type filter")
		timeout := fs.Duration("timeout", 20*time.Second, "stream timeout")
		maxEvents := fs.Int("max", 100, "max events to collect before stopping")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/ws"
		if t := strings.TrimSpace(*eventType); t != "" {
			endpoint = endpoint + "?type=" + url.QueryEscape(t)
		}
		events, err := remoteCollectEvents(endpoint, *token, *timeout, *maxEvents)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote events error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"url":      endpoint,
				"count":    len(events),
				"events":   events,
				"timeout":  timeout.String(),
				"max":      *maxEvents,
				"filter":   *eventType,
				"received": time.Now().UTC().Format(time.RFC3339),
			})
			return 0
		}
		fmt.Printf("Remote events: %s\n", endpoint)
		for _, ev := range events {
			kind := strings.TrimSpace(fmt.Sprint(ev["type"]))
			if kind == "" {
				kind = "event"
			}
			body := truncateLine(compactJSON(ev), 200)
			fmt.Printf("[%s] %s\n", strings.ToUpper(kind), body)
		}
		return 0

	case "ask":
		fs := flag.NewFlagSet("remote ask", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := addRemoteTokenFlag(fs)
		timeout := fs.Duration("timeout", 60*time.Second, "request timeout")
		message := fs.String("message", "", "question/message to send")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if strings.TrimSpace(*message) == "" && len(fs.Args()) > 0 {
			*message = strings.TrimSpace(strings.Join(fs.Args(), " "))
		}
		if strings.TrimSpace(*message) == "" {
			fmt.Fprintln(os.Stderr, "usage: dfmc remote ask [--url ...] [--token ...] --message \"...\"")
			return 2
		}

		events, answer, err := remoteAsk(*baseURL, *token, strings.TrimSpace(*message), *timeout, jsonMode)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote ask error: %v\n", err)
			return 1
		}
		if jsonMode {
			_ = printJSON(map[string]any{
				"url":      *baseURL,
				"message":  *message,
				"events":   events,
				"answer":   answer,
				"event_n":  len(events),
				"received": time.Now().UTC().Format(time.RFC3339),
			})
			return 0
		}
		if !strings.HasSuffix(answer, "\n") {
			fmt.Println()
		}
		return 0

	case "tool":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc remote tool <name> [--url ...] [--token ...] [--param key=value]")
			return 2
		}
		name := strings.TrimSpace(args[1])
		fs := flag.NewFlagSet("remote tool", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := addRemoteTokenFlag(fs)
		timeout := fs.Duration("timeout", 30*time.Second, "request timeout")
		var paramsRaw multiStringFlag
		fs.Var(&paramsRaw, "param", "tool param in key=value format (repeatable)")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		params, err := parseKeyValueParams(paramsRaw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote tool param error: %v\n", err)
			return 2
		}
		payload, _, err := remoteJSONRequest(
			http.MethodPost,
			strings.TrimRight(strings.TrimSpace(*baseURL), "/")+"/api/v1/tools/"+url.PathEscape(name),
			*token,
			map[string]any{"params": params},
			*timeout,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote tool error: %v\n", err)
			return 1
		}
		if jsonMode {
			mustPrintJSON(payload)
			return 0
		}
		if out, ok := payload["output"]; ok {
			fmt.Println(fmt.Sprint(out))
		} else {
			mustPrintJSON(payload)
		}
		return 0

	case "skill":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc remote skill <name> [--url ...] [--token ...] [--input ...]")
			return 2
		}
		name := strings.TrimSpace(args[1])
		fs := flag.NewFlagSet("remote skill", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := addRemoteTokenFlag(fs)
		timeout := fs.Duration("timeout", 60*time.Second, "request timeout")
		input := fs.String("input", "", "skill input text")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		if strings.TrimSpace(*input) == "" && len(fs.Args()) > 0 {
			*input = strings.TrimSpace(strings.Join(fs.Args(), " "))
		}
		payload, _, err := remoteJSONRequest(
			http.MethodPost,
			strings.TrimRight(strings.TrimSpace(*baseURL), "/")+"/api/v1/skills/"+url.PathEscape(name),
			*token,
			map[string]any{"input": *input},
			*timeout,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote skill error: %v\n", err)
			return 1
		}
		if jsonMode {
			mustPrintJSON(payload)
			return 0
		}
		if out, ok := payload["answer"]; ok {
			fmt.Println(fmt.Sprint(out))
		} else {
			mustPrintJSON(payload)
		}
		return 0

	case "analyze":
		fs := flag.NewFlagSet("remote analyze", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := addRemoteTokenFlag(fs)
		timeout := fs.Duration("timeout", 60*time.Second, "request timeout")
		path := fs.String("path", "", "target path")
		full := fs.Bool("full", false, "run full analysis")
		security := fs.Bool("security", false, "include security report")
		complexity := fs.Bool("complexity", false, "include complexity report")
		deadCode := fs.Bool("dead-code", false, "include dead-code report")
		magicDoc := fs.Bool("magicdoc", false, "update magic doc after remote analyze")
		magicDocPath := fs.String("magicdoc-path", "", "custom magic doc path")
		magicDocTitle := fs.String("magicdoc-title", "DFMC Project Brief", "magic doc title")
		magicDocHotspots := fs.Int("magicdoc-hotspots", 8, "max hotspot entries for magic doc")
		magicDocDeps := fs.Int("magicdoc-deps", 8, "max dependency entries for magic doc")
		magicDocRecent := fs.Int("magicdoc-recent", 5, "max recent entries for magic doc")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		payload, _, err := remoteJSONRequest(
			http.MethodPost,
			strings.TrimRight(strings.TrimSpace(*baseURL), "/")+"/api/v1/analyze",
			*token,
			map[string]any{
				"path":              *path,
				"full":              *full,
				"security":          *security,
				"complexity":        *complexity,
				"dead_code":         *deadCode,
				"magicdoc":          *magicDoc,
				"magicdoc_path":     strings.TrimSpace(*magicDocPath),
				"magicdoc_title":    strings.TrimSpace(*magicDocTitle),
				"magicdoc_hotspots": *magicDocHotspots,
				"magicdoc_deps":     *magicDocDeps,
				"magicdoc_recent":   *magicDocRecent,
			},
			*timeout,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote analyze error: %v\n", err)
			return 1
		}
		if jsonMode {
			mustPrintJSON(payload)
			return 0
		}
		mustPrintJSON(payload)
		return 0

	case "context":
		action := "budget"
		parseFrom := 1
		if len(args) >= 2 {
			candidate := strings.ToLower(strings.TrimSpace(args[1]))
			if !strings.HasPrefix(candidate, "-") {
				action = candidate
				parseFrom = 2
			}
		}
		fs := flag.NewFlagSet("remote context", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := addRemoteTokenFlag(fs)
		timeout := fs.Duration("timeout", 20*time.Second, "request timeout")
		query := fs.String("query", "", "query for task-aware budget simulation")
		runtimeProvider := fs.String("runtime-provider", "", "runtime provider override for context simulation")
		runtimeModel := fs.String("runtime-model", "", "runtime model override for context simulation")
		runtimeToolStyle := fs.String("runtime-tool-style", "", "runtime tool style override (function-calling|tool_use|none|provider-native)")
		runtimeMaxContext := fs.Int("runtime-max-context", 0, "runtime max context override for context simulation")
		maxWords := fs.Int("max-words", 240, "max words for context brief")
		briefPath := fs.String("path", "", "path to magic doc file (relative to project root or absolute)")
		if err := fs.Parse(args[parseFrom:]); err != nil {
			return 2
		}

		switch action {
		case "budget", "show":
			q := strings.TrimSpace(*query)
			if q == "" && len(fs.Args()) > 0 {
				q = strings.TrimSpace(strings.Join(fs.Args(), " "))
			}
			v := url.Values{}
			if q != "" {
				v.Set("q", q)
			}
			if p := strings.TrimSpace(*runtimeProvider); p != "" {
				v.Set("runtime_provider", p)
			}
			if m := strings.TrimSpace(*runtimeModel); m != "" {
				v.Set("runtime_model", m)
			}
			if ts := strings.TrimSpace(*runtimeToolStyle); ts != "" {
				v.Set("runtime_tool_style", ts)
			}
			if *runtimeMaxContext > 0 {
				v.Set("runtime_max_context", strconv.Itoa(*runtimeMaxContext))
			}
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/context/budget"
			if encoded := v.Encode(); encoded != "" {
				endpoint += "?" + encoded
			}
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote context budget error: %v\n", err)
				return 1
			}
			mustPrintJSON(payload)
			return 0
		case "recommend", "recommendations":
			q := strings.TrimSpace(*query)
			if q == "" && len(fs.Args()) > 0 {
				q = strings.TrimSpace(strings.Join(fs.Args(), " "))
			}
			v := url.Values{}
			if q != "" {
				v.Set("q", q)
			}
			if p := strings.TrimSpace(*runtimeProvider); p != "" {
				v.Set("runtime_provider", p)
			}
			if m := strings.TrimSpace(*runtimeModel); m != "" {
				v.Set("runtime_model", m)
			}
			if ts := strings.TrimSpace(*runtimeToolStyle); ts != "" {
				v.Set("runtime_tool_style", ts)
			}
			if *runtimeMaxContext > 0 {
				v.Set("runtime_max_context", strconv.Itoa(*runtimeMaxContext))
			}
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/context/recommend"
			if encoded := v.Encode(); encoded != "" {
				endpoint += "?" + encoded
			}
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote context recommend error: %v\n", err)
				return 1
			}
			mustPrintJSON(payload)
			return 0
		case "brief":
			v := url.Values{}
			if *maxWords > 0 {
				v.Set("max_words", strconv.Itoa(*maxWords))
			}
			if p := strings.TrimSpace(*briefPath); p != "" {
				v.Set("path", p)
			}
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/context/brief"
			if encoded := v.Encode(); encoded != "" {
				endpoint += "?" + encoded
			}
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote context brief error: %v\n", err)
				return 1
			}
			mustPrintJSON(payload)
			return 0
		default:
			fmt.Fprintln(os.Stderr, "usage: dfmc remote context [budget --query \"...\" --runtime-tool-style ... --runtime-max-context ...]|[recommend --query \"...\" --runtime-tool-style ... --runtime-max-context ...]|[brief --max-words 240 --path ...] [--url ...] [--token ...]")
			return 2
		}

	case "files":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: dfmc remote files [list|get <path>] [--url ...] [--token ...]")
			return 2
		}
		action := strings.ToLower(strings.TrimSpace(args[1]))
		fs := flag.NewFlagSet("remote files", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := addRemoteTokenFlag(fs)
		timeout := fs.Duration("timeout", 30*time.Second, "request timeout")
		limit := fs.Int("limit", 500, "max files for list")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}

		switch action {
		case "list":
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/files?limit=" + strconv.Itoa(*limit)
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote files list error: %v\n", err)
				return 1
			}
			if jsonMode {
				mustPrintJSON(payload)
				return 0
			}
			root := strings.TrimSpace(fmt.Sprint(payload["root"]))
			if root != "" {
				fmt.Printf("Root: %s\n", root)
			}
			items, _ := payload["files"].([]any)
			for _, it := range items {
				fmt.Println(fmt.Sprint(it))
			}
			return 0
		case "get":
			var rel string
			if len(fs.Args()) > 0 {
				rel = strings.TrimSpace(fs.Args()[0])
			}
			if rel == "" {
				fmt.Fprintln(os.Stderr, "usage: dfmc remote files get <path> [--url ...] [--token ...]")
				return 2
			}
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/files/" + remotePathEscape(rel)
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote files get error: %v\n", err)
				return 1
			}
			if jsonMode {
				mustPrintJSON(payload)
				return 0
			}
			if typ := strings.TrimSpace(fmt.Sprint(payload["type"])); typ == "file" {
				fmt.Println(fmt.Sprint(payload["content"]))
				return 0
			}
			mustPrintJSON(payload)
			return 0
		default:
			fmt.Fprintln(os.Stderr, "usage: dfmc remote files [list|get <path>] [--url ...] [--token ...]")
			return 2
		}

	case "memory":
		action := "working"
		if len(args) >= 2 {
			action = strings.ToLower(strings.TrimSpace(args[1]))
		}
		fs := flag.NewFlagSet("remote memory", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := addRemoteTokenFlag(fs)
		timeout := fs.Duration("timeout", 20*time.Second, "request timeout")
		tier := fs.String("tier", "episodic", "working|episodic|semantic")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}

		switch action {
		case "working":
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/memory?tier=working"
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote memory working error: %v\n", err)
				return 1
			}
			if jsonMode {
				mustPrintJSON(payload)
			} else {
				mustPrintJSON(payload)
			}
			return 0
		case "list":
			v := url.Values{}
			v.Set("tier", strings.ToLower(strings.TrimSpace(*tier)))
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/memory?" + v.Encode()
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote memory list error: %v\n", err)
				return 1
			}
			if jsonMode {
				mustPrintJSON(payload)
				return 0
			}
			mustPrintJSON(payload)
			return 0
		default:
			fmt.Fprintln(os.Stderr, "usage: dfmc remote memory [working|list --tier episodic|semantic] [--url ...] [--token ...]")
			return 2
		}

	case "conversation":
		action := "list"
		if len(args) >= 2 {
			action = strings.ToLower(strings.TrimSpace(args[1]))
		}
		branchAction := "list"
		parseFrom := 2
		if action == "branch" && len(args) >= 3 {
			branchAction = strings.ToLower(strings.TrimSpace(args[2]))
			parseFrom = 3
		}
		fs := flag.NewFlagSet("remote conversation", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := addRemoteTokenFlag(fs)
		timeout := fs.Duration("timeout", 20*time.Second, "request timeout")
		limit := fs.Int("limit", 20, "max results")
		query := fs.String("query", "", "search query")
		id := fs.String("id", "", "conversation id")
		name := fs.String("name", "", "branch name")
		branchA := fs.String("a", "", "branch A name")
		branchB := fs.String("b", "", "branch B name")
		if err := fs.Parse(args[parseFrom:]); err != nil {
			return 2
		}

		switch action {
		case "list":
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversations?limit=" + strconv.Itoa(*limit)
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote conversation list error: %v\n", err)
				return 1
			}
			mustPrintJSON(payload)
			return 0
		case "search":
			q := strings.TrimSpace(*query)
			if q == "" && len(fs.Args()) > 0 {
				q = strings.TrimSpace(strings.Join(fs.Args(), " "))
			}
			v := url.Values{}
			v.Set("q", q)
			v.Set("limit", strconv.Itoa(*limit))
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversations/search?" + v.Encode()
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote conversation search error: %v\n", err)
				return 1
			}
			mustPrintJSON(payload)
			return 0
		case "active":
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation"
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote conversation active error: %v\n", err)
				return 1
			}
			mustPrintJSON(payload)
			return 0
		case "new", "clear":
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation/new"
			payload, _, err := remoteJSONRequest(http.MethodPost, endpoint, *token, map[string]any{}, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote conversation new error: %v\n", err)
				return 1
			}
			mustPrintJSON(payload)
			return 0
		case "save":
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation/save"
			payload, _, err := remoteJSONRequest(http.MethodPost, endpoint, *token, map[string]any{}, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote conversation save error: %v\n", err)
				return 1
			}
			mustPrintJSON(payload)
			return 0
		case "load":
			convID := strings.TrimSpace(*id)
			if convID == "" && len(fs.Args()) > 0 {
				convID = strings.TrimSpace(fs.Args()[0])
			}
			if convID == "" {
				fmt.Fprintln(os.Stderr, "usage: dfmc remote conversation load --id <conversation-id>")
				return 2
			}
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation/load"
			payload, _, err := remoteJSONRequest(http.MethodPost, endpoint, *token, map[string]any{"id": convID}, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote conversation load error: %v\n", err)
				return 1
			}
			mustPrintJSON(payload)
			return 0
		case "branch":
			switch branchAction {
			case "list":
				endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation/branches"
				payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
				if err != nil {
					fmt.Fprintf(os.Stderr, "remote conversation branch list error: %v\n", err)
					return 1
				}
				mustPrintJSON(payload)
				return 0
			case "create":
				branchName := strings.TrimSpace(*name)
				if branchName == "" && len(fs.Args()) > 0 {
					branchName = strings.TrimSpace(fs.Args()[0])
				}
				if branchName == "" {
					fmt.Fprintln(os.Stderr, "usage: dfmc remote conversation branch create --name <branch>")
					return 2
				}
				endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation/branches/create"
				payload, _, err := remoteJSONRequest(http.MethodPost, endpoint, *token, map[string]any{"name": branchName}, *timeout)
				if err != nil {
					fmt.Fprintf(os.Stderr, "remote conversation branch create error: %v\n", err)
					return 1
				}
				mustPrintJSON(payload)
				return 0
			case "switch":
				branchName := strings.TrimSpace(*name)
				if branchName == "" && len(fs.Args()) > 0 {
					branchName = strings.TrimSpace(fs.Args()[0])
				}
				if branchName == "" {
					fmt.Fprintln(os.Stderr, "usage: dfmc remote conversation branch switch --name <branch>")
					return 2
				}
				endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation/branches/switch"
				payload, _, err := remoteJSONRequest(http.MethodPost, endpoint, *token, map[string]any{"name": branchName}, *timeout)
				if err != nil {
					fmt.Fprintf(os.Stderr, "remote conversation branch switch error: %v\n", err)
					return 1
				}
				mustPrintJSON(payload)
				return 0
			case "compare":
				a := strings.TrimSpace(*branchA)
				b := strings.TrimSpace(*branchB)
				if a == "" && len(fs.Args()) >= 1 {
					a = strings.TrimSpace(fs.Args()[0])
				}
				if b == "" && len(fs.Args()) >= 2 {
					b = strings.TrimSpace(fs.Args()[1])
				}
				if a == "" || b == "" {
					fmt.Fprintln(os.Stderr, "usage: dfmc remote conversation branch compare --a <branch-a> --b <branch-b>")
					return 2
				}
				v := url.Values{}
				v.Set("a", a)
				v.Set("b", b)
				endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/conversation/branches/compare?" + v.Encode()
				payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
				if err != nil {
					fmt.Fprintf(os.Stderr, "remote conversation branch compare error: %v\n", err)
					return 1
				}
				mustPrintJSON(payload)
				return 0
			default:
				fmt.Fprintln(os.Stderr, "usage: dfmc remote conversation branch [list|create|switch|compare]")
				return 2
			}
		default:
			fmt.Fprintln(os.Stderr, "usage: dfmc remote conversation [list|search|active|new|save|load|branch ...] [--url ...] [--token ...]")
			return 2
		}

	case "codemap":
		fs := flag.NewFlagSet("remote codemap", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := addRemoteTokenFlag(fs)
		timeout := fs.Duration("timeout", 30*time.Second, "request timeout")
		format := fs.String("format", "json", "json|dot|svg|ascii")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/codemap"
		payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote codemap error: %v\n", err)
			return 1
		}
		nodes, edges, err := decodeCodemapPayload(payload)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote codemap decode error: %v\n", err)
			return 1
		}
		f := strings.ToLower(strings.TrimSpace(*format))
		if jsonMode || f == "json" {
			mustPrintJSON(map[string]any{"nodes": nodes, "edges": edges})
			return 0
		}
		switch f {
		case "dot":
			fmt.Println(graphToDOT(nodes, edges))
		case "svg":
			fmt.Println(graphToSVG(nodes, edges))
		default:
			for _, e := range edges {
				fmt.Printf("%s -> %s (%s)\n", e.From, e.To, e.Type)
			}
		}
		return 0

	case "tools":
		fs := flag.NewFlagSet("remote tools", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := addRemoteTokenFlag(fs)
		timeout := fs.Duration("timeout", 20*time.Second, "request timeout")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/tools"
		payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote tools error: %v\n", err)
			return 1
		}
		mustPrintJSON(payload)
		return 0

	case "skills":
		fs := flag.NewFlagSet("remote skills", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := addRemoteTokenFlag(fs)
		timeout := fs.Duration("timeout", 20*time.Second, "request timeout")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/skills"
		payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote skills error: %v\n", err)
			return 1
		}
		mustPrintJSON(payload)
		return 0

	case "prompt":
		action := "list"
		parseFrom := 1
		if len(args) >= 2 {
			candidate := strings.ToLower(strings.TrimSpace(args[1]))
			if !strings.HasPrefix(candidate, "-") {
				action = candidate
				parseFrom = 2
			}
		}
		fs := flag.NewFlagSet("remote prompt", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := addRemoteTokenFlag(fs)
		timeout := fs.Duration("timeout", 20*time.Second, "request timeout")
		typ := fs.String("type", "system", "prompt type")
		task := fs.String("task", "auto", "prompt task")
		language := fs.String("language", "auto", "prompt language")
		profile := fs.String("profile", "auto", "prompt profile")
		role := fs.String("role", "auto", "prompt role")
		query := fs.String("query", "", "user query")
		contextFiles := fs.String("context-files", "(none)", "context file summary")
		runtimeProvider := fs.String("runtime-provider", "", "runtime provider override for tool policy rendering")
		runtimeModel := fs.String("runtime-model", "", "runtime model override for tool policy rendering")
		runtimeToolStyle := fs.String("runtime-tool-style", "", "runtime tool style override (function-calling|tool_use|none|provider-native)")
		runtimeMaxContext := fs.Int("runtime-max-context", 0, "runtime max context override for tool policy rendering")
		maxTemplateTokens := fs.Int("max-template-tokens", 450, "warning threshold for per-template token estimate")
		failOnWarning := fs.Bool("fail-on-warning", false, "exit with non-zero status if warnings are found")
		var allowVar multiStringFlag
		fs.Var(&allowVar, "allow-var", "extra placeholder variable to allow (repeatable)")
		var varsRaw multiStringFlag
		fs.Var(&varsRaw, "var", "prompt var key=value (repeatable)")
		if err := fs.Parse(args[parseFrom:]); err != nil {
			return 2
		}

		switch action {
		case "list":
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/prompts"
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote prompt list error: %v\n", err)
				return 1
			}
			mustPrintJSON(payload)
			return 0
		case "render":
			extraVars, err := parsePromptVars(varsRaw)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote prompt var parse error: %v\n", err)
				return 2
			}
			payload, _, err := remoteJSONRequest(
				http.MethodPost,
				strings.TrimRight(strings.TrimSpace(*baseURL), "/")+"/api/v1/prompts/render",
				*token,
				map[string]any{
					"type":                *typ,
					"task":                *task,
					"language":            *language,
					"profile":             *profile,
					"role":                *role,
					"query":               *query,
					"context_files":       *contextFiles,
					"runtime_provider":    strings.TrimSpace(*runtimeProvider),
					"runtime_model":       strings.TrimSpace(*runtimeModel),
					"runtime_tool_style":  strings.TrimSpace(*runtimeToolStyle),
					"runtime_max_context": *runtimeMaxContext,
					"vars":                extraVars,
				},
				*timeout,
			)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote prompt render error: %v\n", err)
				return 1
			}
			mustPrintJSON(payload)
			return 0
		case "recommend", "recommendation", "tune":
			v := url.Values{}
			if p := strings.TrimSpace(*query); p != "" {
				v.Set("q", p)
			}
			if p := strings.TrimSpace(*runtimeProvider); p != "" {
				v.Set("runtime_provider", p)
			}
			if m := strings.TrimSpace(*runtimeModel); m != "" {
				v.Set("runtime_model", m)
			}
			if ts := strings.TrimSpace(*runtimeToolStyle); ts != "" {
				v.Set("runtime_tool_style", ts)
			}
			if *runtimeMaxContext > 0 {
				v.Set("runtime_max_context", strconv.Itoa(*runtimeMaxContext))
			}
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/prompts/recommend"
			if encoded := v.Encode(); encoded != "" {
				endpoint += "?" + encoded
			}
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote prompt recommend error: %v\n", err)
				return 1
			}
			mustPrintJSON(payload)
			return 0
		case "stats", "validate", "lint":
			v := url.Values{}
			if *maxTemplateTokens > 0 {
				v.Set("max_template_tokens", strconv.Itoa(*maxTemplateTokens))
			}
			for _, raw := range allowVar {
				if p := strings.TrimSpace(raw); p != "" {
					v.Add("allow_var", p)
				}
			}
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/prompts/stats"
			if encoded := v.Encode(); encoded != "" {
				endpoint += "?" + encoded
			}
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote prompt stats error: %v\n", err)
				return 1
			}
			mustPrintJSON(payload)
			if *failOnWarning {
				if n, ok := payload["warning_count"].(float64); ok && n > 0 {
					return 1
				}
			}
			return 0
		default:
			fmt.Fprintln(os.Stderr, "usage: dfmc remote prompt [list|render --query ... --runtime-tool-style ... --runtime-max-context ...|recommend --query ... --runtime-tool-style ... --runtime-max-context ...|stats --max-template-tokens 450]")
			return 2
		}

	case "magicdoc":
		action := "show"
		if len(args) >= 2 {
			action = strings.ToLower(strings.TrimSpace(args[1]))
		}
		fs := flag.NewFlagSet("remote magicdoc", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
		baseURL := fs.String("url", defaultURL, "remote base URL")
		token := addRemoteTokenFlag(fs)
		timeout := fs.Duration("timeout", 30*time.Second, "request timeout")
		path := fs.String("path", "", "target magic doc path")
		title := fs.String("title", "DFMC Project Brief", "magic doc title")
		hotspots := fs.Int("hotspots", 8, "max hotspot entries")
		deps := fs.Int("deps", 8, "max dependency entries")
		recent := fs.Int("recent", 5, "max recent entries")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}

		switch action {
		case "show", "cat":
			endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/magicdoc"
			if p := strings.TrimSpace(*path); p != "" {
				v := url.Values{}
				v.Set("path", p)
				endpoint += "?" + v.Encode()
			}
			payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote magicdoc show error: %v\n", err)
				return 1
			}
			mustPrintJSON(payload)
			return 0
		case "update", "sync", "generate":
			payload, _, err := remoteJSONRequest(
				http.MethodPost,
				strings.TrimRight(strings.TrimSpace(*baseURL), "/")+"/api/v1/magicdoc/update",
				*token,
				map[string]any{
					"path":     strings.TrimSpace(*path),
					"title":    strings.TrimSpace(*title),
					"hotspots": *hotspots,
					"deps":     *deps,
					"recent":   *recent,
				},
				*timeout,
			)
			if err != nil {
				fmt.Fprintf(os.Stderr, "remote magicdoc update error: %v\n", err)
				return 1
			}
			mustPrintJSON(payload)
			return 0
		default:
			fmt.Fprintln(os.Stderr, "usage: dfmc remote magicdoc [show|update] [--path ...] [--title ...]")
			return 2
		}

	case "start":
		fs := flag.NewFlagSet("remote start", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		host := fs.String("host", eng.Config.Web.Host, "bind host")
		grpcPort := fs.Int("grpc-port", eng.Config.Remote.GRPCPort, "bind grpc port")
		port := fs.Int("ws-port", eng.Config.Remote.WSPort, "bind ws/http port")
		auth := fs.String("auth", eng.Config.Remote.Auth, "none|token")
		token := addRemoteTokenFlag(fs)
		// Parallel to runServe: --insecure acknowledges the risk of
		// serving without auth on a non-loopback host. Remote defaults
		// to auth=token so this is mostly a safety net for users who
		// explicitly pass --auth=none for a one-off localhost test and
		// then reuse the command with a wider --host.
		insecure := fs.Bool("insecure", false, "allow --auth=none on non-loopback hosts (exposes tools/files to the network)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}

		mode := strings.ToLower(strings.TrimSpace(*auth))
		if mode != "none" && mode != "token" {
			fmt.Fprintln(os.Stderr, "remote auth must be none|token")
			return 2
		}
		if mode == "token" && strings.TrimSpace(*token) == "" {
			fmt.Fprintln(os.Stderr, "remote token auth requires --token or DFMC_REMOTE_TOKEN")
			return 2
		}
		if mode == "none" && !isLoopbackBindHost(*host) && !*insecure {
			fmt.Fprintf(os.Stderr,
				"refusing to start remote server with --auth=none on non-loopback host %q: the API exposes file/tool/shell endpoints. "+
					"Pass --auth=token (with --token or DFMC_REMOTE_TOKEN) to require a bearer token, or add --insecure to accept the risk explicitly.\n",
				*host)
			return 2
		}
		if mode == "none" && !isLoopbackBindHost(*host) && *insecure {
			fmt.Fprintf(os.Stderr,
				"WARNING: --auth=none on non-loopback host %q — all API endpoints (file read/write, tool invocation, shell) are reachable without authentication.\n",
				*host)
		}

		base := web.New(eng, *host, *port)
		base.SetBearerToken(*token)
		handler := base.Handler()
		if mode == "token" {
			handler = bearerTokenMiddleware(handler, *token)
		}

		addr := fmt.Sprintf("%s:%d", *host, *port)
		server := web.NewHTTPServer(addr, handler)

		if jsonMode {
			_ = printJSON(map[string]any{
				"status":    "starting",
				"mode":      "remote",
				"host":      *host,
				"grpc_port": *grpcPort,
				"ws_port":   *port,
				"auth":      mode,
				"healthz":   fmt.Sprintf("http://%s/healthz", addr),
				"base_api":  fmt.Sprintf("http://%s/api/v1", addr),
				"grpc":      "not_started",
			})
		} else {
			fmt.Printf("DFMC remote server listening on http://%s\n", addr)
			fmt.Printf("gRPC port (reserved): %d\n", *grpcPort)
			fmt.Printf("Auth: %s\n", mode)
		}

		if err := serveWithContext(ctx, server); err != nil {
			fmt.Fprintf(os.Stderr, "remote server error: %v\n", err)
			return 1
		}
		return 0

	case "drive":
		return runRemoteDrive(eng, args[1:], jsonMode)

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc remote [status|probe|events|ask|tool|tools|skill|skills|prompt|magicdoc|analyze|context|files|memory|conversation (list/search/active/new/save/load/branch)|codemap|drive (start/list/show/resume/delete)|start --host 127.0.0.1 --ws-port 7779 --auth none|token]")
		return 2
	}
}

type remoteProbeResult struct {
	Endpoint   string `json:"endpoint"`
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code"`
	DurationMs int64  `json:"duration_ms"`
	Body       string `json:"body,omitempty"`
	Error      string `json:"error,omitempty"`
}

func parseEndpointList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return []string{"/healthz"}
	}
	return out
}

func probeRemoteEndpoint(client *http.Client, baseURL, endpoint, token string) remoteProbeResult {
	start := time.Now()
	res := remoteProbeResult{Endpoint: endpoint}

	url := strings.TrimRight(strings.TrimSpace(baseURL), "/") + endpoint
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		res.Error = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	if tok := strings.TrimSpace(token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := client.Do(req)
	if err != nil {
		res.Error = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	res.StatusCode = resp.StatusCode
	res.Body = strings.TrimSpace(string(body))
	res.OK = resp.StatusCode >= 200 && resp.StatusCode < 300
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}

type remoteChatEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta,omitempty"`
	Error string `json:"error,omitempty"`
}

func remoteAsk(baseURL, token, message string, timeout time.Duration, streamOutput bool) ([]remoteChatEvent, string, error) {
	payload, err := json.Marshal(map[string]string{"message": message})
	if err != nil {
		return nil, "", err
	}
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/api/v1/chat"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if tok := strings.TrimSpace(token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", fmt.Errorf("remote returned %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	events := make([]remoteChatEvent, 0, 64)
	var answer strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		ev, ok, err := parseSSEDataLine(scanner.Text())
		if err != nil {
			return events, answer.String(), err
		}
		if !ok {
			continue
		}
		events = append(events, ev)
		switch ev.Type {
		case "delta":
			answer.WriteString(ev.Delta)
			if streamOutput {
				fmt.Print(ev.Delta)
			}
		case "error":
			msg := strings.TrimSpace(ev.Error)
			if msg == "" {
				msg = "remote stream error"
			}
			return events, answer.String(), errors.New(msg)
		case "done":
			return events, answer.String(), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return events, answer.String(), err
	}
	return events, answer.String(), nil
}

func parseSSEDataLine(line string) (remoteChatEvent, bool, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return remoteChatEvent{}, false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" {
		return remoteChatEvent{}, false, nil
	}
	var ev remoteChatEvent
	if err := json.Unmarshal([]byte(payload), &ev); err != nil {
		return remoteChatEvent{}, true, fmt.Errorf("invalid sse json: %w", err)
	}
	return ev, true, nil
}

func parseSSEJSONLine(line string) (map[string]any, bool, error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return nil, false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" {
		return nil, false, nil
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		return nil, true, fmt.Errorf("invalid sse json: %w", err)
	}
	return out, true, nil
}

type multiStringFlag []string

func (m *multiStringFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiStringFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func parseKeyValueParams(items []string) (map[string]any, error) {
	out := map[string]any{}
	for _, raw := range items {
		parts := strings.SplitN(strings.TrimSpace(raw), "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid key=value: %s", raw)
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			return nil, fmt.Errorf("empty key in param: %s", raw)
		}
		val, err := parseConfigValue(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, err
		}
		out[key] = val
	}
	return out, nil
}

func parsePromptVars(items []string) (map[string]string, error) {
	out := map[string]string{}
	for _, raw := range items {
		parts := strings.SplitN(strings.TrimSpace(raw), "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid key=value: %s", raw)
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			return nil, fmt.Errorf("empty key in var: %s", raw)
		}
		out[key] = strings.TrimSpace(parts[1])
	}
	return out, nil
}

func remoteJSONRequest(method, endpoint, token string, payload any, timeout time.Duration) (map[string]any, int, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok := strings.TrimSpace(token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	out := map[string]any{}
	if len(strings.TrimSpace(string(rawBody))) > 0 {
		if err := json.Unmarshal(rawBody, &out); err != nil {
			return nil, resp.StatusCode, fmt.Errorf("invalid json response (%d): %s", resp.StatusCode, strings.TrimSpace(string(rawBody)))
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if msg, ok := out["error"]; ok {
			return out, resp.StatusCode, fmt.Errorf("remote returned %s: %v", resp.Status, msg)
		}
		return out, resp.StatusCode, fmt.Errorf("remote returned %s", resp.Status)
	}
	return out, resp.StatusCode, nil
}

func remoteCollectEvents(endpoint, token string, timeout time.Duration, maxEvents int) ([]map[string]any, error) {
	if maxEvents <= 0 {
		maxEvents = 100
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if tok := strings.TrimSpace(token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("remote returned %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	capHint := maxEvents
	if capHint > 32 {
		capHint = 32
	}
	events := make([]map[string]any, 0, capHint)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		ev, ok, err := parseSSEJSONLine(scanner.Text())
		if err != nil {
			return events, err
		}
		if !ok {
			continue
		}
		events = append(events, ev)
		if len(events) >= maxEvents {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return events, err
	}
	return events, nil
}

func compactJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

func decodeCodemapPayload(payload map[string]any) ([]codemap.Node, []codemap.Edge, error) {
	nodes := []codemap.Node{}
	edges := []codemap.Edge{}

	nodesRaw, ok := payload["nodes"]
	if !ok {
		return nodes, edges, fmt.Errorf("missing nodes field")
	}
	edgesRaw, ok := payload["edges"]
	if !ok {
		return nodes, edges, fmt.Errorf("missing edges field")
	}
	nb, err := json.Marshal(nodesRaw)
	if err != nil {
		return nodes, edges, err
	}
	if err := json.Unmarshal(nb, &nodes); err != nil {
		return nodes, edges, err
	}
	eb, err := json.Marshal(edgesRaw)
	if err != nil {
		return nodes, edges, err
	}
	if err := json.Unmarshal(eb, &edges); err != nil {
		return nodes, edges, err
	}
	return nodes, edges, nil
}

func remotePathEscape(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func serveWithContext(ctx context.Context, server *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// isLoopbackBindHost reports whether a `dfmc serve --host` value binds
// only to the local machine. Loopback binds are safe to leave without
// auth — nothing off-box can connect. Anything else (0.0.0.0, "", a
// LAN/public IP) reaches further than the user's own machine and is
// treated as network-exposed for the auth guard in runServe.
//
// Empty host is treated as non-loopback because Go's net package binds
// that to all interfaces, exactly like "0.0.0.0".
func isLoopbackBindHost(host string) bool {
	h := strings.TrimSpace(host)
	// Strip optional brackets around IPv6 literals like "[::1]".
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	h = strings.ToLower(h)
	switch h {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	// A parseable IP covers oddities like "127.0.0.2" or "::ffff:127.0.0.1".
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func bearerTokenMiddleware(next http.Handler, token string) http.Handler {
	rawToken := strings.TrimSpace(token)
	expected := "Bearer " + rawToken
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			writeRemoteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
			return
		}
		// The workbench HTML at GET / is the entry shell — it contains no
		// secrets and the operator needs to load it to enter their token in
		// the browser. Gating it would create a chicken-and-egg lockout.
		// Every API path under /api/v1/ and /ws still requires the token.
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			next.ServeHTTP(w, r)
			return
		}
		// Accept the token via Authorization header everywhere. A query-
		// param fallback is allowed ONLY for /ws because EventSource
		// cannot set custom headers.
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got == expected {
			next.ServeHTTP(w, r)
			return
		}
		if rawToken != "" && r.URL.Path == "/ws" && r.URL.Query().Get("token") == rawToken {
			next.ServeHTTP(w, r)
			return
		}
		writeRemoteJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
	})
}

func writeRemoteJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

// Remote and serve subcommands: serve (embedded HTTP+SSE server),
// remote (client against a running serve). Extracted from cli.go so the
// dispatcher stays focused. These commands share SSE parsing, bearer
// middleware, remote JSON-request helpers, and codemap payload decoding
// so they travel together.

package cli

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

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
		return remoteConversation(eng, args[1:], jsonMode)

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
		return remotePrompt(eng, args[1:], jsonMode)

	case "magicdoc":
		return remoteMagicdoc(eng, args[1:], jsonMode)

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


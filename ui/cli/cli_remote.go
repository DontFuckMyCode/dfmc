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
		return remoteStatus(eng, args[1:], jsonMode)

	case "probe":
		return remoteProbe(eng, args[1:], jsonMode)

	case "events":
		return remoteEvents(eng, args[1:], jsonMode)

	case "ask":
		return remoteAskCmd(eng, args[1:], jsonMode)

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
		return remoteContext(eng, args[1:], jsonMode)

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
		return remoteStart(ctx, eng, args[1:], jsonMode)

	case "drive":
		return runRemoteDrive(eng, args[1:], jsonMode)

	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc remote [status|probe|events|ask|tool|tools|skill|skills|prompt|magicdoc|analyze|context|files|memory|conversation (list/search/active/new/save/load/branch)|codemap|drive (start/list/show/resume/delete)|start --host 127.0.0.1 --ws-port 7779 --auth none|token]")
		return 2
	}
}


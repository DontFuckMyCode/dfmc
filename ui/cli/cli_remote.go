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
	"os"
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
		return remoteTool(eng, args[1:], jsonMode)

	case "skill":
		return remoteSkill(eng, args[1:], jsonMode)

	case "analyze":
		return remoteAnalyze(eng, args[1:], jsonMode)

	case "context":
		return remoteContext(eng, args[1:], jsonMode)

	case "files":
		return remoteFiles(eng, args[1:], jsonMode)

	case "memory":
		return remoteMemory(eng, args[1:], jsonMode)

	case "conversation":
		return remoteConversation(eng, args[1:], jsonMode)

	case "codemap":
		return remoteCodemap(eng, args[1:], jsonMode)

	case "tools":
		return remoteTools(eng, args[1:], jsonMode)

	case "skills":
		return remoteSkills(eng, args[1:], jsonMode)

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


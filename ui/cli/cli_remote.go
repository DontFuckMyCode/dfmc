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
// run a serve and a client side-by-side with separate credentials.
//
// Centralising the flag declaration keeps remote clients from drifting when
// the env var or flag description changes.
func addRemoteTokenFlag(fs *flag.FlagSet) *string {
	return fs.String("token", strings.TrimSpace(os.Getenv("DFMC_REMOTE_TOKEN")), "remote token")
}

func addServeTokenFlag(fs *flag.FlagSet) *string {
	return fs.String("token", strings.TrimSpace(os.Getenv("DFMC_WEB_TOKEN")), "api token (for auth=token)")
}

func remoteDefaultURL(eng *engine.Engine) string {
	return fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
}

func runServe(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	host := fs.String("host", eng.Config.Web.Host, "host")
	port := fs.Int("port", eng.Config.Web.Port, "port")
	auth := fs.String("auth", eng.Config.Web.Auth, "none|token")
	token := addServeTokenFlag(fs)
	openBrowser := fs.Bool("open-browser", eng.Config.Web.OpenBrowser, "open default browser")
	// --insecure is the explicit opt-out for the non-loopback-without-auth
	// guard below. Without it, a LAN bind with --auth=none exposes tool/file
	// endpoints unauthenticated.
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
			"WARNING: --auth=none on non-loopback host %q - all API endpoints (file read/write, tool invocation, shell) are reachable without authentication. Anyone on the network can drive this process.\n",
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
	defer func() { _ = srv.Close() }()
	if mode == "token" {
		handler = bearerTokenMiddleware(handler, *token)
	}

	addr := fmt.Sprintf("%s:%d", *host, *port)
	server := web.NewHTTPServer(addr, handler)
	fmt.Printf("DFMC Web API listening on http://%s\n", addr)
	if *openBrowser {
		target := "http://" + addr
		go func() {
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
	if code, ok := dispatchRemoteCommand(ctx, eng, args[0], args[1:], jsonMode); ok {
		return code
	}
	fmt.Fprintln(os.Stderr, remoteUsage())
	return 2
}

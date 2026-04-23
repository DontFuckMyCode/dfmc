// Remote server start: `dfmc remote start` spins up the embedded
// HTTP+SSE server with optional bearer-token auth. Extracted from
// cli_remote.go. args slice here is the tail after "start".

package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/ui/web"
)

func remoteStart(ctx context.Context, eng *engine.Engine, args []string, jsonMode bool) int {
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
	if err := fs.Parse(args); err != nil {
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
}

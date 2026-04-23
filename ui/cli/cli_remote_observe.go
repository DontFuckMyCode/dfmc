// Remote observation subcommands: `dfmc remote status` (config or live
// server snapshot), `probe` (endpoint reachability), `events` (SSE
// event stream sampler), and `ask` (one-shot chat round-trip).
// Extracted from cli_remote.go. args slice here is the tail after the
// top-level subcommand name.

package cli

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func remoteStatus(eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("remote status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
	live := fs.Bool("live", false, "query remote server status instead of local config")
	baseURL := fs.String("url", defaultURL, "remote base URL (for --live)")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 10*time.Second, "request timeout")
	if err := fs.Parse(args); err != nil {
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
	_ = jsonMode
	mustPrintJSON(out)
	return 0
}

func remoteProbe(eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("remote probe", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 3*time.Second, "request timeout")
	endpointsRaw := fs.String("endpoints", "/healthz,/api/v1/status,/api/v1/providers", "comma-separated endpoint paths")
	if err := fs.Parse(args); err != nil {
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
}

func remoteEvents(eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("remote events", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	eventType := fs.String("type", "*", "event type filter")
	timeout := fs.Duration("timeout", 20*time.Second, "stream timeout")
	maxEvents := fs.Int("max", 100, "max events to collect before stopping")
	if err := fs.Parse(args); err != nil {
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
}

func remoteAskCmd(eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("remote ask", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 60*time.Second, "request timeout")
	message := fs.String("message", "", "question/message to send")
	if err := fs.Parse(args); err != nil {
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
}

// Remote context client: `dfmc remote context [budget|recommend|brief]`
// wraps the /api/v1/context/* endpoints. Extracted from cli_remote.go.
// args slice here is the tail after "context".

package cli

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func remoteContext(eng *engine.Engine, args []string, jsonMode bool) int {
	action := "budget"
	parseFrom := 0
	if len(args) >= 1 {
		candidate := strings.ToLower(strings.TrimSpace(args[0]))
		if !strings.HasPrefix(candidate, "-") {
			action = candidate
			parseFrom = 1
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
	_ = jsonMode

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
}

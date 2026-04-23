// Remote prompt client: `dfmc remote prompt [list|render|recommend|stats]`
// wraps the /api/v1/prompts* endpoints. Extracted from cli_remote.go.
// args slice here is the tail after "prompt".

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

func remotePrompt(eng *engine.Engine, args []string, jsonMode bool) int {
	action := "list"
	parseFrom := 0
	if len(args) >= 1 {
		candidate := strings.ToLower(strings.TrimSpace(args[0]))
		if !strings.HasPrefix(candidate, "-") {
			action = candidate
			parseFrom = 1
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
	_ = jsonMode

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
}

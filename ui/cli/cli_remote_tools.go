// Remote tool/skill subcommands: `dfmc remote tool` and `skill` invoke
// a named tool/skill on the remote, while `tools` and `skills` list
// what's registered. Extracted from cli_remote.go. args slice here is
// the tail after the top-level subcommand name.

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

func remoteTool(eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: dfmc remote tool <name> [--url ...] [--token ...] [--param key=value]")
		return 2
	}
	name := strings.TrimSpace(args[0])
	fs := flag.NewFlagSet("remote tool", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 30*time.Second, "request timeout")
	var paramsRaw multiStringFlag
	fs.Var(&paramsRaw, "param", "tool param in key=value format (repeatable)")
	if err := fs.Parse(args[1:]); err != nil {
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
}

func remoteSkill(eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: dfmc remote skill <name> [--url ...] [--token ...] [--input ...]")
		return 2
	}
	name := strings.TrimSpace(args[0])
	fs := flag.NewFlagSet("remote skill", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 60*time.Second, "request timeout")
	input := fs.String("input", "", "skill input text")
	if err := fs.Parse(args[1:]); err != nil {
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
}

func remoteTools(eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("remote tools", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 20*time.Second, "request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/tools"
	payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote tools error: %v\n", err)
		return 1
	}
	_ = jsonMode
	mustPrintJSON(payload)
	return 0
}

func remoteSkills(eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("remote skills", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 20*time.Second, "request timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/skills"
	payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote skills error: %v\n", err)
		return 1
	}
	_ = jsonMode
	mustPrintJSON(payload)
	return 0
}

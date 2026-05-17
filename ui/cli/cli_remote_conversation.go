// Remote conversation client: `dfmc remote conversation ...` subcommands
// that wrap the /api/v1/conversation(s) endpoints plus the nested branch
// subdispatcher. Extracted from cli_remote.go — args slice here is the
// tail after "conversation" (i.e. what the top-level dispatcher saw as
// args[1:]).

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

func remoteConversation(eng *engine.Engine, args []string, jsonMode bool) int {
	action := "list"
	if len(args) >= 1 {
		action = strings.ToLower(strings.TrimSpace(args[0]))
	}
	branchAction := "list"
	parseFrom := 1
	if action == "branch" && len(args) >= 2 {
		branchAction = strings.ToLower(strings.TrimSpace(args[1]))
		parseFrom = 2
	}
	fs := flag.NewFlagSet("remote conversation", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defaultURL := remoteDefaultURL(eng)
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
	_ = jsonMode

	switch action {
	case "list":
		v := url.Values{}
		v.Set("limit", strconv.Itoa(*limit))
		payload, _, err := remoteJSONEndpoint(http.MethodGet, *baseURL, "/api/v1/conversations", v, *token, nil, *timeout)
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
		payload, _, err := remoteJSONEndpoint(http.MethodGet, *baseURL, "/api/v1/conversations/search", v, *token, nil, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote conversation search error: %v\n", err)
			return 1
		}
		mustPrintJSON(payload)
		return 0
	case "active":
		payload, _, err := remoteJSONEndpoint(http.MethodGet, *baseURL, "/api/v1/conversation", nil, *token, nil, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote conversation active error: %v\n", err)
			return 1
		}
		mustPrintJSON(payload)
		return 0
	case "new", "clear":
		payload, _, err := remoteJSONEndpoint(http.MethodPost, *baseURL, "/api/v1/conversation/new", nil, *token, map[string]any{}, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote conversation new error: %v\n", err)
			return 1
		}
		mustPrintJSON(payload)
		return 0
	case "save":
		payload, _, err := remoteJSONEndpoint(http.MethodPost, *baseURL, "/api/v1/conversation/save", nil, *token, map[string]any{}, *timeout)
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
		payload, _, err := remoteJSONEndpoint(http.MethodPost, *baseURL, "/api/v1/conversation/load", nil, *token, map[string]any{"id": convID}, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote conversation load error: %v\n", err)
			return 1
		}
		mustPrintJSON(payload)
		return 0
	case "branch":
		switch branchAction {
		case "list":
			payload, _, err := remoteJSONEndpoint(http.MethodGet, *baseURL, "/api/v1/conversation/branches", nil, *token, nil, *timeout)
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
			payload, _, err := remoteJSONEndpoint(http.MethodPost, *baseURL, "/api/v1/conversation/branches/create", nil, *token, map[string]any{"name": branchName}, *timeout)
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
			payload, _, err := remoteJSONEndpoint(http.MethodPost, *baseURL, "/api/v1/conversation/branches/switch", nil, *token, map[string]any{"name": branchName}, *timeout)
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
			payload, _, err := remoteJSONEndpoint(http.MethodGet, *baseURL, "/api/v1/conversation/branches/compare", v, *token, nil, *timeout)
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
}

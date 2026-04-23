// Remote workspace subcommands: analyze the project (`analyze`), list
// or read files (`files`), inspect memory tiers (`memory`), and pull
// the codemap graph (`codemap`). Extracted from cli_remote.go. args
// slice here is the tail after the top-level subcommand name.

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

func remoteAnalyze(eng *engine.Engine, args []string, jsonMode bool) int {
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
	if err := fs.Parse(args); err != nil {
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
	_ = jsonMode
	mustPrintJSON(payload)
	return 0
}

func remoteFiles(eng *engine.Engine, args []string, jsonMode bool) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: dfmc remote files [list|get <path>] [--url ...] [--token ...]")
		return 2
	}
	action := strings.ToLower(strings.TrimSpace(args[0]))
	fs := flag.NewFlagSet("remote files", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 30*time.Second, "request timeout")
	limit := fs.Int("limit", 500, "max files for list")
	if err := fs.Parse(args[1:]); err != nil {
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
}

func remoteMemory(eng *engine.Engine, args []string, jsonMode bool) int {
	action := "working"
	parseFrom := 0
	if len(args) >= 1 {
		action = strings.ToLower(strings.TrimSpace(args[0]))
		parseFrom = 1
	}
	fs := flag.NewFlagSet("remote memory", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 20*time.Second, "request timeout")
	tier := fs.String("tier", "episodic", "working|episodic|semantic")
	if err := fs.Parse(args[parseFrom:]); err != nil {
		return 2
	}
	_ = jsonMode

	switch action {
	case "working":
		endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/memory?tier=working"
		payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote memory working error: %v\n", err)
			return 1
		}
		mustPrintJSON(payload)
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
		mustPrintJSON(payload)
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc remote memory [working|list --tier episodic|semantic] [--url ...] [--token ...]")
		return 2
	}
}

func remoteCodemap(eng *engine.Engine, args []string, jsonMode bool) int {
	fs := flag.NewFlagSet("remote codemap", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 30*time.Second, "request timeout")
	format := fs.String("format", "json", "json|dot|svg|ascii")
	if err := fs.Parse(args); err != nil {
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
}

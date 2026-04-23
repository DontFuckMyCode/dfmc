// Remote magicdoc client: `dfmc remote magicdoc [show|update]` wraps
// the /api/v1/magicdoc* endpoints. Extracted from cli_remote.go.
// args slice here is the tail after "magicdoc".

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

func remoteMagicdoc(eng *engine.Engine, args []string, jsonMode bool) int {
	action := "show"
	if len(args) >= 1 {
		action = strings.ToLower(strings.TrimSpace(args[0]))
	}
	fs := flag.NewFlagSet("remote magicdoc", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defaultURL := fmt.Sprintf("http://%s:%d", eng.Config.Web.Host, eng.Config.Remote.WSPort)
	baseURL := fs.String("url", defaultURL, "remote base URL")
	token := addRemoteTokenFlag(fs)
	timeout := fs.Duration("timeout", 30*time.Second, "request timeout")
	path := fs.String("path", "", "target magic doc path")
	title := fs.String("title", "DFMC Project Brief", "magic doc title")
	hotspots := fs.Int("hotspots", 8, "max hotspot entries")
	deps := fs.Int("deps", 8, "max dependency entries")
	recent := fs.Int("recent", 5, "max recent entries")
	parseFrom := 1
	if len(args) < 1 {
		parseFrom = 0
	}
	if err := fs.Parse(args[parseFrom:]); err != nil {
		return 2
	}
	_ = jsonMode

	switch action {
	case "show", "cat":
		endpoint := strings.TrimRight(strings.TrimSpace(*baseURL), "/") + "/api/v1/magicdoc"
		if p := strings.TrimSpace(*path); p != "" {
			v := url.Values{}
			v.Set("path", p)
			endpoint += "?" + v.Encode()
		}
		payload, _, err := remoteJSONRequest(http.MethodGet, endpoint, *token, nil, *timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote magicdoc show error: %v\n", err)
			return 1
		}
		mustPrintJSON(payload)
		return 0
	case "update", "sync", "generate":
		payload, _, err := remoteJSONRequest(
			http.MethodPost,
			strings.TrimRight(strings.TrimSpace(*baseURL), "/")+"/api/v1/magicdoc/update",
			*token,
			map[string]any{
				"path":     strings.TrimSpace(*path),
				"title":    strings.TrimSpace(*title),
				"hotspots": *hotspots,
				"deps":     *deps,
				"recent":   *recent,
			},
			*timeout,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote magicdoc update error: %v\n", err)
			return 1
		}
		mustPrintJSON(payload)
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: dfmc remote magicdoc [show|update] [--path ...] [--title ...]")
		return 2
	}
}

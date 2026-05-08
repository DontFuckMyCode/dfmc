package cli

// cli_update.go — `dfmc update` checks GitHub for a newer release and tells
// the user how to upgrade. It intentionally does NOT self-replace the binary:
//   * self-update is a rabbit hole (rollback, signing, privilege, A/V)
//   * `brew upgrade dfmc` and a fresh release download are already safe
//   * a check-only flow composes cleanly with CI and `dfmc doctor`
//
// The command works in the degraded-startup path (no engine, locked store)
// because upgrade guidance should remain available even when the project
// state is borked.

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultUpdateRepo  = "dontfuckmycode/dfmc"
	defaultUpdateHost  = "https://api.github.com"
	updateHTTPTimeout  = 10 * time.Second
	updateUserAgentFmt = "dfmc-cli/%s"
)

type updateOptions struct {
	repo         string
	host         string
	channel      string
	timeout      time.Duration
	jsonMode     bool
	httpClient   *http.Client
	currentExtra string
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	HTMLURL     string `json:"html_url"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
}

func runUpdate(ctx context.Context, args []string, version string, jsonMode bool) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	channel := fs.String("channel", "stable", "release channel: stable | prerelease")
	repo := fs.String("repo", defaultUpdateRepo, "owner/name on GitHub to query")
	host := fs.String("host", defaultUpdateHost, "GitHub API host (for testing / enterprise)")
	jsonFlag := fs.Bool("json", false, "emit result as JSON")
	timeoutMS := fs.Int("timeout-ms", int(updateHTTPTimeout/time.Millisecond), "HTTP timeout in milliseconds")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	jsonMode = jsonMode || *jsonFlag

	opts := updateOptions{
		repo:     strings.TrimSpace(*repo),
		host:     strings.TrimRight(strings.TrimSpace(*host), "/"),
		channel:  strings.ToLower(strings.TrimSpace(*channel)),
		timeout:  time.Duration(*timeoutMS) * time.Millisecond,
		jsonMode: jsonMode,
	}
	if opts.repo == "" {
		opts.repo = defaultUpdateRepo
	}
	if opts.host == "" {
		opts.host = defaultUpdateHost
	}
	if opts.channel != "stable" && opts.channel != "prerelease" {
		fmt.Fprintf(os.Stderr, "unknown channel %q (expected stable|prerelease)\n", opts.channel)
		return 2
	}
	if opts.timeout <= 0 {
		opts.timeout = updateHTTPTimeout
	}

	report, err := checkForUpdate(ctx, opts, version)
	if err != nil {
		if jsonMode {
			_ = printJSON(map[string]any{
				"ok":      false,
				"error":   err.Error(),
				"current": version,
				"repo":    opts.repo,
			})
			return 1
		}
		fmt.Fprintf(os.Stderr, "dfmc update: %v\n", err)
		return 1
	}

	if jsonMode {
		mustPrintJSON(report)
		return 0
	}
	printUpdateReport(report)
	return 0
}

type updateReport struct {
	OK         bool   `json:"ok"`
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	Channel    string `json:"channel"`
	Repo       string `json:"repo"`
	URL        string `json:"url,omitempty"`
	Published  string `json:"published_at,omitempty"`
	Upgradable bool   `json:"upgradable"`
	UpToDate   bool   `json:"up_to_date"`
	Notes      string `json:"notes,omitempty"`
	Command    string `json:"suggested_command,omitempty"`
}

func checkForUpdate(ctx context.Context, opts updateOptions, current string) (updateReport, error) {
	client := opts.httpClient
	if client == nil {
		client = &http.Client{Timeout: opts.timeout}
	}

	rel, err := fetchLatestRelease(ctx, client, opts)
	if err != nil {
		return updateReport{}, err
	}

	latest := strings.TrimSpace(rel.TagName)
	cmp := compareSemver(current, latest)

	report := updateReport{
		OK:         true,
		Current:    current,
		Latest:     latest,
		Channel:    opts.channel,
		Repo:       opts.repo,
		URL:        rel.HTMLURL,
		Published:  rel.PublishedAt,
		Upgradable: cmp < 0,
		UpToDate:   cmp >= 0,
		Notes:      firstLine(rel.Body, 200),
	}
	if report.Upgradable {
		report.Command = suggestUpgradeCommand()
	}
	return report, nil
}

func printUpdateReport(r updateReport) {
	fmt.Printf("dfmc %s (channel: %s)\n", r.Current, r.Channel)
	fmt.Printf("latest: %s\n", r.Latest)
	if r.Published != "" {
		fmt.Printf("published: %s\n", r.Published)
	}
	if r.URL != "" {
		fmt.Printf("release: %s\n", r.URL)
	}
	switch {
	case r.Upgradable:
		fmt.Println()
		fmt.Println("A newer release is available.")
		if r.Command != "" {
			fmt.Printf("  %s\n", r.Command)
		}
		fmt.Println("Or download the binary for your OS/arch from the release page above.")
	case r.UpToDate:
		fmt.Println()
		fmt.Println("You're on the latest release.")
	}
	if r.Notes != "" {
		fmt.Println()
		fmt.Println("release notes (first line):")
		fmt.Printf("  %s\n", r.Notes)
	}
}

// fetch/decode/errFromStatus + suggestUpgradeCommand + semver helpers
// (compareSemver, splitSemver, firstLine, runtimeGOOS) live in
// cli_update_helpers.go.

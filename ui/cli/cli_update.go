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
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
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

func fetchLatestRelease(ctx context.Context, client *http.Client, opts updateOptions) (githubRelease, error) {
	if opts.channel == "prerelease" {
		return fetchLatestIncludingPrerelease(ctx, client, opts)
	}
	endpoint := fmt.Sprintf("%s/repos/%s/releases/latest", opts.host, opts.repo)
	return doReleaseRequest(ctx, client, opts, endpoint, false)
}

func fetchLatestIncludingPrerelease(ctx context.Context, client *http.Client, opts updateOptions) (githubRelease, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/releases?per_page=10", opts.host, opts.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", fmt.Sprintf(updateUserAgentFmt, opts.currentExtra))
	resp, err := client.Do(req)
	if err != nil {
		return githubRelease{}, fmt.Errorf("github request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err := errFromStatus(resp, body); err != nil {
		return githubRelease{}, err
	}
	var releases []githubRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return githubRelease{}, fmt.Errorf("decode release list: %w", err)
	}
	for _, r := range releases {
		if r.Draft {
			continue
		}
		return r, nil
	}
	return githubRelease{}, fmt.Errorf("no releases found on %s", opts.repo)
}

func doReleaseRequest(ctx context.Context, client *http.Client, opts updateOptions, endpoint string, _ bool) (githubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", fmt.Sprintf(updateUserAgentFmt, opts.currentExtra))
	resp, err := client.Do(req)
	if err != nil {
		return githubRelease{}, fmt.Errorf("github request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err := errFromStatus(resp, body); err != nil {
		return githubRelease{}, err
	}
	var rel githubRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return githubRelease{}, fmt.Errorf("decode release: %w", err)
	}
	if strings.TrimSpace(rel.TagName) == "" {
		return githubRelease{}, fmt.Errorf("github returned empty tag_name")
	}
	return rel, nil
}

func errFromStatus(resp *http.Response, body []byte) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode == http.StatusForbidden && strings.Contains(string(body), "rate limit") {
		reset := resp.Header.Get("X-RateLimit-Reset")
		return fmt.Errorf("github rate limit reached (reset at unix %s); retry later or set GITHUB_TOKEN", reset)
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("repository has no releases yet (404)")
	}
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 200 {
		snippet = snippet[:200] + "..."
	}
	return fmt.Errorf("github returned HTTP %d: %s", resp.StatusCode, snippet)
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

func suggestUpgradeCommand() string {
	switch runtimeGOOS() {
	case "darwin", "linux":
		return "brew upgrade dfmc   # if installed via the dontfuckmycode tap"
	case "windows":
		return "download the zip archive from the release page and replace dfmc.exe"
	default:
		return "download the archive from the release page"
	}
}

// compareSemver returns -1 if a<b, 0 if equal (ignoring build metadata), 1 if
// a>b. Lenient: it strips a leading "v", handles missing segments, treats any
// non-numeric tail as a prerelease that sorts before the equivalent stable.
// Non-semver or "dev"-style current versions are treated as zero so that any
// tagged release looks upgradable.
func compareSemver(a, b string) int {
	ap, apre := splitSemver(a)
	bp, bpre := splitSemver(b)
	for i := 0; i < 3; i++ {
		var av, bv int
		if i < len(ap) {
			av = ap[i]
		}
		if i < len(bp) {
			bv = bp[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	// Core versions equal; compare prerelease. Empty prerelease > any tag.
	switch {
	case apre == "" && bpre == "":
		return 0
	case apre == "":
		return 1
	case bpre == "":
		return -1
	case apre < bpre:
		return -1
	case apre > bpre:
		return 1
	}
	return 0
}

func splitSemver(v string) ([]int, string) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	if v == "" || v == "dev" || v == "unknown" {
		return []int{0, 0, 0}, ""
	}
	// Strip build metadata after '+'.
	if idx := strings.Index(v, "+"); idx >= 0 {
		v = v[:idx]
	}
	pre := ""
	if idx := strings.Index(v, "-"); idx >= 0 {
		pre = v[idx+1:]
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return []int{0, 0, 0}, pre
		}
		out = append(out, n)
	}
	return out, pre
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		s = s[:idx]
	}
	if max > 0 && len(s) > max {
		s = s[:max] + "..."
	}
	return s
}

// runtimeGOOS is a seam so tests can pin the upgrade-command text.
var runtimeGOOS = func() string { return runtime.GOOS }

package cli

// cli_update_helpers.go — GitHub release fetching + semver/format
// helpers used by `dfmc update`. Sibling of cli_update.go which keeps
// the runUpdate dispatcher (flag parsing → checkForUpdate →
// printUpdateReport), the updateOptions / githubRelease / updateReport
// shapes, and the higher-level printUpdateReport that turns the report
// into user-facing copy.
//
// These live in a sibling because the network/parsing path and the
// version-comparison helpers are deterministic plain functions —
// keeping them out of the dispatcher file means runUpdate stays small
// and the behaviour-under-test (compareSemver, errFromStatus) is easy
// to find.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
)

func fetchLatestRelease(ctx context.Context, client *http.Client, opts updateOptions) (githubRelease, error) {
	if opts.channel == "prerelease" {
		return fetchLatestIncludingPrerelease(ctx, client, opts)
	}
	endpoint := fmt.Sprintf("%s/repos/%s/releases/latest", opts.host, opts.repo)
	return doReleaseRequest(ctx, client, opts, endpoint, false)
}

// fetchLatestIncludingPrerelease pulls the most recent 10 releases and
// returns the first non-draft entry — including prereleases — so the
// `--channel prerelease` path can advance to a preview build.
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

// errFromStatus turns a non-2xx GitHub response into a user-facing
// error. Handles the common rate-limit (403 with "rate limit" body)
// and 404 (no releases yet) cases with bespoke messages so the user
// knows what to do next.
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

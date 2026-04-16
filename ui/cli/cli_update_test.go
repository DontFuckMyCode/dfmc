package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// helper — builds an updateOptions pointing at the test server, stable channel.
func updateOptsForTest(host, repo string) updateOptions {
	return updateOptions{
		repo:    repo,
		host:    host,
		channel: "stable",
		timeout: 2 * time.Second,
		httpClient: &http.Client{
			Timeout: 2 * time.Second,
		},
	}
}

func TestCheckForUpdateUpToDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"tag_name":"v1.2.3","html_url":"https://example.test/rel/1.2.3","published_at":"2026-04-10T12:00:00Z"}`)
	}))
	defer srv.Close()

	report, err := checkForUpdate(context.Background(), updateOptsForTest(srv.URL, "me/demo"), "v1.2.3")
	if err != nil {
		t.Fatalf("checkForUpdate: %v", err)
	}
	if !report.UpToDate || report.Upgradable {
		t.Fatalf("want up-to-date report, got %+v", report)
	}
	if report.Latest != "v1.2.3" {
		t.Fatalf("latest=%q", report.Latest)
	}
	if report.URL == "" {
		t.Fatalf("missing release URL")
	}
}

func TestCheckForUpdateNewerAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"tag_name":"v2.0.0","html_url":"https://example.test/rel/2.0.0","body":"## big stuff\n- thing happened\n- other thing"}`)
	}))
	defer srv.Close()

	report, err := checkForUpdate(context.Background(), updateOptsForTest(srv.URL, "me/demo"), "v1.4.9")
	if err != nil {
		t.Fatalf("checkForUpdate: %v", err)
	}
	if !report.Upgradable || report.UpToDate {
		t.Fatalf("want upgradable report, got %+v", report)
	}
	if report.Notes == "" {
		t.Fatalf("expected first-line notes, got empty")
	}
	if !strings.Contains(report.Notes, "big stuff") {
		t.Fatalf("notes=%q want first line", report.Notes)
	}
	if report.Command == "" {
		t.Fatalf("expected upgrade command hint when upgradable")
	}
}

func TestCheckForUpdateDevVersionUpgradable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v0.1.0","html_url":"https://example.test/rel/0.1.0"}`)
	}))
	defer srv.Close()

	report, err := checkForUpdate(context.Background(), updateOptsForTest(srv.URL, "me/demo"), "dev")
	if err != nil {
		t.Fatalf("checkForUpdate: %v", err)
	}
	if !report.Upgradable {
		t.Fatalf("dev build should always look upgradable against a tagged release: %+v", report)
	}
}

func TestCheckForUpdateRateLimitedErrorIsTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Reset", "1234567890")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"API rate limit exceeded for 1.2.3.4"}`)
	}))
	defer srv.Close()

	_, err := checkForUpdate(context.Background(), updateOptsForTest(srv.URL, "me/demo"), "v1.0.0")
	if err == nil {
		t.Fatalf("want error on 403 rate limit")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("err=%v missing rate-limit wording", err)
	}
}

func TestCheckForUpdateMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `not json`)
	}))
	defer srv.Close()

	_, err := checkForUpdate(context.Background(), updateOptsForTest(srv.URL, "me/demo"), "v1.0.0")
	if err == nil {
		t.Fatalf("want decode error")
	}
}

func TestCheckForUpdate404Repo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	}))
	defer srv.Close()

	_, err := checkForUpdate(context.Background(), updateOptsForTest(srv.URL, "me/demo"), "v1.0.0")
	if err == nil || !strings.Contains(err.Error(), "no releases") {
		t.Fatalf("err=%v want 'no releases' message", err)
	}
}

func TestCheckForUpdatePrereleaseChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/releases") || strings.HasSuffix(r.URL.Path, "/latest") {
			t.Errorf("expected list-releases endpoint, got %s", r.URL.Path)
		}
		fmt.Fprint(w, `[
			{"tag_name":"v1.5.0-rc.1","html_url":"https://example.test/rc1","prerelease":true},
			{"tag_name":"v1.4.0","html_url":"https://example.test/1.4.0","prerelease":false}
		]`)
	}))
	defer srv.Close()

	opts := updateOptsForTest(srv.URL, "me/demo")
	opts.channel = "prerelease"
	report, err := checkForUpdate(context.Background(), opts, "v1.4.0")
	if err != nil {
		t.Fatalf("checkForUpdate: %v", err)
	}
	if report.Latest != "v1.5.0-rc.1" {
		t.Fatalf("latest=%q want v1.5.0-rc.1", report.Latest)
	}
	if !report.Upgradable {
		t.Fatalf("expected upgradable for 1.4.0 -> 1.5.0-rc.1")
	}
}

func TestCompareSemverOrdering(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0", "1.0.0", 0},
		{"v1.2.3", "v1.2.4", -1},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.2.3", "v1.2", 1},
		{"dev", "v0.1.0", -1},
		{"v1.0.0-rc.1", "v1.0.0", -1},
		{"v1.0.0", "v1.0.0-rc.1", 1},
		{"v1.0.0-rc.2", "v1.0.0-rc.1", 1},
		{"v1.0.0+build.1", "v1.0.0+build.2", 0},
		{"", "v0.0.1", -1},
	}
	for _, c := range cases {
		got := compareSemver(c.a, c.b)
		if got != c.want {
			t.Errorf("compareSemver(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestRunUpdateJSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v9.9.9","html_url":"https://example.test/rel/9.9.9"}`)
	}))
	defer srv.Close()

	args := []string{"--repo", "me/demo", "--host", srv.URL, "--json"}
	code := runUpdate(context.Background(), args, "v0.0.1", true)
	if code != 0 {
		t.Fatalf("runUpdate exit=%d", code)
	}
}

func TestRunUpdateUnknownChannel(t *testing.T) {
	code := runUpdate(context.Background(), []string{"--channel", "beta"}, "v1.0.0", false)
	if code != 2 {
		t.Fatalf("want exit 2 for unknown channel, got %d", code)
	}
}

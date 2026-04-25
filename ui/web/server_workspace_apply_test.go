package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// TestWorkspaceApply_ApprovalGateDeniesNetwork pins VULN-009: the
// network-class approval list must apply to /api/v1/workspace/apply
// the same way it applies to /api/v1/tools/apply_patch. Earlier
// versions skipped the gate entirely because the handler shelled out
// to `git apply` directly without going through CallTool.
func TestWorkspaceApply_ApprovalGateDeniesNetwork(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("HOME", tmp)

	// Build a project with a tracked file we can patch.
	projectRoot := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	aPath := filepath.Join(projectRoot, "a.txt")
	if err := os.WriteFile(aPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	mustGitInit(t, projectRoot)
	mustGit(t, projectRoot, "add", "a.txt")
	mustGit(t, projectRoot, "commit", "-m", "init")

	cfg := config.DefaultConfig()
	cfg.Web.Host = "127.0.0.1"
	cfg.Web.Port = 0
	cfg.Web.AllowedHosts = []string{"*"}
	cfg.Web.AllowedOrigins = []string{"*"}
	// Network approval list must engage the gate. The default
	// already ships ["*"] but be explicit here so the test pins the
	// behavior even if defaults change later.
	cfg.Tools.RequireApprovalNetwork = []string{"*"}

	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("init engine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Shutdown() })
	eng.ProjectRoot = projectRoot

	srv := New(eng, "127.0.0.1", 0)
	// Strip the default web approver and DON'T register one — the
	// gate should implicit-deny without an approver.
	eng.SetApprover(nil)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	patch := "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-hello\n+world\n"
	body := map[string]any{"patch": patch}
	buf, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/workspace/apply", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 403 Forbidden when no approver registered, got %d: %s", resp.StatusCode, string(raw))
	}
	// File MUST NOT have been modified.
	got, _ := os.ReadFile(aPath)
	if string(got) != "hello\n" {
		t.Fatalf("file was mutated despite gate deny — got %q", string(got))
	}
}

// TestWorkspaceApply_CheckOnlyBypassesGate confirms that --check-only
// dry-run probes don't trip the gate. Read-only validation is fine
// without approval; mutation isn't.
func TestWorkspaceApply_CheckOnlyBypassesGate(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("HOME", tmp)

	projectRoot := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	aPath := filepath.Join(projectRoot, "a.txt")
	if err := os.WriteFile(aPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	mustGitInit(t, projectRoot)
	mustGit(t, projectRoot, "add", "a.txt")
	mustGit(t, projectRoot, "commit", "-m", "init")

	cfg := config.DefaultConfig()
	cfg.Web.Host = "127.0.0.1"
	cfg.Web.Port = 0
	cfg.Web.AllowedHosts = []string{"*"}
	cfg.Web.AllowedOrigins = []string{"*"}
	cfg.Tools.RequireApprovalNetwork = []string{"*"}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("init engine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Shutdown() })
	eng.ProjectRoot = projectRoot

	srv := New(eng, "127.0.0.1", 0)
	eng.SetApprover(nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	patch := "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-hello\n+world\n"
	body := map[string]any{"patch": patch, "check_only": true}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/workspace/apply", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("check_only must not be gated, got %d: %s", resp.StatusCode, string(raw))
	}
}

// TestPathsFromUnifiedDiff covers the diff-header parser used to
// validate paths before `git apply` mutates anything.
func TestPathsFromUnifiedDiff(t *testing.T) {
	cases := []struct {
		name  string
		patch string
		want  []string
	}{
		{
			name:  "simple modification",
			patch: "--- a/foo.go\n+++ b/foo.go\n@@ -1 +1 @@\n-old\n+new\n",
			want:  []string{"foo.go"},
		},
		{
			name:  "new file via /dev/null",
			patch: "--- /dev/null\n+++ b/newfile.go\n@@ -0,0 +1 @@\n+package x\n",
			want:  []string{"newfile.go"},
		},
		{
			name:  "deleted file",
			patch: "--- a/old.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-package x\n",
			want:  []string{"old.go"},
		},
		{
			name:  "tab-padded path with timestamp",
			patch: "--- a/foo.go\t2026-01-01\n+++ b/foo.go\t2026-01-02\n@@ -1 +1 @@\n",
			want:  []string{"foo.go"},
		},
		{
			name:  "traversal target",
			patch: "--- a/../escape.txt\n+++ b/../escape.txt\n@@ -1 +1 @@\n",
			want:  []string{"../escape.txt"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pathsFromUnifiedDiff(c.patch)
			if len(got) != len(c.want) {
				t.Fatalf("paths = %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("paths[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

// TestAssertPathWithinRoot covers the separator-aware containment
// check that replaced the buggy strings.HasPrefix version.
func TestAssertPathWithinRoot(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cases := []struct {
		name    string
		relPath string
		wantErr bool
	}{
		{name: "in-root", relPath: "foo.go", wantErr: false},
		{name: "in-root subdir", relPath: "sub/foo.go", wantErr: false},
		{name: "parent escape", relPath: "../escape.go", wantErr: true},
		{name: "double parent escape", relPath: "../../escape.go", wantErr: true},
		{name: "subdir escape", relPath: "sub/../../escape.go", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := assertPathWithinRoot(root, c.relPath)
			if c.wantErr && err == nil {
				t.Errorf("expected error for relPath=%q, got nil", c.relPath)
			}
			if !c.wantErr && err != nil {
				t.Errorf("unexpected error for relPath=%q: %v", c.relPath, err)
			}
		})
	}
}

// TestAssertPathWithinRoot_PrefixBoundaryBug pins the specific
// regression: HasPrefix(absPath, root) used to accept a sibling
// directory whose name starts with the root path
// (root="/proj", abs="/proj-evil/foo" — passes the HasPrefix check
// but is NOT inside root).
func TestAssertPathWithinRoot_PrefixBoundaryBug(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Create a sibling directory whose name shares the root prefix.
	sibling := filepath.Join(tmp, "proj-evil")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatalf("mkdir sibling: %v", err)
	}
	// A relative path that goes up and into the sibling — must be
	// rejected. The buggy HasPrefix version accepted these.
	if err := assertPathWithinRoot(root, "../proj-evil/foo.go"); err == nil {
		t.Fatalf("expected rejection for sibling with shared prefix; HasPrefix-style check would have accepted it")
	}
}

// mustGitInit / mustGit are minimal git scaffolding for the tests
// above. We need a real git repo because `git apply --check` walks
// the index. Skips the test when git is unavailable on the host.
func mustGitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available — skipping apply-gate test")
	}
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, string(out))
	}
	// Disable signing globally for this temp repo.
	for _, kv := range [][]string{
		{"commit.gpgsign", "false"},
		{"tag.gpgsign", "false"},
	} {
		c := exec.Command("git", "-C", dir, "config", kv[0], kv[1])
		_ = c.Run()
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
	_ = strings.TrimSpace("") // keep "strings" used if test bodies remove their use later
}

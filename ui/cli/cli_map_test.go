package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

func TestRunMap_ASCIIFormat(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := eng.CodeMap.BuildFromFiles(context.Background(), []string{src}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}

	out := captureStdout(t, func() {
		if code := runMap(context.Background(), eng, []string{}, false); code != 0 {
			t.Fatalf("runMap exit=%d", code)
		}
	})
	if out == "" {
		t.Fatal("expected ascii output")
	}
}

func TestRunMap_JSONFormat(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := eng.CodeMap.BuildFromFiles(context.Background(), []string{src}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}

	out := captureStdout(t, func() {
		if code := runMap(context.Background(), eng, []string{"--json"}, false); code != 0 {
			t.Fatalf("runMap --json exit=%d", code)
		}
	})
	if !strings.Contains(out, "\"nodes\"") && !strings.Contains(out, "nodes") {
		t.Fatalf("expected json nodes in output: %s", out)
	}
}

func TestRunMap_DOTFormat(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := eng.CodeMap.BuildFromFiles(context.Background(), []string{src}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}

	out := captureStdout(t, func() {
		if code := runMap(context.Background(), eng, []string{"dot"}, false); code != 0 {
			t.Fatalf("runMap dot exit=%d", code)
		}
	})
	if !strings.Contains(out, "digraph") {
		t.Fatalf("expected dot output: %s", out)
	}
}

func TestRunMap_SVGFormat(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := eng.CodeMap.BuildFromFiles(context.Background(), []string{src}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}

	out := captureStdout(t, func() {
		if code := runMap(context.Background(), eng, []string{"svg"}, false); code != 0 {
			t.Fatalf("runMap svg exit=%d", code)
		}
	})
	if !strings.Contains(out, "<svg") {
		t.Fatalf("expected svg output: %s", out)
	}
}

func TestRunMap_FallbackFormat(t *testing.T) {
	// Unknown format falls through to ASCII edge dump
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := eng.CodeMap.BuildFromFiles(context.Background(), []string{src}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}

	out := captureStdout(t, func() {
		if code := runMap(context.Background(), eng, []string{"unknown-format"}, false); code != 0 {
			t.Fatalf("runMap unknown-format exit=%d", code)
		}
	})
	// Should fall through to ASCII dump (no error for unknown format)
	if !strings.Contains(out, "->") {
		t.Fatalf("expected edge arrows in ascii fallback output: %s", out)
	}
}

func TestRunMap_ParseError(t *testing.T) {
	eng := newCLITestEngine(t)
	if code := runMap(context.Background(), eng, []string{"--invalid-flag"}, false); code != 2 {
		t.Fatalf("expected exit 2 for parse error, got %d", code)
	}
}

func TestGraphToDOT_Basic(t *testing.T) {
	nodes := []codemap.Node{
		{ID: "pkg/main", Name: "main", Kind: "package"},
		{ID: "func/main.main", Name: "main", Kind: "function"},
	}
	edges := []codemap.Edge{
		{From: "func/main.main", To: "pkg/main", Type: "package"},
	}
	got := graphToDOT(nodes, edges)
	if !strings.Contains(got, "digraph DFMC") {
		t.Fatalf("expected digraph header: %s", got)
	}
	if !strings.Contains(got, "func/main.main") || !strings.Contains(got, "pkg/main") {
		t.Fatalf("expected node IDs in output: %s", got)
	}
	if !strings.Contains(got, "->") {
		t.Fatalf("expected edge arrows: %s", got)
	}
}

func TestGraphToDOT_EmptyGraph(t *testing.T) {
	got := graphToDOT(nil, nil)
	if !strings.Contains(got, "digraph DFMC") {
		t.Fatalf("expected digraph header for empty graph: %s", got)
	}
}

func TestGraphToDOT_EscapesSpecialChars(t *testing.T) {
	nodes := []codemap.Node{
		{ID: `pkg/path"with\quotes`, Name: `Node "Name"`, Kind: "type"},
	}
	edges := []codemap.Edge{
		{From: `pkg/path"with\quotes`, To: `pkg/path"with\quotes`, Type: "calls"},
	}
	got := graphToDOT(nodes, edges)
	if strings.Contains(got, `"`) && !strings.Contains(got, `\"`) {
		t.Fatalf("double-quote should be escaped in DOT: %s", got)
	}
	if strings.Contains(got, `\`) && !strings.Contains(got, `\\`) {
		t.Fatalf("backslash should be escaped in DOT: %s", got)
	}
}

func TestEscapeDOT(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "hello", "hello"},
		{"backslash", `a\b`, `a\\b`},
		{"doublequote", `say "hi"`, `say \"hi\"`},
		{"both", `path\"with\\quotes`, `path\\\"with\\\\quotes`},
	}
	for _, c := range cases {
		got := escapeDOT(c.in)
		if got != c.want {
			t.Errorf("%s: escapeDOT(%q)=%q want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestRunCompletion_Bash(t *testing.T) {
	out := captureStdout(t, func() {
		if code := runCompletion([]string{"bash"}, false); code != 0 {
			t.Fatalf("runCompletion bash exit=%d", code)
		}
	})
	if !strings.Contains(out, "complete -F") {
		t.Fatalf("expected bash completion script: %s", out)
	}
}

func TestRunCompletion_Zsh(t *testing.T) {
	out := captureStdout(t, func() {
		if code := runCompletion([]string{"zsh"}, false); code != 0 {
			t.Fatalf("runCompletion zsh exit=%d", code)
		}
	})
	if !strings.Contains(out, "compdef") {
		t.Fatalf("expected zsh completion script: %s", out)
	}
}

func TestRunCompletion_Fish(t *testing.T) {
	out := captureStdout(t, func() {
		if code := runCompletion([]string{"fish"}, false); code != 0 {
			t.Fatalf("runCompletion fish exit=%d", code)
		}
	})
	if !strings.Contains(out, "complete -c dfmc") {
		t.Fatalf("expected fish completion script: %s", out)
	}
}

func TestRunCompletion_PowerShell(t *testing.T) {
	out := captureStdout(t, func() {
		if code := runCompletion([]string{"powershell"}, false); code != 0 {
			t.Fatalf("runCompletion powershell exit=%d", code)
		}
	})
	if !strings.Contains(out, "Register-ArgumentCompleter") {
		t.Fatalf("expected powershell completion script: %s", out)
	}
}

func TestRunCompletion_JSON(t *testing.T) {
	// jsonMode=true bypasses the --json flag check (jsonMode is already true)
	out := captureStdout(t, func() {
		if code := runCompletion([]string{"bash"}, true); code != 0 {
			t.Fatalf("runCompletion bash jsonMode exit=%d", code)
		}
	})
	if !containsJSONKey(out, "commands") {
		t.Fatalf("expected json with commands in stdout: %s", out)
	}
}

func TestRunCompletion_EmptyShell(t *testing.T) {
	if code := runCompletion([]string{}, false); code != 2 {
		t.Fatalf("expected exit 2 for empty shell, got %d", code)
	}
}

func TestRunCompletion_UnsupportedShell(t *testing.T) {
	out := captureStderr(t, func() {
		if code := runCompletion([]string{"unsupported"}, false); code != 2 {
			t.Fatalf("expected exit 2 for unsupported shell, got %d", code)
		}
	})
	if !strings.Contains(out, "unsupported shell") {
		t.Fatalf("expected unsupported shell message: %s", out)
	}
}

func TestRunCompletion_ShellFlag(t *testing.T) {
	out := captureStdout(t, func() {
		if code := runCompletion([]string{"--shell", "bash"}, false); code != 0 {
			t.Fatalf("runCompletion --shell bash exit=%d", code)
		}
	})
	if !strings.Contains(out, "complete -F") {
		t.Fatalf("expected bash completion via --shell flag: %s", out)
	}
}

func TestRunMan_Markdown(t *testing.T) {
	out := captureStdout(t, func() {
		if code := runMan([]string{"--format", "markdown"}, false); code != 0 {
			t.Fatalf("runMan markdown exit=%d", code)
		}
	})
	if !strings.Contains(out, "##") {
		t.Fatalf("expected markdown headers in output: %s", out)
	}
}

func TestRunMan_Roff(t *testing.T) {
	out := captureStdout(t, func() {
		if code := runMan([]string{"--format", "man"}, false); code != 0 {
			t.Fatalf("runMan man exit=%d", code)
		}
	})
	if !strings.Contains(out, ".TH") {
		t.Fatalf("expected roff .TH in output: %s", out)
	}
}

func TestRunMan_UnsupportedFormat(t *testing.T) {
	out := captureStderr(t, func() {
		if code := runMan([]string{"--format", "unsupported"}, false); code != 2 {
			t.Fatalf("expected exit 2 for unsupported format, got %d", code)
		}
	})
	if !strings.Contains(out, "unsupported man format") {
		t.Fatalf("expected unsupported format message: %s", out)
	}
}

func TestRunMan_ParseError(t *testing.T) {
	if code := runMan([]string{"--invalid-flag"}, false); code != 2 {
		t.Fatalf("expected exit 2 for parse error, got %d", code)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()

	done := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- data
	}()

	fn()
	_ = w.Close()
	data := <-done
	return string(data)
}
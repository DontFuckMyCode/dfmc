package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

func TestRunScan_TextOutput(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	out := captureStdout(t, func() {
		code := runScan(context.Background(), eng, []string{tmp}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestRunScan_JSONMode(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	// Include a hardcoded credential so the security report actually has a
	// finding — the JSON omits the secrets/vulnerabilities arrays when they
	// are empty (omitempty), so an empty fixture would produce just
	// {"files_scanned": N} and the key assertions below would not hold.
	// The AKIA-prefixed literal is detected by both the tree-sitter and the
	// regex credential scanner, so the fixture is backend-independent.
	const fixture = "package main\n\nconst apiKey = \"AKIAIOSFODNN7EXAMPLE1234567890AB\"\n\nfunc main() {}\n"
	if err := os.WriteFile(src, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	out := captureStdout(t, func() {
		code := runScan(context.Background(), eng, []string{"--json", tmp}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if !containsJSONKey(out, "secrets") && !containsJSONKey(out, "vulnerabilities") {
		t.Errorf("expected secrets/vulnerabilities key in scan JSON: %s", out)
	}
}

func TestRunScan_WithPath(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	out := captureStdout(t, func() {
		code := runScan(context.Background(), eng, []string{tmp}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestRunScan_ParseError(t *testing.T) {
	eng := newCLITestEngine(t)
	code := runScan(context.Background(), eng, []string{"--invalid-flag"}, false)
	if code != 2 {
		t.Errorf("expected exit 2 for parse error, got %d", code)
	}
}

func TestRunAnalyze_SecurityFlag(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	out := captureStdout(t, func() {
		code := runAnalyze(context.Background(), eng, []string{"--security", tmp}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestRunAnalyze_ComplexityFlag(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	out := captureStdout(t, func() {
		code := runAnalyze(context.Background(), eng, []string{"--complexity", tmp}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestRunAnalyze_DeadCodeFlag(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	out := captureStdout(t, func() {
		code := runAnalyze(context.Background(), eng, []string{"--dead-code", tmp}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestRunAnalyze_DuplicationFlag(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	out := captureStdout(t, func() {
		code := runAnalyze(context.Background(), eng, []string{"--duplication", tmp}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestRunAnalyze_TodosFlag(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\n// TODO: fix this\nfunc main() {\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	out := captureStdout(t, func() {
		code := runAnalyze(context.Background(), eng, []string{"--todos", tmp}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if !strings.Contains(out, "Todo") && !strings.Contains(out, "TODO") {
		t.Errorf("expected TODO marker in output: %s", out)
	}
}

func TestRunAnalyze_DepsFlag(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\nimport \"fmt\"\nfunc main() { fmt.Println(1) }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	out := captureStdout(t, func() {
		code := runAnalyze(context.Background(), eng, []string{"--deps", tmp}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if !strings.Contains(out, "Depend") && !strings.Contains(out, "fmt") {
		t.Errorf("expected dependency info in output: %s", out)
	}
}

func TestRunAnalyze_JSONMode(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	out := captureStdout(t, func() {
		code := runAnalyze(context.Background(), eng, []string{"--json", tmp}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if !containsJSONKey(out, "files") {
		t.Errorf("expected 'files' key in analyze JSON: %s", out)
	}
}

func TestRunAnalyze_FullFlag(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\nimport \"fmt\"\nfunc main() { fmt.Println(1) }\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	out := captureStdout(t, func() {
		code := runAnalyze(context.Background(), eng, []string{"--full", tmp}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestRunAnalyze_WithPath(t *testing.T) {
	eng := newCLITestEngine(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {\n}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	out := captureStdout(t, func() {
		code := runAnalyze(context.Background(), eng, []string{tmp}, false)
		if code != 0 {
			t.Errorf("expected exit 0, got %d", code)
		}
	})
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestRunAnalyze_ParseError(t *testing.T) {
	eng := newCLITestEngine(t)
	code := runAnalyze(context.Background(), eng, []string{"--invalid-flag"}, false)
	if code != 2 {
		t.Errorf("expected exit 2 for parse error, got %d", code)
	}
}

func TestGraphToDOT_NilEdges(t *testing.T) {
	got := graphToDOT(nil, nil)
	if !strings.Contains(got, "digraph DFMC") {
		t.Errorf("expected digraph header, got: %s", got)
	}
}

func TestGraphToDOT_WithEdges(t *testing.T) {
	nodes := []codemap.Node{
		{ID: "a", Name: "A", Kind: "type"},
		{ID: "b", Name: "B", Kind: "function"},
	}
	edges := []codemap.Edge{
		{From: "a", To: "b", Type: "calls"},
	}
	got := graphToDOT(nodes, edges)
	if !strings.Contains(got, "digraph DFMC") {
		t.Errorf("expected digraph header, got: %s", got)
	}
	if !strings.Contains(got, "a") || !strings.Contains(got, "b") {
		t.Errorf("expected node names, got: %s", got)
	}
	if !strings.Contains(got, "calls") {
		t.Errorf("expected edge label, got: %s", got)
	}
}

func TestGraphToSVG_NilNodes(t *testing.T) {
	got := graphToSVG(nil, nil)
	if !strings.Contains(got, "<svg") {
		t.Errorf("expected svg tag, got: %s", got)
	}
	if !strings.Contains(got, "No codemap nodes") {
		t.Errorf("expected empty graph message, got: %s", got)
	}
}

func TestGraphToSVG_WithNodes(t *testing.T) {
	nodes := []codemap.Node{
		{ID: "a", Name: "A", Kind: "type"},
		{ID: "b", Name: "B", Kind: "function"},
	}
	edges := []codemap.Edge{
		{From: "a", To: "b", Type: "calls"},
	}
	got := graphToSVG(nodes, edges)
	if !strings.Contains(got, "<svg") {
		t.Errorf("expected svg tag, got: %s", got)
	}
	if !strings.Contains(got, "A") || !strings.Contains(got, "B") {
		t.Errorf("expected node labels, got: %s", got)
	}
}

func TestXmlEscape(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"<div>", "&lt;div&gt;"},
		{"a & b", "a &amp; b"},
		{`quote "here"`, "quote &#34;here&#34;"},
		{"plain", "plain"},
	}
	for _, c := range cases {
		got := xmlEscape(c.input)
		if got != c.want {
			t.Errorf("xmlEscape(%q)=%q want %q", c.input, got, c.want)
		}
	}
}

func TestEscapeDOT_EscapesQuotes(t *testing.T) {
	got := escapeDOT(`say "hi"`)
	if !strings.Contains(got, `\"`) {
		t.Errorf("expected escaped quote, got: %s", got)
	}
}

func TestCollectDependencyStats_NilEngine(t *testing.T) {
	got := collectDependencyStats(nil, 10)
	if got != nil {
		t.Errorf("expected nil for nil engine, got %v", got)
	}
}

func TestCollectDependencyStats_NilCodeMap(t *testing.T) {
	eng := newCLITestEngine(t)
	eng.CodeMap = nil
	got := collectDependencyStats(eng, 10)
	if got != nil {
		t.Errorf("expected nil for nil codemap, got %v", got)
	}
}

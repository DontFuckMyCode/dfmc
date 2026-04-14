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

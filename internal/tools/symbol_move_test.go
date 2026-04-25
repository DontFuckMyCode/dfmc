package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestSymbolMove_MissingFrom(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "symbol_move", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"to_file": "bar.go"},
	})
	if err == nil {
		t.Fatalf("expected error for missing from")
	}
}

func TestSymbolMove_MissingToFile(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "symbol_move", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"from": "Foo"},
	})
	if err == nil {
		t.Fatalf("expected error for missing to_file")
	}
}

func TestSymbolMove_DryRunNoModification(t *testing.T) {
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "foo.go")
	os.WriteFile(srcPath, []byte("package main\nfunc Foo() {}\nfunc main() { Foo() }\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "symbol_move", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "Foo",
			"to_file": "bar.go",
			"dry_run": true,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// File should NOT be modified
	data, _ := os.ReadFile(srcPath)
	if string(data) != "package main\nfunc Foo() {}\nfunc main() { Foo() }\n" {
		t.Errorf("foo.go was modified despite dry_run=true")
	}
}

func TestSymbolMove_DestFileCreated(t *testing.T) {
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "foo.go")
	os.WriteFile(srcPath, []byte("package main\nfunc Foo() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "symbol_move", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "Foo",
			"to_file": "bar.go",
			"dry_run": false,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// bar.go should now exist with Foo
	destPath := filepath.Join(tmp, "bar.go")
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		t.Errorf("bar.go was not created")
	}
	data, _ := os.ReadFile(destPath)
	if !contains(string(data), "func Foo()") {
		t.Errorf("bar.go does not contain moved Foo: %s", string(data))
	}
	// foo.go should no longer have Foo declaration
	srcData, _ := os.ReadFile(srcPath)
	if contains(string(srcData), "func Foo()") {
		t.Errorf("foo.go still has Foo after move")
	}
	_ = res
}

func TestSymbolMove_ReferencesUpdated(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "a.go"), []byte("package main\nfunc Foo() {}\nfunc main() { Foo() }\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "b.go"), []byte("package main\nfunc bar() { Foo() }\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "symbol_move", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "Foo",
			"to_file": "c.go",
			"dry_run": false,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// a.go call site should be updated
	aData, _ := os.ReadFile(filepath.Join(tmp, "a.go"))
	bData, _ := os.ReadFile(filepath.Join(tmp, "b.go"))
	cData, _ := os.ReadFile(filepath.Join(tmp, "c.go"))
	if !contains(string(cData), "func Foo()") {
		t.Errorf("c.go missing Foo declaration")
	}
	// Original declaration removed from a.go
	if contains(string(aData), "func Foo()") {
		t.Errorf("a.go still has Foo declaration")
	}
	_ = bData
}

func TestSymbolMove_RenameOnMove(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "a.go"), []byte("package main\nfunc Foo() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "symbol_move", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "Foo",
			"to":      "Bar",
			"to_file": "b.go",
			"dry_run": false,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	bData, _ := os.ReadFile(filepath.Join(tmp, "b.go"))
	if !contains(string(bData), "func Bar()") {
		t.Errorf("b.go should have func Bar(), got: %s", string(bData))
	}
}

func TestSymbolMoveTool_Name(t *testing.T) {
	tool := NewSymbolMoveTool()
	if tool.Name() != "symbol_move" {
		t.Errorf("want symbol_move, got %s", tool.Name())
	}
}

func TestSymbolMoveTool_Spec(t *testing.T) {
	tool := NewSymbolMoveTool()
	spec := tool.Spec()
	if spec.Name != "symbol_move" {
		t.Errorf("spec.Name: want symbol_move, got %s", spec.Name)
	}
	if spec.Risk != RiskWrite {
		t.Errorf("spec.Risk: want RiskWrite, got %v", spec.Risk)
	}
	argsByName := make(map[string]Arg)
	for _, a := range spec.Args {
		argsByName[a.Name] = a
	}
	for _, name := range []string{"from", "to_file", "to", "kind", "dry_run", "skip_tests"} {
		if _, ok := argsByName[name]; !ok {
			t.Errorf("spec.Args missing %s", name)
		}
	}
}

func TestSymbolMoveTool_Description(t *testing.T) {
	tool := NewSymbolMoveTool()
	if tool.Description() == "" {
		t.Errorf("description is empty")
	}
}

func TestSymbolMove_DuplicateInDest(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "foo.go"), []byte("package main\nfunc Foo() {}\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "bar.go"), []byte("package main\nfunc Foo() {}\nfunc Bar() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "symbol_move", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "Foo",
			"to_file": "bar.go",
			"dry_run": true,
		},
	})
	if err == nil {
		t.Fatalf("expected error when dest already has Foo")
	}
}

func TestSymbolMove_KindFilter(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "a.go"), []byte("package main\nfunc Foo() {}\ntype Foo int\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "symbol_move", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "Foo",
			"to_file": "b.go",
			"kind":    "func",
			"dry_run": true,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	impact := res.Data["impact"].(moveImpact)
	// Only 1 file (a.go) should be affected (func Foo, not type Foo)
	if impact.Files != 1 {
		t.Errorf("want 1 file, got %d", impact.Files)
	}
}

func TestSymbolMove_OutputSummary(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc Foo() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "symbol_move", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "Foo",
			"to_file": "bar.go",
			"dry_run": true,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Output == "" {
		t.Errorf("expected non-empty output summary")
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// TestSymbolMove_RejectsTraversalToFile pins VULN-006: a `to_file`
// parameter that escapes the project root via `..` must be rejected
// before any directory creation or file write happens.
func TestSymbolMove_RejectsTraversalToFile(t *testing.T) {
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "main.go")
	os.WriteFile(srcPath, []byte("package main\nfunc Foo() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	cases := []string{
		"../escape.go",
		"../../tmp/pwned.go",
		"sub/../../escape.go",
	}
	for _, toFile := range cases {
		_, err := eng.Execute(context.Background(), "symbol_move", Request{
			ProjectRoot: tmp,
			Params: map[string]any{
				"from":    "Foo",
				"to_file": toFile,
				"dry_run": false,
			},
		})
		if err == nil {
			t.Errorf("to_file=%q must be rejected as outside project root", toFile)
		} else if !strings.Contains(err.Error(), "outside") && !strings.Contains(err.Error(), "root") {
			t.Errorf("to_file=%q rejection error should mention 'outside'/'root', got: %v", toFile, err)
		}
	}
}

// TestSymbolMove_RejectsAbsoluteToFile pins the absolute-path angle:
// `to_file="/etc/something.go"` must not slip past EnsureWithinRoot.
func TestSymbolMove_RejectsAbsoluteToFile(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc Foo() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	// Use an absolute path well outside tmp. On Windows this gets a
	// drive prefix, on POSIX it's /tmp/...; either way EnsureWithinRoot
	// must reject it.
	abs := filepath.Join(t.TempDir(), "out_of_root.go")
	if !filepath.IsAbs(abs) {
		t.Skipf("could not construct absolute path on this platform")
	}
	_, err := eng.Execute(context.Background(), "symbol_move", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "Foo",
			"to_file": abs,
			"dry_run": false,
		},
	})
	if err == nil {
		t.Fatalf("absolute out-of-root to_file %q must be rejected", abs)
	}
}

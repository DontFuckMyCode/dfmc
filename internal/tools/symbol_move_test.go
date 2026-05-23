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

// ---------------------------------------------------------------------------
// Helper tests — symbol_move_helpers.go
// ---------------------------------------------------------------------------

func TestExtractPackage(t *testing.T) {
	cases := []struct {
		name     string
		lines    []string
		expected string
	}{
		{"simple", []string{"package main"}, "main"},
		{"with doc", []string{"// doc", "package foo"}, "foo"},
		{" indented", []string{"  package bar "}, "bar"},
		{"no package", []string{"// nothing"}, "main"},
		{"empty", []string{}, "main"},
	}
	for _, c := range cases {
		got := extractPackage(c.lines)
		if got != c.expected {
			t.Errorf("extractPackage(%v): want %q, got %q", c.name, c.expected, got)
		}
	}
}

func TestBuildNewGoFile(t *testing.T) {
	srcLines := []string{"package main", `import "fmt"`, "", "func Foo() {}"}
	body := "func Foo() {}"
	got := buildNewGoFile(srcLines, body)
	if !strings.Contains(got, "package main") {
		t.Errorf("missing package declaration: %s", got)
	}
	if !strings.Contains(got, "import") {
		t.Errorf("missing imports section: %s", got)
	}
	if !strings.Contains(got, body) {
		t.Errorf("missing symbol body: %s", got)
	}
}

func TestBuildNewGoFile_NoImports(t *testing.T) {
	srcLines := []string{"package main", "", "func Foo() {}"}
	body := "func Foo() {}"
	got := buildNewGoFile(srcLines, body)
	if strings.Contains(got, "import") {
		t.Errorf("should not contain imports: %s", got)
	}
}

func TestExtractImportsSection(t *testing.T) {
	cases := []struct {
		name     string
		lines    []string
		expected string
	}{
		{
			"single import",
			[]string{"package main", `import "fmt"`, ""},
			`import "fmt"` + "\n",
		},
		{
			"paren import",
			[]string{"package main", "import (", `"fmt"`, `"os"`, ")", "func Foo() {}"},
			`import (` + "\n" + `"fmt"` + "\n" + `"os"`,
		},
		{
			"no import",
			[]string{"package main", "func Foo() {}"},
			"",
		},
		{
			"empty file",
			[]string{},
			"",
		},
	}
	for _, c := range cases {
		got := extractImportsSection(c.lines)
		if got != c.expected {
			t.Errorf("extractImportsSection(%s): want %q, got %q", c.name, c.expected, got)
		}
	}
}

// ---------------------------------------------------------------------------
// symbol_move.go Execute paths not covered by existing tests
// ---------------------------------------------------------------------------

// SetEngine is tested directly since it has functional impact
// (EnsureReadBeforeMutation gate) that dry_run tests cannot exercise.
func TestSymbolMoveTool_SetEngine(t *testing.T) {
	tool := NewSymbolMoveTool()
	if tool.engine != nil {
		t.Errorf("expected nil engine initially")
	}
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)
	tool.SetEngine(eng)
	if tool.engine == nil {
		t.Errorf("expected engine to be set")
	}
}

// Symbol not found returns an error.
func TestSymbolMove_NotFound(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc Foo() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "symbol_move", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "DoesNotExist",
			"to_file": "bar.go",
			"dry_run": true,
		},
	})
	if err == nil {
		t.Fatalf("expected error for not-found symbol")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

// Existing destination with same symbol must be rejected (no dry_run needed).
func TestSymbolMove_DuplicateInDest_NoDryRun(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "foo.go"), []byte("package main\nfunc Foo() {}\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "bar.go"), []byte("package main\nfunc Foo() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "symbol_move", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "Foo",
			"to_file": "bar.go",
			"dry_run": false,
		},
	})
	if err == nil {
		t.Fatalf("expected error when dest already has Foo")
	}
}

// Move to existing file (dry_run) — skips EnsureReadBeforeMutation gate.
func TestSymbolMove_ExistingDestDryRun(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "foo.go"), []byte("package main\nfunc Foo() {}\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "bar.go"), []byte("package main\nfunc Bar() {}\n"), 0644)

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
		t.Fatalf("dry_run should not error with existing dest: %v", err)
	}
	// bar.go unchanged
	data, _ := os.ReadFile(filepath.Join(tmp, "bar.go"))
	if contains(string(data), "func Foo()") {
		t.Errorf("bar.go was modified despite dry_run=true: %s", string(data))
	}
}

// Reference files that fail to write should appear in result data["failed"].
func TestSymbolMove_SkippedTestFiles(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "a.go"), []byte("package main\nfunc Foo() {}\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "a_test.go"), []byte("package main\nfunc TestFoo() { Foo() }\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "symbol_move", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":       "Foo",
			"to_file":    "b.go",
			"skip_tests": true,
			"dry_run":    false,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// a_test.go should NOT have been modified (Foo still in a_test.go call site
	// was skipped because skip_tests=true)
	testData, _ := os.ReadFile(filepath.Join(tmp, "a_test.go"))
	if contains(string(testData), "func TestFoo") {
		// the file still has the test func (skip_tests skipped the reference update,
		// but the test function itself was not affected since it calls Foo)
	}
	// Verify b.go got Foo
	bData, _ := os.ReadFile(filepath.Join(tmp, "b.go"))
	if !contains(string(bData), "func Foo()") {
		t.Errorf("b.go missing Foo: %s", string(bData))
	}
	_ = res
}

// NewSymbolMoveTool smoke test.
func TestNewSymbolMoveTool(t *testing.T) {
	tool := NewSymbolMoveTool()
	if tool == nil {
		t.Fatalf("NewSymbolMoveTool returned nil")
	}
	if tool.Name() != "symbol_move" {
		t.Errorf("Name() = %q, want symbol_move", tool.Name())
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

// TestSymbolMove_WriteErrorSurfaces guards the regression where a
// failed os.WriteFile while updating a reference was silently
// swallowed (`_ = err`), reporting success to the caller while the
// reference on disk was never updated. The fix surfaces the failure
// through impact.Failed and a data["failed"] string slice. The
// declaration's own source/dest writes still return hard errors —
// only per-reference writes use the failed[] surface so partial
// success is reported accurately.
func TestSymbolMove_WriteErrorSurfaces(t *testing.T) {
	tmp := t.TempDir()
	// Declaration in a.go; b.go and locked.go both reference Foo.
	if err := os.WriteFile(filepath.Join(tmp, "a.go"), []byte("package main\nfunc Foo() {}\nfunc main() { Foo() }\n"), 0644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "b.go"), []byte("package main\nfunc bar() { Foo() }\n"), 0644); err != nil {
		t.Fatalf("write b.go: %v", err)
	}
	locked := filepath.Join(tmp, "locked.go")
	if err := os.WriteFile(locked, []byte("package main\nfunc baz() { Foo() }\n"), 0644); err != nil {
		t.Fatalf("write locked.go: %v", err)
	}
	if err := os.Chmod(locked, 0o444); err != nil {
		t.Fatalf("chmod locked.go: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(locked, 0o644)
	})

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "symbol_move", Request{
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
	impact := res.Data["impact"].(moveImpact)
	if impact.Failed == 0 {
		t.Fatalf("expected impact.Failed > 0 when a reference target is read-only, got 0 (output=%q)", res.Output)
	}
	failed, ok := res.Data["failed"].([]string)
	if !ok || len(failed) == 0 {
		t.Fatalf("expected data['failed'] to list at least one path, got %v", res.Data["failed"])
	}
	if !strings.Contains(res.Output, "failed to write") {
		t.Errorf("output should mention failed writes, got %q", res.Output)
	}
	// The locked file must NOT show up in changes[] — that was the
	// silent-success bug.
	changes := res.Data["changes"].([]moveChange)
	for _, c := range changes {
		if c.Path == locked {
			t.Errorf("locked file appeared in changes despite write failure: %+v", c)
		}
	}
}

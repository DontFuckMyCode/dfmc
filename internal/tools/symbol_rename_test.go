package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestSymbolRename_MissingFrom(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "symbol_rename", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"to": "NewName"},
	})
	if err == nil {
		t.Fatalf("expected error for missing from")
	}
}

func TestSymbolRename_MissingTo(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "symbol_rename", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"from": "OldName"},
	})
	if err == nil {
		t.Fatalf("expected error for missing to")
	}
}

func TestSymbolRename_IdenticalNames(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "symbol_rename", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"from": "Foo", "to": "Foo"},
	})
	if err == nil {
		t.Fatalf("expected error for identical from/to")
	}
}

func TestSymbolRename_NotFound(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc main() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "symbol_rename", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"from": "NonExistent", "to": "NewName"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data := res.Data["impact"].(renameImpact)
	if data.Locations != 0 {
		t.Errorf("expected 0 locations, got %d", data.Locations)
	}
}

func TestSymbolRename_DryRunNoModification(t *testing.T) {
	tmp := t.TempDir()
	fpath := filepath.Join(tmp, "main.go")
	os.WriteFile(fpath, []byte("package main\nfunc oldName() { oldName() }\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "symbol_rename", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "oldName",
			"to":      "newName",
			"file":    "main.go",
			"dry_run": true,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// File should NOT be modified
	data, _ := os.ReadFile(fpath)
	if string(data) != "package main\nfunc oldName() { oldName() }\n" {
		t.Errorf("file was modified despite dry_run=true")
	}
	impact := res.Data["impact"].(renameImpact)
	if impact.Locations == 0 {
		t.Errorf("expected at least 1 location found")
	}
}

func TestSymbolRename_DryRunImpact(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc Foo() {}\ntype Bar struct{}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "symbol_rename", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "Foo",
			"to":      "Baz",
			"file":    "main.go",
			"dry_run": true,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	impact := res.Data["impact"].(renameImpact)
	if impact.Files != 1 {
		t.Errorf("want 1 file, got %d", impact.Files)
	}
	if impact.Locations == 0 {
		t.Errorf("expected at least 1 location")
	}
}

func TestSymbolRename_FullProject(t *testing.T) {
	tmp := t.TempDir()
	for _, fname := range []string{"a.go", "b.go"} {
		os.WriteFile(filepath.Join(tmp, fname), []byte("package main\nfunc Target() {}\n"), 0644)
	}

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "symbol_rename", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from": "Target",
			"to":   "Renamed",
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	impact := res.Data["impact"].(renameImpact)
	if impact.Files != 2 {
		t.Errorf("want 2 files, got %d", impact.Files)
	}
	if impact.Locations != 2 {
		t.Errorf("want 2 locations, got %d", impact.Locations)
	}
}

func TestSymbolRename_SkipTests(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc Target() {}\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "main_test.go"), []byte("package main\nfunc TestTarget() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "symbol_rename", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":       "Target",
			"to":         "Renamed",
			"skip_tests": true,
			"dry_run":    true,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	impact := res.Data["impact"].(renameImpact)
	if impact.Files != 1 {
		t.Errorf("want 1 file (test skipped), got %d", impact.Files)
	}
}

func TestSymbolRename_KindFilter(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc Foo() {}\ntype Foo struct {}\nvar Foo = 1\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "symbol_rename", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":     "Foo",
			"to":       "Bar",
			"file":     "main.go",
			"kind":     "func",
			"dry_run":  true,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	impact := res.Data["impact"].(renameImpact)
	// Only 1 func Foo should match
	if impact.Locations != 1 {
		t.Errorf("want 1 func match, got %d", impact.Locations)
	}
}

func TestSymbolRename_DoesNotMatchSubstring(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc FooBar() {}\nfunc Foo() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "symbol_rename", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "Foo",
			"to":      "Baz",
			"file":    "main.go",
			"dry_run": true,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	impact := res.Data["impact"].(renameImpact)
	// Only standalone Foo, not FooBar
	if impact.Locations != 1 {
		t.Errorf("want 1 match (not FooBar), got %d", impact.Locations)
	}
}

func TestSymbolRenameTool_Name(t *testing.T) {
	tool := NewSymbolRenameTool()
	if tool.Name() != "symbol_rename" {
		t.Errorf("want symbol_rename, got %s", tool.Name())
	}
}

func TestSymbolRenameTool_Spec(t *testing.T) {
	tool := NewSymbolRenameTool()
	spec := tool.Spec()
	if spec.Name != "symbol_rename" {
		t.Errorf("spec.Name: want symbol_rename, got %s", spec.Name)
	}
	if spec.Risk != RiskWrite {
		t.Errorf("spec.Risk: want RiskWrite, got %v", spec.Risk)
	}
	argsByName := make(map[string]Arg)
	for _, a := range spec.Args {
		argsByName[a.Name] = a
	}
	for _, name := range []string{"from", "to", "file", "kind", "dry_run", "skip_tests"} {
		if _, ok := argsByName[name]; !ok {
			t.Errorf("spec.Args missing %s", name)
		}
	}
}

func TestSymbolRenameTool_Description(t *testing.T) {
	tool := NewSymbolRenameTool()
	if tool.Description() == "" {
		t.Errorf("description is empty")
	}
}

func TestSymbolRenameTool_SetEngine(t *testing.T) {
	tool := NewSymbolRenameTool()
	eng := New(*config.DefaultConfig())
	tool.SetEngine(eng)
	if tool.Name() != "symbol_rename" {
		t.Errorf("name mismatch")
	}
}

func TestApplyRenameInLine(t *testing.T) {
	cases := []struct {
		line  string
		from  string
		to    string
		want  string
	}{
		{"func Foo()", "Foo", "Bar", "func Bar()"},
		{"var x = Foo", "Foo", "Bar", "var x = Bar"},
		{"Foo + Foo", "Foo", "Bar", "Bar + Bar"},
		{"FooBar", "Foo", "Bar", "FooBar"},
		{"xFoo", "Foo", "Bar", "xFoo"},
		{"Foo", "Foo", "Bar", "Bar"},
	}
	for _, c := range cases {
		got := applyRenameInLine(c.line, c.from, c.to)
		if got != c.want {
			t.Errorf("applyRenameInLine(%q, %q, %q): got %q, want %q", c.line, c.from, c.to, got, c.want)
		}
	}
}

func TestMatchSymbolKind(t *testing.T) {
	cases := []struct {
		line  string
		name  string
		kind  string
		match bool
	}{
		{"func Foo() {}", "Foo", "func", true},
		{"type Foo int", "Foo", "type", true},
		{"var Foo = 1", "Foo", "var", true},
		{"const Foo = 1", "Foo", "const", true},
		{"(s *Server) Foo()", "Foo", "method", true},
		{"func Bar() {}", "Foo", "func", false},
		{"type Bar int", "Foo", "type", false},
		{"func Foo() {}", "Foo", "all", true},
		{"someRandomText", "Foo", "all", true},
	}
	for _, c := range cases {
		got := matchSymbolKind(c.line, c.name, c.kind)
		if got != c.match {
			t.Errorf("matchSymbolKind(%q, %q, %q): got %v, want %v", c.line, c.name, c.kind, got, c.match)
		}
	}
}

func TestInCommentOrString(t *testing.T) {
	cases := []struct {
		line   string
		name   string
		inComm bool
	}{
		{"// Foo comment", "Foo", true},
		{"func Foo() {}", "Foo", false},
		{"/* Foo block */", "Foo", true},
	}
	for _, c := range cases {
		got := inCommentOrString(c.line, c.name)
		if got != c.inComm {
			t.Errorf("inCommentOrString(%q, %q): got %v, want %v", c.line, c.name, got, c.inComm)
		}
	}
}

func TestCollectGoFiles(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "sub"), 0755)
	os.WriteFile(filepath.Join(tmp, "a.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "sub", "b.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "c.txt"), []byte("not go"), 0644)

	files := collectGoFiles(tmp, false)
	if len(files) != 2 {
		t.Errorf("want 2 .go files, got %d: %v", len(files), files)
	}
}

func TestCollectGoFiles_SkipTests(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "main_test.go"), []byte("package main\n"), 0644)

	files := collectGoFiles(tmp, true)
	if len(files) != 1 {
		t.Errorf("want 1 non-test file, got %d", len(files))
	}
}

func TestSymbolRename_MultiLine(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\n// Foo comment\nfunc Foo() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "symbol_rename", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "Foo",
			"to":      "Bar",
			"file":    "main.go",
			"dry_run": true,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	changes := res.Data["changes"].([]renameChange)
	// Should only match the function, not the comment
	if len(changes) != 1 {
		t.Errorf("expected 1 change (func only), got %d", len(changes))
	}
}

func TestSymbolRename_OutputSummary(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc Foo() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "symbol_rename", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "Foo",
			"to":      "Bar",
			"file":    "main.go",
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

func TestSymbolRename_ChangesIncludeOldAndNew(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc Foo() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "symbol_rename", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"from":    "Foo",
			"to":      "Bar",
			"file":    "main.go",
			"dry_run": true,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	changes := res.Data["changes"].([]renameChange)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Old == "" || changes[0].New == "" {
		t.Errorf("change missing old/new content")
	}
	if changes[0].Line == 0 {
		t.Errorf("change missing line number")
	}
}

// TestSymbolRename_RejectsTraversalFile pins VULN-015: a `file`
// parameter that escapes the project root via `..` must be refused
// before the `os.ReadFile` reads outside the project — earlier
// versions surfaced foreign file content via `changes[].fullLine`.
func TestSymbolRename_RejectsTraversalFile(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc Foo() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	cases := []string{
		"../escape.go",
		"../../etc/hosts",
		"sub/../../escape.go",
	}
	for _, file := range cases {
		_, err := eng.Execute(context.Background(), "symbol_rename", Request{
			ProjectRoot: tmp,
			Params: map[string]any{
				"from": "Foo",
				"to":   "Bar",
				"file": file,
			},
		})
		if err == nil {
			t.Errorf("file=%q must be rejected as outside project root", file)
		} else if !strings.Contains(err.Error(), "outside") && !strings.Contains(err.Error(), "root") {
			t.Errorf("file=%q rejection should mention 'outside'/'root', got: %v", file, err)
		}
	}
}
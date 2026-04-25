package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestSemanticSearch_MissingQuery(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "semantic_search", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{},
	})
	if err == nil {
		t.Fatalf("expected error for missing query")
	}
}

func TestSemanticSearch_FileScope(t *testing.T) {
	tmp := t.TempDir()
	fpath := filepath.Join(tmp, "main.go")
	os.WriteFile(fpath, []byte("package main\nfunc foo() {}\nfunc bar() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "semantic_search", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"query": "FunctionDecl:name=foo",
			"file":  "main.go",
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data := res.Data["matches"].([]semanticMatch)
	if len(data) != 1 {
		t.Errorf("want 1 match, got %d", len(data))
	}
	if len(data) > 0 && data[0].Name != "foo" {
		t.Errorf("want name=foo, got %s", data[0].Name)
	}
}

func TestSemanticSearch_ProjectScope(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "a.go"), []byte("package main\nfunc alpha() {}\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "b.go"), []byte("package main\nfunc beta() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "semantic_search", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"query": "FunctionDecl:*",
			"lang":  "go",
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data := res.Data["matches"].([]semanticMatch)
	if len(data) < 2 {
		t.Errorf("want at least 2 matches, got %d", len(data))
	}
}

func TestSemanticSearch_TypeDecl(t *testing.T) {
	tmp := t.TempDir()
	fpath := filepath.Join(tmp, "main.go")
	os.WriteFile(fpath, []byte("package main\ntype Foo struct{}\ntype Bar int\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "semantic_search", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"query": "TypeDecl:name=Foo",
			"file":  "main.go",
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data := res.Data["matches"].([]semanticMatch)
	if len(data) != 1 {
		t.Errorf("want 1 match, got %d", len(data))
	}
}

func TestSemanticSearch_MaxResults(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc a() {}\nfunc b() {}\nfunc c() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "semantic_search", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"query":       "FunctionDecl:*",
			"file":        "main.go",
			"max_results": 1,
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data := res.Data["matches"].([]semanticMatch)
	if len(data) != 1 {
		t.Errorf("want 1 match (max_results cap), got %d", len(data))
	}
}

func TestSemanticSearch_LangFilter(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc foo() {}\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "util.ts"), []byte("export function bar() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "semantic_search", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"query": "FunctionDecl:*",
			"lang":  "go",
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data := res.Data["matches"].([]semanticMatch)
	// Should only find main.go, not util.ts
	goFound := false
	for _, m := range data {
		if m.Path == filepath.Join(tmp, "main.go") {
			goFound = true
		}
	}
	if !goFound {
		t.Errorf("should find main.go with lang=go")
	}
}

func TestSemanticSearchTool_Name(t *testing.T) {
	tool := NewSemanticSearchTool()
	if tool.Name() != "semantic_search" {
		t.Errorf("want semantic_search, got %s", tool.Name())
	}
}

func TestSemanticSearchTool_Spec(t *testing.T) {
	tool := NewSemanticSearchTool()
	spec := tool.Spec()
	if spec.Name != "semantic_search" {
		t.Errorf("spec.Name: want semantic_search, got %s", spec.Name)
	}
	if spec.Risk != RiskRead {
		t.Errorf("spec.Risk: want RiskRead, got %v", spec.Risk)
	}
	argsByName := make(map[string]Arg)
	for _, a := range spec.Args {
		argsByName[a.Name] = a
	}
	for _, name := range []string{"query", "file", "lang", "max_results"} {
		if _, ok := argsByName[name]; !ok {
			t.Errorf("spec.Args missing %s", name)
		}
	}
}

func TestSemanticSearchTool_Description(t *testing.T) {
	tool := NewSemanticSearchTool()
	if tool.Description() == "" {
		t.Errorf("description is empty")
	}
}

func TestSemanticSearchTool_New(t *testing.T) {
	tool := NewSemanticSearchTool()
	if tool.Name() != "semantic_search" {
		t.Errorf("want semantic_search, got %s", tool.Name())
	}
}

func TestParseQuery(t *testing.T) {
	cases := []struct {
		input       string
		nodeType    string
		name        string
		typeFilter  string
		context     int
	}{
		{"FunctionCall:name=foo", "FunctionCall", "foo", "", 0},
		{"FunctionDecl:name=bar", "FunctionDecl", "bar", "", 0},
		{"ReturnStmt:type=error", "ReturnStmt", "", "error", 0},
		{"IfStmt:context=2", "IfStmt", "", "", 2},
		{"FieldDecl:name=X:type=int", "FieldDecl", "X", "int", 0},
		{"FunctionDecl", "FunctionDecl", "", "", 0},
		{"VarDecl", "VarDecl", "", "", 0},
	}
	for _, c := range cases {
		pq := parseQuery(c.input)
		if pq.nodeType != c.nodeType {
			t.Errorf("parseQuery(%q): nodeType got %q, want %q", c.input, pq.nodeType, c.nodeType)
		}
		if pq.name != c.name {
			t.Errorf("parseQuery(%q): name got %q, want %q", c.input, pq.name, c.name)
		}
		if pq.typeFilter != c.typeFilter {
			t.Errorf("parseQuery(%q): typeFilter got %q, want %q", c.input, pq.typeFilter, c.typeFilter)
		}
		if pq.context != c.context {
			t.Errorf("parseQuery(%q): context got %d, want %d", c.input, pq.context, c.context)
		}
	}
}

func TestNameMatches(t *testing.T) {
	cases := []struct {
		symName string
		pattern string
		match   bool
	}{
		{"foo", "foo", true},
		{"foo", "bar", false},
		{"fooBar", "*Bar*", true},
		{"fooBar", "foo*", true},
		{"foo", "*", true},
		{"foo", "name=foo", true},
	}
	for _, c := range cases {
		got := patternNameMatches(c.symName, c.pattern)
		if got != c.match {
			t.Errorf("patternNameMatches(%q, %q): got %v, want %v", c.symName, c.pattern, got, c.match)
		}
	}
}

func TestSemanticSearch_NoFiles(t *testing.T) {
	tmp := t.TempDir()
	// Write a non-Go file
	os.WriteFile(filepath.Join(tmp, "readme.md"), []byte("# readme\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "semantic_search", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"query": "FunctionDecl:*",
			"lang":  "go",
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data := res.Data["matches"].([]semanticMatch)
	if len(data) != 0 {
		t.Errorf("want 0 matches for non-go project, got %d", len(data))
	}
}

func TestSemanticSearch_TotalField(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc alpha() {}\nfunc beta() {}\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "semantic_search", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"query": "FunctionDecl:*",
			"file":  "main.go",
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Data["total"] == nil {
		t.Errorf("expected total field")
	}
}

// TestSemanticSearch_RejectsTraversalFile pins VULN-016: a `file`
// parameter that escapes the project root must be refused — the
// returned `Snippet` and `ContextLines` surface file content.
func TestSemanticSearch_RejectsTraversalFile(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)
	tmp := t.TempDir()

	cases := []string{
		"../escape.go",
		"../../etc/hosts",
	}
	for _, file := range cases {
		_, err := eng.Execute(context.Background(), "semantic_search", Request{
			ProjectRoot: tmp,
			Params: map[string]any{
				"query": "func",
				"file":  file,
			},
		})
		if err == nil {
			t.Errorf("file=%q must be rejected as outside project root", file)
		} else if !strings.Contains(err.Error(), "outside") && !strings.Contains(err.Error(), "root") {
			t.Errorf("file=%q rejection should mention 'outside'/'root', got: %v", file, err)
		}
	}
}

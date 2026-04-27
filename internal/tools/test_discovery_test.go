package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestTestDiscovery_GoCompanion(t *testing.T) {
	tmp := t.TempDir()
	src := `package foo
func Add(a, b int) int { return a + b }
`
	if err := os.WriteFile(filepath.Join(tmp, "calc.go"), []byte(src), 0644); err != nil {
		t.Fatalf("write calc.go: %v", err)
	}
	test := `package foo
func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Error("fail")
	}
}
func BenchmarkAdd(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Add(2, 3)
	}
}
func ExampleAdd() {
	Add(1, 1)
}
`
	if err := os.WriteFile(filepath.Join(tmp, "calc_test.go"), []byte(test), 0644); err != nil {
		t.Fatalf("write calc_test.go: %v", err)
	}

	eng := New(*config.DefaultConfig())
	_, _ = eng.Execute(context.Background(), "read_file", Request{ProjectRoot: tmp, Params: map[string]any{"path": "calc.go"}})
	res, err := eng.Execute(context.Background(), "test_discovery", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"target": "calc.go"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, ok := res.Data["files"].([]map[string]any)
	if !ok || len(data) == 0 {
		t.Fatalf("want at least one file, got %+v", res.Data)
	}
	funcs := data[0]["functions"].([]map[string]any)
	if len(funcs) < 3 {
		t.Errorf("want 3 funcs (Test, Benchmark, Example), got %d", len(funcs))
	}
}

func TestTestDiscovery_GoGlobPattern(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "foo_test.go"), []byte(`package foo
func TestFoo(t *testing.T) {}
func TestBar(t *testing.T) {}
`), 0644)
	os.WriteFile(filepath.Join(tmp, "bar_test.go"), []byte(`package bar
func TestBaz(t *testing.T) {}
`), 0644)

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "test_discovery", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"pattern": "**/*_test.go"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, ok := res.Data["files"].([]map[string]any)
	if !ok {
		t.Fatalf("expected []data, got %T", res.Data["files"])
	}
	if len(data) != 2 {
		t.Errorf("want 2 test files, got %d", len(data))
	}
}

func TestTestDiscovery_SymbolFilter(t *testing.T) {
	tmp := t.TempDir()
	// TestSub name doesn't contain "add" and its preceding comment "sub test"
	// also doesn't contain "add", so TestSub gets filtered out.
	os.WriteFile(filepath.Join(tmp, "math_test.go"), []byte(`package math
func TestAdd(t *testing.T) {}
// sub test helper
func TestSub(t *testing.T) {}
func BenchmarkMul(b *testing.B) {}
`), 0644)

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "test_discovery", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"pattern": "**/*_test.go", "symbol": "add"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, ok := res.Data["files"].([]map[string]any)
	if !ok || len(data) == 0 {
		t.Fatalf("want results, got %+v", res.Data)
	}
	funcs := data[0]["functions"].([]map[string]any)
	if len(funcs) != 1 {
		t.Errorf("want 1 func matching 'add', got %d", len(funcs))
	}
}

func TestTestDiscovery_LanguageFilter(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "sample_test.py"), []byte(`import unittest
class TestMath(unittest.TestCase):
    def test_add(self): pass
`), 0644)
	os.WriteFile(filepath.Join(tmp, "sample_test.go"), []byte(`package sample
func TestSample(t *testing.T) {}
`), 0644)

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "test_discovery", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"pattern": "**/*_test.*", "language": "go"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, ok := res.Data["files"].([]map[string]any)
	if !ok {
		t.Fatalf("got %T", res.Data["files"])
	}
	if len(data) != 1 {
		t.Errorf("want 1 go file, got %d", len(data))
	}
	if data[0]["path"] != "sample_test.go" {
		t.Errorf("expected sample_test.go, got %v", data[0]["path"])
	}
}

func TestTestDiscovery_MaxFiles(t *testing.T) {
	tmp := t.TempDir()
	for range 10 {
		os.WriteFile(filepath.Join(tmp, "pkg_test.go"), []byte(`package pkg
func TestX(t *testing.T) {}
`), 0644)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "test_discovery", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"pattern": "**/*_test.go", "max_files": 3},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	count := res.Data["count"].(int)
	if count > 3 {
		t.Errorf("want at most 3 files, got %d", count)
	}
}

func TestTestDiscovery_PythonCompanion(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "calc.py"), []byte(`def add(a, b): return a + b`), 0644)
	os.WriteFile(filepath.Join(tmp, "test_calc.py"), []byte(`import pytest
def test_add():
    assert add(2, 3) == 5
`), 0644)
	os.MkdirAll(filepath.Join(tmp, "tests"), 0755)
	os.WriteFile(filepath.Join(tmp, "tests", "calc_test.py"), []byte(`import pytest
def test_sub():
    assert True
`), 0644)

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "test_discovery", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"target": "calc.py"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	count := res.Data["count"].(int)
	if count < 1 {
		t.Errorf("want at least 1 python test file, got %d", count)
	}
}

func TestTestDiscovery_NoFilesFound(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte(`package main`), 0644)

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "test_discovery", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"target": "main.go"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Output != "no test files found" {
		t.Errorf("expected 'no test files found', got %q", res.Output)
	}
}

func TestTestDiscovery_MissingParams(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "test_discovery", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err == nil {
		t.Fatalf("expected error for missing target/pattern")
	}
}
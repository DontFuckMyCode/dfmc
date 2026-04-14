package ast

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseFile_GoSymbolsAndImports(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sample.go")
	src := `package sample

import "fmt"

type User struct {}

func Hello(name string) string {
    return fmt.Sprintf("hi %s", name)
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	e := New()
	res, err := e.ParseFile(context.Background(), path)
	if err != nil {
		t.Fatalf("parse file: %v", err)
	}

	if res.Language != "go" {
		t.Fatalf("expected go language, got %s", res.Language)
	}
	if len(res.Symbols) < 2 {
		t.Fatalf("expected at least 2 symbols, got %d", len(res.Symbols))
	}
	if len(res.Imports) != 1 || res.Imports[0] != "fmt" {
		t.Fatalf("expected import fmt, got %#v", res.Imports)
	}
}

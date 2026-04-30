package ast

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// BenchmarkExtractGoSymbols_RegexFallback locks in the regex-hoisting
// win. Pre-fix the function rebuilt 6 regexes per call (extractGoSymbols)
// + 1 (splitIdentifierList, called per matched const/var line) + 1
// (extractGoImports's reQuoted), so a Go file in the regex-fallback path
// (CGO off, or tree-sitter parse failed) cost ~28µs of regex compile
// overhead alone. Post-fix that's ~7-8µs total — the fixed cost moves
// to package init (one compile per regex per process). On a 10K-file
// !cgo codemap rebuild that's roughly 200ms saved.
//
// If a contributor ever puts these regexes back inside the function body
// this benchmark won't catch it directly, but the wall-clock will
// regress noticeably and BenchmarkExtractGoImports_RegexFallback below
// pins extractGoImports's reGoQuotedImport hoist by exercising the
// import path with a small file.
func BenchmarkExtractGoSymbols_RegexFallback(b *testing.B) {
	src := []byte(`package main

import "fmt"

func add(a, b int) int { return a + b }
func sub(x, y int) int { return x - y }

type User struct { Name string }

func (u User) Greet() string { return "hi " + u.Name }
func (u *User) SetName(n string) { u.Name = n }

const X = 1
var Y = 2
const (
	A = 1
	B = 2
)
var (
	P = 1
	Q = 2
)
type T interface {
	Foo() string
	Bar() int
}
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractGoSymbols("test.go", "go", src)
	}
}

func BenchmarkExtractGoImports_RegexFallback(b *testing.B) {
	src := []byte(`package main

import (
	"fmt"
	"strings"
	"github.com/dontfuckmycode/dfmc/internal/ast"
)

import "os"
`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractGoImports(src)
	}
}

// TestEngine_DetectLanguageConcurrentRead exercises the read-only-map
// invariant for Engine.extToLang. The map is built once in the
// constructor and never mutated, so concurrent reads must be race-free.
// Run with -race to validate (CI does); without it this still catches
// any future change that quietly introduces a write path. We deliberately
// hit the 3-arg detectLanguage (which reads extToLang twice — once for
// extension, once for basename) from many goroutines to maximize the
// chance of surfacing a torn read if someone later adds a writer.
func TestEngine_DetectLanguageConcurrentRead(t *testing.T) {
	e := New()
	const goroutines = 32
	const iters = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				// Mix of extension hits, basename hits, and misses.
				_ = e.detectLanguage("foo.go", nil)
				_ = e.detectLanguage("Dockerfile", nil)
				_ = e.detectLanguage("foo.unknown", nil)
				_ = e.detectLanguage("script", []byte("#!/usr/bin/env python\n"))
			}
		}()
	}
	wg.Wait()
}

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

func TestParseFile_GoBlockImportsAndGroupedDecls(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sample.go")
	src := `package sample

import (
    "fmt"
    alias "net/http"
    _ "unsafe"
)

type Service interface {
    Serve()
}

const (
    Ready = true
    Count, Total = 1, 2
)

var (
    Name string
    Enabled, Visible bool
)

func (s *Server) Serve() {}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	e := New()
	res, err := e.ParseFile(context.Background(), path)
	if err != nil {
		t.Fatalf("parse file: %v", err)
	}

	expectImports := map[string]bool{
		"fmt":      false,
		"net/http": false,
		"unsafe":   false,
	}
	for _, item := range res.Imports {
		if _, ok := expectImports[item]; ok {
			expectImports[item] = true
		}
	}
	for item, seen := range expectImports {
		if !seen {
			t.Fatalf("expected import %q in %#v", item, res.Imports)
		}
	}

	expected := map[string]types.SymbolKind{
		"Service": types.SymbolInterface,
		"Ready":   types.SymbolConstant,
		"Count":   types.SymbolConstant,
		"Total":   types.SymbolConstant,
		"Name":    types.SymbolVariable,
		"Enabled": types.SymbolVariable,
		"Visible": types.SymbolVariable,
		"Serve":   types.SymbolMethod,
	}
	found := map[string]types.SymbolKind{}
	for _, sym := range res.Symbols {
		found[sym.Name] = sym.Kind
	}
	for name, kind := range expected {
		if found[name] != kind {
			t.Fatalf("expected symbol %s to have kind %s, got %s (all=%#v)", name, kind, found[name], res.Symbols)
		}
	}
}

func TestParseContent_TypeScriptModernDeclarations(t *testing.T) {
	e := New()
	src := []byte(`export default function makeThing() {}
export abstract class Service {}
export enum Mode { On, Off }
export const runTask = async task => task
`)
	res, err := e.ParseContent(context.Background(), "sample.ts", src)
	if err != nil {
		t.Fatalf("parse content: %v", err)
	}

	expected := map[string]types.SymbolKind{
		"makeThing": types.SymbolFunction,
		"Service":   types.SymbolClass,
		"Mode":      types.SymbolEnum,
		"runTask":   types.SymbolFunction,
	}
	found := map[string]types.SymbolKind{}
	for _, sym := range res.Symbols {
		found[sym.Name] = sym.Kind
	}
	for name, kind := range expected {
		if found[name] != kind {
			t.Fatalf("expected symbol %s to have kind %s, got %s (all=%#v)", name, kind, found[name], res.Symbols)
		}
	}
}

func TestParseContent_PythonAsyncFunction(t *testing.T) {
	e := New()
	src := []byte(`class Service:
    pass

async def fetch_data(client):
    return client
`)
	res, err := e.ParseContent(context.Background(), "sample.py", src)
	if err != nil {
		t.Fatalf("parse content: %v", err)
	}

	expected := map[string]types.SymbolKind{
		"Service":    types.SymbolClass,
		"fetch_data": types.SymbolFunction,
	}
	found := map[string]types.SymbolKind{}
	for _, sym := range res.Symbols {
		found[sym.Name] = sym.Kind
	}
	for name, kind := range expected {
		if found[name] != kind {
			t.Fatalf("expected symbol %s to have kind %s, got %s (all=%#v)", name, kind, found[name], res.Symbols)
		}
	}
}

func TestBackendStatus_LanguageMatrix(t *testing.T) {
	e := New()
	status := e.BackendStatus()
	if status.Preferred == "" {
		t.Fatal("expected preferred backend to be populated")
	}
	if len(status.Languages) == 0 {
		t.Fatal("expected language matrix to be populated")
	}

	expected := map[string]bool{
		"go":         false,
		"javascript": false,
		"jsx":        false,
		"typescript": false,
		"tsx":        false,
		"python":     false,
	}
	for _, item := range status.Languages {
		if _, ok := expected[item.Language]; ok {
			expected[item.Language] = true
		}
		if item.Preferred != "tree-sitter" {
			t.Fatalf("expected %q preferred backend to be tree-sitter, got %q", item.Language, item.Preferred)
		}
		if item.Active == "" {
			t.Fatalf("expected %q active backend to be populated", item.Language)
		}
	}
	for lang, seen := range expected {
		if !seen {
			t.Fatalf("expected %q in backend language matrix: %#v", lang, status.Languages)
		}
	}
}

func TestParseMetrics_TracksRequestsCacheAndBackend(t *testing.T) {
	e := New()
	src := []byte(`package sample

func Hello() {}
`)

	if _, err := e.ParseContent(context.Background(), "sample.go", src); err != nil {
		t.Fatalf("first parse: %v", err)
	}
	if _, err := e.ParseContent(context.Background(), "sample.go", src); err != nil {
		t.Fatalf("second parse: %v", err)
	}

	metrics := e.ParseMetrics()
	if metrics.Requests != 2 {
		t.Fatalf("expected 2 requests, got %d", metrics.Requests)
	}
	if metrics.Parsed != 1 {
		t.Fatalf("expected 1 parsed file, got %d", metrics.Parsed)
	}
	if metrics.CacheHits != 1 || metrics.CacheMisses != 1 {
		t.Fatalf("expected cache hit/miss to be 1/1, got %d/%d", metrics.CacheHits, metrics.CacheMisses)
	}
	if metrics.LastLanguage != "go" {
		t.Fatalf("expected last language go, got %q", metrics.LastLanguage)
	}
	if metrics.LastBackend == "" {
		t.Fatal("expected last backend to be populated")
	}
	if metrics.ByLanguage["go"] != 1 {
		t.Fatalf("expected go parse count to be 1, got %d", metrics.ByLanguage["go"])
	}
	if metrics.ByBackend[metrics.LastBackend] != 1 {
		t.Fatalf("expected backend %q parse count to be 1, got %d", metrics.LastBackend, metrics.ByBackend[metrics.LastBackend])
	}
}

func TestEngineCloseClearsCacheAndMetrics(t *testing.T) {
	e := New()
	src := []byte("package sample\n\nfunc Hello() {}\n")

	if _, err := e.ParseContent(context.Background(), "sample.go", src); err != nil {
		t.Fatalf("first parse: %v", err)
	}
	if _, err := e.ParseContent(context.Background(), "sample.go", src); err != nil {
		t.Fatalf("second parse: %v", err)
	}
	before := e.ParseMetrics()
	if before.CacheHits != 1 {
		t.Fatalf("expected cache hit before close, got %+v", before)
	}

	if err := e.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	afterClose := e.ParseMetrics()
	if afterClose.Requests != 0 || afterClose.Parsed != 0 || afterClose.CacheHits != 0 || afterClose.CacheMisses != 0 {
		t.Fatalf("close must reset metrics, got %+v", afterClose)
	}

	if _, err := e.ParseContent(context.Background(), "sample.go", src); err != nil {
		t.Fatalf("parse after close: %v", err)
	}
	afterReparse := e.ParseMetrics()
	if afterReparse.CacheMisses != 1 || afterReparse.CacheHits != 0 || afterReparse.Parsed != 1 {
		t.Fatalf("expected cache to be empty after close, got %+v", afterReparse)
	}
}

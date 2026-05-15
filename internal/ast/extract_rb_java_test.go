package ast

import (
	"context"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// TestParseContent_RubyClassModuleDef pins the regex extractor for
// Ruby. Classes, modules, and methods (def, def self.) all surface
// as named symbols.
func TestParseContent_RubyClassModuleDef(t *testing.T) {
	eng := New()
	defer eng.Close()

	src := []byte(`require "json"
require_relative "./helper"

module Billing
  class Invoice
    def initialize(id)
      @id = id
    end

    def self.find(id)
      Invoice.new(id)
    end
  end
end
`)

	res, err := eng.ParseContent(context.Background(), "billing.rb", src)
	if err != nil {
		t.Fatalf("ParseContent error: %v", err)
	}

	want := map[string]types.SymbolKind{
		"Billing":    types.SymbolClass, // module surfaced as class
		"Invoice":    types.SymbolClass,
		"initialize": types.SymbolFunction,
		"find":       types.SymbolFunction,
	}
	got := map[string]types.SymbolKind{}
	for _, sym := range res.Symbols {
		got[sym.Name] = sym.Kind
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("ruby symbol %q: want kind %q, got %q (all=%v)",
				name, kind, got[name], res.Symbols)
		}
	}

	// Imports: both require + require_relative should be captured.
	wantImports := map[string]bool{"json": false, "./helper": false}
	for _, imp := range res.Imports {
		if _, ok := wantImports[imp]; ok {
			wantImports[imp] = true
		}
	}
	for imp, seen := range wantImports {
		if !seen {
			t.Errorf("ruby import %q missing from %v", imp, res.Imports)
		}
	}
}

// TestParseContent_RubyMethodNameWithBangPredicate pins that the def
// regex accepts Ruby's `?` / `!` method-name suffix conventions.
func TestParseContent_RubyMethodNameWithBangPredicate(t *testing.T) {
	eng := New()
	defer eng.Close()

	src := []byte(`class User
  def admin?
    @role == "admin"
  end

  def save!
    raise "boom"
  end
end
`)
	res, err := eng.ParseContent(context.Background(), "user.rb", src)
	if err != nil {
		t.Fatalf("ParseContent error: %v", err)
	}
	want := []string{"admin?", "save!"}
	have := map[string]bool{}
	for _, sym := range res.Symbols {
		have[sym.Name] = true
	}
	for _, name := range want {
		if !have[name] {
			t.Errorf("expected ruby symbol %q (with bang/predicate suffix), got %v",
				name, res.Symbols)
		}
	}
}

// TestParseContent_JavaClassesInterfacesEnumsMethods pins the regex
// extractor for Java: every top-level declaration kind plus method
// names with mixed modifier orderings.
func TestParseContent_JavaClassesInterfacesEnumsMethods(t *testing.T) {
	eng := New()
	defer eng.Close()

	src := []byte(`package com.example.app;

import java.util.List;
import java.util.Map;
import static java.util.Collections.emptyList;

public class Server {
    private final int port;

    public Server(int port) {
        this.port = port;
    }

    public static void main(String[] args) {
        new Server(8080);
    }
}

interface Handler {
    void handle();
}

public enum Status {
    OK, ERROR;
}
`)

	res, err := eng.ParseContent(context.Background(), "Server.java", src)
	if err != nil {
		t.Fatalf("ParseContent error: %v", err)
	}

	want := map[string]types.SymbolKind{
		"Server":  types.SymbolClass,
		"Handler": types.SymbolInterface,
		"Status":  types.SymbolEnum,
		"main":    types.SymbolFunction,
	}
	got := map[string]types.SymbolKind{}
	for _, sym := range res.Symbols {
		got[sym.Name] = sym.Kind
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("java symbol %q: want kind %q, got %q (all=%v)",
				name, kind, got[name], res.Symbols)
		}
	}

	// Imports + package: all four entries (3 imports + 1 package) should
	// surface in res.Imports. Static imports keep the fully-qualified
	// member name; that's expected for downstream symbol resolution.
	wantImports := []string{
		"com.example.app",
		"java.util.List",
		"java.util.Map",
		"java.util.Collections.emptyList",
	}
	have := map[string]bool{}
	for _, imp := range res.Imports {
		have[imp] = true
	}
	for _, imp := range wantImports {
		if !have[imp] {
			t.Errorf("java import %q missing from %v", imp, res.Imports)
		}
	}
}

// TestParseContent_JavaConstructorAndMethodOverloads pins that
// constructors (return-type-less `Server(...)`) and overloaded methods
// (`void handle()` + `void handle(int)`) both extract by name. Java
// allows same-name overloads -- our extractor just produces a Symbol
// per declaration site; callers dedupe at higher layers if needed.
func TestParseContent_JavaConstructorAndMethodOverloads(t *testing.T) {
	eng := New()
	defer eng.Close()

	src := []byte(`public class Foo {
    public Foo() {}
    public Foo(int n) {}
    public void run() {}
    public void run(int times) {}
}
`)
	res, err := eng.ParseContent(context.Background(), "Foo.java", src)
	if err != nil {
		t.Fatalf("ParseContent error: %v", err)
	}
	counts := map[string]int{}
	for _, sym := range res.Symbols {
		counts[sym.Name]++
	}
	// Foo class itself appears once; constructor `Foo()` matches the
	// method regex so it would surface as a second "Foo" -- this is
	// acceptable (callers see both declarations). What we care about:
	// `run` appears at least twice (both overloads extracted).
	if counts["run"] < 2 {
		t.Errorf("expected at least 2 'run' overloads, got %d (all=%v)",
			counts["run"], res.Symbols)
	}
}

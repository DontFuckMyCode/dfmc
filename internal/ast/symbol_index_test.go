package ast

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestSymbolIndex_BuildSkipsNonCallableKinds(t *testing.T) {
	files := []*ParseResult{
		{
			Path:     "a.go",
			Language: "go",
			Symbols: []types.Symbol{
				{Name: "Run", Kind: types.SymbolFunction, Line: 10},
				{Name: "User", Kind: types.SymbolClass, Line: 20},
				{Name: "Foo", Kind: types.SymbolInterface, Line: 30},
				{Name: "Bar", Kind: types.SymbolType, Line: 40},
				{Name: "Color", Kind: types.SymbolEnum, Line: 50},
				{Name: "MaxSize", Kind: types.SymbolConstant, Line: 60},
				{Name: "count", Kind: types.SymbolVariable, Line: 70},
			},
		},
	}
	idx := BuildSymbolIndex(files)
	for _, want := range []string{"Run", "User", "Foo", "Bar", "Color"} {
		if got := idx.Lookup(want); len(got) != 1 {
			t.Errorf("expected %q to be indexed once, got %d defs", want, len(got))
		}
	}
	for _, skip := range []string{"MaxSize", "count"} {
		if got := idx.Lookup(skip); len(got) != 0 {
			t.Errorf("expected %q to be skipped (non-callable kind), got %d defs", skip, len(got))
		}
	}
}

func TestSymbolIndex_LookupReturnsCopy(t *testing.T) {
	files := []*ParseResult{
		{
			Path: "a.go", Language: "go",
			Symbols: []types.Symbol{{Name: "Run", Kind: types.SymbolFunction, Line: 1}},
		},
	}
	idx := BuildSymbolIndex(files)
	a := idx.Lookup("Run")
	a[0].Line = 999 // mutate the returned slice
	b := idx.Lookup("Run")
	if b[0].Line == 999 {
		t.Fatal("Lookup must return a fresh slice; mutation leaked back into the index")
	}
}

func TestSymbolIndex_NilSafety(t *testing.T) {
	var idx *SymbolIndex
	if got := idx.Lookup("x"); got != nil {
		t.Errorf("nil-receiver Lookup must return nil, got %v", got)
	}
	if got := idx.Resolve("a.go", "x"); got != nil {
		t.Errorf("nil-receiver Resolve must return nil, got %v", got)
	}
	if got := idx.Size(); got != 0 {
		t.Errorf("nil-receiver Size must return 0, got %d", got)
	}
	if got := idx.ResolveCalls("a.go", []Call{{Callee: "x", Line: 1}}); len(got) != 1 || got[0].Target != nil {
		t.Errorf("nil-receiver ResolveCalls must return unresolved entries, got %v", got)
	}
}

func TestSymbolIndex_ResolveSameFilePreferred(t *testing.T) {
	files := []*ParseResult{
		{Path: "a.go", Language: "go", Symbols: []types.Symbol{
			{Name: "Run", Kind: types.SymbolFunction, Line: 10},
		}},
		{Path: "b.go", Language: "go", Symbols: []types.Symbol{
			{Name: "Run", Kind: types.SymbolFunction, Line: 20},
		}},
	}
	idx := BuildSymbolIndex(files)
	got := idx.Resolve("b.go", "Run")
	if got == nil {
		t.Fatal("Resolve returned nil for known multi-file name with same-file fallback")
	}
	if got.File != "b.go" || got.Line != 20 {
		t.Fatalf("expected b.go:20 (caller's local), got %v:%d", got.File, got.Line)
	}
}

func TestSymbolIndex_ResolveSingleWorkspaceMatch(t *testing.T) {
	files := []*ParseResult{
		{Path: "lib/util.go", Language: "go", Symbols: []types.Symbol{
			{Name: "Helper", Kind: types.SymbolFunction, Line: 5},
		}},
	}
	idx := BuildSymbolIndex(files)
	got := idx.Resolve("caller.go", "Helper")
	if got == nil {
		t.Fatal("single-match Resolve returned nil")
	}
	if got.File != "lib/util.go" || got.Line != 5 {
		t.Fatalf("expected lib/util.go:5, got %v:%d", got.File, got.Line)
	}
}

func TestSymbolIndex_ResolveAmbiguousReturnsNil(t *testing.T) {
	files := []*ParseResult{
		{Path: "a.go", Language: "go", Symbols: []types.Symbol{
			{Name: "Save", Kind: types.SymbolFunction, Line: 10},
		}},
		{Path: "b.go", Language: "go", Symbols: []types.Symbol{
			{Name: "Save", Kind: types.SymbolFunction, Line: 20},
		}},
	}
	idx := BuildSymbolIndex(files)
	// Caller is neither file -- ambiguous, must NOT guess.
	got := idx.Resolve("caller.go", "Save")
	if got != nil {
		t.Fatalf("ambiguous resolve must return nil, got %v", got)
	}
}

func TestSymbolIndex_ResolveDottedUsesLastSegment(t *testing.T) {
	files := []*ParseResult{
		{Path: "os/path.py", Language: "python", Symbols: []types.Symbol{
			{Name: "join", Kind: types.SymbolFunction, Line: 100},
		}},
	}
	idx := BuildSymbolIndex(files)
	// Dotted callee -- "os.path.join" should resolve via "join".
	got := idx.Resolve("client.py", "os.path.join")
	if got == nil {
		t.Fatal("dotted Resolve returned nil")
	}
	if got.Name != "join" || got.File != "os/path.py" {
		t.Fatalf("expected join @ os/path.py, got %v", got)
	}
}

func TestSymbolIndex_ResolveUnknownReturnsNil(t *testing.T) {
	files := []*ParseResult{
		{Path: "a.go", Language: "go", Symbols: []types.Symbol{
			{Name: "Foo", Kind: types.SymbolFunction, Line: 1},
		}},
	}
	idx := BuildSymbolIndex(files)
	if got := idx.Resolve("caller.go", "Bar"); got != nil {
		t.Fatalf("unknown name must resolve to nil, got %v", got)
	}
	if got := idx.Resolve("caller.go", ""); got != nil {
		t.Fatalf("empty callee must resolve to nil, got %v", got)
	}
}

func TestSymbolIndex_ResolveCallsMixedHitsAndMisses(t *testing.T) {
	files := []*ParseResult{
		{Path: "lib.go", Language: "go", Symbols: []types.Symbol{
			{Name: "Helper", Kind: types.SymbolFunction, Line: 5},
		}},
	}
	idx := BuildSymbolIndex(files)
	calls := []Call{
		{Callee: "Helper", Line: 1},
		{Callee: "Unknown", Line: 2},
		{Callee: "pkg.Helper", Line: 3}, // dotted: resolves via "Helper"
	}
	edges := idx.ResolveCalls("caller.go", calls)
	if len(edges) != 3 {
		t.Fatalf("expected 3 edges, got %d", len(edges))
	}
	// Edge 0: Helper resolves.
	if edges[0].Target == nil || edges[0].Target.Name != "Helper" {
		t.Errorf("edge[0]: expected resolved Helper, got %v", edges[0].Target)
	}
	// Edge 1: Unknown stays unresolved.
	if edges[1].Target != nil {
		t.Errorf("edge[1]: expected unresolved Unknown, got %v", edges[1].Target)
	}
	// Edge 2: dotted callee resolves via last segment.
	if edges[2].Target == nil || edges[2].Target.Name != "Helper" {
		t.Errorf("edge[2]: expected resolved Helper via last segment, got %v", edges[2].Target)
	}
}

// TestSymbolIndex_EndToEndWithExtractCalls pins the integration:
// build an index from two parsed files, extract calls from a third,
// resolve every call, and verify the targets line up.
func TestSymbolIndex_EndToEndWithExtractCalls(t *testing.T) {
	lib := &ParseResult{
		Path: "lib.go", Language: "go",
		Symbols: []types.Symbol{
			{Name: "Greet", Kind: types.SymbolFunction, Line: 3},
			{Name: "Compute", Kind: types.SymbolFunction, Line: 10},
		},
	}
	idx := BuildSymbolIndex([]*ParseResult{lib})
	callerSrc := []byte(`package main
func run() {
	Greet("hi")
	x := Compute(1)
	_ = x
	Unresolved(x)
}
`)
	calls := ExtractCalls("go", callerSrc)
	edges := idx.ResolveCalls("main.go", calls)
	if len(edges) != 3 {
		t.Fatalf("expected 3 call edges, got %d (%v)", len(edges), edges)
	}
	resolved := map[string]string{}
	for _, e := range edges {
		if e.Target != nil {
			resolved[e.Call.Callee] = e.Target.File
		}
	}
	if resolved["Greet"] != "lib.go" {
		t.Errorf("Greet must resolve to lib.go, got %q", resolved["Greet"])
	}
	if resolved["Compute"] != "lib.go" {
		t.Errorf("Compute must resolve to lib.go, got %q", resolved["Compute"])
	}
	if _, ok := resolved["Unresolved"]; ok {
		t.Errorf("Unresolved must NOT have a resolved target")
	}
}

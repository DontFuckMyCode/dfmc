package context

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestExtractIdentifiers_KeepsCamelAndDotted(t *testing.T) {
	got := extractIdentifiers("please fix parseToken and handleRequest in internal/auth.Token")
	want := map[string]bool{
		"parseToken":          true,
		"handleRequest":       true,
		"internal/auth.Token": true,
	}
	for _, tok := range got {
		delete(want, tok)
	}
	if len(want) > 0 {
		t.Fatalf("expected tokens missing from extraction %q: %v (got %v)", "parseToken+handleRequest+dotted", want, got)
	}
}

func TestExtractIdentifiers_DropsEnglishTurkishStopwords(t *testing.T) {
	// ASCII-only inputs: the identifier regex is ASCII-bound by design
	// (we don't want to match arbitrary Unicode words as identifiers).
	// Any non-ASCII stopword is handled upstream by the fact that the
	// regex won't even match it.
	got := extractIdentifiers("please fix add remove update write ekle sil yaz")
	if len(got) != 0 {
		t.Fatalf("expected stopwords to drop out entirely, got %v", got)
	}
}

func TestExtractIdentifiers_CapsOutputLength(t *testing.T) {
	var words []string
	for i := range 40 {
		words = append(words, "fn"+string(rune('A'+i%26))+"Name"+string(rune('A'+i%26)))
	}
	got := extractIdentifiers(strings.Join(words, " "))
	if len(got) > 16 {
		t.Fatalf("extractIdentifiers exceeded cap: got %d, want <=16", len(got))
	}
}

func TestResolveSymbolSeeds_MatchesByName(t *testing.T) {
	tmp := t.TempDir()
	tokenGo := filepath.Join(tmp, "token.go")
	otherGo := filepath.Join(tmp, "other.go")
	if err := os.WriteFile(tokenGo, []byte("package auth\nfunc parseToken(s string) string { return s }\n"), 0o644); err != nil {
		t.Fatalf("write token.go: %v", err)
	}
	if err := os.WriteFile(otherGo, []byte("package auth\nfunc Unrelated() {}\n"), 0o644); err != nil {
		t.Fatalf("write other.go: %v", err)
	}

	cm := codemap.New(ast.New())
	if err := cm.BuildFromFiles(context.Background(), []string{tokenGo, otherGo}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}

	seeds := resolveSymbolSeeds(cm.Graph(), []string{"parseToken"})
	if len(seeds) == 0 {
		t.Fatalf("expected at least one seed for parseToken, got %v", seeds)
	}
	found := false
	for path := range seeds {
		if strings.HasSuffix(filepath.ToSlash(path), "/token.go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected token.go among seeds, got %v", seeds)
	}
}

// End-to-end: a query mentioning a function name should pull the file
// that defines it and tag the chunk with ChunkSourceSymbolMatch, even
// when no [[file:]] marker is used.
func TestBuildWithOptions_SymbolAwarePullsDefiningFile(t *testing.T) {
	tmp := t.TempDir()
	tokenGo := filepath.Join(tmp, "token.go")
	unrelatedGo := filepath.Join(tmp, "unrelated.go")
	if err := os.WriteFile(tokenGo, []byte("package auth\nfunc ParseAccessToken(s string) string { return s }\n"), 0o644); err != nil {
		t.Fatalf("write token.go: %v", err)
	}
	if err := os.WriteFile(unrelatedGo, []byte("package auth\nfunc Unrelated() {}\n"), 0o644); err != nil {
		t.Fatalf("write unrelated.go: %v", err)
	}

	cm := codemap.New(ast.New())
	if err := cm.BuildFromFiles(context.Background(), []string{tokenGo, unrelatedGo}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}

	mgr := New(cm)
	chunks, err := mgr.BuildWithOptions("please document ParseAccessToken", BuildOptions{
		MaxFiles:         3,
		MaxTokensTotal:   600,
		MaxTokensPerFile: 300,
		Compression:      "none",
		IncludeTests:     true,
		IncludeDocs:      true,
		SymbolAware:      true,
		GraphDepth:       1,
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
	var tokenChunk *struct {
		path   string
		source string
	}
	for _, ch := range chunks {
		if strings.HasSuffix(filepath.ToSlash(ch.Path), "/token.go") {
			tokenChunk = &struct {
				path   string
				source string
			}{path: ch.Path, source: ch.Source}
			break
		}
	}
	if tokenChunk == nil {
		t.Fatalf("expected token.go in chunks, got %+v", chunkPaths(chunks))
	}
	if tokenChunk.source != ChunkSourceSymbolMatch {
		t.Fatalf("expected token.go to be tagged symbol-match, got %q (all=%+v)", tokenChunk.source, chunks)
	}
}

// When SymbolAware is off, the seed file still comes back on
// text-matching, but its Source must NOT be ChunkSourceSymbolMatch —
// confirms the toggle really disables the semantic path.
func TestBuildWithOptions_SymbolAwareDisabled(t *testing.T) {
	tmp := t.TempDir()
	tokenGo := filepath.Join(tmp, "token.go")
	if err := os.WriteFile(tokenGo, []byte("package auth\nfunc ParseAccessToken(s string) string { return s }\n"), 0o644); err != nil {
		t.Fatalf("write token.go: %v", err)
	}

	cm := codemap.New(ast.New())
	if err := cm.BuildFromFiles(context.Background(), []string{tokenGo}); err != nil {
		t.Fatalf("build codemap: %v", err)
	}

	mgr := New(cm)
	chunks, err := mgr.BuildWithOptions("document ParseAccessToken", BuildOptions{
		MaxFiles:         2,
		MaxTokensTotal:   400,
		MaxTokensPerFile: 200,
		Compression:      "none",
		IncludeTests:     true,
		IncludeDocs:      true,
		SymbolAware:      false,
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	for _, ch := range chunks {
		if ch.Source == ChunkSourceSymbolMatch || ch.Source == ChunkSourceGraphNeighborhood {
			t.Fatalf("symbol/graph sources must not appear when SymbolAware=false, got %+v", chunks)
		}
	}
}

func TestChunkSourceRank_Ordering(t *testing.T) {
	order := []string{
		ChunkSourceMarker,
		ChunkSourceSymbolMatch,
		ChunkSourceGraphNeighborhood,
		ChunkSourceQueryMatch,
		ChunkSourceHotspot,
	}
	for i := 0; i < len(order)-1; i++ {
		if chunkSourceRank(order[i]) <= chunkSourceRank(order[i+1]) {
			t.Fatalf("rank should strictly decrease: %s(%d) vs %s(%d)", order[i], chunkSourceRank(order[i]), order[i+1], chunkSourceRank(order[i+1]))
		}
	}
	if got := chunkSourceRank("nonsense"); got != 0 {
		t.Fatalf("unknown source should rank 0, got %d", got)
	}
}

func chunkPaths(chunks []types.ContextChunk) []string {
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, filepath.ToSlash(c.Path))
	}
	return out
}

// TestExpandViaGraph_DepthHonored pins multi-iteration expansion. Two
// modules chain A → B → C via shared imports; depth=1 should surface B
// only (hop 2), depth=2 should also surface C (hop 4).
func TestExpandViaGraph_DepthHonored(t *testing.T) {
	g := codemap.NewGraph()
	fileA := codemap.Node{ID: "file:a.go", Kind: "file", Path: "a.go", Name: "a.go"}
	fileB := codemap.Node{ID: "file:b.go", Kind: "file", Path: "b.go", Name: "b.go"}
	fileC := codemap.Node{ID: "file:c.go", Kind: "file", Path: "c.go", Name: "c.go"}
	modX := codemap.Node{ID: "module:x", Kind: "module", Name: "x"}
	modY := codemap.Node{ID: "module:y", Kind: "module", Name: "y"}
	for _, n := range []codemap.Node{fileA, fileB, fileC, modX, modY} {
		g.AddNode(n)
	}
	// A and B share modX (A↔B siblings); B and C share modY (B↔C siblings).
	// So from A, B is 1 iteration away and C is 2 iterations away.
	for _, e := range []codemap.Edge{
		{From: fileA.ID, To: modX.ID, Type: "imports"},
		{From: fileB.ID, To: modX.ID, Type: "imports"},
		{From: fileB.ID, To: modY.ID, Type: "imports"},
		{From: fileC.ID, To: modY.ID, Type: "imports"},
	} {
		g.AddEdge(e)
	}

	got1 := expandViaGraph(g, []string{"a.go"}, 1)
	if _, ok := got1["b.go"]; !ok {
		t.Fatalf("depth=1 should surface b.go, got %v", got1)
	}
	if _, ok := got1["c.go"]; ok {
		t.Fatalf("depth=1 must NOT surface c.go (needs 2 iterations), got %v", got1)
	}
	if got1["b.go"] != 2 {
		t.Fatalf("b.go hop cost should be 2 at depth=1, got %d", got1["b.go"])
	}

	got2 := expandViaGraph(g, []string{"a.go"}, 2)
	if got2["b.go"] != 2 {
		t.Fatalf("b.go hop cost should remain 2 at depth=2, got %d", got2["b.go"])
	}
	if got2["c.go"] != 4 {
		t.Fatalf("c.go hop cost should be 4 at depth=2, got %d (full=%v)", got2["c.go"], got2)
	}

	got0 := expandViaGraph(g, []string{"a.go"}, 0)
	if len(got0) != 0 {
		t.Fatalf("depth=0 should return empty, got %v", got0)
	}
}

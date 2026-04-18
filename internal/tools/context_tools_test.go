package tools

// Tests for the 4-layer context-gathering tools enhancements:
//   - read_file:    total_lines / returned_lines / truncated / language fields
//   - find_symbol:  parent disambiguation + fallback flag
//   - grep_codebase: context lines, include/exclude globs, case_sensitive,
//                    .gitignore parser
//   - codemap:      basic invocation + signatures-only output shape
//
// Each test seeds a small temp project, calls the tool through the public
// engine.Execute surface (so PathRelativeToRoot, the Spec, and the
// Result.Data plumbing are all in scope), and asserts on the user-visible
// output + the structured Data the model receives back.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// TestReadFile_NewMetadataFields covers the spec's `view` requirements:
// every read_file response carries total_lines, returned_lines, truncated,
// and language so the model can decide whether to ask for more without a
// second probe round.
func TestReadFile_NewMetadataFields(t *testing.T) {
	tmp := t.TempDir()
	// 50 source lines + trailing newline → strings.Split gives 51 entries
	// (the final empty token after the last \n). The default 200-line
	// window covers the whole file so truncated must be false.
	body := strings.Repeat("// line of source\n", 50)
	if err := os.WriteFile(filepath.Join(tmp, "small.go"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg := *config.DefaultConfig()
	cfg.Security.Sandbox.MaxOutput = "1MB"
	eng := New(cfg)

	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "small.go"},
	})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	total := dataInt(res.Data, "total_lines")
	if total != 51 {
		t.Fatalf("total_lines = %d, want 51", total)
	}
	returned := dataInt(res.Data, "returned_lines")
	if returned != 51 {
		t.Fatalf("returned_lines = %d, want 51", returned)
	}
	if trunc, _ := res.Data["truncated"].(bool); trunc {
		t.Fatalf("truncated must be false when the whole file fits in the default window")
	}
	if lang, _ := res.Data["language"].(string); lang != "go" {
		t.Fatalf("language = %q, want %q", lang, "go")
	}
}

// TestReadFile_TruncatedFlagOnLargeFile confirms truncated=true when the
// caller didn't ask for a range but the file is bigger than the default
// window.
func TestReadFile_TruncatedFlagOnLargeFile(t *testing.T) {
	tmp := t.TempDir()
	body := strings.Repeat("x\n", 500) // 500 lines > 200 default window
	if err := os.WriteFile(filepath.Join(tmp, "big.txt"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := New(*config.DefaultConfig())

	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "big.txt"},
	})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if trunc, _ := res.Data["truncated"].(bool); !trunc {
		t.Fatalf("expected truncated=true for a 500-line file under default 200-line window")
	}
	total := dataInt(res.Data, "total_lines")
	returned := dataInt(res.Data, "returned_lines")
	if total != 501 { // 500 newlines + trailing empty token after split
		t.Fatalf("total_lines = %d, want 501", total)
	}
	if returned >= total {
		t.Fatalf("returned_lines (%d) must be < total_lines (%d) when truncated", returned, total)
	}
	if !strings.Contains(res.Output, "[truncated -") {
		t.Fatalf("truncated read output should carry a visible marker, got: %q", res.Output)
	}
}

// TestReadFile_TruncatedWhenSliceSmallerThanFile: the contract is "did
// you get the whole file?", not "did you ask for less?". Even an
// explicit narrow range on a big file must report truncated=true so the
// model knows the file extends past the slice.
func TestReadFile_TruncatedWhenSliceSmallerThanFile(t *testing.T) {
	tmp := t.TempDir()
	body := strings.Repeat("x\n", 500)
	if err := os.WriteFile(filepath.Join(tmp, "big.txt"), []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := New(*config.DefaultConfig())

	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "big.txt", "line_start": 10, "line_end": 30},
	})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if trunc, _ := res.Data["truncated"].(bool); !trunc {
		t.Fatalf("truncated must be true when caller's slice is smaller than the full file")
	}
	if returned := dataInt(res.Data, "returned_lines"); returned != 21 {
		t.Fatalf("returned_lines = %d, want 21 (line 10..30 inclusive)", returned)
	}
}

// TestFindSymbol_ParentDisambiguatesGoReceivers covers the spec's
// receiver-collision case: two methods named Start, one on *Server, one
// on *Client. Without parent the tool returns both; with parent="Server"
// only the matching one comes back.
func TestFindSymbol_ParentDisambiguatesGoReceivers(t *testing.T) {
	tmp := t.TempDir()
	src := `package svc

type Server struct{}
type Client struct{}

func (s *Server) Start() error { return nil }
func (c *Client) Start() error { return nil }
`
	if err := os.WriteFile(filepath.Join(tmp, "svc.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := New(*config.DefaultConfig())

	// Without parent — both methods should match.
	res, err := eng.Execute(context.Background(), "find_symbol", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"name": "Start", "kind": "method"},
	})
	if err != nil {
		t.Fatalf("find_symbol (no parent): %v", err)
	}
	if got := dataInt(res.Data, "count"); got != 2 {
		t.Fatalf("without parent: count=%d, want 2", got)
	}

	// With parent="Server" — only the Server method.
	res, err = eng.Execute(context.Background(), "find_symbol", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"name": "Start", "kind": "method", "parent": "Server"},
	})
	if err != nil {
		t.Fatalf("find_symbol (parent=Server): %v", err)
	}
	if got := dataInt(res.Data, "count"); got != 1 {
		t.Fatalf("parent=Server: count=%d, want 1", got)
	}
	matches, _ := res.Data["matches"].([]map[string]any)
	if len(matches) != 1 {
		t.Fatalf("parent=Server: matches len=%d, want 1", len(matches))
	}
	if p, _ := matches[0]["parent"].(string); p != "Server" {
		t.Fatalf("matches[0].parent = %q, want %q", p, "Server")
	}
	// Header should render Parent.Name so the LLM sees disambiguation
	// directly in the output.
	if !strings.Contains(res.Output, "Server.Start") {
		t.Fatalf("output should render Server.Start, got: %s", res.Output)
	}
}

// TestGrepCodebase_ContextLines exercises the new before/after window:
// asking for context: 1 must wrap each hit in one line of surrounding
// source, separated by `--`.
func TestGrepCodebase_ContextLines(t *testing.T) {
	tmp := t.TempDir()
	src := "// header\nfunc Foo() {}\n// trailing\nfunc Bar() {}\n"
	if err := os.WriteFile(filepath.Join(tmp, "x.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := New(*config.DefaultConfig())

	res, err := eng.Execute(context.Background(), "grep_codebase", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"pattern": "Foo", "context": 1},
	})
	if err != nil {
		t.Fatalf("grep_codebase: %v", err)
	}
	// The match line uses `:`, the context line uses `-` (ripgrep-style).
	if !strings.Contains(res.Output, "x.go:2:func Foo") {
		t.Fatalf("expected match line `x.go:2:func Foo` in output, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "x.go-1-// header") {
		t.Fatalf("expected before-context `x.go-1-// header`, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "x.go-3-// trailing") {
		t.Fatalf("expected after-context `x.go-3-// trailing`, got: %s", res.Output)
	}
	if before, _ := res.Data["context_before"].(int); before != 1 {
		t.Fatalf("context_before = %d, want 1", before)
	}
	if after, _ := res.Data["context_after"].(int); after != 1 {
		t.Fatalf("context_after = %d, want 1", after)
	}
}

// TestGrepCodebase_IncludeExcludeGlobs confirms the new file-shape filters:
// include keeps only matching paths, exclude drops them. Both accept array
// shape; the comma-string shape is covered separately.
func TestGrepCodebase_IncludeExcludeGlobs(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "keep.go"), []byte("// FINDME\n"), 0o644); err != nil {
		t.Fatalf("seed go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "skip.md"), []byte("FINDME\n"), 0o644); err != nil {
		t.Fatalf("seed md: %v", err)
	}
	eng := New(*config.DefaultConfig())

	// include=*.go — only keep.go must show up.
	res, err := eng.Execute(context.Background(), "grep_codebase", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"pattern": "FINDME",
			"include": []any{"*.go"},
		},
	})
	if err != nil {
		t.Fatalf("grep include: %v", err)
	}
	if !strings.Contains(res.Output, "keep.go") {
		t.Fatalf("include=*.go must keep keep.go, got: %s", res.Output)
	}
	if strings.Contains(res.Output, "skip.md") {
		t.Fatalf("include=*.go must drop skip.md, got: %s", res.Output)
	}

	// exclude=*.md — keep.go in, skip.md out.
	res, err = eng.Execute(context.Background(), "grep_codebase", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"pattern": "FINDME",
			"exclude": "*.md",
		},
	})
	if err != nil {
		t.Fatalf("grep exclude: %v", err)
	}
	if !strings.Contains(res.Output, "keep.go") {
		t.Fatalf("exclude=*.md must keep keep.go, got: %s", res.Output)
	}
	if strings.Contains(res.Output, "skip.md") {
		t.Fatalf("exclude=*.md must drop skip.md, got: %s", res.Output)
	}
}

// TestGrepCodebase_CaseInsensitive: case_sensitive=false should match
// regardless of letter case without forcing the model to write (?i).
func TestGrepCodebase_CaseInsensitive(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "x.txt"), []byte("Hello World\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := New(*config.DefaultConfig())

	// Case-sensitive (default) — lowercase pattern misses.
	res, err := eng.Execute(context.Background(), "grep_codebase", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"pattern": "hello"},
	})
	if err != nil {
		t.Fatalf("grep cs: %v", err)
	}
	if dataInt(res.Data, "count") != 0 {
		t.Fatalf("case-sensitive lowercase grep should miss `Hello`, got: %s", res.Output)
	}

	// case_sensitive=false — same pattern matches.
	res, err = eng.Execute(context.Background(), "grep_codebase", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"pattern": "hello", "case_sensitive": false},
	})
	if err != nil {
		t.Fatalf("grep ci: %v", err)
	}
	if dataInt(res.Data, "count") != 1 {
		t.Fatalf("case_sensitive=false should match `Hello`, got count=%d output=%s", dataInt(res.Data, "count"), res.Output)
	}
}

// TestGrepCodebase_RespectsGitignore: a top-level .gitignore line must
// keep that path out of the search results when respect_gitignore is on
// (default), and let it back in when explicitly disabled.
func TestGrepCodebase_RespectsGitignore(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "secret.env"), []byte("API_KEY=findme\n"), 0o644); err != nil {
		t.Fatalf("seed env: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte("// findme\n"), 0o644); err != nil {
		t.Fatalf("seed go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".gitignore"), []byte("*.env\n"), 0o644); err != nil {
		t.Fatalf("seed gitignore: %v", err)
	}
	eng := New(*config.DefaultConfig())

	// Default — *.env hidden by .gitignore.
	res, err := eng.Execute(context.Background(), "grep_codebase", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"pattern": "findme"},
	})
	if err != nil {
		t.Fatalf("grep default: %v", err)
	}
	if strings.Contains(res.Output, "secret.env") {
		t.Fatalf("respect_gitignore default should hide secret.env, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "main.go") {
		t.Fatalf("expected main.go in results, got: %s", res.Output)
	}

	// Opt-out — secret.env reappears.
	res, err = eng.Execute(context.Background(), "grep_codebase", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"pattern": "findme", "respect_gitignore": false},
	})
	if err != nil {
		t.Fatalf("grep opt-out: %v", err)
	}
	if !strings.Contains(res.Output, "secret.env") {
		t.Fatalf("respect_gitignore=false must surface secret.env, got: %s", res.Output)
	}
}

func TestGrepCodebase_SkipsSymlinkEscapeTargets(t *testing.T) {
	tmp := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outside, []byte("SECRET-OUTSIDE\n"), 0o644); err != nil {
		t.Fatalf("seed outside: %v", err)
	}
	link := filepath.Join(tmp, "leak.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported in this environment: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "grep_codebase", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"pattern": "SECRET-OUTSIDE"},
	})
	if err != nil {
		t.Fatalf("grep symlink escape: %v", err)
	}
	if got := dataInt(res.Data, "count"); got != 0 {
		t.Fatalf("symlinked outside-root file should be skipped, got count=%d output=%q", got, res.Output)
	}
}

func TestGrepCodebase_SkipsOversizeFiles(t *testing.T) {
	tmp := t.TempDir()
	large := filepath.Join(tmp, "large.log")
	small := filepath.Join(tmp, "small.txt")
	if err := os.WriteFile(large, []byte(strings.Repeat("A", (10<<20)+1024)+"MATCHME"), 0o644); err != nil {
		t.Fatalf("seed large: %v", err)
	}
	if err := os.WriteFile(small, []byte("MATCHME\n"), 0o644); err != nil {
		t.Fatalf("seed small: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "grep_codebase", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"pattern": "MATCHME"},
	})
	if err != nil {
		t.Fatalf("grep oversize skip: %v", err)
	}
	if strings.Contains(res.Output, "large.log") {
		t.Fatalf("oversize file should be skipped, got output: %s", res.Output)
	}
	if !strings.Contains(res.Output, "small.txt:1:MATCHME") {
		t.Fatalf("expected small file match to remain visible, got: %s", res.Output)
	}
}

func TestGrepCodebase_TruncatesOversizeOutput(t *testing.T) {
	tmp := t.TempDir()
	line := "MATCH " + strings.Repeat("filler-", 32) + "for truncation coverage\n"
	body := strings.Repeat(line, maxGrepMatchesPerFile)
	for i := 0; i < 12; i++ {
		file := filepath.Join(tmp, fmt.Sprintf("huge_%02d.txt", i))
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatalf("seed huge grep file %d: %v", i, err)
		}
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "grep_codebase", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"pattern":     "MATCH",
			"max_results": 250,
			"context":     2,
		},
	})
	if err != nil {
		t.Fatalf("grep truncated output: %v", err)
	}
	if !res.Truncated {
		t.Fatal("expected grep result to mark truncated when output cap kicks in")
	}
	if !strings.Contains(res.Output, "[grep output truncated - refine pattern or narrow path]") {
		t.Fatalf("expected grep truncation marker, got: %q", res.Output)
	}
	if got, _ := res.Data["output_truncated"].(bool); !got {
		t.Fatalf("expected output_truncated metadata, got: %+v", res.Data)
	}
}

// TestCodemap_Basic verifies the Layer-4 wrapper renders a markdown
// outline grouped by file with line numbers, and that the Data carries
// the file/symbol/duration metadata the spec requires.
func TestCodemap_Basic(t *testing.T) {
	tmp := t.TempDir()
	// Multi-line function bodies — the codemap must show only the
	// signature line + L<n>, never the inner statements.
	src := `package widgets

type Widget struct{}

func NewWidget() *Widget {
	w := &Widget{}
	return w
}

func (w *Widget) Render() string {
	parts := []string{"a", "b"}
	return parts[0]
}
`
	if err := os.WriteFile(filepath.Join(tmp, "widget.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	eng := New(*config.DefaultConfig())

	res, err := eng.Execute(context.Background(), "codemap", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("codemap: %v", err)
	}
	if !strings.Contains(res.Output, "widget.go") {
		t.Fatalf("expected widget.go in output, got: %s", res.Output)
	}
	for _, want := range []string{"NewWidget", "Render", "Widget"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("expected %q in codemap output, got: %s", want, res.Output)
		}
	}
	// Codemap is signatures-only by design. Multi-line function bodies
	// must NOT appear — these inner statements are the proof.
	for _, body := range []string{`w := &Widget{}`, `return w`, `parts := []string{"a"`, `return parts[0]`} {
		if strings.Contains(res.Output, body) {
			t.Fatalf("codemap leaked function body %q:\n%s", body, res.Output)
		}
	}
	if got := dataInt(res.Data, "files"); got < 1 {
		t.Fatalf("codemap data.files = %d, want >= 1", got)
	}
	if got := dataInt(res.Data, "symbols"); got < 3 {
		t.Fatalf("codemap data.symbols = %d, want >= 3", got)
	}
}

func TestCodemap_SkipsSymlinkEscapeFiles(t *testing.T) {
	skipIfNoSymlink(t)

	tmp := t.TempDir()
	outside := t.TempDir()
	insideSrc := `package inside

func Visible() {}
`
	outsideSrc := `package outside

func ShouldNotLeak() {}
`
	if err := os.WriteFile(filepath.Join(tmp, "inside.go"), []byte(insideSrc), 0o644); err != nil {
		t.Fatalf("seed inside: %v", err)
	}
	outsideFile := filepath.Join(outside, "outside.go")
	if err := os.WriteFile(outsideFile, []byte(outsideSrc), 0o644); err != nil {
		t.Fatalf("seed outside: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(tmp, "escape.go")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "codemap", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("codemap: %v", err)
	}
	if !strings.Contains(res.Output, "Visible") {
		t.Fatalf("expected in-root symbol in codemap output, got: %s", res.Output)
	}
	if strings.Contains(res.Output, "ShouldNotLeak") || strings.Contains(res.Output, "escape.go") {
		t.Fatalf("codemap should skip symlink escapes, got: %s", res.Output)
	}
}

// dataInt safely extracts an int from a Result.Data map, accepting either
// the raw int (typical for our tools) or float64 (when JSON-roundtripped).
// Returns 0 when missing or wrong-typed — tests treat that as a failure.
func dataInt(d map[string]any, key string) int {
	if d == nil {
		return 0
	}
	switch v := d[key].(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

func TestAsIntRejectsFractionalFloat(t *testing.T) {
	params := map[string]any{
		"whole":      42.0,
		"fractional": 1.5,
	}
	if got := asInt(params, "whole", 7); got != 42 {
		t.Fatalf("whole float should coerce to 42, got %d", got)
	}
	if got := asInt(params, "fractional", 7); got != 7 {
		t.Fatalf("fractional float should fall back, got %d", got)
	}
}

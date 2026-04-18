package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// TestFindSymbol_GoFunctionFullScope is the headline case from the user's
// 2026-04-18 ask: "find the aliveli function with full scope". The tool
// must find the function by name and return its body with brace balance,
// not just the start line.
func TestFindSymbol_GoFunctionFullScope(t *testing.T) {
	tmp := t.TempDir()
	src := `package demo

import "fmt"

func helper() string { return "x" }

// aliveli does the thing.
func aliveli(name string) string {
	if name == "" {
		return "anon"
	}
	for i := 0; i < 3; i++ {
		fmt.Println(i, name)
	}
	return name
}

func tail() {}
`
	if err := os.WriteFile(filepath.Join(tmp, "demo.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "find_symbol", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"name": "aliveli"},
	})
	if err != nil {
		t.Fatalf("find_symbol: %v", err)
	}
	if !strings.Contains(res.Output, "aliveli") {
		t.Fatalf("expected aliveli in output, got: %s", res.Output)
	}
	// Body must include the inner control flow (proves brace-balance walk
	// went past the first `{`/`}` pair) and the closing brace.
	if !strings.Contains(res.Output, "for i := 0") {
		t.Fatalf("expected scope body to include the loop, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "return name") {
		t.Fatalf("expected scope body to include the return, got: %s", res.Output)
	}
	if strings.Contains(res.Output, "func tail") {
		t.Fatalf("scope must NOT bleed into the next function, got: %s", res.Output)
	}
	matches, _ := res.Data["matches"].([]map[string]any)
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 match, got %d", len(matches))
	}
	if start, _ := matches[0]["start_line"].(int); start < 7 {
		t.Fatalf("aliveli starts at line ≥ 7, got %d", start)
	}
	if end, _ := matches[0]["end_line"].(int); end < 14 {
		t.Fatalf("aliveli ends at the closing brace (line ≥ 14), got %d", end)
	}
}

// TestFindSymbol_PythonClassByIndent covers the Python path: scope ends
// at the first non-empty line whose indent drops back to the header's
// level. AST regex backend (CGO off) still finds class definitions, and
// the indent walker handles the rest.
func TestFindSymbol_PythonClassByIndent(t *testing.T) {
	tmp := t.TempDir()
	src := `# top-level comment
class Settings:
    name = "x"

    def render(self):
        print(self.name)
        return self.name

class Other:
    pass
`
	if err := os.WriteFile(filepath.Join(tmp, "settings.py"), []byte(src), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "find_symbol", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"name": "Settings", "kind": "class"},
	})
	if err != nil {
		t.Fatalf("find_symbol: %v", err)
	}
	if !strings.Contains(res.Output, "class Settings") {
		t.Fatalf("expected the class header in body, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "def render") {
		t.Fatalf("expected the indented method in body, got: %s", res.Output)
	}
	if strings.Contains(res.Output, "class Other") {
		t.Fatalf("scope must NOT cross into the next class, got: %s", res.Output)
	}
}

// TestFindSymbol_HTMLByID covers the HTML path: the model asks for an
// element by id and gets the balanced tag block back, not a single line.
func TestFindSymbol_HTMLByID(t *testing.T) {
	tmp := t.TempDir()
	src := `<!doctype html>
<html>
<body>
<div id="header">site header</div>
<section id="login">
  <form>
    <input name="user">
    <input name="pass" type="password">
    <button>go</button>
  </form>
</section>
<footer>tail</footer>
</body>
</html>
`
	if err := os.WriteFile(filepath.Join(tmp, "page.html"), []byte(src), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "find_symbol", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"name": "login", "kind": "html_id"},
	})
	if err != nil {
		t.Fatalf("find_symbol: %v", err)
	}
	if !strings.Contains(res.Output, `id="login"`) {
		t.Fatalf("expected the opening tag in body, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "</section>") {
		t.Fatalf("expected the balanced closing tag in body, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "<input name=\"user\">") {
		t.Fatalf("expected nested input element, got: %s", res.Output)
	}
	if strings.Contains(res.Output, "<footer>") {
		t.Fatalf("scope must NOT cross into the next element, got: %s", res.Output)
	}
}

// TestFindSymbol_NoMatchesGivesActionableHint pins the empty-result UX:
// when nothing matches, the tool must list what it searched for and
// suggest the easiest broadening (match=contains) so the model can
// self-correct in one round.
func TestFindSymbol_NoMatchesGivesActionableHint(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "x.go"), []byte("package x\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "find_symbol", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"name": "DoesNotExist", "kind": "function"},
	})
	if err != nil {
		t.Fatalf("find_symbol: %v", err)
	}
	for _, want := range []string{"no symbols matched", "DoesNotExist", "kind=function", "match=contains"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("empty-result hint should mention %q, got: %s", want, res.Output)
		}
	}
	if cnt, _ := res.Data["count"].(int); cnt != 0 {
		t.Fatalf("expected count=0, got %v", res.Data["count"])
	}
}

// TestFindSymbol_BodyTruncationLeavesMarker covers the per-result body
// cap: when the scope is bigger than body_max_lines, the tool keeps the
// head and writes a "// … (N lines elided)" marker so the model knows
// to ask for more.
func TestFindSymbol_BodyTruncationLeavesMarker(t *testing.T) {
	tmp := t.TempDir()
	var b strings.Builder
	b.WriteString("package demo\n\nfunc Big() {\n")
	for i := 0; i < 50; i++ {
		b.WriteString("\tprintln(\"x\")\n")
	}
	b.WriteString("}\n")
	if err := os.WriteFile(filepath.Join(tmp, "big.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	eng := New(*config.DefaultConfig())
	res, err := eng.Execute(context.Background(), "find_symbol", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"name": "Big", "body_max_lines": 5},
	})
	if err != nil {
		t.Fatalf("find_symbol: %v", err)
	}
	if !strings.Contains(res.Output, "lines elided") {
		t.Fatalf("expected truncation marker in body, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "[truncated]") {
		t.Fatalf("expected [truncated] flag in header, got: %s", res.Output)
	}
}

// TestFindSymbol_MissingNameIsActionable confirms the tool follows the
// project-wide missing-param shape (name + received keys + example +
// hint) instead of a bare error.
func TestFindSymbol_MissingNameIsActionable(t *testing.T) {
	eng := New(*config.DefaultConfig())
	_, err := eng.Execute(context.Background(), "find_symbol", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"kind": "function"},
	})
	if err == nil {
		t.Fatal("missing name must error")
	}
	msg := err.Error()
	for _, want := range []string{"name", "params keys", "Correct shape:", "kind", "match"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing-name error should contain %q, got: %s", want, msg)
		}
	}
}

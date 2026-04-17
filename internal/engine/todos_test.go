// Unit tests for the TODO/FIXME marker collector.

package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTodoFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func TestCollectTodoMarkers_CountsAllKinds(t *testing.T) {
	tmp := t.TempDir()
	src := `package demo
// TODO: finish this
// FIXME(ersin): handle edge case
// HACK: works only on Windows
// XXX remove before ship
// NOTE: this is the new path
func Do() {}
`
	p := writeTodoFile(t, tmp, "a.go", src)
	rep := collectTodoMarkers([]string{p})

	if rep.Total != 5 {
		t.Fatalf("want 5 markers, got %d", rep.Total)
	}
	want := []string{"TODO", "FIXME", "HACK", "XXX", "NOTE"}
	for _, k := range want {
		if rep.Kinds[k] != 1 {
			t.Errorf("kind %q: want 1, got %d (kinds=%+v)", k, rep.Kinds[k], rep.Kinds)
		}
	}
}

func TestCollectTodoMarkers_SkipsStringLiteralsAndCode(t *testing.T) {
	// The collector is comment-only: a TODO mentioned inside a
	// string literal (help text, error message) or on a code line
	// like `var TODOS = ...` must NOT count.
	tmp := t.TempDir()
	src := `package demo
var TODOS = []string{"pending TODO item"}
func Err() string { return "TODO: see docs" }
`
	p := writeTodoFile(t, tmp, "nope.go", src)
	rep := collectTodoMarkers([]string{p})
	if rep.Total != 0 {
		t.Fatalf("want 0 (no comment markers), got %d: %+v", rep.Total, rep.Items)
	}
}

func TestCollectTodoMarkers_PythonHashComments(t *testing.T) {
	tmp := t.TempDir()
	src := `# TODO: port to new API
def f():
    return 1
`
	p := writeTodoFile(t, tmp, "m.py", src)
	rep := collectTodoMarkers([]string{p})
	if rep.Total != 1 || rep.Kinds["TODO"] != 1 {
		t.Fatalf("python # comment not detected: %+v", rep)
	}
}

func TestCollectTodoMarkers_IgnoresUnknownExtensions(t *testing.T) {
	tmp := t.TempDir()
	src := "// TODO: this file's extension is weird\n"
	p := writeTodoFile(t, tmp, "skip.xyz", src)
	rep := collectTodoMarkers([]string{p})
	if rep.Total != 0 {
		t.Fatalf("unknown ext should be skipped, got %+v", rep)
	}
}

func TestCollectTodoMarkers_LowercaseDoesNotTrigger(t *testing.T) {
	// We match only ALL-CAPS markers to keep the pass terse.
	// "// todo: ..." is a style preference, not a convention — if we
	// matched it we'd catch "todo" in lots of prose comments too.
	tmp := t.TempDir()
	src := "// todo: lowercase\n// Fixme: partial case\nfunc f() {}\n"
	p := writeTodoFile(t, tmp, "a.go", src)
	rep := collectTodoMarkers([]string{p})
	if rep.Total != 0 {
		t.Fatalf("want 0 for non-ALLCAPS markers, got %d: %+v", rep.Total, rep.Items)
	}
}

func TestCollectTodoMarkers_CaptureTextAndLineNumbers(t *testing.T) {
	tmp := t.TempDir()
	src := "package demo\n" +
		"\n" +
		"// TODO: wire up the approver\n" +
		"func Do() {}\n"
	p := writeTodoFile(t, tmp, "b.go", src)
	rep := collectTodoMarkers([]string{p})
	if len(rep.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(rep.Items))
	}
	item := rep.Items[0]
	if item.Line != 3 {
		t.Errorf("line number: want 3, got %d", item.Line)
	}
	if item.Kind != "TODO" {
		t.Errorf("kind: want TODO, got %q", item.Kind)
	}
}

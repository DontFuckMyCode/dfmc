package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocGenerate_RejectsUnsupportedFormat pins #54: doc_generate only
// implements godoc, so jsdoc/rustdoc must return a clear error instead of
// silently emitting godoc (package mode) or empty bodies (func mode).
func TestDocGenerate_RejectsUnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "x.go")
	if err := os.WriteFile(src, []byte("package x\n\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewDocGenerateTool()
	for _, tc := range []struct{ mode, format string }{
		{"func", "jsdoc"},
		{"package", "rustdoc"},
	} {
		_, err := tool.Execute(context.Background(), Request{Params: map[string]any{
			"mode": tc.mode, "target": src, "format": tc.format,
		}})
		if err == nil {
			t.Errorf("mode=%s format=%s: expected an error", tc.mode, tc.format)
			continue
		}
		if !strings.Contains(err.Error(), "not supported") {
			t.Errorf("mode=%s format=%s: error should say the format isn't supported, got %v", tc.mode, tc.format, err)
		}
	}
}

// TestDocGenerate_GodocAccepted confirms the supported path still works
// (and that an empty format defaults to godoc rather than erroring).
func TestDocGenerate_GodocAccepted(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "x.go")
	if err := os.WriteFile(src, []byte("package x\n\nfunc Foo(a int) int { return a }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewDocGenerateTool()
	for _, format := range []string{"godoc", ""} {
		res, err := tool.Execute(context.Background(), Request{Params: map[string]any{
			"mode": "func", "target": src, "format": format,
		}})
		if err != nil {
			t.Fatalf("format=%q should be accepted, got: %v", format, err)
		}
		if !strings.Contains(res.Output, "Foo") {
			t.Errorf("format=%q: expected godoc output to mention Foo:\n%s", format, res.Output)
		}
	}
}

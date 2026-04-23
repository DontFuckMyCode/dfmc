package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// TestApplyPatchPreservesCRLFLineEndings pins the companion guarantee
// to TestApplyPatchHandlesCRLFSource (new_tools_test.go): the anchor
// match already tolerates CR/LF skew, but the replacement path used to
// hard-code "\n" on every emitted line, so applying an LF-format hunk
// to a CRLF-ended source produced a file with MIXED endings (the
// replaced region was LF, the rest stayed CRLF). The prior CRLF test
// explicitly declined to assert the post-apply byte layout; this test
// asserts it - a Windows user watching `git diff` after the patch
// should see a content change, not a whole-file EOL flip.
func TestApplyPatchPreservesCRLFLineEndings(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "mixed.txt")
	original := []byte("one\r\ntwo\r\nthree\r\n")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	patch := `--- a/mixed.txt
+++ b/mixed.txt
@@ -1,3 +1,3 @@
 one
-two
+TWO
 three
`

	eng := New(*config.DefaultConfig())
	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "mixed.txt"},
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if _, err := eng.Execute(context.Background(), "apply_patch", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": patch},
	}); err != nil {
		t.Fatalf("apply_patch: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	want := "one\r\nTWO\r\nthree\r\n"
	if string(got) != want {
		t.Fatalf("line endings got flipped mid-file:\n  want %q\n  got  %q", want, string(got))
	}
	// Defensive: no bare LF survives.
	if strings.Contains(strings.ReplaceAll(string(got), "\r\n", ""), "\n") {
		t.Fatalf("mixed line endings in result: %q", string(got))
	}
}

// TestApplyPatchPreservesLFLineEndings is the LF-source twin. A CRLF
// patch (e.g. one pasted from a Windows-only IDE) applied to an
// LF-ended source should NOT leave embedded CR bytes in the result.
// The emitted replacement line must match the file's dominant ending.
func TestApplyPatchPreservesLFLineEndings(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "mixed.txt")
	original := []byte("one\ntwo\nthree\n")
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Patch body uses CRLF on the hunk lines.
	patch := "--- a/mixed.txt\n+++ b/mixed.txt\n@@ -1,3 +1,3 @@\n one\r\n-two\r\n+TWO\r\n three\r\n"

	eng := New(*config.DefaultConfig())
	if _, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"path": "mixed.txt"},
	}); err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if _, err := eng.Execute(context.Background(), "apply_patch", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": patch},
	}); err != nil {
		t.Fatalf("apply_patch: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	want := "one\nTWO\nthree\n"
	if string(got) != want {
		t.Fatalf("patch leaked CR bytes into LF source:\n  want %q\n  got  %q", want, string(got))
	}
}

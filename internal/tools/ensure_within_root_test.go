package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// TestWriteFileAtomic_EscapesViaSymlink — L3 / writeFileAtomic security.
// writeFileAtomic creates the temp file in filepath.Dir(path). If that
// directory is a symlink that escapes the project root, the atomic
// rename could place the final file outside the sandbox. The write_file
// tool already calls EnsureWithinRoot before reaching writeFileAtomic,
// so this is validated at the tool layer. This test verifies the
// integration: write_file refuses to create a file through an escaping
// symlink even though writeFileAtomic would handle the atomic write
// correctly if called.
func TestWriteFileAtomic_EscapesViaSymlink(t *testing.T) {
	skipIfNoSymlink(t)
	root := t.TempDir()
	outside := t.TempDir()

	// Create root/evil -> outside (symlink escape)
	if err := os.Symlink(outside, filepath.Join(root, "evil")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	eng := New(*config.DefaultConfig())

	// write_file calls EnsureWithinRoot(root, "evil/newfile").
	// resolveExistingAncestor walks up from root/evil/newfile (doesn't exist yet).
	// root/evil exists but is a symlink -> resolvedPath = outside.
	// isPathWithin(root, outside) = false -> error "path escapes project root".
	// writeFileAtomic is never reached.
	_, err := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: root,
		Params: map[string]any{
			"path":    "evil/newfile.txt",
			"content": "must be refused",
		},
	})
	if err == nil {
		t.Fatal("expected error for write through escaping symlink, got nil")
	}
}

// TestEnsureWithinRoot_SymlinkFallbackFailure — M1
// When resolveExistingAncestor fails (no existing ancestor found), EnsureWithinRoot
// must return an error, NOT the unresolved path. Previously it returned absPath, nil
// which bypassed the symlink containment check.
func TestEnsureWithinRoot_SymlinkFallbackFailure(t *testing.T) {
	tmp := t.TempDir()

	eng := New(*config.DefaultConfig())

	// Path that has an existing ancestor that resolves but ultimately
	// the reconstructed path escapes root. On Windows, creating a chain
	// like project/symlink -> C:\ (or / on Unix) is hard in tests, so we
	// test the failure path: a path with no existing ancestor at all.
	// resolveExistingAncestor reaches root without finding anything -> error.
	// EnsureWithinRoot must propagate that error.
	escapePath := filepath.Join(tmp, "nonexistent", "..", "..", "outside.txt")

	_, err := eng.Execute(context.Background(), "write_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path":    escapePath,
			"content": "should not write",
		},
	})
	if err == nil {
		t.Fatal("expected error for path with unresolvable ancestor, got nil")
	}
}

// TestEnsureWithinRoot_SymlinkedSubdirFalsePositive — S1
// When the parent directory is a symlink but the target file would be within
// the project root, EnsureWithinRoot must NOT false-positive escape.
// This tests the reconstructPath logic: symlink -> elsewhere, but target is safe.
func TestEnsureWithinRoot_SymlinkedSubdirFalsePositive(t *testing.T) {
	tmp := t.TempDir()

	// Create: tmp/symdir -> tmp/real  (symlink to sibling)
	symdir := filepath.Join(tmp, "symdir")
	realDir := filepath.Join(tmp, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatalf("mkdir realDir: %v", err)
	}
	if err := os.Symlink(realDir, symdir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Write a file inside the real directory
	targetFile := filepath.Join(realDir, "safe.txt")
	if err := os.WriteFile(targetFile, []byte("content"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	eng := New(*config.DefaultConfig())

	// Access the file via its symlink path (tmp/symdir/safe.txt).
	// resolveExistingAncestor resolves tmp/symdir -> tmp/real.
	// Reconstructed path: tmp/real/safe.txt -> isPathWithin(root, tmp/real/safe.txt) -> true.
	// Must NOT error.
	res, err := eng.Execute(context.Background(), "read_file", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"path": filepath.Join("symdir", "safe.txt"),
		},
	})
	if err != nil {
		t.Fatalf("read_file via symlinked dir should not error, got: %v", err)
	}
	if res.Output != "content" {
		t.Fatalf("expected content, got: %q", res.Output)
	}
}

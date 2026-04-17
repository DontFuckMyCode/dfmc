// Tests for SanitizeGitRoot — the git-root hardening shared by the
// TUI and web git-diff helpers.

package security

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeGitRoot_AcceptsValidDirectory(t *testing.T) {
	dir := t.TempDir()
	got, err := SanitizeGitRoot(dir)
	if err != nil {
		t.Fatalf("unexpected error for valid tempdir: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("result must be absolute, got %q", got)
	}
}

func TestSanitizeGitRoot_EmptyFallsBackToCWD(t *testing.T) {
	got, err := SanitizeGitRoot("")
	if err != nil {
		t.Fatalf("empty input should fall back to cwd, got error: %v", err)
	}
	cwd, _ := os.Getwd()
	if got != filepath.Clean(cwd) {
		t.Fatalf("empty input should return cwd %q, got %q", cwd, got)
	}
}

// Argument-injection guard: even though exec.Command doesn't spawn a
// shell, a path that starts with `-` could be read as a CLI flag if
// it ever gets passed to git/another tool as an argv element. The
// sanitizer rejects such paths outright.
func TestSanitizeGitRoot_RejectsLeadingDashComponent(t *testing.T) {
	tmp := t.TempDir()
	// Create a subdirectory that begins with '-' under a safe parent.
	bad := filepath.Join(tmp, "-fakeflag")
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatalf("mkdir -fakeflag: %v", err)
	}
	_, err := SanitizeGitRoot(bad)
	if err == nil {
		t.Fatal("expected error for path component starting with '-'")
	}
	if !errors.Is(err, ErrInvalidGitRoot) {
		t.Fatalf("error should wrap ErrInvalidGitRoot, got %v", err)
	}
	if !strings.Contains(err.Error(), "-fakeflag") {
		t.Fatalf("error should mention the bad component, got %v", err)
	}
}

func TestSanitizeGitRoot_RejectsNonexistentPath(t *testing.T) {
	_, err := SanitizeGitRoot(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
	if !errors.Is(err, ErrInvalidGitRoot) {
		t.Fatalf("error should wrap ErrInvalidGitRoot, got %v", err)
	}
}

func TestSanitizeGitRoot_RejectsFilePath(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "not-a-dir-*.txt")
	if err != nil {
		t.Fatalf("tempfile: %v", err)
	}
	f.Close()
	_, err = SanitizeGitRoot(f.Name())
	if err == nil {
		t.Fatal("expected error when path is a file, not a directory")
	}
	if !errors.Is(err, ErrInvalidGitRoot) {
		t.Fatalf("error should wrap ErrInvalidGitRoot, got %v", err)
	}
}

func TestSanitizeGitRoot_CleansRelativePath(t *testing.T) {
	tmp := t.TempDir()
	nested := filepath.Join(tmp, "a", "..", "a") // clean form: tmp/a
	if err := os.Mkdir(filepath.Join(tmp, "a"), 0o755); err != nil {
		t.Fatalf("mkdir a: %v", err)
	}
	got, err := SanitizeGitRoot(nested)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Clean(filepath.Join(tmp, "a"))
	if got != want {
		t.Fatalf("path should be cleaned, want %q got %q", want, got)
	}
}

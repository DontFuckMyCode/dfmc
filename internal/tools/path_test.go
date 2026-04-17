// Path-containment tests for EnsureWithinRoot. The lexical check is
// simple and was well covered by the surrounding file-tool tests, but
// symlink resistance is new and easy to regress — tests here pin the
// specific shapes it guards against.

package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnsureWithinRoot_AllowsSubpath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := EnsureWithinRoot(root, "a.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(got, "a.txt") {
		t.Fatalf("resolved path missing suffix: %s", got)
	}
}

func TestEnsureWithinRoot_RefusesDotDotTraversal(t *testing.T) {
	root := t.TempDir()
	if _, err := EnsureWithinRoot(root, "../etc/passwd"); err == nil {
		t.Fatal("../etc/passwd must be refused")
	}
}

func TestEnsureWithinRoot_RefusesAbsoluteOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := EnsureWithinRoot(root, outside); err == nil {
		t.Fatal("absolute path outside root must be refused")
	}
}

func TestEnsureWithinRoot_AllowsNonExistentWriteTarget(t *testing.T) {
	// write_file creates new files — the target doesn't exist yet,
	// but as long as the parent chain resolves inside root, accept.
	root := t.TempDir()
	out, err := EnsureWithinRoot(root, "newdir/newfile.txt")
	if err != nil {
		t.Fatalf("new file inside root should be allowed, got: %v", err)
	}
	if !strings.HasPrefix(out, root) && !strings.Contains(out, filepath.Base(root)) {
		t.Fatalf("resolved path looks odd: %s", out)
	}
}

// The core symlink-escape guard: commit a symlink to /etc/passwd
// INSIDE the root, then try to resolve a path through it. The lexical
// rel-check passes (the symlink itself sits under root) but the
// symbolic check must catch the escape.
func TestEnsureWithinRoot_RefusesSymlinkEscape(t *testing.T) {
	skipIfNoSymlink(t)
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("leaked"), 0o644); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	// evil-link lives INSIDE root and points OUTSIDE — the lexical
	// check won't see the escape, only EvalSymlinks will.
	link := filepath.Join(root, "evil")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := EnsureWithinRoot(root, "evil"); err == nil {
		t.Fatal("symlink escape must be refused")
	} else if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error should mention symlink, got: %v", err)
	}
}

// Symlinks that stay INSIDE the root are fine — e.g. a symlink from
// `docs/latest` to `docs/v1`. The symbolic check must not false-
// positive on these.
func TestEnsureWithinRoot_AllowsInternalSymlink(t *testing.T) {
	skipIfNoSymlink(t)
	root := t.TempDir()
	real := filepath.Join(root, "real.txt")
	if err := os.WriteFile(real, []byte("ok"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := EnsureWithinRoot(root, "alias"); err != nil {
		t.Fatalf("intra-root symlink must be allowed, got: %v", err)
	}
}

// Creating a new file inside a directory whose PARENT is an
// escaping symlink must be refused — the parent-chain fallback in
// resolveExistingAncestor must catch it.
func TestEnsureWithinRoot_RefusesNewFileUnderSymlinkedEscape(t *testing.T) {
	skipIfNoSymlink(t)
	root := t.TempDir()
	outside := t.TempDir()
	// Symlink a directory inside root to an outside directory; then
	// ask to write to a NEW file under that symlinked directory. The
	// target doesn't exist yet, but the ancestor resolve must catch
	// the escape.
	if err := os.Symlink(outside, filepath.Join(root, "esc")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := EnsureWithinRoot(root, "esc/newfile.txt"); err == nil {
		t.Fatal("writing through a symlinked escape directory must be refused")
	}
}

func TestEnsureWithinRoot_RejectsEmptyPath(t *testing.T) {
	root := t.TempDir()
	if _, err := EnsureWithinRoot(root, ""); err == nil {
		t.Fatal("empty path must be refused")
	}
	if _, err := EnsureWithinRoot(root, "   "); err == nil {
		t.Fatal("whitespace-only path must be refused")
	}
}

// skipIfNoSymlink probes the current environment by attempting to
// create a symlink in a temp dir. On Windows, this fails unless the
// running process has SeCreateSymbolicLinkPrivilege (developer mode
// or admin). On POSIX this always succeeds. Skipping on failure is
// correct — the guard we're testing is environment-agnostic, but we
// can't exercise it if the host won't let us build the fixture.
func skipIfNoSymlink(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "t")
	if err := os.WriteFile(target, []byte{}, 0o644); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	probe := filepath.Join(dir, "probe")
	if err := os.Symlink(target, probe); err != nil {
		t.Skipf("symlink creation unavailable on this host: %v", err)
	}
	_ = runtime.GOOS // keep import referenced on GOOS-irrelevant builds
}

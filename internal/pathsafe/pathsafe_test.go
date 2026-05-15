package pathsafe

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestEnsureWithinRoot_Lexical pins the syntactic-only checks: empty
// input is rejected, paths that walk out of root with .. are rejected,
// and absolute paths outside the root are rejected. Symlinks are not
// exercised here — those have their own test that creates a tree.
func TestEnsureWithinRoot_Lexical(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"empty", "", true},
		{"whitespace", "   ", true},
		{"relative-inside", "foo/bar.txt", false},
		{"dot-prefix-inside", "./foo.txt", false},
		{"dotdot-escape", "../escape.txt", true},
		{"nested-dotdot-escape", "a/b/../../../escape.txt", true},
		{"dotdot-then-back-in", "a/../b.txt", false},
		{"absolute-inside", filepath.Join(root, "ok.txt"), false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := EnsureWithinRoot(root, c.path)
			if (err != nil) != c.wantErr {
				t.Fatalf("EnsureWithinRoot(%q, %q) err=%v, wantErr=%v",
					root, c.path, err, c.wantErr)
			}
		})
	}
}

// TestEnsureWithinRoot_RejectsAbsoluteOutsideRoot uses a sibling tempdir
// so the absolute path is genuinely outside the project tree.
func TestEnsureWithinRoot_RejectsAbsoluteOutsideRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir() // different tempdir
	target := filepath.Join(outside, "secret.txt")
	if _, err := EnsureWithinRoot(root, target); err == nil {
		t.Fatalf("expected escape rejection for %q outside %q", target, root)
	}
}

// TestEnsureWithinRoot_NewFileInExistingDir confirms that a target
// which does not yet exist (write_file create case) still resolves
// correctly when its parent exists inside root.
func TestEnsureWithinRoot_NewFileInExistingDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, err := EnsureWithinRoot(root, "fresh.txt"); err != nil {
		t.Fatalf("new file inside root should pass, got: %v", err)
	}
}

// TestEnsureWithinRoot_SymlinkEscapeBlocked — a symlink at
// root/evil pointing outside the root must be rejected on resolution.
// Symlink creation is unprivileged-friendly on Unix; on Windows the
// test runs only when symlink creation succeeds (it requires admin
// or developer mode by default).
func TestEnsureWithinRoot_SymlinkEscapeBlocked(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "escape.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o644); err != nil {
		t.Fatalf("setup escape target: %v", err)
	}
	linkPath := filepath.Join(root, "evil")
	if err := os.Symlink(target, linkPath); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation not permitted on this Windows host: %v", err)
		}
		t.Fatalf("symlink setup: %v", err)
	}
	if _, err := EnsureWithinRoot(root, "evil"); err == nil {
		t.Fatal("EnsureWithinRoot should refuse a symlink that escapes the root")
	}
}

// TestIsPathWithin pins the leaf primitive used by EnsureWithinRoot.
func TestIsPathWithin(t *testing.T) {
	t.Parallel()
	root := filepath.Clean("/a/b")
	cases := []struct {
		target string
		want   bool
	}{
		{"/a/b", true},
		{"/a/b/c", true},
		{"/a/b/c/d.txt", true},
		{"/a/bc", false}, // sibling-by-prefix isn't "within"
		{"/a", false},
		{"/", false},
		{"/x/y", false},
	}
	for _, c := range cases {
		got := IsPathWithin(root, filepath.Clean(c.target))
		if got != c.want {
			t.Errorf("IsPathWithin(%q, %q) = %v, want %v", root, c.target, got, c.want)
		}
	}
}

// FuzzEnsureWithinRoot — invariant: EnsureWithinRoot must never panic
// and must never return a successful result for a path that, once
// lexically cleaned and joined against a real tempdir, resolves
// outside that tempdir. We don't try to handle every corner symbolic
// case (that's exercised in TestEnsureWithinRoot_SymlinkEscapeBlocked);
// the fuzzer just probes the lexical layer for crashes and
// invariant violations.
func FuzzEnsureWithinRoot(f *testing.F) {
	seeds := []string{
		"",
		"foo",
		"foo/bar",
		"../escape",
		"../../etc/passwd",
		"./.././ok.txt",
		"a/./b/../c",
		"\x00null",
		strings.Repeat("a/", 64) + "deep.txt",
		"C:\\Windows\\System32",
		"/etc/passwd",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, path string) {
		root := t.TempDir()
		out, err := EnsureWithinRoot(root, path)
		if err != nil {
			return // rejected — fine
		}
		// Successful resolutions must end up inside the absolute root.
		absRoot, _ := filepath.Abs(root)
		if !IsPathWithin(absRoot, out) {
			t.Fatalf("EnsureWithinRoot accepted %q but result %q escapes root %q",
				path, out, absRoot)
		}
	})
}

// FuzzIsPathWithin — invariant: never panic. Always returns false
// when filepath.Rel returns an error (e.g., malformed paths on
// Windows that cross drive letters).
func FuzzIsPathWithin(f *testing.F) {
	f.Add("/a", "/a/b")
	f.Add("/a", "/a")
	f.Add("/a", "/b")
	f.Add("a/b", "a/b/c")
	f.Add("", "/a")
	f.Add("/a", "")
	f.Fuzz(func(t *testing.T, root, target string) {
		_ = IsPathWithin(root, target) // must not panic
	})
}

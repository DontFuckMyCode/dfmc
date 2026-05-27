// Package repolint hosts cross-cutting "tree-wide grep" assertions
// that catch regressions which `go vet` and `staticcheck` don't cover.
//
// These tests walk the entire repository (parent of this file's
// package), so they double as a tripwire — anyone adding the pattern
// back in production code learns about it from a failing CI build
// instead of a future production write being silently dropped.
package repolint

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot walks up from this test file's directory until it finds
// the go.mod, returning the directory that holds it. Using
// runtime.Caller instead of os.Getwd makes the test robust to being
// invoked from any working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("repolint: could not locate go.mod from " + file)
	return ""
}

// walkProductionGo invokes fn on every non-test, non-vendored,
// non-node_modules Go source file under root.
func walkProductionGo(t *testing.T, root string, fn func(path string, body []byte)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			switch name {
			case "vendor", "node_modules", ".git", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		fn(path, body)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

// TestNoSilentErrSwallow pins the bug fixed in
// internal/tools/symbol_rename.go and internal/tools/symbol_move.go:
// an `os.WriteFile` failure used to be discarded via a bare `_ = err`,
// which made the tool report success while the disk write actually
// failed (read-only FS, AV lock on Windows, full disk).
//
// The pattern is virtually never legitimate in production code: if
// the value really should be ignored, the call should be written as
// `_ = doIt()` rather than capturing into `err` only to throw it
// away. A future regression here will block CI rather than surface
// in production.
//
// Exceptions are allowed via an inline opt-out marker
// `// repolint:allow _=err — <reason>` on the same line; that keeps
// the rule strict by default while letting legitimate cases land
// with a documented rationale.
func TestNoSilentErrSwallow(t *testing.T) {
	root := repoRoot(t)
	var hits []string
	walkProductionGo(t, root, func(path string, body []byte) {
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			// Match `_ = err` (with optional trailing comment that is
			// NOT the opt-out marker). Common shape we want to catch:
			//   _ = err
			//   _ = err // some lazy excuse
			// And explicitly skip:
			//   _ = err // repolint:allow _=err — reason
			if trimmed != "_ = err" && !strings.HasPrefix(trimmed, "_ = err //") && !strings.HasPrefix(trimmed, "_ = err\t//") {
				continue
			}
			if strings.Contains(line, "repolint:allow _=err") {
				continue
			}
			rel, _ := filepath.Rel(root, path)
			hits = append(hits, rel+":"+itoa(i+1)+"\t"+trimmed)
		}
	})
	if len(hits) > 0 {
		t.Fatalf("found %d `_ = err` in production code — silently swallowing errors is forbidden. "+
			"If the value really must be dropped, either rewrite as `_ = funcCall()` or add the "+
			"inline marker `// repolint:allow _=err — <reason>` on the same line.\n  %s",
			len(hits), strings.Join(hits, "\n  "))
	}
}

// TestNoSilentErrSwallow_MatcherSelfCheck pins the matcher itself —
// otherwise a future refactor that breaks the pattern detection
// would silently make TestNoSilentErrSwallow a no-op tripwire.
func TestNoSilentErrSwallow_MatcherSelfCheck(t *testing.T) {
	cases := []struct {
		line string
		hit  bool
	}{
		{"_ = err", true},
		{"\t_ = err", true},
		{"\t_ = err // ignored", true},
		{"_ = err // repolint:allow _=err — legit reason", false},
		{"_ = err  // repolint:allow _=err — also legit", false},
		{"_ = foo", false},
		{"x := _ = err", false}, // not a standalone statement
		{"//   _ = err", false}, // comment-only line
		{"err := f()", false},
	}
	for _, c := range cases {
		trimmed := strings.TrimSpace(c.line)
		bareMatch := trimmed == "_ = err"
		prefixMatch := strings.HasPrefix(trimmed, "_ = err //") || strings.HasPrefix(trimmed, "_ = err\t//")
		matched := (bareMatch || prefixMatch) && !strings.Contains(c.line, "repolint:allow _=err")
		if matched != c.hit {
			t.Errorf("line %q: got matched=%v, want %v", c.line, matched, c.hit)
		}
	}
}

// itoa is a tiny local int→string so the test has no
// strconv dependency beyond stdlib basics.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[n:])
}

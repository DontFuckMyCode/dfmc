package tools

// path_utils.go — exported path helpers used across tools to keep
// every file path inside the project root. EnsureWithinRoot is the
// single gate every read/write/edit tool must clear; bypassing it
// would let a fabricated path read /etc/passwd or write outside the
// workspace. PathRelativeToRoot turns absolute paths into
// project-relative ones for surfacing to the model — keeps the host's
// filesystem prefix out of conversation logs and episodic memory.
//
// The containment logic itself lives in internal/pathsafe so the
// [[file:...]] prompt-injection resolver in internal/context can use
// the same primitive without creating an import cycle.

import (
	"path/filepath"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/pathsafe"
)

// PathRelativeToRoot turns an absolute path into a project-root-relative
// one for surfacing in tool Output / Data fields. Pre-2026-04-18 every
// read/write/edit_file leaked the host's full filesystem prefix
// (`C:\Users\...`, `/home/...`) into Data["path"], which then flowed
// into conversation logs, episodic memory, and any downstream
// transcript. The model never needs the absolute path — it operates in
// project-relative space. Falls back to the absolute path (slash-
// normalized) when filepath.Rel can't compute a relative form (e.g.
// different volume on Windows), so the model always gets SOME path.
func PathRelativeToRoot(root, abs string) string {
	if strings.TrimSpace(abs) == "" {
		return ""
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	rel, err := filepath.Rel(absRoot, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// EnsureWithinRoot resolves `path` relative to `root` and refuses
// anything that escapes (lexical `..` walks AND symlink-out-of-tree).
// Thin wrapper over pathsafe.EnsureWithinRoot so the same primitive
// guards the tool surface and the [[file:...]] prompt-injection
// resolver in internal/context — see the package comment in
// internal/pathsafe for the layered defense rationale.
func EnsureWithinRoot(root, path string) (string, error) {
	return pathsafe.EnsureWithinRoot(root, path)
}

// isPathWithin is retained for callers inside this package that need
// the lexical-only containment primitive (no symlink resolution).
func isPathWithin(root, target string) bool {
	return pathsafe.IsPathWithin(root, target)
}

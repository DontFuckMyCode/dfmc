package tools

// path_utils.go — exported path helpers used across tools to keep
// every file path inside the project root. EnsureWithinRoot is the
// single gate every read/write/edit tool must clear; bypassing it
// would let a fabricated path read /etc/passwd or write outside the
// workspace. PathRelativeToRoot turns absolute paths into
// project-relative ones for surfacing to the model — keeps the host's
// filesystem prefix out of conversation logs and episodic memory.

import (
	"fmt"
	"path/filepath"
	"strings"
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
// anything that escapes. Resistance comes from two layers:
//
//  1. Syntactic: filepath.Abs + filepath.Rel; any `..` prefix means
//     the path walks out of the root tree.
//  2. Symbolic: once the lexical check passes, resolve symlinks on
//     both `root` and `absPath` (via filepath.EvalSymlinks) and
//     re-check. This stops a committed symlink like
//     `project/evil -> /etc/passwd` from being reachable through the
//     tool API. If the target doesn't exist yet (e.g. write_file
//     creating a new file), we resolve the nearest existing ancestor
//     instead and re-run the containment check on that, so new-file
//     writes under a sanitary tree still work.
func EnsureWithinRoot(root, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath := path
	if !filepath.IsAbs(path) {
		absPath = filepath.Join(absRoot, path)
	}
	absPath, err = filepath.Abs(absPath)
	if err != nil {
		return "", err
	}
	if !isPathWithin(absRoot, absPath) {
		return "", fmt.Errorf("path escapes project root: %s", path)
	}
	// Symlink check. Evaluate both sides so a root that is itself
	// /var/task (symlinked from /opt) still matches a path resolved
	// through the same symlink.
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve project root symlinks: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// Target doesn't exist yet (write_file creating new file) or
		// a dangling symlink. Walk up until we find an existing
		// ancestor and resolve that — any escape through a symlink
		// in the existing ancestor chain still gets caught.
		resolvedPath, err = resolveExistingAncestor(absPath)
		if err != nil {
			return "", fmt.Errorf("cannot resolve symlink ancestry for %q: %w", path, err)
		}
	}
	if !isPathWithin(resolvedRoot, resolvedPath) {
		return "", fmt.Errorf("path escapes project root via symlink: %s", path)
	}
	return absPath, nil
}

// isPathWithin reports whether target is at or under root using Rel —
// so it handles trailing-slash and case-insensitive-FS oddities via
// the same primitive the old check used.
func isPathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// resolveExistingAncestor walks upward from `absPath` until it finds
// a directory that exists, then returns its symlink-resolved form.
// This is the fallback used when the target of a write_file call
// doesn't exist yet — we still want to catch an attempt to write
// through `projectRoot/symlink-to-etc/newfile`.
func resolveExistingAncestor(absPath string) (string, error) {
	current := absPath
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor")
		}
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			// Resolved the existing ancestor. Check if the full target
			// path under the resolved ancestor is within the project root.
			// We reconstruct the path by appending the remaining relative
			// components so symlink-to-absolute-path escapes are caught.
			rel, err := filepath.Rel(parent, absPath)
			if err != nil {
				return "", fmt.Errorf("cannot compute relative path: %w", err)
			}
			reconstructed := filepath.Join(resolved, rel)
			return reconstructed, nil
		}
		current = parent
	}
}

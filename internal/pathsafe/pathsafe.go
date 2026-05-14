// Package pathsafe owns the single canonical implementation of
// "is this path safely contained inside that root, symlinks and all".
// Lives in its own leaf package (no internal/* dependencies) so it can
// be imported by both internal/tools (read/write/edit gate) and
// internal/context (the [[file:...]] prompt-injection resolver)
// without creating an import cycle.
package pathsafe

import (
	"fmt"
	"path/filepath"
	"strings"
)

// EnsureWithinRoot resolves `path` relative to `root` and refuses
// anything that escapes. Resistance comes from two layers:
//
//  1. Syntactic: filepath.Abs + filepath.Rel; any `..` prefix means
//     the path walks out of the root tree.
//  2. Symbolic: once the lexical check passes, resolve symlinks on
//     both `root` and `absPath` (via filepath.EvalSymlinks) and
//     re-check. This stops a committed symlink like
//     `project/evil -> /etc/passwd` from being reachable. If the
//     target doesn't exist yet (e.g. write_file creating a new file),
//     we resolve the nearest existing ancestor instead and re-run
//     the containment check on that.
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
	if !IsPathWithin(absRoot, absPath) {
		return "", fmt.Errorf("path escapes project root: %s", path)
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve project root symlinks: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		resolvedPath, err = resolveExistingAncestor(absPath)
		if err != nil {
			return "", fmt.Errorf("cannot resolve symlink ancestry for %q: %w", path, err)
		}
	}
	if !IsPathWithin(resolvedRoot, resolvedPath) {
		return "", fmt.Errorf("path escapes project root via symlink: %s", path)
	}
	return absPath, nil
}

// IsPathWithin reports whether target is at or under root using Rel —
// handles trailing-slash and case-insensitive-FS oddities via the same
// primitive the lexical check uses.
func IsPathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// resolveExistingAncestor walks upward from absPath until it finds a
// directory that exists, then returns the symlink-resolved form of
// the full target. Used when the target of a write_file call doesn't
// exist yet — still catches attempts to write through
// `projectRoot/symlink-to-elsewhere/newfile`.
func resolveExistingAncestor(absPath string) (string, error) {
	current := absPath
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor")
		}
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			rel, err := filepath.Rel(parent, absPath)
			if err != nil {
				return "", fmt.Errorf("cannot compute relative path: %w", err)
			}
			return filepath.Join(resolved, rel), nil
		}
		current = parent
	}
}

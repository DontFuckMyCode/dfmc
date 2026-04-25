// File listing / read handlers for the web API. Extracted from server.go to
// keep the construction/wiring lean. The listFiles walk and the
// resolvePathWithinRoot guard live here because they're only consumed by
// the file endpoints (and the handful of callers that defend against path
// traversal in other files already reach for resolvePathWithinRoot through
// this file's symbol).

package web

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/security"
)

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	root := s.engine.Status().ProjectRoot
	if strings.TrimSpace(root) == "" {
		writeJSON(w, http.StatusOK, map[string]any{"root": "", "files": []any{}})
		return
	}
	limit := 500
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	files, err := listFiles(root, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"root":  filepath.ToSlash(root),
		"files": files,
	})
}

func (s *Server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	root := s.engine.Status().ProjectRoot
	if strings.TrimSpace(root) == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "project root is not set"})
		return
	}
	rel := strings.TrimSpace(r.PathValue("path"))
	if rel == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "path is required"})
		return
	}
	target, err := resolvePathWithinRoot(root, rel)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return
	}

	// VULN-013: redact secret paths instead of serving raw content.
	// Serving a 403 would reveal the path exists to an attacker who
	// doesn't know the exact names. A redacted=true response tells the
	// UI to show "hidden" without leaking that the file exists.
	if security.LooksLikeSecretFile(rel) {
		info, _ := os.Stat(target)
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":     filepath.ToSlash(rel),
			"type":     "file",
			"size":     size,
			"content":  "",
			"redacted": true,
		})
		return
	}

	info, err := os.Stat(target)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	if info.IsDir() {
		entries, err := os.ReadDir(target)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		items := make([]string, 0, len(entries))
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			items = append(items, name)
		}
		sort.Strings(items)
		writeJSON(w, http.StatusOK, map[string]any{
			"path":    filepath.ToSlash(rel),
			"type":    "dir",
			"entries": items,
		})
		return
	}

	data, err := os.ReadFile(target)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    filepath.ToSlash(rel),
		"type":    "file",
		"size":    len(data),
		"content": string(data),
	})
}

func listFiles(root string, limit int) ([]string, error) {
	out := make([]string, 0, limit)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".dfmc", "node_modules", "vendor", "dist", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, filepath.ToSlash(rel))
		if len(out) >= limit {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return nil, err
	}
	return out, nil
}

// resolvePathWithinRoot resolves `rel` against `root` and returns the
// absolute path, refusing anything that escapes the root via `..` or
// symlink traversal. Pre-2026-04-18 it called EvalSymlinks on `target`
// directly — which fails for paths the caller is about to *create*
// (e.g. /api/v1/admin/magicdoc writing a fresh `docs/brief.md`). The
// active TestResolveMagicDocPath_HonoursRelativeInsideRoot has been
// red on main because of this. Post-fix we walk back to the deepest
// existing ancestor, EvalSymlinks THAT, then re-attach the remaining
// (non-existent) tail. Same containment check still applies, so
// symlink-escapes can't sneak through a not-yet-created leaf.
func resolvePathWithinRoot(root, rel string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("eval root symlinks: %w", err)
	}
	target := rel
	if !filepath.IsAbs(target) {
		target = filepath.Join(absRoot, rel)
	}
	target = filepath.Clean(target)

	resolvedAncestor, tail, err := resolveDeepestExistingAncestor(target)
	if err != nil {
		return "", fmt.Errorf("path resolution failed: %w", err)
	}
	absTarget := resolvedAncestor
	if tail != "" {
		absTarget = filepath.Join(resolvedAncestor, tail)
	}

	relPath, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", err
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project root")
	}
	// Belt-and-braces: even if the lexical Rel says we're inside the
	// root, the RESOLVED ancestor must also be — catches a symlink in
	// the middle of the path that points outside.
	if ancestorRel, aerr := filepath.Rel(absRoot, resolvedAncestor); aerr == nil {
		if ancestorRel == ".." || strings.HasPrefix(ancestorRel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("path escapes project root via symlink")
		}
	}
	return absTarget, nil
}

// resolveDeepestExistingAncestor walks `target` upward until EvalSymlinks
// succeeds, then returns (resolved-ancestor, joined-tail). If the full
// path already exists, tail is "". This lets resolvePathWithinRoot keep
// its symlink-safety guarantees for paths the caller is about to create.
func resolveDeepestExistingAncestor(target string) (string, string, error) {
	current := target
	var tail []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return resolved, filepath.Join(tail...), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			// Reached the volume root and still couldn't resolve — give
			// up cleanly so the caller surfaces a real error.
			return "", "", err
		}
		tail = append([]string{filepath.Base(current)}, tail...)
		current = parent
	}
}

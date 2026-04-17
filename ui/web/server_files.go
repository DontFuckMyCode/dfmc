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

func resolvePathWithinRoot(root, rel string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := rel
	if !filepath.IsAbs(target) {
		target = filepath.Join(absRoot, rel)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	relPath, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", err
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project root")
	}
	return absTarget, nil
}

// list_dir tool: recursive or single-level directory listing with a
// hardcoded skip set for the usual noise (.git, node_modules, vendor,
// bin, dist) and a bounded max_entries cap so an unconstrained call
// can't dump a giant tree into the model's context. Extracted from
// builtin.go.

package tools

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type ListDirTool struct{}

var listDirSkippedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"bin":          true,
	"dist":         true,
}

func NewListDirTool() *ListDirTool  { return &ListDirTool{} }
func (t *ListDirTool) Name() string { return "list_dir" }
func (t *ListDirTool) Description() string {
	return "List files and directories under a path."
}
func (t *ListDirTool) Execute(_ context.Context, req Request) (Result, error) {
	path := asString(req.Params, "path", ".")
	recursive := asBool(req.Params, "recursive", false)
	maxEntries := asInt(req.Params, "max_entries", 200)
	if maxEntries <= 0 {
		maxEntries = 200
	}
	if maxEntries > 500 {
		maxEntries = 500
	}

	absPath, err := EnsureWithinRoot(req.ProjectRoot, path)
	if err != nil {
		return Result{}, err
	}

	var out []string
	if recursive {
		_ = filepath.WalkDir(absPath, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && p != absPath && listDirSkippedDirs[d.Name()] {
				return fs.SkipDir
			}
			rel, _ := filepath.Rel(req.ProjectRoot, p)
			out = append(out, filepath.ToSlash(rel))
			if len(out) >= maxEntries {
				return fs.SkipAll
			}
			return nil
		})
	} else {
		entries, err := os.ReadDir(absPath)
		if err != nil {
			return Result{}, err
		}
		for _, e := range entries {
			if listDirSkippedDirs[e.Name()] {
				continue
			}
			rel, _ := filepath.Rel(req.ProjectRoot, filepath.Join(absPath, e.Name()))
			name := filepath.ToSlash(rel)
			if e.IsDir() {
				name += "/"
			}
			out = append(out, name)
			if len(out) >= maxEntries {
				break
			}
		}
	}

	return Result{
		Data: map[string]any{
			"path":  PathRelativeToRoot(req.ProjectRoot, absPath),
			"count": len(out),
			// `entries` was duplicated in Output (joined newline blob) AND
			// here as a JSON array — the model received every name twice
			// per list_dir call. Output is the canonical surface; Data
			// keeps just the count so the model can branch on emptiness
			// without re-reading the list.
		},
		Output: strings.Join(out, "\n"),
	}, nil
}

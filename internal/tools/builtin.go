package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type ReadFileTool struct{}

func NewReadFileTool() *ReadFileTool { return &ReadFileTool{} }
func (t *ReadFileTool) Name() string { return "read_file" }
func (t *ReadFileTool) Description() string {
	return "Read a text file with optional line range."
}
func (t *ReadFileTool) Execute(_ context.Context, req Request) (Result, error) {
	path := asString(req.Params, "path", "")
	absPath, err := EnsureWithinRoot(req.ProjectRoot, path)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return Result{}, err
	}
	text := string(data)
	lines := strings.Split(text, "\n")

	start := asInt(req.Params, "line_start", 1)
	end := asInt(req.Params, "line_end", len(lines))
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if end < start {
		end = start
	}

	segment := strings.Join(lines[start-1:end], "\n")
	return Result{
		Output: segment,
		Data: map[string]any{
			"path":       absPath,
			"line_start": start,
			"line_end":   end,
			"line_count": len(lines),
		},
	}, nil
}

type ListDirTool struct{}

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
			"path":    absPath,
			"entries": out,
			"count":   len(out),
		},
		Output: strings.Join(out, "\n"),
	}, nil
}

type GrepCodebaseTool struct{}

func NewGrepCodebaseTool() *GrepCodebaseTool { return &GrepCodebaseTool{} }
func (t *GrepCodebaseTool) Name() string     { return "grep_codebase" }
func (t *GrepCodebaseTool) Description() string {
	return "Regex search across project files."
}
func (t *GrepCodebaseTool) Execute(_ context.Context, req Request) (Result, error) {
	pattern := asString(req.Params, "pattern", "")
	if strings.TrimSpace(pattern) == "" {
		return Result{}, fmt.Errorf("pattern is required")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Result{}, fmt.Errorf("invalid regex pattern: %w", err)
	}
	maxResults := asInt(req.Params, "max_results", 100)
	if maxResults <= 0 {
		maxResults = 100
	}

	var matches []string
	err = filepath.WalkDir(req.ProjectRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".dfmc", "node_modules", "vendor", "bin", "dist":
				return fs.SkipDir
			}
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				rel, _ := filepath.Rel(req.ProjectRoot, path)
				matches = append(matches, fmt.Sprintf("%s:%d:%s", filepath.ToSlash(rel), i+1, strings.TrimSpace(line)))
				if len(matches) >= maxResults {
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return Result{}, err
	}

	return Result{
		Output: strings.Join(matches, "\n"),
		Data: map[string]any{
			"pattern": pattern,
			"matches": matches,
			"count":   len(matches),
		},
		Truncated: len(matches) >= maxResults,
	}, nil
}

func asString(m map[string]any, key, fallback string) string {
	if m == nil {
		return fallback
	}
	if v, ok := m[key]; ok {
		switch vv := v.(type) {
		case string:
			return vv
		default:
			return fmt.Sprint(v)
		}
	}
	return fallback
}

func asInt(m map[string]any, key string, fallback int) int {
	if m == nil {
		return fallback
	}
	v, ok := m[key]
	if !ok {
		return fallback
	}
	switch vv := v.(type) {
	case int:
		return vv
	case int32:
		return int(vv)
	case int64:
		return int(vv)
	case float64:
		return int(vv)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(vv))
		if err == nil {
			return n
		}
	}
	return fallback
}

func asBool(m map[string]any, key string, fallback bool) bool {
	if m == nil {
		return fallback
	}
	v, ok := m[key]
	if !ok {
		return fallback
	}
	switch vv := v.(type) {
	case bool:
		return vv
	case string:
		return strings.EqualFold(vv, "true") || vv == "1"
	}
	return fallback
}

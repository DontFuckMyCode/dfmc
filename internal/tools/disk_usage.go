// disk_usage.go — Phase 7 filesystem diagnostics tool.
// Reports disk usage breakdown by file type and language,
// largest files, and per-directory size summaries.
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type DiskUsageTool struct{}

func NewDiskUsageTool() *DiskUsageTool { return &DiskUsageTool{} }
func (t *DiskUsageTool) Name() string  { return "disk_usage" }
func (t *DiskUsageTool) Description() string {
	return "Analyze disk usage: total bytes, per-extension and per-language breakdowns, largest files, and directory sizes."
}

func (t *DiskUsageTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "disk_usage",
		Title:   "Disk usage",
		Summary: "Analyze disk usage for a directory tree with breakdowns by file type and language.",
		Purpose: `Use when you need to understand what's consuming disk space. Returns structured data: total bytes, file count, per-extension breakdown, per-language breakdown (using the standard language map), top 10 largest files, and per-directory summaries to the configured depth. Skips .git, node_modules, vendor, and other standard ignored directories.`,
		Risk:     RiskRead,
		Tags:     []string{"filesystem", "diagnostics", "size", "analysis"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Description: `Directory to analyze. Default: project root.`},
			{Name: "depth", Type: ArgInteger, Default: 3, Description: `Max directory depth for dir summaries. Default: 3.`},
			{Name: "by_type", Type: ArgBoolean, Default: true, Description: `Group by file extension. Default: true.`},
		},
		Returns:        "{total_bytes, files, by_extension: {ext: bytes}, by_language: {lang: bytes}, largest_files: [{path, bytes}], dirs: [{path, bytes, files}]}",
		Idempotent:     true,
		CostHint:       "io-bound",
	}
}

type fileEntry struct {
	path  string
	bytes int64
}

type dirEntry struct {
	path  string
	bytes int64
	files int
	depth int
}

func (t *DiskUsageTool) Execute(ctx context.Context, req Request) (Result, error) {
	path := strings.TrimSpace(asString(req.Params, "path", ""))
	depth := asInt(req.Params, "depth", 3)
	if depth <= 0 {
		depth = 3
	}

	projectRoot := req.ProjectRoot
	if projectRoot == "" {
		projectRoot = "."
	}

	root := projectRoot
	if path != "" {
		// EnsureWithinRoot fences `path="../../"` so the walk can't
		// enumerate the user's home / system filesystem (recon
		// primitive: file paths, sizes, top-N largest with full
		// names — not bytes, but enough to identify ssh keys, db
		// dumps, project layouts).
		abs, err := EnsureWithinRoot(projectRoot, path)
		if err != nil {
			return Result{}, fmt.Errorf("disk_usage: path outside project root: %w", err)
		}
		root = abs
	}

	var totalBytes int64
	var totalFiles int
	byExt := make(map[string]int64)
	byLang := make(map[string]int64)
	var largest []fileEntry
	var dirs []dirEntry

	skipDirs := []string{".git", "node_modules", "vendor", "bin", "dist", ".dfmc", "__pycache__", ".venv", ".idea", ".vscode"}

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			for _, d := range skipDirs {
				if info.Name() == d {
					return filepath.SkipDir
				}
			}
			return nil
		}

		sz := info.Size()
		totalBytes += sz
		totalFiles++

		ext := strings.ToLower(filepath.Ext(path))
		if ext != "" {
			byExt[ext] += sz
		}

		if lang, ok := langMap[ext]; ok {
			byLang[lang] += sz
		}

		largest = append(largest, fileEntry{path, sz})
		return nil
	})

	// Sort largest files, keep top 10.
	sort.Slice(largest, func(i, j int) bool {
		return largest[i].bytes > largest[j].bytes
	})
	if len(largest) > 10 {
		largest = largest[:10]
	}

	// Directory summaries at configured depth.
	dirs = buildDirSummaries(root, depth, skipDirs)

	return Result{
		Output: fmt.Sprintf("disk_usage: %d bytes across %d files (%d dirs to depth %d)",
			totalBytes, totalFiles, len(dirs), depth),
		Data: map[string]any{
			"total_bytes":   totalBytes,
			"files":         totalFiles,
			"by_extension":  byExt,
			"by_language":   byLang,
			"largest_files": largest,
			"dirs":          dirs,
		},
	}, nil
}

func buildDirSummaries(root string, maxDepth int, skipDirs []string) []dirEntry {
	type acc struct {
		bytes int64
		files int
	}
	var results []dirEntry
	accMap := make(map[string]acc)
	depthMap := make(map[string]int)

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			for _, d := range skipDirs {
				if info.Name() == d {
					return filepath.SkipDir
				}
			}
			depth := strings.Count(filepath.ToSlash(path), "/") - strings.Count(filepath.ToSlash(root), "/")
			depthMap[path] = depth
			return nil
		}
		dir := filepath.Dir(path)
		a := accMap[dir]
		a.bytes += info.Size()
		a.files++
		accMap[dir] = a
		return nil
	})

	for path, a := range accMap {
		depth := depthMap[path]
		if depth < 0 || depth > maxDepth {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			rel = root
		}
		results = append(results, dirEntry{
			path:  rel,
			bytes: a.bytes,
			files: a.files,
			depth: depth,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].bytes > results[j].bytes
	})
	if len(results) > 20 {
		results = results[:20]
	}
	return results
}

var langMap = map[string]string{
	".go":   "go",
	".ts":   "typescript",
	".tsx":  "typescript",
	".js":   "javascript",
	".jsx":  "javascript",
	".py":   "python",
	".java": "java",
	".rs":   "rust",
	".c":    "c",
	".cpp":  "cpp",
	".h":    "c-header",
	".cs":   "csharp",
	".rb":   "ruby",
	".php":  "php",
	".md":   "markdown",
	".yaml": "yaml",
	".yml":  "yaml",
	".json": "json",
	".toml": "toml",
	".sh":   "shell",
}

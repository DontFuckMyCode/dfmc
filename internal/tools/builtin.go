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
	if start > len(lines)+1 {
		start = len(lines) + 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if end < start-1 {
		end = start - 1
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

type WriteFileTool struct{}

func NewWriteFileTool() *WriteFileTool { return &WriteFileTool{} }
func (t *WriteFileTool) Name() string  { return "write_file" }
func (t *WriteFileTool) Description() string {
	return "Write or create a text file."
}
func (t *WriteFileTool) Execute(_ context.Context, req Request) (Result, error) {
	path := asString(req.Params, "path", "")
	content := asString(req.Params, "content", "")
	createDirs := asBool(req.Params, "create_dirs", true)
	overwrite := asBool(req.Params, "overwrite", true)

	absPath, err := EnsureWithinRoot(req.ProjectRoot, path)
	if err != nil {
		return Result{}, err
	}
	if createDirs {
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return Result{}, err
		}
	}
	if !overwrite {
		if _, err := os.Stat(absPath); err == nil {
			return Result{}, fmt.Errorf("file already exists: %s", absPath)
		}
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		return Result{}, err
	}
	return Result{
		Output: "file written",
		Data: map[string]any{
			"path":  absPath,
			"bytes": len([]byte(content)),
		},
	}, nil
}

type EditFileTool struct{}

func NewEditFileTool() *EditFileTool { return &EditFileTool{} }
func (t *EditFileTool) Name() string { return "edit_file" }
func (t *EditFileTool) Description() string {
	return "Apply exact string replacement on a text file."
}
func (t *EditFileTool) Execute(_ context.Context, req Request) (Result, error) {
	path := asString(req.Params, "path", "")
	oldStr := asString(req.Params, "old_string", "")
	newStr := asString(req.Params, "new_string", "")
	replaceAll := asBool(req.Params, "replace_all", false)

	if strings.TrimSpace(oldStr) == "" {
		return Result{}, fmt.Errorf("old_string is required")
	}
	if oldStr == newStr {
		return Result{}, fmt.Errorf("old_string and new_string are identical — nothing to do")
	}

	absPath, err := EnsureWithinRoot(req.ProjectRoot, path)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return Result{}, err
	}
	src := string(data)

	// Normalize both haystack and needle to LF for matching so an agent
	// running on Linux can edit a file written on Windows (CRLF) without
	// the match silently failing. The normalized forms are used for the
	// replace; afterward we re-apply the file's original newline style
	// to the rewritten content so we don't flip line endings as a
	// side-effect of the edit.
	wasCRLF := strings.Contains(src, "\r\n")
	normSrc := strings.ReplaceAll(src, "\r\n", "\n")
	normOld := strings.ReplaceAll(oldStr, "\r\n", "\n")
	normNew := strings.ReplaceAll(newStr, "\r\n", "\n")

	n := strings.Count(normSrc, normOld)
	if n == 0 {
		return Result{}, editFileMissMessage(absPath, normSrc, normOld, wasCRLF != strings.Contains(oldStr, "\r\n"))
	}
	if n > 1 && !replaceAll {
		return Result{}, editFileAmbiguityMessage(normSrc, normOld, n)
	}

	replacedN := 1
	updatedNorm := strings.Replace(normSrc, normOld, normNew, 1)
	if replaceAll {
		replacedN = n
		updatedNorm = strings.ReplaceAll(normSrc, normOld, normNew)
	}

	// Restore the file's original newline style so the edit stays a
	// diff of content, not line endings.
	updated := updatedNorm
	if wasCRLF {
		updated = strings.ReplaceAll(updatedNorm, "\n", "\r\n")
	}

	if err := os.WriteFile(absPath, []byte(updated), 0o644); err != nil {
		return Result{}, err
	}
	return Result{
		Output: fmt.Sprintf("file edited (%d replacement%s)", replacedN, plural(replacedN)),
		Data: map[string]any{
			"path":         absPath,
			"replacements": replacedN,
		},
	}, nil
}

// editFileMissMessage crafts a specific "old_string not found" error
// that actually tells the agent *why* the match failed. Zero-context
// errors burn tool rounds — the agent retries the same input and fails
// identically. The hints here (whitespace-trim fuzzy match, CRLF
// mismatch, unique-prefix anchor) steer the retry toward the real
// problem.
func editFileMissMessage(absPath, haystack, needle string, crlfMismatch bool) error {
	var hints []string

	trimmedNeedle := strings.TrimSpace(needle)
	if trimmedNeedle != needle && trimmedNeedle != "" {
		if strings.Contains(haystack, trimmedNeedle) {
			hints = append(hints, "a trimmed form of old_string matches — leading/trailing whitespace in old_string differs from the file")
		}
	}

	// Check whether the first non-trivial line of the needle appears
	// anywhere — helps the agent anchor the retry.
	firstLine := ""
	for _, line := range strings.Split(needle, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			firstLine = line
			break
		}
	}
	if firstLine != "" && !strings.Contains(haystack, firstLine) {
		hints = append(hints, "first non-empty line of old_string doesn't appear verbatim — the indentation may be off")
	}

	if crlfMismatch {
		hints = append(hints, "file uses CRLF line endings; supply old_string with the same line endings or rely on the tool's auto-normalization (already attempted)")
	}

	if strings.Contains(haystack, "\t") && !strings.Contains(needle, "\t") {
		hints = append(hints, "file contains tab indentation; old_string may be using spaces")
	}

	base := fmt.Sprintf("old_string not found in %s", absPath)
	if len(hints) == 0 {
		return fmt.Errorf("%s — re-read the file and copy the exact lines you want to replace", base)
	}
	return fmt.Errorf("%s: %s", base, strings.Join(hints, "; "))
}

// editFileAmbiguityMessage tells the agent exactly how many matches
// were found and the line numbers of the first few, so the retry can
// either pick a more specific old_string or set replace_all=true
// intentionally.
func editFileAmbiguityMessage(haystack, needle string, count int) error {
	lines := strings.Split(haystack, "\n")
	offsets := make([]int, 0, 3)
	for i, line := range lines {
		if strings.Contains(line, needle) || (strings.HasPrefix(needle, "\n") && i+strings.Count(needle, "\n") <= len(lines)) {
			offsets = append(offsets, i+1)
			if len(offsets) >= 3 {
				break
			}
		}
	}
	// Fall back to character-index-based line approximation when needle
	// spans lines (the per-line scan above misses multi-line needles).
	if len(offsets) == 0 {
		idx := 0
		for len(offsets) < 3 {
			hit := strings.Index(haystack[idx:], needle)
			if hit < 0 {
				break
			}
			lineNum := 1 + strings.Count(haystack[:idx+hit], "\n")
			offsets = append(offsets, lineNum)
			idx += hit + len(needle)
		}
	}
	locations := make([]string, 0, len(offsets))
	for _, l := range offsets {
		locations = append(locations, fmt.Sprintf("line %d", l))
	}
	loc := strings.Join(locations, ", ")
	if loc != "" {
		loc = " (" + loc + ")"
	}
	return fmt.Errorf("old_string is not unique: %d matches%s — extend it with surrounding lines for a unique anchor, or pass replace_all=true", count, loc)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
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

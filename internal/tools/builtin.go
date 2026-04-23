package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)



const (
	maxIntValue = int(^uint(0) >> 1)
	minIntValue = -maxIntValue - 1
)



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
	overwrite := asBool(req.Params, "overwrite", false)

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
			rel := PathRelativeToRoot(req.ProjectRoot, absPath)
			return Result{}, fmt.Errorf(
				"write_file refused: %s already exists. "+
					"To replace it intentionally, set overwrite=true (and read it first via read_file so the engine knows the prior contents). "+
					"To make a small edit, use edit_file instead — it only needs old_string/new_string and preserves the rest. "+
					`Recover (overwrite shape): {"name":"write_file","args":{"path":%q,"content":"...","overwrite":true}}.`,
				rel, rel)
		}
	}
	data := map[string]any{
		"path":               PathRelativeToRoot(req.ProjectRoot, absPath),
		"bytes":              len([]byte(content)),
		"overwrote_existing": false,
	}
	if overwrite {
		if oldContent, err := os.ReadFile(absPath); err == nil {
			sum := sha256.Sum256(oldContent)
			data["overwrote_existing"] = true
			data["previous_hash"] = hex.EncodeToString(sum[:])
			data["previous_bytes"] = len(oldContent)
			data["previous_hash_scope"] = "best_effort_prewrite"
			data["previous_hash_verified"] = false
		}
	}
	if err := writeFileAtomic(absPath, []byte(content), 0o644); err != nil {
		return Result{}, err
	}
	return Result{
		Output: "file written",
		Data:   data,
	}, nil
}


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

// missingParamError builds the actionable "<param> is required" reply
// for built-in tools. Pre-fix the error was just "pattern is required" —
// the model couldn't tell whether it had passed the wrong key, sent the
// path AS the pattern, or just forgotten the field. The 2026-04-18
// screenshot caught this exactly: the model hammered grep_codebase /
// glob with only `path: "D:/Codebox/PROJECTS/DFMC"` six times in a row
// because every reply just said "pattern is required" again.
//
// Post-fix we list the keys it ACTUALLY sent + the canonical example +
// (when applicable) the most likely confusion, so the next call can
// self-correct in one round instead of looping with the same bug.
//
// `confusionHint` is appended verbatim when non-empty; pass "" to skip.
func missingParamError(toolName, paramName string, params map[string]any, example, confusionHint string) error {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	got := "(empty)"
	if len(keys) > 0 {
		got = "[" + strings.Join(keys, ", ") + "]"
	}
	msg := fmt.Sprintf(
		"%s requires a `%s` field. Got params keys %s but no `%s`. Correct shape: %s",
		toolName, paramName, got, paramName, example)
	if hint := strings.TrimSpace(confusionHint); hint != "" {
		msg += " " + hint
	}
	return fmt.Errorf("%s", msg)
}

// valueLooksLikePath reports whether `s` is shaped like a filesystem
// path the model might have meant to put in `path` rather than
// `pattern`. Used to add a sharper "you put the path where the pattern
// goes" hint to the missing-pattern error — that was the exact mistake
// in the 2026-04-18 screenshot. Distinct from command.go's looksLikePath
// (which gates run_command's binary slot) because the heuristics differ:
// here a glob meta-char means it's a pattern, not a path.
func valueLooksLikePath(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.ContainsAny(s, "*?[") {
		return false
	}
	if len(s) >= 2 && s[1] == ':' {
		return true
	}
	if strings.ContainsAny(s, "/\\") {
		return true
	}
	return false
}

// formatGrepRegexError turns Go's bare RE2 compile error into an
// actionable message. The model often reaches for Perl/PCRE syntax
// (`\d`, `(?P<name>)`, `(?<=...)`, `\b` lookbehind, possessive `*+`)
// because that's what most regex tutorials teach. Go's `regexp` is
// pure RE2 — none of that works. Pre-fix the error was just
// "invalid regex pattern: error parsing regexp: invalid or unsupported
// Perl syntax" which gave the model nothing to recover from. Post-fix
// the error names the offending construct AND suggests the RE2
// equivalent so the next call self-corrects.

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
		if math.IsNaN(vv) || math.IsInf(vv, 0) || vv != math.Trunc(vv) {
			return fallback
		}
		if vv < float64(minIntValue) || vv > float64(maxIntValue) {
			return fallback
		}
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






func truncateToolTextWithMarker(s string, maxBytes int, marker string) string {
	if maxBytes <= 0 {
		return marker
	}
	if len([]byte(s)) <= maxBytes {
		return s
	}
	markerBytes := len([]byte(marker))
	limit := maxBytes - markerBytes
	if limit < 0 {
		limit = 0
	}
	body := truncateUTF8ByBytes(s, limit)
	body = strings.TrimSuffix(body, "\n... [truncated]")
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return marker
	}
	return body + marker
}



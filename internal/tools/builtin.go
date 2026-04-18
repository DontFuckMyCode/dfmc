package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

	info, err := os.Stat(absPath)
	if err != nil {
		return Result{}, err
	}
	const maxFileSize = 10 << 20 // 10 MB
	if info.Size() > maxFileSize {
		return Result{}, fmt.Errorf("file too large (%d bytes, limit %d) \u2014 use line_start/line_end to read a segment", info.Size(), maxFileSize)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return Result{}, err
	}

	// Reject binary files: if the first 512 bytes contain a NUL, this is
	// almost certainly not text. Reading the whole binary into memory is a
	// waste and produces garbage output for the model.
	checkLen := len(data)
	if checkLen > 512 {
		checkLen = 512
	}
	for i := 0; i < checkLen; i++ {
		if data[i] == 0 {
			return Result{}, fmt.Errorf("file appears to be binary (NUL byte at offset %d) \u2014 read_file only supports text files", i)
		}
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
	if err := writeFileAtomic(absPath, []byte(content), 0o644); err != nil {
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
		return Result{}, missingParamError("edit_file", "old_string", req.Params,
			`{"path":"main.go","old_string":"return nil","new_string":"return ctx.Err()"}`,
			`old_string must be the EXACT text already in the file (whitespace, indentation, line endings included). Read the file first, then copy the unique anchor you want to replace.`)
	}
	if strings.TrimSpace(path) == "" {
		return Result{}, missingParamError("edit_file", "path", req.Params,
			`{"path":"internal/engine/engine.go","old_string":"<exact match>","new_string":"<replacement>"}`,
			`path is the file to edit (relative to project root).`)
	}
	if oldStr == newStr {
		return Result{}, fmt.Errorf(`edit_file: old_string and new_string are identical — nothing to do. Either change new_string, or use read_file if you only wanted to view the section.`)
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

	if err := writeFileAtomic(absPath, []byte(updated), 0o644); err != nil {
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
func formatGrepRegexError(pattern string, err error) error {
	original := err.Error()
	hint := grepRE2Hint(pattern, original)
	if hint == "" {
		return fmt.Errorf("invalid regex pattern %q: %w. grep_codebase uses Go RE2 syntax (https://github.com/google/re2/wiki/Syntax) — Perl/PCRE features like lookbehind, backrefs, possessive quantifiers, and named groups `(?P<name>...)` are NOT supported", pattern, err)
	}
	return fmt.Errorf("invalid regex pattern %q: %w. %s", pattern, err, hint)
}

// grepRE2Hint maps the most common "model wrote PCRE" mistakes to a
// one-line "use this RE2 form instead" suggestion. Empty when the
// pattern doesn't match a known footgun — caller falls back to the
// generic RE2 link.
func grepRE2Hint(pattern, errMsg string) string {
	switch {
	case strings.Contains(pattern, "(?P<"):
		return `RE2 uses (?P<name>...) the same way Python does — but if you're seeing this error you may have nested or unsupported group flags. Try the unnamed (...) form, then index by group number.`
	case strings.Contains(pattern, "(?<=") || strings.Contains(pattern, "(?<!"):
		return `Lookbehind ((?<=...) / (?<!...)) is NOT supported in RE2. Restructure: match the surrounding context with a capturing group instead, or filter the matches in a follow-up step.`
	case strings.Contains(pattern, "(?=") || strings.Contains(pattern, "(?!"):
		return `Lookahead ((?=...) / (?!...)) is NOT supported in RE2. Match the full sequence and post-filter, or use a non-capturing group (?:...) where you don't need consumption.`
	case strings.Contains(pattern, `\1`) || strings.Contains(pattern, `\2`) || strings.Contains(pattern, `\3`):
		return `Backreferences (\1, \2, ...) are NOT supported in RE2 — RE2 guarantees linear-time matching, which precludes them. Match the candidates and check equality in a follow-up.`
	case strings.Contains(pattern, "*+") || strings.Contains(pattern, "++") || strings.Contains(pattern, "?+"):
		return `Possessive quantifiers (*+, ++, ?+) are NOT supported in RE2. Use the regular greedy quantifier — RE2's matching algorithm doesn't backtrack so possessives are unnecessary.`
	case strings.Contains(errMsg, "missing closing"):
		return `An opening bracket / parenthesis is unclosed. Check that every (, [, {, "..." has a matching close.`
	case strings.Contains(errMsg, "invalid character class") || strings.Contains(errMsg, "invalid escape sequence"):
		return `An escape inside a character class or in the body is invalid. RE2 supports \d \w \s \b but not \K \z \Z \v in the same way as Perl. Stick to literals + \d \w \s [a-z] [^...] for portability.`
	}
	return ""
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
		hint := ""
		if p := strings.TrimSpace(asString(req.Params, "path", "")); valueLooksLikePath(p) {
			hint = fmt.Sprintf(`Looks like you put the directory %q in "path" but forgot the regex. grep_codebase searches CONTENT — pass the regex as "pattern" and (optionally) restrict the search with "path". To list files matching a glob instead, use the glob tool.`, p)
		}
		return Result{}, missingParamError("grep_codebase", "pattern", req.Params,
			`{"pattern":"func\\s+NewEngine"} or {"pattern":"TODO","path":"internal"}`,
			hint)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Result{}, formatGrepRegexError(pattern, err)
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

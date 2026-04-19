package tools

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

type ReadFileTool struct{}

const readFileBinaryCheckBytes = 512

const (
	maxGrepFileSize           = 10 << 20
	maxGrepMatchesPerFile     = 50
	maxGrepOutputBytes        = 64 << 10
	defaultGrepScannerBufSize = 64 << 10
)

const (
	maxIntValue = int(^uint(0) >> 1)
	minIntValue = -maxIntValue - 1
)

type matchHit struct {
	Rel  string
	Line int
	Text string
}

const (
	readFileEncodingUTF8       = "utf-8"
	readFileEncodingUTF16LEBOM = "utf-16le-bom"
	readFileEncodingUTF16BEBOM = "utf-16be-bom"
)

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

	text, encoding, checkLen, err := decodeReadFileText(data)
	if err != nil {
		return Result{}, err
	}

	// Reject binary files using a cheap text heuristic: if the first 512
	// bytes contain a NUL, this is almost certainly not text. We surface the
	// window size in Result.Data for successful reads too, so callers know
	// exactly what "binary-safe" means here.
	lines := strings.Split(text, "\n")

	start := asInt(req.Params, "line_start", 1)
	// Default end caps the window at 200 lines from `start` so a bare
	// {"path":"X"} call doesn't dump a 5000-line file into the model's
	// context. The Spec advertises this default and the spec's `view`
	// contract relies on `truncated:true` firing when the cap kicks in;
	// without the cap, truncated was dead code. Callers that genuinely
	// want the whole file can pass line_end explicitly.
	const defaultWindow = 200
	defaultEnd := start + defaultWindow - 1
	if defaultEnd > len(lines) {
		defaultEnd = len(lines)
	}
	end := asInt(req.Params, "line_end", defaultEnd)
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
	// Returned-vs-total telemetry — mirrors the spec's `view` contract:
	// the model gets `total_lines` so it can decide whether to widen the
	// range, and `truncated` so a partial read is loud, not silent.
	totalLines := len(lines)
	returnedLines := end - start + 1
	if returnedLines < 0 {
		returnedLines = 0
	}
	// truncated means "the file extends past what you got" — always set
	// when returned < total. We can't reliably distinguish "caller asked
	// for a slice" from "engine clamped to default 200" here because the
	// engine's normalizeToolParams injects default line_start/line_end
	// before Execute runs, so the caller-intent signal is gone by this
	// point. Honest answer: tell the model whether it got everything.
	truncated := returnedLines < totalLines
	if truncated {
		segment = appendReadFileTruncationMarker(segment, start, end, totalLines)
	}
	return Result{
		Output: segment,
		Data: map[string]any{
			"path":               PathRelativeToRoot(req.ProjectRoot, absPath),
			"line_start":         start,
			"line_end":           end,
			"line_count":         totalLines, // legacy field name
			"total_lines":        totalLines, // spec-aligned alias
			"returned_lines":     returnedLines,
			"truncated":          truncated,
			"language":           detectLanguageFromExt(absPath),
			"encoding":           encoding,
			"binary_check_bytes": checkLen,
			"binary_heuristic":   "nul-in-first-window",
		},
		Truncated: truncated,
	}, nil
}

func decodeReadFileText(data []byte) (string, string, int, error) {
	checkLen := len(data)
	if checkLen > readFileBinaryCheckBytes {
		checkLen = readFileBinaryCheckBytes
	}
	if len(data) >= 2 {
		switch {
		case data[0] == 0xFF && data[1] == 0xFE:
			text, err := decodeUTF16WithBOM(data[2:], binary.LittleEndian)
			return text, readFileEncodingUTF16LEBOM, checkLen, err
		case data[0] == 0xFE && data[1] == 0xFF:
			text, err := decodeUTF16WithBOM(data[2:], binary.BigEndian)
			return text, readFileEncodingUTF16BEBOM, checkLen, err
		}
	}
	for i := 0; i < checkLen; i++ {
		if data[i] == 0 {
			return "", "", checkLen, fmt.Errorf("file appears to be binary (NUL byte at offset %d) \u2014 read_file only supports text files", i)
		}
	}
	return string(data), readFileEncodingUTF8, checkLen, nil
}

func decodeUTF16WithBOM(data []byte, order binary.ByteOrder) (string, error) {
	if len(data)%2 != 0 {
		return "", fmt.Errorf("file appears to be malformed UTF-16 text (odd payload length after BOM)")
	}
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i < len(data); i += 2 {
		units = append(units, order.Uint16(data[i:i+2]))
	}
	return string(utf16.Decode(units)), nil
}

// detectLanguageFromExt maps a file path to a short language tag the
// model can use for syntax-highlighting hints. Mirrors the AST engine's
// extension table; centralised here as a small lookup so read_file
// doesn't pull in the full AST stack just for a string label.
func detectLanguageFromExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".jsx":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".java":
		return "java"
	case ".rs":
		return "rust"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".cxx", ".hpp":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".swift":
		return "swift"
	case ".kt", ".kts":
		return "kotlin"
	case ".scala":
		return "scala"
	case ".php":
		return "php"
	case ".rb":
		return "ruby"
	case ".lua":
		return "lua"
	case ".md", ".markdown":
		return "markdown"
	case ".yml", ".yaml":
		return "yaml"
	case ".json":
		return "json"
	case ".toml":
		return "toml"
	case ".sh", ".bash":
		return "bash"
	}
	return ""
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

	// Restore the file's original per-line newline style so the edit
	// stays a diff of content, not an accidental whole-file EOL rewrite.
	updated := updatedNorm
	if wasCRLF {
		updated = restoreOriginalLineEndings(src, updatedNorm)
	}

	if err := writeFileAtomic(absPath, []byte(updated), 0o644); err != nil {
		return Result{}, err
	}
	return Result{
		Output: fmt.Sprintf("file edited (%d replacement%s)", replacedN, plural(replacedN)),
		Data: map[string]any{
			"path":         PathRelativeToRoot(req.ProjectRoot, absPath),
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
	offsets := make([]int, 0, 3)
	seen := map[int]struct{}{}
	idx := 0
	for len(offsets) < 3 {
		hit := strings.Index(haystack[idx:], needle)
		if hit < 0 {
			break
		}
		lineNum := 1 + strings.Count(haystack[:idx+hit], "\n")
		if _, ok := seen[lineNum]; !ok {
			seen[lineNum] = struct{}{}
			offsets = append(offsets, lineNum)
		}
		idx += hit + len(needle)
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
		pattern = asString(req.Params, "query", "")
	}
	if strings.TrimSpace(pattern) == "" {
		hint := ""
		if p := strings.TrimSpace(asString(req.Params, "path", "")); valueLooksLikePath(p) {
			hint = fmt.Sprintf(`Looks like you put the directory %q in "path" but forgot the regex. grep_codebase searches CONTENT — pass the regex as "pattern" and (optionally) restrict the search with "path". To list files matching a glob instead, use the glob tool.`, p)
		}
		return Result{}, missingParamError("grep_codebase", "pattern", req.Params,
			`{"pattern":"func\\s+NewEngine"} or {"pattern":"TODO","path":"internal"}`,
			hint)
	}

	caseSensitive := asBool(req.Params, "case_sensitive", true)
	compilePattern := pattern
	// case_sensitive=false short-circuits to a (?i) prefix so the model
	// doesn't have to know about RE2 inline flags. Honour an explicit
	// (?i) the model wrote — don't double-prefix.
	if !caseSensitive && !strings.HasPrefix(strings.TrimSpace(compilePattern), "(?i)") {
		compilePattern = "(?i)" + compilePattern
	}
	re, err := regexp.Compile(compilePattern)
	if err != nil {
		return Result{}, formatGrepRegexError(pattern, err)
	}

	maxResults := asInt(req.Params, "max_results", 100)
	if maxResults <= 0 {
		maxResults = 100
	}

	// Search root: optional sub-path; falls back to project root.
	base := req.ProjectRoot
	if rootArg := strings.TrimSpace(asString(req.Params, "path", "")); rootArg != "" {
		p, perr := EnsureWithinRoot(req.ProjectRoot, rootArg)
		if perr != nil {
			return Result{}, perr
		}
		base = p
	}

	// Context window for each match. `context` sets symmetric N before/after;
	// `before` / `after` override per side. Capped at 50 lines per side so a
	// runaway value can't blow the result budget.
	contextLines := asInt(req.Params, "context", 0)
	beforeLines := asInt(req.Params, "before", contextLines)
	afterLines := asInt(req.Params, "after", contextLines)
	if beforeLines < 0 {
		beforeLines = 0
	}
	if afterLines < 0 {
		afterLines = 0
	}
	if beforeLines > 50 {
		beforeLines = 50
	}
	if afterLines > 50 {
		afterLines = 50
	}

	includeGlobs := splitGlobList(req.Params, "include")
	excludeGlobs := splitGlobList(req.Params, "exclude")

	respectGitignore := asBool(req.Params, "respect_gitignore", true)
	var ignoreMatcher *gitignoreMatcher
	if respectGitignore {
		ignoreMatcher = loadGitignore(req.ProjectRoot)
	}

	var hits []matchHit
	var blocks []string
	skippedFiles := 0

	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".dfmc", "node_modules", "vendor", "bin", "dist":
				return fs.SkipDir
			}
			if ignoreMatcher != nil {
				rel, _ := filepath.Rel(req.ProjectRoot, path)
				relSlash := filepath.ToSlash(rel)
				if relSlash != "." && ignoreMatcher.matchDir(relSlash) {
					return fs.SkipDir
				}
			}
			return nil
		}
		rel, _ := filepath.Rel(req.ProjectRoot, path)
		relSlash := filepath.ToSlash(rel)

		if ignoreMatcher != nil && ignoreMatcher.matchFile(relSlash) {
			return nil
		}
		if len(includeGlobs) > 0 && !anyGlobMatches(includeGlobs, relSlash) {
			return nil
		}
		if len(excludeGlobs) > 0 && anyGlobMatches(excludeGlobs, relSlash) {
			return nil
		}

		safePath, serr := EnsureWithinRoot(req.ProjectRoot, path)
		if serr != nil {
			skippedFiles++
			return nil
		}
		info, statErr := os.Stat(safePath)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > maxGrepFileSize {
			return nil
		}
		fileHits, fileBlocks, truncated, grepErr := grepFileMatches(safePath, relSlash, re, beforeLines, afterLines, maxResults-len(hits))
		if grepErr != nil {
			return nil
		}
		hits = append(hits, fileHits...)
		blocks = append(blocks, fileBlocks...)
		if len(hits) >= maxResults || truncated {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return Result{}, err
	}

	var output string
	if beforeLines == 0 && afterLines == 0 {
		simple := make([]string, 0, len(hits))
		for _, h := range hits {
			simple = append(simple, fmt.Sprintf("%s:%d:%s", h.Rel, h.Line, h.Text))
		}
		output = strings.Join(simple, "\n")
	} else {
		output = strings.Join(blocks, "\n--\n")
	}
	outputTruncated := false
	if len([]byte(output)) > maxGrepOutputBytes {
		output = truncateToolTextWithMarker(output, maxGrepOutputBytes, "\n... [grep output truncated - refine pattern or narrow path]")
		outputTruncated = true
	}

	data := map[string]any{
		"pattern":        pattern,
		"count":          len(hits),
		"case_sensitive": caseSensitive,
	}
	if beforeLines > 0 || afterLines > 0 {
		data["context_before"] = beforeLines
		data["context_after"] = afterLines
	}
	if len(includeGlobs) > 0 {
		data["include"] = includeGlobs
	}
	if len(excludeGlobs) > 0 {
		data["exclude"] = excludeGlobs
	}
	if outputTruncated {
		data["output_truncated"] = true
	}
	if skippedFiles > 0 {
		data["skipped_files"] = skippedFiles
	}

	return Result{
		Output:    output,
		Data:      data,
		Truncated: len(hits) >= maxResults || outputTruncated,
	}, nil
}

// splitGlobList accepts either ["pattern1","pattern2"] (preferred) or a
// comma-separated string ("**/*.go,internal/**"). Empty/whitespace
// entries are dropped. The model often guesses one shape per language so
// we accept both rather than rejecting the call.
func splitGlobList(params map[string]any, key string) []string {
	if params == nil {
		return nil
	}
	v, ok := params[key]
	if !ok || v == nil {
		return nil
	}
	switch vv := v.(type) {
	case []any:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		out := []string{}
		for _, part := range strings.Split(vv, ",") {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// anyGlobMatches returns true when any glob in `patterns` matches the
// project-relative `relSlash` (forward-slash). Matches doublestar via
// the same helper glob.go uses, plus a basename fallback so `*.go`
// catches Go files at any depth.
func anyGlobMatches(patterns []string, relSlash string) bool {
	base := filepath.Base(relSlash)
	for _, raw := range patterns {
		p := filepath.ToSlash(raw)
		doublestar := strings.Contains(p, "**")
		if globMatch(p, relSlash, doublestar) {
			return true
		}
		if !doublestar {
			if ok, _ := filepath.Match(p, base); ok {
				return true
			}
		}
	}
	return false
}

// formatGrepBlock renders a single match with context lines. The format
// is `path:line:text` for the match line and `path-line-text` for context
// lines (matches ripgrep's rendering — the dash vs colon distinguishes
// context from a real hit at a glance). Lines are 1-indexed.
func formatGrepBlock(rel string, lines []string, idx, before, after int) string {
	start := idx - before
	if start < 0 {
		start = 0
	}
	end := idx + after
	if end >= len(lines) {
		end = len(lines) - 1
	}
	var b strings.Builder
	for j := start; j <= end; j++ {
		sep := "-"
		if j == idx {
			sep = ":"
		}
		fmt.Fprintf(&b, "%s%s%d%s%s", rel, sep, j+1, sep, strings.TrimRight(lines[j], "\r"))
		if j != end {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// gitignoreMatcher is a small, top-level-only `.gitignore` reader. It
// handles the common shapes: blank/comment lines skipped, trailing `/`
// flags directory-only patterns, leading `/` anchors to the root, `**`
// for any-depth, and a basename match for bare patterns. Negation (`!`)
// and per-subdir `.gitignore` files are NOT handled — the cost of a full
// gitignore implementation isn't worth it for an LLM grep helper. The
// hardcoded skip set (.git, node_modules, vendor, bin, dist) catches the
// 90% case anyway; this layer adds project-specific ignores on top.
type gitignoreMatcher struct {
	dirPatterns  []string
	filePatterns []string
}

func loadGitignore(root string) *gitignoreMatcher {
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return nil
	}
	m := &gitignoreMatcher{}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "!") {
			// Negation — skip; full gitignore semantics aren't worth
			// implementing for the LLM grep path.
			continue
		}
		dirOnly := strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		if line == "" {
			continue
		}
		// Strip leading `/` (anchored) — we always match from the project
		// root anyway, so the anchor doesn't change behaviour for our
		// use case.
		line = strings.TrimPrefix(line, "/")
		if dirOnly {
			m.dirPatterns = append(m.dirPatterns, line)
		} else {
			m.filePatterns = append(m.filePatterns, line)
		}
	}
	return m
}

func (m *gitignoreMatcher) matchDir(relSlash string) bool {
	if m == nil {
		return false
	}
	base := filepath.Base(relSlash)
	for _, p := range m.dirPatterns {
		if matchGitignorePattern(p, relSlash, base) {
			return true
		}
	}
	for _, p := range m.filePatterns {
		// Bare patterns also catch directories with the same name.
		if matchGitignorePattern(p, relSlash, base) {
			return true
		}
	}
	return false
}

func (m *gitignoreMatcher) matchFile(relSlash string) bool {
	if m == nil {
		return false
	}
	base := filepath.Base(relSlash)
	for _, p := range m.filePatterns {
		if matchGitignorePattern(p, relSlash, base) {
			return true
		}
	}
	return false
}

func matchGitignorePattern(pattern, relSlash, base string) bool {
	pattern = filepath.ToSlash(pattern)
	doublestar := strings.Contains(pattern, "**")
	if globMatch(pattern, relSlash, doublestar) {
		return true
	}
	if !strings.Contains(pattern, "/") {
		// Bare basename pattern matches anywhere in the tree.
		if ok, _ := filepath.Match(pattern, base); ok {
			return true
		}
	}
	return false
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

func appendReadFileTruncationMarker(segment string, start, end, totalLines int) string {
	if end < start {
		return segment
	}
	beforeOmitted := start - 1
	afterOmitted := totalLines - end
	if afterOmitted < 0 {
		afterOmitted = 0
	}
	if beforeOmitted < 0 {
		beforeOmitted = 0
	}
	msg := ""
	switch {
	case beforeOmitted > 0 && afterOmitted > 0:
		msg = fmt.Sprintf("... [truncated - %d lines omitted before, %d after]", beforeOmitted, afterOmitted)
	case beforeOmitted > 0:
		msg = fmt.Sprintf("... [truncated - %d lines omitted before]", beforeOmitted)
	case afterOmitted > 0:
		msg = fmt.Sprintf("... [truncated - %d more lines omitted]", afterOmitted)
	default:
		return segment
	}
	if strings.TrimSpace(segment) == "" {
		return msg
	}
	return strings.TrimRight(segment, "\n") + "\n" + msg
}

func restoreOriginalLineEndings(original, updatedNorm string) string {
	_, endings := splitLinesAndEndings(original)
	if len(endings) == 0 {
		return strings.ReplaceAll(updatedNorm, "\n", "\r\n")
	}
	defaultEnding := dominantLineEnding(endings)
	updatedLines, trailingNewline := splitNormalizedLines(updatedNorm)
	if len(updatedLines) == 0 {
		return ""
	}

	var b strings.Builder
	for i, line := range updatedLines {
		b.WriteString(line)
		if i == len(updatedLines)-1 && !trailingNewline {
			continue
		}
		ending := defaultEnding
		if i < len(endings) && endings[i] != "" {
			ending = endings[i]
		}
		if ending == "" {
			ending = defaultEnding
		}
		b.WriteString(ending)
	}
	return b.String()
}

func splitLinesAndEndings(s string) ([]string, []string) {
	if s == "" {
		return nil, nil
	}
	lines := make([]string, 0, strings.Count(s, "\n")+1)
	endings := make([]string, 0, cap(lines))
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' {
			continue
		}
		end := i
		ending := "\n"
		if i > start && s[i-1] == '\r' {
			end = i - 1
			ending = "\r\n"
		}
		lines = append(lines, s[start:end])
		endings = append(endings, ending)
		start = i + 1
	}
	if start < len(s) {
		lines = append(lines, s[start:])
		endings = append(endings, "")
	}
	return lines, endings
}

func splitNormalizedLines(s string) ([]string, bool) {
	if s == "" {
		return []string{""}, false
	}
	trailingNewline := strings.HasSuffix(s, "\n")
	trimmed := strings.TrimSuffix(s, "\n")
	lines := strings.Split(trimmed, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines, trailingNewline
}

func dominantLineEnding(endings []string) string {
	crlf := 0
	lf := 0
	for _, ending := range endings {
		switch ending {
		case "\r\n":
			crlf++
		case "\n":
			lf++
		}
	}
	if crlf > lf {
		return "\r\n"
	}
	return "\n"
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

func grepFileMatches(path, rel string, re *regexp.Regexp, beforeLines, afterLines, remaining int) ([]matchHit, []string, bool, error) {
	if remaining <= 0 {
		return nil, nil, true, nil
	}
	if beforeLines > 0 || afterLines > 0 {
		return grepFileMatchesWithContext(path, rel, re, beforeLines, afterLines, remaining)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	bufSize := defaultGrepScannerBufSize
	if bufSize > maxGrepFileSize {
		bufSize = maxGrepFileSize
	}
	scanner.Buffer(make([]byte, 0, bufSize), maxGrepFileSize)

	lines := make([]string, 0, 128)
	lineNo := 0
	hits := make([]matchHit, 0, 4)
	blocks := make([]string, 0, 4)
	perFileMatches := 0
	truncated := false

	for scanner.Scan() {
		lineNo++
		line := strings.TrimRight(scanner.Text(), "\r")
		lines = append(lines, line)
		if !re.MatchString(line) {
			continue
		}
		hits = append(hits, matchHit{Rel: rel, Line: lineNo, Text: strings.TrimSpace(line)})
		if beforeLines > 0 || afterLines > 0 {
			blocks = append(blocks, formatGrepBlock(rel, lines, len(lines)-1, beforeLines, afterLines))
		}
		perFileMatches++
		if len(hits) >= remaining || perFileMatches >= maxGrepMatchesPerFile {
			truncated = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, false, err
	}
	return hits, blocks, truncated, nil
}

func grepFileMatchesWithContext(path, rel string, re *regexp.Regexp, beforeLines, afterLines, remaining int) ([]matchHit, []string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, false, err
	}
	defer f.Close()

	type grepContextLine struct {
		Number int
		Text   string
	}
	type grepOpenBlock struct {
		Lines          []grepContextLine
		MatchLineIndex int
		RemainingAfter int
	}

	scanner := bufio.NewScanner(f)
	bufSize := defaultGrepScannerBufSize
	if bufSize > maxGrepFileSize {
		bufSize = maxGrepFileSize
	}
	scanner.Buffer(make([]byte, 0, bufSize), maxGrepFileSize)

	formatBlock := func(lines []grepContextLine, matchLineIndex int) string {
		var b strings.Builder
		for i, line := range lines {
			sep := "-"
			if i == matchLineIndex {
				sep = ":"
			}
			fmt.Fprintf(&b, "%s%s%d%s%s", rel, sep, line.Number, sep, strings.TrimRight(line.Text, "\r"))
			if i != len(lines)-1 {
				b.WriteByte('\n')
			}
		}
		return b.String()
	}

	beforeBuf := make([]grepContextLine, 0, beforeLines)
	openBlocks := make([]*grepOpenBlock, 0, afterLines+1)
	hits := make([]matchHit, 0, 4)
	blocks := make([]string, 0, 4)
	perFileMatches := 0
	lineNo := 0
	truncated := false

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()

		if len(openBlocks) > 0 {
			active := openBlocks[:0]
			for _, block := range openBlocks {
				block.Lines = append(block.Lines, grepContextLine{Number: lineNo, Text: line})
				block.RemainingAfter--
				if block.RemainingAfter <= 0 {
					blocks = append(blocks, formatBlock(block.Lines, block.MatchLineIndex))
					continue
				}
				active = append(active, block)
			}
			openBlocks = active
		}

		if !re.MatchString(line) {
			if beforeLines > 0 {
				beforeBuf = append(beforeBuf, grepContextLine{Number: lineNo, Text: line})
				if len(beforeBuf) > beforeLines {
					beforeBuf = beforeBuf[1:]
				}
			}
			continue
		}
		hits = append(hits, matchHit{Rel: rel, Line: lineNo, Text: strings.TrimSpace(strings.TrimRight(line, "\r"))})
		window := make([]grepContextLine, 0, len(beforeBuf)+1)
		window = append(window, beforeBuf...)
		window = append(window, grepContextLine{Number: lineNo, Text: line})
		if afterLines > 0 {
			openBlocks = append(openBlocks, &grepOpenBlock{
				Lines:          window,
				MatchLineIndex: len(window) - 1,
				RemainingAfter: afterLines,
			})
		} else {
			blocks = append(blocks, formatBlock(window, len(window)-1))
		}
		perFileMatches++
		if len(hits) >= remaining || perFileMatches >= maxGrepMatchesPerFile {
			truncated = true
			break
		}
		if beforeLines > 0 {
			beforeBuf = append(beforeBuf, grepContextLine{Number: lineNo, Text: line})
			if len(beforeBuf) > beforeLines {
				beforeBuf = beforeBuf[1:]
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, false, err
	}
	for _, block := range openBlocks {
		blocks = append(blocks, formatBlock(block.Lines, block.MatchLineIndex))
	}
	return hits, blocks, truncated, nil
}

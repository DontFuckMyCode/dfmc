// grep_codebase tool + its supporting machinery: RE2 regex-error
// self-teaching hints, the streaming matcher (with and without before/
// after context), include/exclude glob filters, and a top-level-only
// .gitignore reader. Extracted from builtin.go so the grep path stays
// together in one file.

package tools

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxGrepFileSize           = 10 << 20
	maxGrepMatchesPerFile     = 50
	maxGrepOutputBytes        = 64 << 10
	defaultGrepScannerBufSize = 64 << 10
)

type matchHit struct {
	Rel  string
	Line int
	Text string
}

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

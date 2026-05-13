// builtin_grep.go — grep_codebase tool entry: parses params, walks
// the project tree honouring include/exclude globs and the top-level
// .gitignore, runs each candidate file through the scanner pair, and
// formats the result with optional before/after context. The RE2-error
// hint and gitignore reader live in builtin_grep_helpers.go; the
// scanner pair lives in builtin_grep_scan.go.

package tools

import (
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

type GrepCodebaseTool struct{}

func NewGrepCodebaseTool() *GrepCodebaseTool { return &GrepCodebaseTool{} }
func (t *GrepCodebaseTool) Name() string     { return "grep_codebase" }
func (t *GrepCodebaseTool) Description() string {
	return "Regex search across project files."
}
func (t *GrepCodebaseTool) Execute(ctx context.Context, req Request) (Result, error) {
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
	if isLikelyCatastrophic(compilePattern) {
		return Result{}, fmt.Errorf("grep_codebase: pattern may cause exponential backtracking; simplify or use literal search")
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
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if defaultWalkSkipDirs[d.Name()] {
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
// splitGlobList is a thin alias for asStringSlice kept for grep-internal
// readability — call sites read `splitGlobList(params, "include")`
// which conveys "this is a glob list" better than the generic name.
// All shape-coercion logic lives in asStringSlice.
func splitGlobList(params map[string]any, key string) []string {
	return asStringSlice(params, key)
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

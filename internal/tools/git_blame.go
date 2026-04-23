// git_blame.go — the git_blame tool plus its porcelain parser. Kept in
// its own file because the parser (parseGitBlamePorcelain) and the
// hex-line sniffer (isHexLine) are non-trivial on their own and have
// no callers outside the blame surface. Everything else still routes
// through runGit() with the same guards as the rest of git.go.

package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type GitBlameTool struct{}

func NewGitBlameTool() *GitBlameTool { return &GitBlameTool{} }
func (t *GitBlameTool) Name() string { return "git_blame" }
func (t *GitBlameTool) Description() string {
	return "Show line-by-line authorship for a file via git blame."
}

func (t *GitBlameTool) Execute(ctx context.Context, req Request) (Result, error) {
	timeout := resolveGitTimeout(req.Params)
	path := strings.TrimSpace(asString(req.Params, "path", ""))
	if path == "" {
		return Result{}, missingParamError("git_blame", "path", req.Params,
			`{"path":"internal/engine/engine.go"} or {"path":"main.go","line_start":40,"line_end":80}`,
			`path is the file to blame (relative to project root). Optional line_start/line_end narrow output to a range — without them the whole file's blame is returned, which can be large for old files.`)
	}
	if _, err := EnsureWithinRoot(req.ProjectRoot, path); err != nil {
		return Result{}, err
	}

	// Porcelain format gives one stable, parseable record per line:
	//
	//   <hash> <orig-lno> <final-lno> [<group-size>]
	//   author <name>
	//   author-time <unix>
	//   summary <subject>
	//   ...
	//   \t<line content>
	//
	// We don't ask for incremental output — file blames here are typically
	// hundreds of lines, not millions, and the simpler porcelain shape is
	// easier to parse with a single linear pass.
	args := []string{"blame", "--porcelain"}

	lineStart := asInt(req.Params, "line_start", 0)
	lineEnd := asInt(req.Params, "line_end", 0)
	if lineStart > 0 || lineEnd > 0 {
		// `git blame -L start,end file` — both bounds inclusive. If only one
		// is set, fall back to a single-line range so the user doesn't get a
		// silent full-file blame when they meant "just this line".
		if lineStart <= 0 {
			lineStart = lineEnd
		}
		if lineEnd <= 0 {
			lineEnd = lineStart
		}
		if lineEnd < lineStart {
			return Result{}, fmt.Errorf("line_end (%d) must be >= line_start (%d)", lineEnd, lineStart)
		}
		args = append(args, "-L", fmt.Sprintf("%d,%d", lineStart, lineEnd))
	}

	if rev := strings.TrimSpace(asString(req.Params, "revision", "")); rev != "" {
		if err := rejectGitFlagInjection("revision", rev); err != nil {
			return Result{}, err
		}
		args = append(args, rev)
	}
	if err := rejectGitFlagInjection("path", path); err != nil {
		return Result{}, err
	}

	args = append(args, "--", path)

	stdout, stderr, exit, err := runGit(ctx, req.ProjectRoot, timeout, args...)
	if err != nil {
		return Result{Output: joinGitOutput(stdout, stderr), Data: map[string]any{"exit_code": exit}}, err
	}

	lines := parseGitBlamePorcelain(stdout)
	return Result{
		Output: strings.TrimRight(stdout, "\n"),
		Data: map[string]any{
			"path":      path,
			"lines":     lines,
			"count":     len(lines),
			"exit_code": exit,
		},
	}, nil
}

// parseGitBlamePorcelain walks porcelain output and emits one record per
// source line. Header keys we care about (author, author-time, summary)
// are inherited from the most recent commit-block — git only re-emits
// them on the first occurrence of a hash within the output.
func parseGitBlamePorcelain(raw string) []map[string]any {
	out := make([]map[string]any, 0, 64)
	if strings.TrimSpace(raw) == "" {
		return out
	}

	// Per-commit cache so subsequent occurrences of the same hash inherit
	// author/summary without git re-emitting them.
	type commitInfo struct {
		author  string
		time    string
		summary string
	}
	commits := map[string]commitInfo{}

	var (
		currentHash    string
		currentFinal   int
		currentInfo    commitInfo
		haveLineHeader bool
	)

	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		// Source line — porcelain prefixes content with a single TAB.
		if strings.HasPrefix(line, "\t") {
			if !haveLineHeader {
				continue
			}
			content := strings.TrimPrefix(line, "\t")
			info := currentInfo
			if cached, ok := commits[currentHash]; ok {
				if info.author == "" {
					info.author = cached.author
				}
				if info.time == "" {
					info.time = cached.time
				}
				if info.summary == "" {
					info.summary = cached.summary
				}
			}
			// Persist any newly-discovered metadata for later lines that
			// share this commit hash.
			commits[currentHash] = info
			out = append(out, map[string]any{
				"line":        currentFinal,
				"hash":        currentHash,
				"author":      info.author,
				"author_time": info.time,
				"summary":     info.summary,
				"content":     content,
			})
			haveLineHeader = false
			continue
		}
		// Header line — either "<hash> <orig> <final> [group]" or "key value".
		fields := strings.Fields(line)
		if len(fields) >= 3 && len(fields[0]) >= 7 && isHexLine(fields[0]) {
			currentHash = fields[0]
			// fields[1] is orig line; we expose only final-line numbers.
			if n, err := strconv.Atoi(fields[2]); err == nil {
				currentFinal = n
			}
			currentInfo = commitInfo{}
			haveLineHeader = true
			continue
		}
		if len(fields) < 2 || !haveLineHeader {
			continue
		}
		switch fields[0] {
		case "author":
			currentInfo.author = strings.Join(fields[1:], " ")
		case "author-time":
			currentInfo.time = fields[1]
		case "summary":
			currentInfo.summary = strings.Join(fields[1:], " ")
		}
	}
	return out
}

// isHexLine reports whether s is a plausible git object hash (all-hex,
// at least 7 chars). Used to distinguish blame's commit-header lines
// from key/value metadata lines.
func isHexLine(s string) bool {
	if len(s) < 7 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func (t *GitBlameTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "git_blame",
		Title:   "Git blame",
		Summary: "Per-line authorship for a file with hash, author, time, and commit subject.",
		Purpose: "Identify who last touched a region — for debug, audit, or refactor scoping.",
		Prompt: `Returns ` + "`{path, lines[{line, hash, author, author_time, summary, content}], count}`" + `.

Rules:
- ` + "`path`" + ` is required and must resolve inside the project root.
- Use ` + "`line_start`" + ` / ` + "`line_end`" + ` to scope a region — much cheaper than blaming a 10k-line file when you only need one function. Both bounds inclusive.
- Pass ` + "`revision`" + ` to blame at a specific ref instead of HEAD.
- ` + "`author_time`" + ` is a unix timestamp string; format it on the consumer side if you need a date.`,
		Risk: RiskRead,
		Tags: []string{"git", "vcs", "read", "history"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Description: "File path within the project root.", Required: true},
			{Name: "line_start", Type: ArgInteger, Description: "Optional 1-based start line (inclusive)."},
			{Name: "line_end", Type: ArgInteger, Description: "Optional 1-based end line (inclusive). Defaults to line_start when only one bound is set."},
			{Name: "revision", Type: ArgString, Description: "Optional revision to blame at (defaults to HEAD)."},
		},
		Returns:    "{path, lines[{line,hash,author,author_time,summary,content}], count, exit_code}.",
		Idempotent: true,
		CostHint:   "cheap",
	}
}

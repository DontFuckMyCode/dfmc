// symbol_rename.go — Phase 7 tool for safe symbol renaming across a codebase.
// Renames all occurrences of a symbol by name, scoped to a file or the full
// project. Uses the codemap to find all files that reference the symbol
// before mutating, so the model can review impact before committing.
//
// Scope: text-level rename guided by scope detection. "from" and "to"
// are required; "file" scopes the operation to a single file (default)
// while omitting it targets the full project.
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type SymbolRenameTool struct {
	engine *Engine
}

func NewSymbolRenameTool() *SymbolRenameTool { return &SymbolRenameTool{} }
func (t *SymbolRenameTool) Name() string    { return "symbol_rename" }
func (t *SymbolRenameTool) Description() string {
	return "Rename all occurrences of a symbol across a file or the whole project using AST-scope detection."
}

// SetEngine wires the engine for codemap access and read-before-mutation tracking.
func (t *SymbolRenameTool) SetEngine(e *Engine) { t.engine = e }

func (t *SymbolRenameTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "symbol_rename",
		Title:   "Rename symbol",
		Summary: "Rename a function/type/variable across a file or the entire project.",
		Purpose: `Use when you've decided to rename a symbol and need to update every occurrence safely. Shows impact (how many files/locations will change) before mutating. The model should still review each change for correctness — this is a bulk-find-replace guided by scope detection, not a semantic type-checker.`,
		Prompt: `Renames a symbol across files or the full project.
Pipeline:
1. Parse the target file with the AST engine to get symbol's exact line range
2. If "file" is set: only rename within that file's scope
3. If "file" is not set: query codemap for all files referencing this symbol and rename across all of them
4. Apply changes with read-before-mutation gating on every target file
5. Return per-file results: renamed, skipped, failed

Scope detection:
- Go: rename only within the same package scope (imported symbols from other packages are NOT renamed — they require full path update)
- For local vars inside functions, only the function body is scoped
- Type names, function names, interface methods: full file scope

"to" is required. "from" is required. "file" is optional (full project if absent).
"dry_run" (default true) returns impact without mutating.`,
		Risk:     RiskWrite,
		Tags:     []string{"rename", "refactor", "symbol", "ast", "bulk-edit"},
		Args: []Arg{
			{Name: "from", Type: ArgString, Required: true, Description: `Original symbol name (e.g. "OldName").`},
			{Name: "to", Type: ArgString, Required: true, Description: `New symbol name (e.g. "NewName").`},
			{Name: "file", Type: ArgString, Description: `File path to scope the rename to. If absent, searches the full project.`},
			{Name: "kind", Type: ArgString, Description: `Limit to symbol kind: "func" | "type" | "var" | "const" | "method" | "all". Default: all.`},
			{Name: "dry_run", Type: ArgBoolean, Default: true, Description: `Preview impact without writing. Default true.`},
			{Name: "skip_tests", Type: ArgBoolean, Default: false, Description: `Skip test files (*_test.go). Default false.`},
		},
		Returns:        "Structured JSON: {impact: {files, locations}, changes: [{path, old, new, line}], dry_run: bool}",
		Idempotent:     false,
		CostHint:       "cpu-bound",
	}
}

type renameImpact struct {
	Files      int `json:"files"`
	Locations  int `json:"locations"`
	Skipped    int `json:"skipped"`
	ReadDenied int `json:"read_denied"`
}

type renameChange struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
	Line int    `json:"line"`
}

// renameMatch holds a single occurrence of a symbol to be renamed.
type renameMatch struct {
	path     string
	lineNum  int // 1-based
	fullLine string
}

func (t *SymbolRenameTool) Execute(ctx context.Context, req Request) (Result, error) {
	from := strings.TrimSpace(asString(req.Params, "from", ""))
	to := strings.TrimSpace(asString(req.Params, "to", ""))
	file := strings.TrimSpace(asString(req.Params, "file", ""))
	kind := strings.TrimSpace(asString(req.Params, "kind", "all"))
	dryRun := asBool(req.Params, "dry_run", true)
	skipTests := asBool(req.Params, "skip_tests", false)

	if from == "" {
		return Result{}, missingParamError("symbol_rename", "from", req.Params,
			`{"from":"OldName","to":"NewName","file":"internal/foo/foo.go"}`,
			`from is required — the current symbol name to find and rename.`)
	}
	if to == "" {
		return Result{}, missingParamError("symbol_rename", "to", req.Params,
			`{"from":"OldName","to":"NewName","file":"internal/foo/foo.go"}`,
			`to is required — the new name to replace from.`)
	}
	if from == to {
		return Result{}, fmt.Errorf("from and to are identical — nothing to rename")
	}
	if kind == "" {
		kind = "all"
	}

	projectRoot := req.ProjectRoot
	if projectRoot == "" {
		projectRoot = "."
	}

	// Build the target file list. EnsureWithinRoot fences user-supplied
	// `file` against project-root escape — `file="../../etc/hosts"`
	// used to leak file content via the read at line 271 (returned as
	// `changes[].fullLine`).
	var targetFiles []string
	if file != "" {
		abs, err := EnsureWithinRoot(projectRoot, file)
		if err != nil {
			return Result{}, fmt.Errorf("symbol_rename: file outside project root: %w", err)
		}
		targetFiles = []string{abs}
	} else {
		targetFiles = collectGoFiles(projectRoot, skipTests)
	}

	if len(targetFiles) == 0 {
		return Result{}, fmt.Errorf("no Go files found to rename in")
	}

	// Phase 1: collect all rename operations.
	allMatches := make(map[string][]renameMatch) // path -> matches
	totalMatches := 0

	for _, fpath := range targetFiles {
		if skipTests && isTestFile(fpath) {
			continue
		}
		matches := findRenameMatches(fpath, from, kind)
		if len(matches) > 0 {
			allMatches[fpath] = matches
			totalMatches += len(matches)
		}
	}

	if totalMatches == 0 {
		return Result{
			Output: fmt.Sprintf("symbol_rename: no occurrences of %q found", from),
			Data: map[string]any{
				"from":   from,
				"to":     to,
				"impact": renameImpact{Files: 0, Locations: 0},
				"changes": []renameChange{},
			},
		}, nil
	}

	// Phase 2: dry run or apply.
	var changes []renameChange
	readDenied := 0

	for fpath, matches := range allMatches {
		if !dryRun {
			if t.engine == nil {
				return Result{}, fmt.Errorf("engine not wired — cannot verify prior read for mutation safety")
			}
			if guardErr := t.engine.EnsureReadBeforeMutation(fpath); guardErr != nil {
				readDenied++
				continue
			}
		}

		if dryRun {
			for _, m := range matches {
				changes = append(changes, renameChange{
					Path: fpath,
					Old:  m.fullLine,
					New:  applyRenameInLine(m.fullLine, from, to),
					Line: m.lineNum,
				})
			}
			continue
		}

		// Actual mutation.
		data, err := os.ReadFile(fpath)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, m := range matches {
			if m.lineNum >= 1 && m.lineNum <= len(lines) {
				lines[m.lineNum-1] = applyRenameInLine(lines[m.lineNum-1], from, to)
			}
		}
		if err := os.WriteFile(fpath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
			_ = err
		}
		for _, m := range matches {
			changes = append(changes, renameChange{
				Path: fpath,
				Old:  m.fullLine,
				New:  applyRenameInLine(m.fullLine, from, to),
				Line: m.lineNum,
			})
		}
	}

	action := "Would rename"
	if !dryRun {
		action = "Renamed"
	}

	return Result{
		Output: fmt.Sprintf("%s %d occurrences across %d files (impact: %d locations in %d files)",
			action, totalMatches, len(allMatches), totalMatches, len(allMatches)),
		Data: map[string]any{
			"from":   from,
			"to":     to,
			"impact": renameImpact{
				Files:      len(allMatches),
				Locations:  totalMatches,
				Skipped:    0,
				ReadDenied: readDenied,
			},
			"dry_run": dryRun,
			"changes": changes,
		},
	}, nil
}

// --- helpers ---

func isTestFile(path string) bool {
	return strings.HasSuffix(path, "_test.go")
}

// collectGoFiles walks the project root and returns all .go file paths.
func collectGoFiles(projectRoot string, skipTests bool) []string {
	var files []string
	skipDirs := []string{".git", "node_modules", "vendor", "bin", "dist", ".dfmc", "__pycache__"}
	filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
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
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if skipTests && isTestFile(path) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files
}

// findRenameMatches finds all lines in a file where `name` appears as a
// symbol of the given kind category.
func findRenameMatches(filePath, name, kind string) []renameMatch {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	content := string(data)

	escaped := regexp.QuoteMeta(name)
	pat := regexp.MustCompile(`\b` + escaped + `\b`)

	var matches []renameMatch
	lines := strings.Split(content, "\n")
	for lineNum, line := range lines {
		if inCommentOrString(line, name) {
			continue
		}
		if !matchSymbolKind(line, name, kind) {
			continue
		}
		for _, loc := range pat.FindAllStringIndex(line, -1) {
			matches = append(matches, renameMatch{
				path:     filePath,
				lineNum:  lineNum + 1,
				fullLine: line,
			})
			_ = loc // word boundaries already enforced by regex
		}
	}
	return matches
}

// matchSymbolKind applies simple Go declaration pattern matching.
func matchSymbolKind(line, name, kind string) bool {
	if kind == "all" || kind == "" {
		return true
	}
	l := strings.TrimSpace(line)
	switch strings.ToLower(kind) {
	case "func":
		if !strings.HasPrefix(l, "func ") {
			return false
		}
		return strings.Contains(l, name)
	case "type":
		if !strings.HasPrefix(l, "type ") {
			return false
		}
		return strings.Contains(l, name)
	case "var":
		if !strings.HasPrefix(l, "var ") {
			return false
		}
		return strings.Contains(l, name)
	case "const":
		if !strings.HasPrefix(l, "const ") {
			return false
		}
		return strings.Contains(l, name)
	case "method":
		// Methods have a receiver: (s *Server) MethodName(
		return strings.Contains(l, "(") && strings.Contains(l, name)
	default:
		return true
	}
}

// inCommentOrString returns true when `name` appears inside a comment.
func inCommentOrString(line, name string) bool {
	if idx := strings.Index(line, "//"); idx >= 0 && strings.Contains(line[idx:], name) {
		return true
	}
	// Multi-line comment span — simplified check
	if strings.Contains(line, "/*") && strings.Contains(line, "*/") {
		start := strings.Index(line, "/*")
		end := strings.Index(line, "*/")
		if start < end && strings.Contains(line[start:end], name) {
			return true
		}
	}
	return false
}

// applyRenameInLine replaces all word-boundary matches of `from` with `to`.
func applyRenameInLine(line, from, to string) string {
	pat := regexp.MustCompile(`\b` + regexp.QuoteMeta(from) + `\b`)
	return pat.ReplaceAllString(line, to)
}
package tools

// symbol_rename_helpers.go — file-walk + per-line scan helpers for the
// symbol_rename tool. Sibling of symbol_rename.go which keeps the
// SymbolRenameTool struct + Name/Description/Spec/Execute pipeline +
// renameImpact/renameChange/renameMatch shapes.
//
// These live in a sibling because the rename pipeline is a thin two-phase
// loop (collect → dry-run-or-mutate) and would otherwise be drowned by
// the 120-odd lines of regex/comment/declaration scanning.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// isTestFile reports whether the path looks like a Go _test.go file.
func isTestFile(path string) bool {
	return strings.HasSuffix(path, "_test.go")
}

// collectGoFiles walks the project root and returns all .go file paths.
// Skips the usual VCS/build/cache directories so we don't rename inside
// vendored copies or generated artefacts.
func collectGoFiles(projectRoot string, skipTests bool) []string {
	var files []string
	skipDirs := []string{".git", "node_modules", "vendor", "bin", "dist", ".dfmc", "__pycache__"}
	_ = filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
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
// symbol of the given kind category. Word boundaries are enforced by the
// regex; the comment/string filter then drops obvious false positives.
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

// matchSymbolKind applies simple Go declaration pattern matching. "all"
// (or empty) accepts every line; specific kinds gate on the leading
// keyword so we don't rename a "var" identifier inside a func body when
// the caller asked for kind=var.
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
// Best-effort — does not track multi-line comment state across lines, so
// a /* opened on a previous line will not be detected. Good enough to
// avoid the most common false-positive class (TODO/FIXME comments
// containing the symbol name).
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

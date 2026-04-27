package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// TestDiscoveryTool locates test files and their test functions for a given
// source file or search pattern. This collapses the "find tests for X"
// workflow that requires either convention knowledge (Go: foo_test.go) or
// running `go test -list` / `pytest --collect-only` / `jest --findRelatedTests`.
// The model should still validate with the actual test runner, but this tool
// pre-populates the search space so the model doesn't issue a shell command
// just to discover test file paths.
type TestDiscoveryTool struct{}

func NewTestDiscoveryTool() *TestDiscoveryTool { return &TestDiscoveryTool{} }
func (t *TestDiscoveryTool) Name() string    { return "test_discovery" }
func (t *TestDiscoveryTool) Description() string {
	return "Find test files and test functions that cover a given source file or symbol."
}

func (t *TestDiscoveryTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "test_discovery",
		Title:   "Discover tests",
		Summary: "Find test files and test functions that cover a source file, directory, or named symbol.",
		Purpose: `Use after editing code to find which tests to run, or before editing to understand what a function/module is expected to do. Returns structured test location data — still validate with the actual test runner.`,
		Prompt: `Locates tests for the given source file, directory, or symbol name.

Pipeline:
1. If "target" is provided and is a source file (not a glob), look for the corresponding test file using per-language conventions.
2. If "pattern" is provided, treat it as a glob pattern for test file discovery.
3. Walk the project tree (skips .git, node_modules, vendor, bin, dist, .dfmc, __pycache__, .venv) collecting test files.
4. Parse test function markers from each discovered test file.
5. Return structured results grouped by test file.

Supported conventions:
- Go: *_test.go files next to the source, "Test" / "Benchmark" / "Example" prefix
- Python: test_*.py / *_test.py / tests/ directory, "test" prefix or unittest.TestCase
- JS/TS/JSX/TSX: *.test.ts / *.spec.ts / *.test.tsx / *.spec.tsx / __tests__/ directory, "test" / "it" / "describe" blocks
- Java: *Test.java / Test*.java in src/test/, @Test annotations
- Rust: *_test.rs or #[test] attributes in the same module

Always validate with the actual test runner (go test, pytest, jest, cargo test, etc.) after locating tests.`,
		Risk: RiskRead,
		Tags: []string{"test", "discovery", "coverage"},
		Args: []Arg{
			{Name: "target", Type: ArgString, Description: `Source file path to find tests for (e.g. "internal/foo/bar.go"). Companion test file is found via language conventions. One of target or pattern is required.`},
			{Name: "pattern", Type: ArgString, Description: `Glob pattern to search for test files (e.g. "**/*_test.go" or "**/*.test.ts"). One of target or pattern is required.`},
			{Name: "language", Type: ArgString, Description: `Restrict to: "go", "python", "javascript", "typescript", "java", "rust".`},
			{Name: "symbol", Type: ArgString, Description: `Limit results to test functions whose name or doc mentions this symbol name.`},
			{Name: "max_files", Type: ArgInteger, Description: `Maximum test files to scan (default 50, max 200).`},
		},
	}
}

func (t *TestDiscoveryTool) Execute(_ context.Context, req Request) (Result, error) {
	target := strings.TrimSpace(asString(req.Params, "target", ""))
	pattern := strings.TrimSpace(asString(req.Params, "pattern", ""))
	language := strings.TrimSpace(asString(req.Params, "language", ""))
	symbol := strings.TrimSpace(asString(req.Params, "symbol", ""))
	maxFiles := asInt(req.Params, "max_files", 50)
	if maxFiles <= 0 {
		maxFiles = 50
	}
	if maxFiles > 200 {
		maxFiles = 200
	}

	if target == "" && pattern == "" {
		return Result{}, missingParamError("test_discovery", "target", req.Params,
			`{"target":"internal/foo/bar.go"} or {"pattern":"**/*_test.go"}`,
			`Either "target" (a specific source file) or "pattern" (a glob for test file discovery) is required. Use "target" when you know the file you edited; use "pattern" to search broadly.`)
	}

	root := req.ProjectRoot
	if root == "" {
		return Result{}, fmt.Errorf("test_discovery: project root is not set")
	}

	var testFiles []string
	if target != "" {
		testFiles = findCompanionTests(root, target, language)
	}

	if pattern != "" || len(testFiles) == 0 {
		if pattern == "" {
			pattern = guessTestPattern(target)
		}
		if pattern != "" {
			found := findTestFilesByPattern(root, pattern, language, maxFiles)
			seen := map[string]bool{}
			for _, f := range testFiles {
				seen[f] = true
			}
			for _, f := range found {
				if !seen[f] {
					testFiles = append(testFiles, f)
					if len(testFiles) >= maxFiles {
						break
					}
				}
			}
		}
	}

	if len(testFiles) == 0 {
		return Result{
			Output: "no test files found",
			Data:   map[string]any{"files": nil, "count": 0},
		}, nil
	}

	var results []map[string]any
	for _, fp := range testFiles {
		functions := extractTestFunctions(fp, language, symbol)
		if len(functions) == 0 {
			continue
		}
		rel, _ := filepath.Rel(root, fp)
		results = append(results, map[string]any{
			"path":      rel,
			"abs_path":  fp,
			"count":     len(functions),
			"functions": functions,
		})
	}

	if len(results) == 0 {
		return Result{
			Output: "test files found but no test functions parsed",
			Data:   map[string]any{"files": testFiles, "count": 0},
		}, nil
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i]["path"].(string) < results[j]["path"].(string)
	})

	totalFuncs := 0
	for _, r := range results {
		totalFuncs += r["count"].(int)
	}

	summary := fmt.Sprintf("%d test file(s), %d test function(s)", len(results), totalFuncs)
	return Result{
		Output: summary,
		Data: map[string]any{
			"files":   results,
			"count":   len(results),
			"summary": summary,
		},
	}, nil
}

func findCompanionTests(root, target, language string) []string {
	absTarget, err := EnsureWithinRoot(root, target)
	if err != nil {
		return nil
	}
	dir := filepath.Dir(absTarget)
	base := filepath.Base(absTarget)
	ext := filepath.Ext(base)
	nameWithoutExt := strings.TrimSuffix(base, ext)

	var candidates []string
	switch {
	case language == "" || language == "go":
		candidates = append(candidates, filepath.Join(dir, nameWithoutExt+"_test.go"))
	case language == "" || language == "python":
		candidates = append(candidates,
			filepath.Join(dir, "test_"+nameWithoutExt+".py"),
			filepath.Join(dir, nameWithoutExt+"_test.py"),
			filepath.Join(dir, "tests", nameWithoutExt+".py"),
			filepath.Join(dir, "test", nameWithoutExt+".py"),
		)
	case language == "" || language == "javascript" || language == "typescript" || language == "jsx" || language == "tsx":
		candidates = append(candidates,
			filepath.Join(dir, nameWithoutExt+".test.ts"),
			filepath.Join(dir, nameWithoutExt+".spec.ts"),
			filepath.Join(dir, nameWithoutExt+".test.tsx"),
			filepath.Join(dir, nameWithoutExt+".spec.tsx"),
			filepath.Join(dir, nameWithoutExt+".test.js"),
			filepath.Join(dir, nameWithoutExt+".spec.js"),
			filepath.Join(dir, "__tests__", nameWithoutExt+".ts"),
			filepath.Join(dir, "__tests__", nameWithoutExt+".tsx"),
		)
	}

	var found []string
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			found = append(found, c)
		}
	}
	return found
}

func guessTestPattern(target string) string {
	ext := filepath.Ext(target)
	name := strings.TrimSuffix(filepath.Base(target), ext)
	switch ext {
	case ".go":
		return "**/*_test.go"
	case ".py":
		return "**/test_" + name + ".py"
	case ".ts", ".tsx":
		return "**/" + name + ".test.*"
	case ".js", ".jsx":
		return "**/" + name + ".test.*"
	}
	return ""
}

var skipDirs = []string{".git", "node_modules", "vendor", "bin", "dist", ".dfmc", "__pycache__", ".venv", ".venv36", "site-packages", "build", "target", "__tests__", "tests"}

func shouldSkipDir(path string) bool {
	rel := filepath.ToSlash(path)
	for _, d := range skipDirs {
		if strings.Contains(rel, "/"+d+"/") || strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

func findTestFilesByPattern(root, pattern, language string, maxFiles int) []string {
	pattern = filepath.ToSlash(pattern)
	var results []string
	doublestar := strings.Contains(pattern, "**")

	if doublestar {
		basePattern := strings.Split(pattern, "**")[0]
		rootSuffix := filepath.Join(root, strings.TrimPrefix(basePattern, "/"))
		if rootSuffix == root {
			rootSuffix = root
		}
		filepath.WalkDir(rootSuffix, func(path string, info fs.DirEntry, err error) error {
			if err != nil || info.IsDir() || shouldSkipDir(path) {
				return nil
			}
			matched, _ := filepath.Match(filepath.ToSlash(pattern), filepath.ToSlash(path))
			if matched {
				results = append(results, path)
				if len(results) >= maxFiles {
					return fs.SkipDir
				}
			}
			return nil
		})
	} else {
		dir := root
		if idx := strings.LastIndex(pattern, "/"); idx >= 0 {
			dir = filepath.Join(root, pattern[:idx])
			pattern = pattern[idx+1:]
		}
		entries, err := fs.ReadDir(os.DirFS(dir), ".")
		if err != nil {
			return nil
		}
		for _, entry := range entries {
			if entry.IsDir() || shouldSkipDir(filepath.Join(dir, entry.Name())) {
				continue
			}
			matched, _ := filepath.Match(pattern, entry.Name())
			if matched {
				results = append(results, filepath.Join(dir, entry.Name()))
			}
		}
	}

	if language != "" {
		filtered := make([]string, 0, len(results))
		for _, f := range results {
			if matchesLanguage(f, language) {
				filtered = append(filtered, f)
			}
		}
		return filtered
	}
	return results
}

func matchesLanguage(path, language string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch language {
	case "go":
		return ext == ".go"
	case "python":
		return ext == ".py"
	case "javascript":
		return ext == ".js" || ext == ".jsx"
	case "typescript":
		return ext == ".ts" || ext == ".tsx"
	case "java":
		return ext == ".java"
	case "rust":
		return ext == ".rs"
	default:
		return true
	}
}

func extractTestFunctions(path, language, symbol string) []map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := string(data)
	lines := strings.Split(content, "\n")
	symLower := strings.ToLower(symbol)

	switch filepath.Ext(path) {
	case ".go":
		return extractGoTests(lines, symLower)
	case ".py":
		return extractPythonTests(lines, symLower)
	case ".ts", ".tsx", ".js", ".jsx":
		return extractJSTests(lines, symLower)
	case ".java":
		return extractJavaTests(lines, symLower)
	case ".rs":
		return extractRustTests(lines, symLower)
	default:
		return extractGenericTests(lines, symLower)
	}
}

func extractGoTests(lines []string, symLower string) []map[string]any {
	var funcs []map[string]any
	for i, line := range lines {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "func Test") || strings.HasPrefix(l, "func Benchmark") || strings.HasPrefix(l, "func Example") {
			name := strings.TrimPrefix(l, "func ")
			name = strings.TrimSuffix(strings.TrimSuffix(name, "("), ")")
			if symLower != "" && !strings.Contains(strings.ToLower(name), symLower) {
				hit := false
				for j := i; j < len(lines) && j < i+10; j++ {
					if strings.Contains(strings.ToLower(lines[j]), symLower) {
						hit = true
						break
					}
				}
				if !hit {
					continue
				}
			}
			kind := "test"
			if strings.HasPrefix(l, "func Benchmark") {
				kind = "benchmark"
			} else if strings.HasPrefix(l, "func Example") {
				kind = "example"
			}
			funcs = append(funcs, map[string]any{"name": name, "line": i + 1, "kind": kind})
		}
	}
	return funcs
}

var rePythonTest = regexp.MustCompile(`^\s*(?:def|async def)\s+(test_\w+)\s*\(`)

func extractPythonTests(lines []string, symLower string) []map[string]any {
	var funcs []map[string]any
	for i, line := range lines {
		m := rePythonTest.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) > 0 {
			name := m[1]
			if symLower == "" || strings.Contains(strings.ToLower(name), symLower) {
				funcs = append(funcs, map[string]any{"name": name, "line": i + 1, "kind": "function"})
			}
		}
	}
	return funcs
}

var reJSTest = regexp.MustCompile(`^\s*(it|test|describe|spec)\s*\(\s*['"]([^'"]+)['"]`)

func extractJSTests(lines []string, symLower string) []map[string]any {
	var funcs []map[string]any
	for i, line := range lines {
		m := reJSTest.FindStringSubmatch(line)
		if len(m) > 0 {
			name := m[2]
			if symLower == "" || strings.Contains(strings.ToLower(name), symLower) {
				funcs = append(funcs, map[string]any{"name": name, "line": i + 1, "kind": m[1]})
			}
		}
	}
	return funcs
}

var reJavaTest = regexp.MustCompile(`^\s*(?:public\s+)?(?:static\s+)?void\s+(\w+)\s*\(`)

func extractJavaTests(lines []string, symLower string) []map[string]any {
	var funcs []map[string]any
	for i, line := range lines {
		if strings.Contains(line, "@Test") {
			for j := i; j < len(lines) && j < i+5; j++ {
				m := reJavaTest.FindStringSubmatch(lines[j])
				if len(m) > 0 {
					name := m[1]
					if symLower == "" || strings.Contains(strings.ToLower(name), symLower) {
						funcs = append(funcs, map[string]any{"name": name, "line": j + 1, "kind": "test"})
					}
					break
				}
			}
		}
	}
	return funcs
}

var reRustTest = regexp.MustCompile(`fn\s+(\w+)\s*\(`)

func extractRustTests(lines []string, symLower string) []map[string]any {
	var funcs []map[string]any
	for i, line := range lines {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "#[test]") {
			for j := i + 1; j < len(lines) && j < i+3; j++ {
				m := reRustTest.FindStringSubmatch(lines[j])
				if len(m) > 0 {
					name := m[1]
					if symLower == "" || strings.Contains(strings.ToLower(name), symLower) {
						funcs = append(funcs, map[string]any{"name": name, "line": j + 1, "kind": "test"})
					}
					break
				}
			}
		}
	}
	return funcs
}

var reGenericTest = regexp.MustCompile(`(?i)^\s*(function|def|fn|test|it|spec)\s+(\w+)`)

func extractGenericTests(lines []string, symLower string) []map[string]any {
	var funcs []map[string]any
	for i, line := range lines {
		m := reGenericTest.FindStringSubmatch(line)
		if len(m) > 0 {
			name := m[2]
			prefix := strings.ToLower(name)
			isLikelyTest := strings.HasPrefix(prefix, "test") ||
				strings.HasPrefix(prefix, "it") || strings.HasPrefix(prefix, "spec") ||
				strings.HasPrefix(prefix, "describe")
			if symLower == "" {
				if !isLikelyTest {
					continue
				}
			} else if !strings.Contains(strings.ToLower(name), symLower) {
				continue
			}
			funcs = append(funcs, map[string]any{"name": name, "line": i + 1, "kind": m[1]})
		}
	}
	return funcs
}
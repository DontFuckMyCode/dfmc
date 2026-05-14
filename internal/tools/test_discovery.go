package tools

import (
	"context"
	"fmt"
	"path/filepath"
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
func (t *TestDiscoveryTool) Name() string      { return "test_discovery" }
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

// findCompanionTests, guessTestPattern, skipDirs/shouldSkipDir,
// findTestFilesByPattern, and matchesLanguage live in
// test_discovery_search.go.

// ExtractTestFunctions (capital E) lives in test_discovery_extractors.go.
// This shim keeps callers that call extractTestFunctions working while we
// migrate to the exported name.
func extractTestFunctions(path, fileContents, symbol string) []map[string]any {
	return ExtractTestFunctions(path, fileContents, symbol)
}


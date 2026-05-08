package tools

// test_discovery_extractors.go — per-language test-function extractors
// dispatched from extractTestFunctions. Each picks names + line
// numbers from a slice of source lines according to the host
// language's test-naming convention; the symLower filter (when set)
// narrows to functions whose name OR nearby block mentions the
// caller's symbol. Sibling to test_discovery.go which owns the
// TestDiscoveryTool surface, the file walker, and the per-language
// dispatch table.

import (
	"regexp"
	"strings"
)

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

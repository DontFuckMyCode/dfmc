package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func extractGoTests(lines []string, symLower string) []map[string]any {
	var funcs []map[string]any
	for i, line := range lines {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "func Test") || strings.HasPrefix(l, "func Benchmark") || strings.HasPrefix(l, "func Example") {
			name := strings.TrimPrefix(l, "func ")
			name = strings.TrimRight(name, " )")
			name = strings.TrimSpace(name)
			name = strings.TrimSuffix(name, " {}")
			name = strings.TrimSuffix(name, "{")
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
			funcs = append(funcs, map[string]any{"name": name, "line": i + 1})
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
				funcs = append(funcs, map[string]any{"name": name, "line": i + 1})
			}
		}
	}
	return funcs
}

var reJSTest = regexp.MustCompile(`^\s*(it|test|describe|spec)\s*\(\s*['"]([^'"]+)['"]`)

func extractJSTests(lines []string, symLower string) []map[string]any {
	var funcs []map[string]any
	for _, line := range lines {
		m := reJSTest.FindStringSubmatch(line)
		if len(m) > 0 {
			name := m[2]
			if symLower == "" || strings.Contains(strings.ToLower(name), symLower) {
				funcs = append(funcs, map[string]any{"name": name, "line": 0, "kind": m[1]})
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
						funcs = append(funcs, map[string]any{"name": name, "line": j + 1})
					}
					break
				}
			}
		}
	}
	return funcs
}

func extractRustTests(lines []string, symLower string) []map[string]any {
	var funcs []map[string]any
	for i, line := range lines {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "#[") && strings.Contains(l, "test") {
			for j := i + 1; j < len(lines) && j < i+3; j++ {
				l2 := strings.TrimSpace(lines[j])
				if strings.HasPrefix(l2, "fn ") || strings.HasPrefix(l2, "async fn ") {
					name := strings.TrimPrefix(l2, "fn ")
					name = strings.TrimPrefix(name, "async fn ")
					if idx := strings.Index(name, "("); idx > 0 {
						name = name[:idx]
					}
					if symLower == "" || strings.Contains(strings.ToLower(name), symLower) {
						funcs = append(funcs, map[string]any{"name": name, "line": j + 1})
					}
					break
				}
			}
		}
	}
	return funcs
}

func extractGenericTests(lines []string, symLower string) []map[string]any {
	var funcs []map[string]any
	reTest := regexp.MustCompile(`(?i)(?:^|[\s_])test[a-z0-9_]*\s*\(`)
	reDesc := regexp.MustCompile(`(?i)(describe|context)\s*\(`)

	for i, line := range lines {
		if m := reTest.FindStringSubmatch(line); len(m) > 0 {
			name := m[0]
			if idx := strings.Index(name, "("); idx > 0 {
				name = strings.TrimSpace(name[:idx])
			}
			if symLower == "" || strings.Contains(strings.ToLower(name), symLower) {
				funcs = append(funcs, map[string]any{"name": name, "line": i + 1, "kind": "generic"})
			}
		} else if m := reDesc.FindStringSubmatch(line); len(m) > 0 {
			name := m[0]
			if idx := strings.Index(name, "("); idx > 0 {
				name = strings.TrimSpace(name[:idx])
			}
			if symLower == "" || strings.Contains(strings.ToLower(name), symLower) {
				funcs = append(funcs, map[string]any{"name": name, "line": i + 1, "kind": m[1]})
			}
		}
	}
	return funcs
}

// ExtractTestFunctions dispatches to the per-language extractor based on file extension.
// If fileContents is non-empty, it is used directly instead of reading from disk.
func ExtractTestFunctions(path, fileContents, symbol string) []map[string]any {
	if fileContents == "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		fileContents = string(data)
	}
	lines := strings.Split(fileContents, "\n")
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

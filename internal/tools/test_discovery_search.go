// test_discovery_search.go — file discovery + per-language path
// conventions for the TestDiscoveryTool. Sibling of
// test_discovery.go which keeps the tool registration (Name,
// Description, Spec, Execute pipeline) and the extractTestFunctions
// dispatcher; per-language test-function extractors live in
// test_discovery_extractors.go.
//
// Splitting search out keeps test_discovery.go scoped to "what does
// the tool surface look like" while this file owns "given a target
// or pattern, which files are candidates and which directories do we
// skip during the walk." Adding a new language adds entries to
// findCompanionTests + guessTestPattern + matchesLanguage here, and
// a new extractor in test_discovery_extractors.go.

package tools

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

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
	addGo := language == "" || language == "go"
	addPython := language == "" || language == "python"
	addJS := language == "" || language == "javascript" || language == "typescript" || language == "jsx" || language == "tsx"
	if addGo {
		candidates = append(candidates, filepath.Join(dir, nameWithoutExt+"_test.go"))
	}
	if addPython {
		candidates = append(candidates,
			filepath.Join(dir, "test_"+nameWithoutExt+".py"),
			filepath.Join(dir, nameWithoutExt+"_test.py"),
			filepath.Join(dir, "tests", nameWithoutExt+".py"),
			filepath.Join(dir, "test", nameWithoutExt+".py"),
		)
	}
	if addJS {
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

var skipDirs = []string{".git", "node_modules", "vendor", "bin", "dist", ".dfmc", "__pycache__", ".venv", ".venv36", "site-packages", "build", "target"}

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
		_ = filepath.WalkDir(rootSuffix, func(path string, info fs.DirEntry, err error) error {
			if err != nil || info.IsDir() || shouldSkipDir(path) {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return nil
			}
			if globMatch(pattern, filepath.ToSlash(rel), doublestar) {
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

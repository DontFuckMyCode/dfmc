package promptlib

// promptlib_detect.go — task and language inference helpers used by callers
// that need to pick a RenderRequest's Task / Language from a free-form user
// query plus the surrounding context chunks. The matching here is
// intentionally tiny and deterministic — anything fuzzier belongs upstream
// in the intent layer, not in the prompt library.

import (
	"path/filepath"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func DetectTask(query string) string {
	q := strings.ToLower(" " + strings.TrimSpace(query) + " ")
	qFolded := " " + foldSearchText(strings.TrimSpace(query)) + " "
	has := func(words ...string) bool {
		for _, w := range words {
			key := strings.ToLower(strings.TrimSpace(w))
			if key == "" {
				continue
			}
			if strings.Contains(q, " "+key+" ") || strings.Contains(qFolded, " "+foldSearchText(key)+" ") {
				return true
			}
		}
		return false
	}
	switch {
	case has("security", "audit", "vuln", "vulnerability", "xss", "sqli", "threat", "exploit"):
		return "security"
	case has("review", "code review", "inspect", "analysis"):
		return "review"
	case has("refactor", "cleanup", "restructure"):
		return "refactor"
	case has("test", "tests", "unit test", "integration test"):
		return "test"
	case has("doc", "docs", "documentation", "document"):
		return "doc"
	case has("plan", "planning", "roadmap", "phase", "sprint", "step-by-step"):
		return "planning"
	case has("bug", "fix", "error", "exception", "panic", "debug", "traceback"):
		return "debug"
	default:
		return "general"
	}
}

func foldSearchText(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch r {
		case 0x131, 0x130:
			r = 'i'
		case 0x11f, 0x11e:
			r = 'g'
		case 0x15f, 0x15e:
			r = 's'
		case 0xfc, 0xdc:
			r = 'u'
		case 0xf6, 0xd6:
			r = 'o'
		case 0xe7, 0xc7:
			r = 'c'
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func InferLanguage(query string, chunks []types.ContextChunk) string {
	q := strings.ToLower(query)
	explicit := map[string]string{
		"golang":     "go",
		"go ":        "go",
		"typescript": "typescript",
		"javascript": "javascript",
		" python":    "python",
		" rust":      "rust",
		" java":      "java",
		" c#":        "csharp",
		" csharp":    "csharp",
		" php":       "php",
		" kotlin":    "kotlin",
		" swift":     "swift",
	}
	for needle, lang := range explicit {
		if strings.Contains(" "+q+" ", needle) {
			return lang
		}
	}

	counts := map[string]int{}
	for _, ch := range chunks {
		lang := normalizeKey(ch.Language)
		if lang == "" {
			lang = languageFromPath(ch.Path)
		}
		if lang == "" {
			continue
		}
		counts[lang]++
	}
	bestLang := ""
	bestCount := 0
	for lang, n := range counts {
		if n > bestCount {
			bestLang = lang
			bestCount = n
		}
	}
	if bestLang != "" {
		return bestLang
	}
	return "generic"
}

func languageFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".cs":
		return "csharp"
	case ".php":
		return "php"
	case ".kt", ".kts":
		return "kotlin"
	case ".swift":
		return "swift"
	default:
		return ""
	}
}

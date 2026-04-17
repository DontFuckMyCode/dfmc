// TODO/FIXME/HACK marker collector. Plain informational pass — NOT
// a vulnerability, NOT a dead-code candidate, just a curated list so
// an operator can see at a glance where the unfinished business is.
// Runs alongside the other analyzer passes behind AnalyzeOptions.Todos
// (or --full). Keeps the surface tiny so the report stays cheap even
// on large repos.

package engine

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// TodoItem is one marker hit: the kind (TODO / FIXME / HACK / NOTE /
// XXX), the file it lives in, the 1-indexed line, and the trimmed
// surrounding comment line for quick context.
type TodoItem struct {
	Kind string `json:"kind"`
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// TodoReport is the aggregated view. Kinds maps kind → count for the
// TUI / CLI header line; Items is the full enumeration (capped).
type TodoReport struct {
	Kinds map[string]int `json:"kinds"`
	Items []TodoItem     `json:"items"`
	Total int            `json:"total"`
}

const (
	// Hard cap on emitted items so a repo with 5000 TODOs doesn't
	// stuff the JSON body. 200 is enough to skim but small enough to
	// keep the report light. Full count is preserved via Total.
	todoItemsLimit = 200
)

// todoMarkerPattern matches the common English conventions at the
// START of a comment body. The `^` anchor rules out prose mentions
// of the marker words inside a sentence. The trailing `[:\s(]`
// guard rules out words that merely begin with the prefix (TODOS,
// HACKY, NOTES_ARE). Case is strict — only ALL-CAPS markers are
// conventions; lowercase appears in ordinary prose too often to be
// reliable. See collectTodoMarkers for the matching flow and the
// tests in todos_test.go for the positive/negative cases.
var todoMarkerPattern = regexp.MustCompile(
	`^(TODO|FIXME|HACK|XXX|NOTE)[:\s(]`,
)

// collectTodoMarkers walks `paths` and returns a TodoReport. Only
// comment lines are searched — finding a literal "TODO" inside a
// string constant is almost always noise (help text, error message).
// Each hit carries the first ~180 characters of the surrounding
// comment line for context.
func collectTodoMarkers(paths []string) TodoReport {
	report := TodoReport{Kinds: map[string]int{}}
	if len(paths) == 0 {
		return report
	}

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lang := languageFromPath(path)
		if lang == "" {
			continue
		}
		scanner := bufio.NewScanner(bytes.NewReader(content))
		scanner.Buffer(make([]byte, 512*1024), 512*1024)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if !isLineCommentLang(trimmed, lang) {
				continue
			}
			// Strip the comment prefix and any immediate whitespace so
			// the regex's ^-anchor matches against the comment BODY,
			// not the prefix. "// TODO:" → "TODO:" which starts with
			// the marker. "// Unit tests for the TODO/FIXME collector"
			// → "Unit tests for ..." which does NOT start with a
			// marker, so it won't fire.
			body := stripLineCommentPrefix(trimmed, lang)
			match := todoMarkerPattern.FindString(body)
			if match == "" {
				continue
			}
			kind := strings.TrimRight(match, ":\t (")
			kind = strings.TrimSpace(kind)
			if kind == "" {
				continue
			}
			report.Total++
			report.Kinds[kind]++
			if len(report.Items) < todoItemsLimit {
				report.Items = append(report.Items, TodoItem{
					Kind: kind,
					File: filepath.ToSlash(path),
					Line: lineNo,
					Text: todoSnippet(trimmed, 180),
				})
			}
		}
	}

	sort.Slice(report.Items, func(i, j int) bool {
		if report.Items[i].Kind != report.Items[j].Kind {
			return report.Items[i].Kind < report.Items[j].Kind
		}
		if report.Items[i].File != report.Items[j].File {
			return report.Items[i].File < report.Items[j].File
		}
		return report.Items[i].Line < report.Items[j].Line
	})
	return report
}

// languageFromPath maps a file extension to the minimal language
// label this collector cares about (only for the comment-syntax
// dispatch in isLineCommentLang). A nil/unknown return means "skip
// this file."
func languageFromPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
		".java", ".c", ".cpp", ".cc", ".h", ".hpp", ".cs",
		".rs", ".swift", ".kt", ".kts", ".scala":
		return "c-family"
	case ".py", ".pyw":
		return "python"
	case ".sh", ".bash", ".zsh", ".yaml", ".yml", ".toml":
		return "hash"
	}
	return ""
}

// isLineCommentLang reports whether a trimmed source line is a pure
// line comment for a given comment-family.
func isLineCommentLang(trimmed, lang string) bool {
	switch lang {
	case "c-family":
		return strings.HasPrefix(trimmed, "//")
	case "python", "hash":
		return strings.HasPrefix(trimmed, "#")
	}
	return false
}

// stripLineCommentPrefix drops the comment marker and any immediate
// whitespace so the downstream regex can anchor on the comment BODY.
// The caller has already verified the line IS a comment via
// isLineCommentLang, so the dispatch here is a pure-text operation.
func stripLineCommentPrefix(trimmed, lang string) string {
	switch lang {
	case "c-family":
		return strings.TrimLeft(strings.TrimPrefix(trimmed, "//"), " \t")
	case "python", "hash":
		return strings.TrimLeft(strings.TrimPrefix(trimmed, "#"), " \t")
	}
	return trimmed
}

func todoSnippet(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

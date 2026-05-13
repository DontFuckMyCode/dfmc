package tools

// codemap_render.go — rendering helpers for the codemap tool: per-symbol
// formatting, file-grouped markdown body, and the one-line stats footer.
// Plus the small filter helpers (language allowlist + interesting-file
// extension classifier + dropDirsForCodemap walk-skip set). Sibling to
// codemap.go which keeps the CodemapTool surface, ToolSpec, and the
// Execute walk that drives the AST parse passes.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

var defaultWalkSkipDirs = map[string]bool{
	".git": true, ".dfmc": true, "node_modules": true, "vendor": true,
	"bin": true, "dist": true, "build": true, "target": true,
	".venv": true, ".idea": true, ".vscode": true, "__pycache__": true,
}

// dropDirsForCodemap mirrors the grep_codebase exclude list so the two
// surfaces agree on "what counts as project source". Anything here is
// silently skipped during the walk.
var dropDirsForCodemap = map[string]bool{
	".git":         true,
	".dfmc":        true,
	".project":     true,
	"node_modules": true,
	"vendor":       true,
	"bin":          true,
	"dist":         true,
	"build":        true,
	".next":        true,
	"target":       true,
	".venv":        true,
	"__pycache__":  true,
}

// codemapWantedLanguages parses an optional `languages` array filter.
// Returns nil when no filter is set (caller treats nil as "accept all").
// Lowercased for case-insensitive comparison.
func codemapWantedLanguages(params map[string]any) map[string]bool {
	raw, ok := params["languages"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make(map[string]bool, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok {
			continue
		}
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" {
			out[s] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// codemapInterestingFile returns true for file extensions worth parsing.
// Matches the AST engine's coverage — anything else (lock files, images,
// archives) we skip without opening to keep the walk fast.
func codemapInterestingFile(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".java", ".rs",
		".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".cs", ".swift",
		".kt", ".kts", ".scala", ".php", ".rb", ".lua":
		return true
	}
	return false
}

// codemapRenderMarkdown produces the LLM-friendly grouped output. The
// model parses this format easily and re-uses the file paths verbatim
// for follow-up read_file / find_symbol calls.
func codemapRenderMarkdown(entries []codemapFileEntry, totalSymbols *int) string {
	var b strings.Builder
	for _, e := range entries {
		// Sort symbols within a file by line so the output reads top-to-
		// bottom like the source.
		sort.SliceStable(e.symbols, func(i, j int) bool { return e.symbols[i].Line < e.symbols[j].Line })

		lang := strings.TrimSpace(e.language)
		if lang == "" {
			lang = "?"
		}
		b.WriteString(e.path)
		b.WriteString(" (")
		b.WriteString(lang)
		b.WriteString(")\n")
		for _, sym := range e.symbols {
			line := codemapSymbolLine(sym)
			if line == "" {
				continue
			}
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteString("\n")
			*totalSymbols++
		}
	}
	if b.Len() == 0 {
		return "(no symbols extracted — empty project, all files filtered, or AST backend unavailable)"
	}
	return b.String()
}

// codemapSymbolLine renders one symbol row. Falls back to a synthetic
// signature when the AST didn't surface one (regex backend) so the
// output stays informative even on the stub backend.
func codemapSymbolLine(sym types.Symbol) string {
	name := strings.TrimSpace(sym.Name)
	if name == "" {
		return ""
	}
	kind := strings.TrimSpace(string(sym.Kind))
	sig := strings.TrimSpace(sym.Signature)
	if sig == "" {
		if kind != "" {
			sig = kind + " " + name
		} else {
			sig = name
		}
	}
	// Right-align line number column at 50 chars so the output reads
	// like a manual page. Truncate over-long signatures so a 200-arg
	// generated function can't blow up the line width.
	const sigBudget = 70
	runes := []rune(sig)
	if len(runes) > sigBudget {
		sig = string(runes[:sigBudget-3]) + "..."
	}
	const lineCol = 80
	pad := lineCol - utf8.RuneCountInString(sig) - 2 // " " gap on either side of the line marker
	if pad < 1 {
		pad = 1
	}
	return fmt.Sprintf("%s%sL%d", sig, strings.Repeat(" ", pad), sym.Line)
}

// codemapStatsLine renders the one-line header banner the renderer
// puts above the body. Surfaces coverage so the model can spot when
// the map is partial (e.g. truncated, parse errors, missing languages).
func codemapStatsLine(files, symbols, parseErrors, skippedFiles int, langs map[string]int, dur time.Duration, truncated bool) string {
	langKeys := make([]string, 0, len(langs))
	for k := range langs {
		langKeys = append(langKeys, k)
	}
	sort.Strings(langKeys)
	parts := []string{
		fmt.Sprintf("files=%d", files),
		fmt.Sprintf("symbols=%d", symbols),
	}
	if parseErrors > 0 {
		parts = append(parts, fmt.Sprintf("parse_errors=%d", parseErrors))
	}
	if skippedFiles > 0 {
		parts = append(parts, fmt.Sprintf("skipped=%d", skippedFiles))
	}
	if len(langKeys) > 0 {
		var langPairs []string
		for _, k := range langKeys {
			langPairs = append(langPairs, fmt.Sprintf("%s=%d", k, langs[k]))
		}
		parts = append(parts, "langs={"+strings.Join(langPairs, ",")+"}")
	}
	parts = append(parts, fmt.Sprintf("dur=%dms", dur.Milliseconds()))
	if truncated {
		parts = append(parts, "truncated")
	}
	return "[codemap " + strings.Join(parts, " ") + "]"
}

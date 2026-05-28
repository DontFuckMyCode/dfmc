package provider

// offline_analyzer_helpers.go — formatting + shape-detection helpers
// for the offline analyzer. Sibling of offline_analyzer.go which keeps
// the task-routing pipeline (detectOfflineTask / offlineTaskFromText /
// offlineLeadingSlashTask regex), the analyzeOffline dispatcher,
// offlineHeader, and the shared offlineFinding struct.
//
// These helpers split into two clusters:
//   - rendering: renderFindings / renderFileInventory /
//     dedupeSortFindings / filterByKeywords
//   - per-line shape detection: lineNumber / truncate /
//     looksLikeFunctionStart / extractFunctionName / extractTopSymbols /
//     briefSymbols / sketchStructure / humanLang
//
// They live together because they're all small, language-aware string
// helpers shared across the per-task report generators in
// offline_reports.go and the per-language scanners in
// offline_scanners.go.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func renderFindings(findings []offlineFinding) string {
	groups := map[string][]offlineFinding{}
	order := []string{"critical", "high", "medium", "low"}
	for _, f := range findings {
		sev := strings.ToLower(strings.TrimSpace(f.Severity))
		if sev == "" {
			sev = "low"
		}
		groups[sev] = append(groups[sev], f)
	}
	var b strings.Builder
	for _, sev := range order {
		bucket := groups[sev]
		if len(bucket) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n**%s** (%d)\n", strings.ToUpper(sev), len(bucket))
		for _, f := range bucket {
			fmt.Fprintf(&b, "- `%s:%d` · %s · %s", f.Path, f.Line, f.Category, f.Message)
			if f.Evidence != "" {
				b.WriteString("\n  > ")
				b.WriteString(f.Evidence)
			}
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func renderFileInventory(chunks []types.ContextChunk) string {
	if len(chunks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("**Files in scope:**\n")
	limit := len(chunks)
	if limit > 8 {
		limit = 8
	}
	for i := 0; i < limit; i++ {
		ch := chunks[i]
		b.WriteString(fmt.Sprintf("- `%s` · %s · L%d-L%d · score %.2f\n",
			ch.Path, humanLang(ch.Language), ch.LineStart, ch.LineEnd, ch.Score))
	}
	if len(chunks) > limit {
		b.WriteString(fmt.Sprintf("- … +%d more\n", len(chunks)-limit))
	}
	return strings.TrimSpace(b.String())
}

func dedupeSortFindings(in []offlineFinding) []offlineFinding {
	seen := map[string]struct{}{}
	out := make([]offlineFinding, 0, len(in))
	for _, f := range in {
		key := f.Severity + "|" + f.Path + "|" + fmt.Sprintf("%d", f.Line) + "|" + f.Message
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}
	sevRank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := sevRank[out[i].Severity], sevRank[out[j].Severity]
		if ri != rj {
			return ri < rj
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func filterByKeywords(in []offlineFinding, keys []string) []offlineFinding {
	if len(keys) == 0 {
		return in
	}
	out := make([]offlineFinding, 0, len(in))
	for _, f := range in {
		hay := strings.ToLower(f.Message + " " + f.Category + " " + f.Evidence)
		for _, k := range keys {
			if strings.Contains(hay, k) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

func lineNumber(ch types.ContextChunk, idx int) int {
	if ch.LineStart > 0 {
		return ch.LineStart + idx
	}
	return idx + 1
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func looksLikeFunctionStart(lang, line string) bool {
	switch strings.ToLower(lang) {
	case "go":
		return strings.HasPrefix(line, "func ") || strings.HasPrefix(line, "func(")
	case "python", "py":
		return strings.HasPrefix(line, "def ") || strings.HasPrefix(line, "async def ")
	case "typescript", "javascript", "ts", "js", "tsx", "jsx":
		return strings.Contains(line, "function ") || strings.Contains(line, " => {") || strings.Contains(line, " => (")
	case "rust", "rs":
		return strings.HasPrefix(line, "fn ") || strings.HasPrefix(line, "pub fn ")
	}
	return false
}

func extractFunctionName(lang, line string) string {
	switch strings.ToLower(lang) {
	case "go":
		if strings.HasPrefix(line, "func ") {
			rest := strings.TrimPrefix(line, "func ")
			if i := strings.IndexAny(rest, "( "); i >= 0 {
				return strings.TrimSpace(rest[:i])
			}
			return rest
		}
	case "python", "py":
		rest := strings.TrimPrefix(strings.TrimPrefix(line, "async "), "def ")
		if i := strings.Index(rest, "("); i >= 0 {
			return strings.TrimSpace(rest[:i])
		}
	case "rust", "rs":
		rest := strings.TrimPrefix(strings.TrimPrefix(line, "pub "), "fn ")
		if i := strings.Index(rest, "("); i >= 0 {
			return strings.TrimSpace(rest[:i])
		}
	}
	return "<anon>"
}

func extractTopSymbols(ch types.ContextChunk) []string {
	var out []string
	for _, line := range strings.Split(ch.Content, "\n") {
		stripped := strings.TrimSpace(line)
		if name := extractFunctionName(ch.Language, stripped); name != "<anon>" && name != "" && looksLikeFunctionStart(ch.Language, stripped) {
			out = append(out, name)
			if len(out) >= 6 {
				break
			}
		}
	}
	return out
}

func briefSymbols(syms []string) string {
	if len(syms) == 0 {
		return "no top-level symbols detected"
	}
	if len(syms) <= 4 {
		return strings.Join(syms, ", ")
	}
	return strings.Join(syms[:4], ", ") + fmt.Sprintf(" (+%d)", len(syms)-4)
}

func sketchStructure(chunks []types.ContextChunk) string {
	langs := map[string]int{}
	for _, ch := range chunks {
		langs[humanLang(ch.Language)]++
	}
	if len(langs) == 0 {
		return "no structural data"
	}
	var parts []string
	for l, n := range langs {
		parts = append(parts, fmt.Sprintf("%d %s", n, l))
	}
	sort.Strings(parts)
	return fmt.Sprintf("%d files — %s.", len(chunks), strings.Join(parts, ", "))
}

func humanLang(l string) string {
	if l == "" {
		return "text"
	}
	return l
}

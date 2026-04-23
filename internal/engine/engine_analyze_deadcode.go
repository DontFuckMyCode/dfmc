// engine_analyze_deadcode.go — dead-code detection pass for the
// analyze pipeline. Heuristic, not sound: a symbol is reported when
// it's declared but has ≤1 total occurrences across all stripped
// source (the 1 being the declaration itself). String literals and
// comments are stripped BEFORE counting so a name merely mentioned
// in `// TODO: replace foo` doesn't make foo look used. Entrypoints
// (main/init/test*, Go-exported identifiers, test-runner hooks) are
// skipped up front — they're called by external code or the runtime,
// not by siblings in the same project.
//
// Supporting helpers stay colocated because they only make sense in
// this pass:
//
//   - declarationLineLooksReal: confirms the AST-reported line
//     actually looks like a real declaration in the host language.
//     Guards against the regex-AST fallback surfacing `const wrapper`
//     inside a Go raw-string that embeds JavaScript.
//   - lineStartsWithAny / isLetterByte: byte-level predicates used by
//     declarationLineLooksReal's per-language prefix check.
//   - goExportedEntrypoint: an uppercase first letter on a Go symbol
//     means a downstream importer could be using it — we can't prove
//     dead, skip.
//   - isTestingIdentifier: TestX / BenchmarkX / ExampleX / FuzzX in
//     Go, test_X / setUp / tearDown in Python / JS unittest-family —
//     all invoked by the test runner by name, not by other code.

package engine

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func (e *Engine) detectDeadCode(ctx context.Context, paths []string) ([]DeadCodeItem, error) {
	// Each symbol is keyed by (path, name, line) so two packages that
	// happen to export the same identifier don't collide — the old
	// `map[name]` version silently dropped duplicates, losing one of
	// the two from the final report.
	type symbolRef struct {
		Name string
		File string
		Line int
		Kind string
	}
	var symbols []symbolRef
	codeContents := map[string]string{} // comments + string literals stripped
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(content)
		// Strip strings + comments before counting occurrences. Without
		// this a symbol merely mentioned in `// TODO: replace foo` looks
		// "used" and the detector gives it a pass — a real source of
		// false negatives noted in audits.
		stripped := stripStringsAndComments(text, filepath.Ext(path))
		codeContents[path] = stripped
		if e.AST == nil {
			continue
		}
		res, err := e.AST.ParseContent(ctx, path, content)
		if err != nil {
			continue
		}
		// Use the STRIPPED content for declaration-line verification.
		// A declaration inside a backtick raw string (e.g. JS embedded
		// in the web server's HTML bundle) will have its line blanked
		// by the stripper, so isGoDeclarationLine will correctly say
		// "not a real Go decl" and the symbol gets skipped.
		strippedLines := strings.Split(stripped, "\n")
		ext := strings.ToLower(filepath.Ext(path))
		for _, sym := range res.Symbols {
			if strings.TrimSpace(sym.Name) == "" {
				continue
			}
			if !declarationLineLooksReal(strippedLines, sym.Line, ext) {
				// AST matched a `const`/`let`/`function` inside a
				// raw string literal (e.g. embedded JS/CSS) — not a
				// real symbol of THIS file's language.
				continue
			}
			symbols = append(symbols, symbolRef{
				Name: sym.Name,
				File: path,
				Line: sym.Line,
				Kind: string(sym.Kind),
			})
		}
	}

	// Compile one regex per distinct name (names often repeat across
	// packages; dedupe to save on compile cost).
	nameRegexes := map[string]*regexp.Regexp{}
	for _, s := range symbols {
		if _, ok := nameRegexes[s.Name]; ok {
			continue
		}
		nameRegexes[s.Name] = regexp.MustCompile(`\b` + regexp.QuoteMeta(s.Name) + `\b`)
	}

	out := make([]DeadCodeItem, 0)
	for _, s := range symbols {
		if looksEntrypoint(s.Name, s.File) {
			continue
		}
		if goExportedEntrypoint(s.Name, s.File) {
			continue
		}
		if isTestingIdentifier(s.Name) {
			continue
		}
		re := nameRegexes[s.Name]
		total := 0
		for _, c := range codeContents {
			total += len(re.FindAllStringIndex(c, -1))
		}
		// n counts ALL occurrences in the stripped code (including
		// the definition line itself). <= 1 means "defined but
		// nothing else references it."
		if total > 1 {
			continue
		}
		out = append(out, DeadCodeItem{
			Name:        s.Name,
			Kind:        s.Kind,
			File:        filepath.ToSlash(s.File),
			Line:        s.Line,
			Occurrences: total,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Occurrences == out[j].Occurrences {
			if out[i].File == out[j].File {
				return out[i].Line < out[j].Line
			}
			return out[i].File < out[j].File
		}
		return out[i].Occurrences < out[j].Occurrences
	})
	if len(out) > 100 {
		out = out[:100]
	}
	return out, nil
}

// declarationLineLooksReal answers whether the AST-reported symbol
// at `line` (1-indexed) points at a line that actually looks like a
// declaration in the host language. Needed because the regex-based
// AST fallback happily extracts `const wrapper = ...` from inside a
// Go raw-string literal that embeds JavaScript for a served HTML
// page — those aren't real Go symbols, just text the AST scanned.
//
// Passing lines = stripped source (strings + comments blanked out).
// A symbol inside a string literal will have its line blanked, so
// the check for a real declaration keyword (const, var, func, type,
// class, let, def, ...) correctly returns false.
func declarationLineLooksReal(lines []string, line int, ext string) bool {
	if line <= 0 || line > len(lines) {
		// Out-of-range — can happen when the AST and stripped
		// content diverge by a few lines. Be permissive; dropping a
		// real symbol is worse than including a false positive here.
		return true
	}
	t := strings.TrimSpace(lines[line-1])
	if t == "" {
		return false
	}
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "go":
		return lineStartsWithAny(t, "func", "var", "const", "type", "package")
	case "ts", "tsx", "js", "jsx", "mjs", "cjs":
		return lineStartsWithAny(t, "function", "const", "let", "var",
			"class", "interface", "type", "enum", "export", "import",
			"async function", "abstract class")
	case "py", "pyw":
		return lineStartsWithAny(t, "def", "async def", "class")
	case "rs":
		return lineStartsWithAny(t, "fn", "pub fn", "struct", "enum",
			"trait", "impl", "const", "static", "type", "mod",
			"use")
	case "java", "cs", "kt", "kts", "scala", "swift":
		// Broad: these languages have many valid decl prefixes; require
		// at least something that looks alphanumeric before a name. A
		// stripped-to-spaces string literal line will be empty (already
		// rejected above).
		return isLetterByte(t[0]) || t[0] == '@'
	case "c", "h", "cpp", "cc", "hpp":
		return isLetterByte(t[0]) || t[0] == '#'
	}
	// Unknown language — default to permissive to avoid dropping real
	// dead-code findings.
	return true
}

func lineStartsWithAny(line string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(line, p) {
			// Must be followed by whitespace, punctuation, or end of
			// line — otherwise `function` matches inside `functional`.
			rest := line[len(p):]
			if rest == "" {
				return true
			}
			c := rest[0]
			if c == ' ' || c == '\t' || c == '(' || c == '{' || c == ':' || c == '<' {
				return true
			}
		}
	}
	return false
}

func isLetterByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

// goExportedEntrypoint reports whether a Go symbol is potentially
// consumed by another package (uppercase first letter is Go's
// export marker). A package-private lowercase symbol with no uses
// is a strong dead-code candidate; an exported Public one could
// legitimately be called by a downstream importer we can't see, so
// we skip it. Language check is path-based because types.Symbol
// carries language only via the ParseResult, not the symbol itself.
func goExportedEntrypoint(name, file string) bool {
	if !strings.HasSuffix(strings.ToLower(file), ".go") {
		return false
	}
	if name == "" {
		return false
	}
	first := name[0]
	return first >= 'A' && first <= 'Z'
}

// isTestingIdentifier recognises Go / Python / JS testing entrypoints
// that the runtime discovers by name. TestX, BenchmarkX, ExampleX in
// Go; test_X / Test class methods in Python; describe / it / test in
// JS. These are called by the test runner, not by other code, so
// zero-references isn't dead code.
func isTestingIdentifier(name string) bool {
	switch {
	case strings.HasPrefix(name, "Test"),
		strings.HasPrefix(name, "Benchmark"),
		strings.HasPrefix(name, "Example"),
		strings.HasPrefix(name, "Fuzz"):
		return true
	case strings.HasPrefix(name, "test_"),
		strings.HasPrefix(name, "setUp"),
		strings.HasPrefix(name, "tearDown"):
		return true
	}
	return false
}

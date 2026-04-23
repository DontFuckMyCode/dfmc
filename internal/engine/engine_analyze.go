// Analyze-pipeline entry points for the Engine. Hosts the project-
// wide analyze run + dead-code detection. Cyclomatic complexity lives
// in engine_analyze_complexity.go; the language-aware text-strippers
// that feed both passes live in engine_analyze_strip.go. All exported
// entry points route through AnalyzeWithOptions; helpers are
// package-private and colocated with their callers.

package engine

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func (e *Engine) ensureIndexed(ctx context.Context) {
	if e.CodeMap == nil || e.CodeMap.Graph() == nil {
		return
	}
	if len(e.CodeMap.Graph().Nodes()) > 0 {
		return
	}
	paths, err := e.collectSourceFiles(e.ProjectRoot)
	if err != nil || len(paths) == 0 {
		return
	}
	_ = e.CodeMap.BuildFromFiles(ctx, paths)
}

func (e *Engine) Analyze(ctx context.Context, path string) (AnalyzeReport, error) {
	return e.AnalyzeWithOptions(ctx, AnalyzeOptions{Path: path})
}

func (e *Engine) AnalyzeWithOptions(ctx context.Context, opts AnalyzeOptions) (AnalyzeReport, error) {
	root := e.ProjectRoot
	if strings.TrimSpace(opts.Path) != "" {
		root = opts.Path
	}
	paths, err := e.collectSourceFiles(root)
	if err != nil {
		return AnalyzeReport{}, err
	}
	if e.CodeMap != nil {
		_ = e.CodeMap.BuildFromFiles(ctx, paths)
	}
	report := AnalyzeReport{
		ProjectRoot: root,
		Files:       len(paths),
	}
	if e.CodeMap != nil && e.CodeMap.Graph() != nil {
		graph := e.CodeMap.Graph()
		report.Nodes = len(graph.Nodes())
		report.Edges = len(graph.Edges())
		report.Cycles = len(graph.Cycles())
		report.HotSpots = graph.HotSpots(10)
	}

	runSecurity := opts.Full || opts.Security
	runDeadCode := opts.Full || opts.DeadCode
	runComplexity := opts.Full || opts.Complexity
	runDuplication := opts.Full || opts.Duplication

	if runSecurity && e.Security != nil {
		secReport, err := e.Security.ScanPaths(paths)
		if err != nil {
			return report, err
		}
		report.Security = &secReport
	}
	if runDeadCode {
		items, err := e.detectDeadCode(ctx, paths)
		if err != nil {
			return report, err
		}
		report.DeadCode = items
	}
	if runComplexity {
		cx, err := e.computeComplexity(ctx, paths)
		if err != nil {
			return report, err
		}
		report.Complexity = &cx
	}
	if runDuplication {
		dup := detectDuplication(paths, duplicationMinLines)
		report.Duplication = &dup
	}
	if opts.Full || opts.Todos {
		td := collectTodoMarkers(paths)
		report.Todos = &td
	}

	return report, nil
}

func (e *Engine) collectSourceFiles(root string) ([]string, error) {
	var out []string
	if strings.TrimSpace(root) == "" {
		return out, nil
	}

	skipDirs := map[string]struct{}{
		".git":         {},
		".dfmc":        {},
		"vendor":       {},
		"node_modules": {},
		"dist":         {},
		"build":        {},
		"bin":          {},
	}
	allowed := map[string]struct{}{
		".go": {}, ".ts": {}, ".tsx": {}, ".js": {}, ".jsx": {},
		".py": {}, ".rs": {}, ".java": {}, ".cs": {}, ".php": {},
		".rb": {}, ".c": {}, ".h": {}, ".cpp": {}, ".cc": {}, ".hpp": {},
		".swift": {}, ".kt": {}, ".kts": {}, ".scala": {}, ".sql": {}, ".lua": {},
	}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if _, ok := skipDirs[d.Name()]; ok {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if _, ok := allowed[ext]; ok || d.Name() == "Dockerfile" {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

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


func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

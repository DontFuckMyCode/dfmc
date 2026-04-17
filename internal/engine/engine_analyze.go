// Analyze-pipeline methods for the Engine. Extracted from engine.go.
// Hosts the project-wide analyze run, dead-code detection, cyclomatic
// complexity scoring, and the language-aware text-strippers that feed
// both. All exported entry points route through AnalyzeWithOptions;
// helpers are package-private and colocated with their callers.

package engine

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/tokens"
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

// stripStringsAndComments removes string literals and comments from
// source so a symbol's name occurring inside them does not inflate
// the "looks used" count. Extension-keyed language heuristics:
//
//   - .go / .js / .jsx / .ts / .tsx / .java / .c / .cpp / .cs: `//`
//     line comments, `/* ... */` block comments (cross-line),
//     double-quoted strings with `\` escapes, single-quoted runes,
//     backtick raw strings.
//   - .py: `#` line comments, `"""..."""` / `'''...'''` triple-quoted
//     docstrings (cross-line), single-line "..." and '...' strings.
//   - others: no stripping — a conservative choice that may leave
//     false-usage mentions for unknown languages.
//
// Replaced characters become spaces so line numbers and whitespace
// alignment stay intact for any downstream line-oriented analysis.
func stripStringsAndComments(text, ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "go", "js", "jsx", "mjs", "cjs", "ts", "tsx",
		"java", "c", "cpp", "cc", "h", "hpp", "cs", "rs":
		return stripCFamily(text)
	case "py", "pyw":
		return stripPython(text)
	}
	return text
}

// stripCommentsOnly removes comments but preserves string literals.
// Used by callers (duplication detector) that need to distinguish
// struct-literal tables with different data from real copy-paste —
// without string content, `Name: "review"` and `Name: "explain"`
// collapse to the same normalised line and report as a clone even
// though the semantic content differs. Dead-code and similar
// occurrence-counting passes still use stripStringsAndComments
// because a symbol merely mentioned in a help-text string ISN'T a
// real usage.
func stripCommentsOnly(text, ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "go", "js", "jsx", "mjs", "cjs", "ts", "tsx",
		"java", "c", "cpp", "cc", "h", "hpp", "cs", "rs":
		return stripCFamilyComments(text)
	case "py", "pyw":
		return stripPythonComments(text)
	}
	return text
}

// stripCFamilyComments blanks out line (`//`) and block (`/* ... */`)
// comments while leaving string / rune / backtick literals intact.
// Mirrors stripCFamily's structure so the behaviour is easy to
// reason about.
func stripCFamilyComments(text string) string {
	b := []byte(text)
	out := make([]byte, len(b))
	copy(out, b)
	i := 0
	for i < len(out) {
		c := out[i]
		if c == '/' && i+1 < len(out) && out[i+1] == '/' {
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(out) && out[i+1] == '*' {
			for i < len(out) {
				if out[i] == '*' && i+1 < len(out) && out[i+1] == '/' {
					out[i] = ' '
					out[i+1] = ' '
					i += 2
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote := c
			i++
			for i < len(out) {
				if quote != '`' && out[i] == '\\' && i+1 < len(out) {
					i += 2
					continue
				}
				if out[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		}
		i++
	}
	return string(out)
}

// stripPythonComments blanks out `#` line comments and `"""..."""` /
// `'''...'''` docstrings while leaving ordinary string literals
// intact. Single-quoted / double-quoted single-line strings are
// preserved — they carry the data we need to distinguish struct /
// dict entries.
func stripPythonComments(text string) string {
	b := []byte(text)
	out := make([]byte, len(b))
	copy(out, b)
	i := 0
	for i < len(out) {
		c := out[i]
		if c == '#' {
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
			continue
		}
		if (c == '"' || c == '\'') && i+2 < len(out) && out[i+1] == c && out[i+2] == c {
			quote := c
			out[i] = ' '
			out[i+1] = ' '
			out[i+2] = ' '
			i += 3
			for i < len(out) {
				if i+2 < len(out) && out[i] == quote && out[i+1] == quote && out[i+2] == quote {
					out[i] = ' '
					out[i+1] = ' '
					out[i+2] = ' '
					i += 3
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		// Leave single-line strings alone.
		if c == '"' || c == '\'' {
			quote := c
			i++
			for i < len(out) && out[i] != '\n' {
				if out[i] == '\\' && i+1 < len(out) {
					i += 2
					continue
				}
				if out[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		}
		i++
	}
	return string(out)
}

func stripCFamily(text string) string {
	b := []byte(text)
	out := make([]byte, len(b))
	copy(out, b)
	i := 0
	for i < len(out) {
		c := out[i]
		// Line comment
		if c == '/' && i+1 < len(out) && out[i+1] == '/' {
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
			continue
		}
		// Block comment
		if c == '/' && i+1 < len(out) && out[i+1] == '*' {
			for i < len(out) {
				if out[i] == '*' && i+1 < len(out) && out[i+1] == '/' {
					out[i] = ' '
					out[i+1] = ' '
					i += 2
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		// String / rune / raw-string literal
		if c == '"' || c == '\'' || c == '`' {
			quote := c
			out[i] = ' '
			i++
			for i < len(out) {
				if quote != '`' && out[i] == '\\' && i+1 < len(out) {
					out[i] = ' '
					out[i+1] = ' '
					i += 2
					continue
				}
				if out[i] == quote {
					out[i] = ' '
					i++
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		i++
	}
	return string(out)
}

func stripPython(text string) string {
	b := []byte(text)
	out := make([]byte, len(b))
	copy(out, b)
	i := 0
	for i < len(out) {
		c := out[i]
		if c == '#' {
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
			continue
		}
		// Triple-quoted docstrings.
		if (c == '"' || c == '\'') && i+2 < len(out) && out[i+1] == c && out[i+2] == c {
			quote := c
			out[i] = ' '
			out[i+1] = ' '
			out[i+2] = ' '
			i += 3
			for i < len(out) {
				if i+2 < len(out) && out[i] == quote && out[i+1] == quote && out[i+2] == quote {
					out[i] = ' '
					out[i+1] = ' '
					out[i+2] = ' '
					i += 3
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		// Single-quoted / double-quoted strings (single-line).
		if c == '"' || c == '\'' {
			quote := c
			out[i] = ' '
			i++
			for i < len(out) && out[i] != '\n' {
				if out[i] == '\\' && i+1 < len(out) {
					out[i] = ' '
					out[i+1] = ' '
					i += 2
					continue
				}
				if out[i] == quote {
					out[i] = ' '
					i++
					break
				}
				out[i] = ' '
				i++
			}
			continue
		}
		i++
	}
	return string(out)
}

func (e *Engine) computeComplexity(ctx context.Context, paths []string) (ComplexityReport, error) {
	report := ComplexityReport{Files: len(paths)}
	functions := make([]FunctionComplexity, 0, 128)
	fileScores := make([]FunctionComplexity, 0, len(paths))
	totalScore := 0
	maxScore := 0
	totalSymbols := 0
	scannedSymbols := 0

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(content)
		fileScore := complexityScore(text)
		fileScores = append(fileScores, FunctionComplexity{
			Name:  filepath.Base(path),
			File:  filepath.ToSlash(path),
			Line:  1,
			Score: fileScore,
		})
		totalScore += fileScore
		if fileScore > maxScore {
			maxScore = fileScore
		}

		if e.AST == nil {
			continue
		}
		res, err := e.AST.ParseContent(ctx, path, content)
		if err != nil {
			continue
		}
		totalSymbols += len(res.Symbols)
		lines := strings.Split(text, "\n")
		for _, sym := range res.Symbols {
			kind := strings.ToLower(string(sym.Kind))
			if kind != "function" && kind != "method" {
				continue
			}
			scannedSymbols++
			start := sym.Line - 1
			if start < 0 || start >= len(lines) {
				continue
			}
			// Slice the function body by tracking brace depth from the
			// declaration line. Works for Go, C, JS/TS, Java. Python
			// (indent-based) falls back to the next-symbol heuristic
			// via endByNextSymbol because Python has no '{'.
			end := endOfFunctionBody(lines, start, res.Language)
			if end <= start {
				end = start + 1
			}
			segment := strings.Join(lines[start:minInt(end, len(lines))], "\n")
			score := complexityScore(segment)
			functions = append(functions, FunctionComplexity{
				Name:  sym.Name,
				File:  filepath.ToSlash(path),
				Line:  sym.Line,
				Score: score,
			})
		}
	}

	report.Max = maxScore
	if len(fileScores) > 0 {
		report.Average = math.Round((float64(totalScore)/float64(len(fileScores)))*100) / 100
	}
	report.TotalSymbols = totalSymbols
	report.ScannedSymbol = scannedSymbols

	sort.Slice(functions, func(i, j int) bool { return functions[i].Score > functions[j].Score })
	sort.Slice(fileScores, func(i, j int) bool { return fileScores[i].Score > fileScores[j].Score })
	if len(functions) > 20 {
		functions = functions[:20]
	}
	if len(fileScores) > 10 {
		fileScores = fileScores[:10]
	}
	report.TopFunctions = functions
	report.TopFiles = fileScores
	return report, nil
}

// complexityScore approximates McCabe cyclomatic complexity. It counts
// decision points using word-boundary regex so the scorer catches
// `if(x)` (no trailing space), tab-indented `\tif`, `}else if{`, etc.
// — all of which the previous space-padded substring variant missed.
// False positives from identifiers containing keyword substrings (e.g.
// `verifyUser`) are avoided by anchoring on `\b`.
//
// The score is language-agnostic: any branch/loop/jump keyword in any
// of the supported languages contributes +1. A function with zero
// branches returns 1 (the single entry path).
func complexityScore(text string) int {
	if text == "" {
		return 1
	}
	score := 1
	for _, re := range complexityBranchRegexes {
		score += len(re.FindAllStringIndex(text, -1))
	}
	return score
}

// endOfFunctionBody returns the (0-indexed) line AFTER the closing
// delimiter of the function that STARTS at `start`. For brace-based
// languages it tracks `{` / `}` depth while respecting strings, runes,
// and line/block comments. For Python (indent-based) it walks until a
// non-blank line's indentation drops to or below the function's own
// indent. If neither strategy finds a clean end, returns `len(lines)`
// so the caller still gets a sensible segment (whole rest of file).
func endOfFunctionBody(lines []string, start int, language string) int {
	if start < 0 || start >= len(lines) {
		return len(lines)
	}
	lang := strings.ToLower(strings.TrimSpace(language))
	if lang == "python" {
		return endOfPythonBody(lines, start)
	}
	return endOfBraceBody(lines, start)
}

// endOfBraceBody walks lines from `start`, counting balanced braces
// outside strings/comments. Stops one line past the line where depth
// returns to zero AFTER having been positive at least once. This is
// resilient to nested closures — the body of an outer function
// legitimately contains many `{}` pairs and only the outermost match
// closes it.
func endOfBraceBody(lines []string, start int) int {
	depth := 0
	opened := false
	inBlockComment := false
	for i := start; i < len(lines); i++ {
		line := lines[i]
		j := 0
		for j < len(line) {
			if inBlockComment {
				if j+1 < len(line) && line[j] == '*' && line[j+1] == '/' {
					inBlockComment = false
					j += 2
					continue
				}
				j++
				continue
			}
			// Line comment — rest of the line is not code.
			if j+1 < len(line) && line[j] == '/' && line[j+1] == '/' {
				break
			}
			// Block comment start.
			if j+1 < len(line) && line[j] == '/' && line[j+1] == '*' {
				inBlockComment = true
				j += 2
				continue
			}
			// Skip string / rune literals so their braces don't count.
			if c := line[j]; c == '"' || c == '\'' || c == '`' {
				j = skipStringLiteral(line, j)
				continue
			}
			if line[j] == '{' {
				depth++
				opened = true
			} else if line[j] == '}' {
				depth--
				if opened && depth <= 0 {
					return i + 1
				}
			}
			j++
		}
	}
	return len(lines)
}

// skipStringLiteral returns the index of the character AFTER the
// closing quote of a string/rune/backtick literal starting at
// `line[start]`. Respects escape sequences for "" and ''. Backtick
// (raw) strings don't honour escapes in Go. If the literal doesn't
// close on this line (multi-line raw strings), returns len(line).
func skipStringLiteral(line string, start int) int {
	if start >= len(line) {
		return start
	}
	quote := line[start]
	j := start + 1
	for j < len(line) {
		c := line[j]
		if quote != '`' && c == '\\' {
			j += 2
			continue
		}
		if c == quote {
			return j + 1
		}
		j++
	}
	return len(line)
}

// endOfPythonBody walks until a non-blank line whose indentation is
// ≤ the def's indentation. That line belongs to the enclosing scope,
// so the function ends the line before.
func endOfPythonBody(lines []string, start int) int {
	defIndent := leadingWhitespaceLen(lines[start])
	for i := start + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if leadingWhitespaceLen(line) <= defIndent {
			return i
		}
	}
	return len(lines)
}

func leadingWhitespaceLen(s string) int {
	n := 0
	for n < len(s) && (s[n] == ' ' || s[n] == '\t') {
		n++
	}
	return n
}

// complexityBranchRegexes is compiled once and reused; compiling per
// call is expensive and shows up in profiles for big codebases. Each
// regex is word-boundary-anchored on the keyword side and loose on
// the delimiter side (accepts space / paren / brace / line end).
var complexityBranchRegexes = func() []*regexp.Regexp {
	keywords := []string{
		"if", "else if", "elif",
		"for", "while", "do",
		"switch", "case",
		"catch", "except", "rescue", "finally",
		"goto",
	}
	out := make([]*regexp.Regexp, 0, len(keywords)+3)
	for _, kw := range keywords {
		// Keyword must be preceded by non-word OR start-of-string,
		// and followed by a space/paren/brace/colon. `\b...\b` alone
		// would match inside identifiers when followed by whitespace
		// only, which is why we also require the trailing-char class.
		out = append(out, regexp.MustCompile(`(^|\W)`+regexp.QuoteMeta(kw)+`[\s(:{]`))
	}
	// Short-circuit boolean operators — one decision per && / ||.
	out = append(out,
		regexp.MustCompile(`&&`),
		regexp.MustCompile(`\|\|`),
	)
	// Ternary: match `?` followed by non-punct so we don't count
	// `foo?.bar` (JS optional chaining) or `type?` annotations.
	out = append(out, regexp.MustCompile(`\?\s`))
	return out
}()

func looksEntrypoint(name, file string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "main" || n == "init" {
		return true
	}
	if strings.HasPrefix(n, "test") {
		return true
	}
	base := strings.ToLower(filepath.Base(file))
	return strings.HasSuffix(base, "_test.go")
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

func estimateTokens(text string) int {
	return tokens.Estimate(text)
}

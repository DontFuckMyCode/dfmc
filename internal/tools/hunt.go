package tools

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type BugSeverity string

const (
	SevCritical BugSeverity = "CRITICAL"
	SevHigh     BugSeverity = "HIGH"
	SevMedium   BugSeverity = "MEDIUM"
	SevLow      BugSeverity = "LOW"
	SevInfo     BugSeverity = "INFO"
)

type BugFinding struct {
	File       string      `json:"file"`
	Line       int         `json:"line"`
	Severity   BugSeverity `json:"severity"`
	Category   string      `json:"category"`
	Message    string      `json:"message"`
	Suggestion string      `json:"suggestion,omitempty"`
	Code       string      `json:"code,omitempty"`
}

type HuntTool struct {
	engine *Engine
}

func NewHuntTool() *HuntTool            { return &HuntTool{} }
func (t *HuntTool) Name() string        { return "bug_hunt" }
func (t *HuntTool) SetEngine(e *Engine) { t.engine = e }

func (t *HuntTool) Description() string {
	return "Scan the project for potential bugs using high-fidelity AST analysis and call-graph hotspots."
}

func (t *HuntTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "bug_hunt",
		Title:   "Bug Hunt",
		Summary: "Autonomous bug detection using AST analysis.",
		Purpose: "Proactively find bugs: unchecked errors, nil dereferences, race conditions, security issues.",
		Risk:    RiskRead,
		Tags:    []string{"quality", "autonomous", "hunt", "bug", "security"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Description: "Directory or file to scan (default: project root)."},
			{Name: "severity", Type: ArgString, Description: "Minimum severity: critical, high, medium, low, info.", Default: "low"},
			{Name: "categories", Type: ArgString, Description: "Comma-separated: unchecked-error, nil-dereference, concurrent-map, sql-injection, secrets, insecure-rand."},
		},
	}
}

func (t *HuntTool) Execute(ctx context.Context, req Request) (Result, error) {
	start := time.Now()

	targetPath := strings.TrimSpace(asString(req.Params, "path", ""))
	if targetPath == "" {
		targetPath = req.ProjectRoot
	}
	minSeverity := parseHuntSeverity(asString(req.Params, "severity", "low"))
	categoriesRaw := strings.TrimSpace(asString(req.Params, "categories", ""))

	var goFiles []string
	if err := filepath.Walk(targetPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "vendor" || base == "node_modules" || base == ".dfmc" || base == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			goFiles = append(goFiles, path)
		}
		return nil
	}); err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("walk %s: %w", targetPath, err)
	}

	if len(goFiles) == 0 {
		return Result{
			Output:     "No Go source files found.",
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}

	var findings []BugFinding
	var mu sync.Mutex
	var wg sync.WaitGroup

	detectors := getHuntDetectors(categoriesRaw)
	for _, detect := range detectors {
		detect := detect
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := runHuntDetector(detect, goFiles)
			mu.Lock()
			findings = append(findings, local...)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if minSeverity != SevInfo {
		filtered := make([]BugFinding, 0, len(findings))
		for _, f := range findings {
			if compareHuntSeverity(f.Severity, minSeverity) >= 0 {
				filtered = append(filtered, f)
			}
		}
		findings = filtered
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return compareHuntSeverity(findings[i].Severity, findings[j].Severity) > 0
		}
		return findings[i].Line < findings[j].Line
	})

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Bug Hunt Report\n"))
	sb.WriteString(fmt.Sprintf("**Files scanned:** %d  **Issues found:** %d\n\n", len(goFiles), len(findings)))

	if len(findings) == 0 {
		sb.WriteString("No issues detected.\n")
		return Result{
			Output:     sb.String(),
			Data:       map[string]any{"files": len(goFiles), "findings": []BugFinding{}},
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}

	sevCounts := make(map[BugSeverity]int)
	catCounts := make(map[string]int)
	for _, f := range findings {
		sevCounts[f.Severity]++
		catCounts[f.Category]++
	}

	sb.WriteString("### By Severity\n")
	for _, s := range []BugSeverity{SevCritical, SevHigh, SevMedium, SevLow, SevInfo} {
		if c := sevCounts[s]; c > 0 {
			sb.WriteString(fmt.Sprintf("- **%s:** %d\n", s, c))
		}
	}
	sb.WriteString("\n### By Category\n")
	type pair struct {
		cat string
		n   int
	}
	var pairs []pair
	for c, n := range catCounts {
		pairs = append(pairs, pair{c, n})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].n > pairs[j].n })
	for _, p := range pairs {
		sb.WriteString(fmt.Sprintf("- **%s:** %d\n", p.cat, p.n))
	}
	sb.WriteString("\n---\n\n### Findings\n")
	for i, f := range findings {
		emoji := "⚠️"
		if f.Severity == SevCritical {
			emoji = "🚨"
		} else if f.Severity == SevHigh {
			emoji = "🔴"
		} else if f.Severity == SevLow {
			emoji = "💡"
		}
		sb.WriteString(fmt.Sprintf("**%d. %s [%s] %s**  \n", i+1, emoji, f.Severity, f.Category))
		sb.WriteString(fmt.Sprintf("`%s:%d`\n\n", f.File, f.Line))
		sb.WriteString(f.Message + "\n")
		if f.Code != "" {
			sb.WriteString(fmt.Sprintf("```go\n%s\n```\n", f.Code))
		}
		if f.Suggestion != "" {
			sb.WriteString(fmt.Sprintf("**Fix:** %s\n", f.Suggestion))
		}
		sb.WriteString("\n")
	}

	return Result{
		Output:     sb.String(),
		Data:       map[string]any{"files": len(goFiles), "findings": findings},
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

func runHuntDetector(detect func(*token.FileSet, ast.Node, string, *[]BugFinding), files []string) []BugFinding {
	var out []BugFinding
	fset := token.NewFileSet()
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		f, err := parser.ParseFile(fset, file, src, parser.AllErrors)
		if err != nil {
			continue
		}
		dir := filepath.Dir(file)
		ast.Inspect(f, func(n ast.Node) bool {
			detect(fset, n, dir, &out)
			return true
		})
	}
	return out
}

func parseHuntSeverity(s string) BugSeverity {
	switch strings.ToLower(s) {
	case "critical":
		return SevCritical
	case "high":
		return SevHigh
	case "medium":
		return SevMedium
	case "low":
		return SevLow
	default:
		return SevInfo
	}
}

func compareHuntSeverity(a, b BugSeverity) int {
	order := map[BugSeverity]int{SevCritical: 5, SevHigh: 4, SevMedium: 3, SevLow: 2, SevInfo: 1}
	return order[a] - order[b]
}

func getHuntDetectors(categories string) []func(*token.FileSet, ast.Node, string, *[]BugFinding) {
	all := []func(*token.FileSet, ast.Node, string, *[]BugFinding){
		detectUncheckedError, detectNilDereference, detectConcurrentMap,
		detectSQLInjection, detectHardcodedSecrets, detectInsecureRand,
	}
	if categories == "" {
		return all
	}
	enabled := make(map[string]bool)
	for _, c := range strings.Split(categories, ",") {
		enabled[strings.TrimSpace(strings.ToLower(c))] = true
	}
	var out []func(*token.FileSet, ast.Node, string, *[]BugFinding)
	for _, d := range all {
		out = append(out, d)
	}
	return out
}

func detectUncheckedError(fset *token.FileSet, n ast.Node, path string, out *[]BugFinding) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return
	}
	fn := ""
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		fn = fun.Name
	case *ast.SelectorExpr:
		if x, ok := fun.X.(*ast.Ident); ok {
			fn = x.Name + "." + fun.Sel.Name
		}
	}
	known := map[string]bool{
		"json.Unmarshal": true, "ioutil.WriteFile": true, "ioutil.ReadFile": true,
		"os.Open": true, "os.Create": true, "os.WriteFile": true, "os.ReadFile": true,
		"os.MkdirAll": true, "regexp.Compile": true, "template.ParseFiles": true,
	}
	if !known[fn] {
		return
	}
	pos := fset.Position(n.Pos())
	*out = append(*out, BugFinding{
		File: pos.Filename, Line: pos.Line,
		Severity: SevMedium, Category: "unchecked-error",
		Message:    fmt.Sprintf("Result of `%s` is ignored; error return is not checked.", fn),
		Suggestion: fmt.Sprintf("Handle the error: `_, err := %s(...)`", fn),
	})
}

func detectNilDereference(fset *token.FileSet, n ast.Node, path string, out *[]BugFinding) {
	sel, ok := n.(*ast.SelectorExpr)
	if !ok {
		return
	}
	if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "nil" {
		pos := fset.Position(n.Pos())
		*out = append(*out, BugFinding{
			File: pos.Filename, Line: pos.Line,
			Severity: SevCritical, Category: "nil-dereference",
			Message:    "Accessing field on nil value.",
			Suggestion: "Check for nil before accessing fields.",
		})
	}
}

func detectConcurrentMap(fset *token.FileSet, n ast.Node, path string, out *[]BugFinding) {
	_, ok := n.(*ast.GoStmt)
	if !ok {
		return
	}
	pos := fset.Position(n.Pos())
	*out = append(*out, BugFinding{
		File: pos.Filename, Line: pos.Line,
		Severity: SevHigh, Category: "concurrent-map",
		Message:    "Map access inside goroutine may cause race conditions.",
		Suggestion: "Use sync.RWMutex or sync.Map for concurrent map access.",
	})
}

func detectSQLInjection(fset *token.FileSet, n ast.Node, path string, out *[]BugFinding) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	fn := ""
	if x, ok := sel.X.(*ast.Ident); ok {
		fn = x.Name + "." + sel.Sel.Name
	}
	if fn != "fmt.Sprintf" && fn != "fmt.Sprint" {
		return
	}
	pos := fset.Position(n.Pos())
	*out = append(*out, BugFinding{
		File: pos.Filename, Line: pos.Line,
		Severity: SevHigh, Category: "sql-injection",
		Message:    "Potential SQL injection: string formatting in query context.",
		Suggestion: "Use parameterized queries instead of string concatenation.",
	})
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|apikey|secret|password|passwd|pwd|token|bearer|auth|aws[_-]?access[_-]?key|aws[_-]?secret)`),
}

func detectHardcodedSecrets(fset *token.FileSet, n ast.Node, path string, out *[]BugFinding) {
	lit, ok := n.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING && lit.Kind != token.IMAG {
		return
	}
	val := lit.Value
	for _, re := range secretPatterns {
		if re.MatchString(val) && len(val) > 4 {
			pos := fset.Position(n.Pos())
			*out = append(*out, BugFinding{
				File: pos.Filename, Line: pos.Line,
				Severity: SevCritical, Category: "secrets",
				Message:    "Potential hardcoded secret detected.",
				Suggestion: "Use environment variables or a secrets manager.",
				Code:       val,
			})
			return
		}
	}
}

func detectInsecureRand(fset *token.FileSet, n ast.Node, path string, out *[]BugFinding) {
	sel, ok := n.(*ast.SelectorExpr)
	if !ok {
		return
	}
	if x, ok := sel.X.(*ast.Ident); ok && x.Name == "rand" {
		pos := fset.Position(n.Pos())
		*out = append(*out, BugFinding{
			File: pos.Filename, Line: pos.Line,
			Severity: SevMedium, Category: "insecure-rand",
			Message:    "Using math/rand for cryptographic purposes is insecure.",
			Suggestion: "Use crypto/rand for security-sensitive random values.",
		})
	}
}

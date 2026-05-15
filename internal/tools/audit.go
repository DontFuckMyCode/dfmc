package tools

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type AuditSeverity string

const (
	AuditCritical AuditSeverity = "CRITICAL"
	AuditHigh     AuditSeverity = "HIGH"
	AuditMedium   AuditSeverity = "MEDIUM"
	AuditLow      AuditSeverity = "LOW"
	AuditInfo     AuditSeverity = "INFO"
)

type AuditFinding struct {
	File        string        `json:"file"`
	Line        int           `json:"line"`
	Severity    AuditSeverity `json:"severity"`
	Category    string        `json:"category"`
	CWE         string        `json:"cwe,omitempty"`
	Message     string        `json:"message"`
	Exploitable string        `json:"exploitable,omitempty"`
	Suggestion  string        `json:"suggestion,omitempty"`
	Snippet     string        `json:"snippet,omitempty"`
}

type AuditTool struct {
	engine *Engine
}

func NewAuditTool() *AuditTool           { return &AuditTool{} }
func (t *AuditTool) Name() string        { return "audit" }
func (t *AuditTool) SetEngine(e *Engine) { t.engine = e }

func (t *AuditTool) Description() string {
	return "Security audit: scan for vulnerabilities, hardcoded secrets, injection risks, and exploit patterns."
}

func (t *AuditTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "audit",
		Title:   "Security Audit",
		Summary: "Proactive security vulnerability scanner with CWE mapping.",
		Purpose: "Find security issues: SQL injection, XSS, hardcoded secrets, insecure crypto, command injection, path traversal.",
		Risk:    RiskRead,
		Tags:    []string{"security", "audit", "vulnerability", "cwe", "owasp"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Description: "Directory or file to audit (default: project root)."},
			{Name: "severity", Type: ArgString, Description: "Minimum severity: critical, high, medium, low, info.", Default: "low"},
			{Name: "categories", Type: ArgString, Description: "Comma-separated: secrets,sql-injection,xss,cmd-injection,path-traversal,insecure-crypto,unsafe-redirect,xxe"},
			{Name: "confidence", Type: ArgString, Description: "Minimum confidence: high, medium, low.", Default: "low"},
		},
	}
}

func (t *AuditTool) Execute(ctx context.Context, req Request) (Result, error) {
	start := time.Now()

	targetPath := strings.TrimSpace(asString(req.Params, "path", ""))
	if targetPath == "" {
		targetPath = req.ProjectRoot
	}
	minSeverity := parseAuditSeverity(asString(req.Params, "severity", "low"))
	categoriesRaw := strings.TrimSpace(asString(req.Params, "categories", ""))

	var goFiles []string
	if err := filepath.Walk(targetPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "vendor" || base == "node_modules" || base == ".dfmc" {
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

	var findings []AuditFinding
	var mu sync.Mutex
	var wg sync.WaitGroup

	detectors := getAuditDetectors(categoriesRaw)

	for _, detect := range detectors {
		detect := detect
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := runAuditDetector(detect, goFiles)
			mu.Lock()
			findings = append(findings, local...)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if minSeverity != AuditInfo {
		filtered := make([]AuditFinding, 0, len(findings))
		for _, f := range findings {
			if compareAuditSeverity(f.Severity, minSeverity) >= 0 {
				filtered = append(filtered, f)
			}
		}
		findings = filtered
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return compareAuditSeverity(findings[i].Severity, findings[j].Severity) > 0
		}
		return findings[i].Line < findings[j].Line
	})

	var sb strings.Builder
	sb.WriteString("## Security Audit Report\n")
	sb.WriteString(fmt.Sprintf("**Files scanned:** %d  **Issues found:** %d\n\n", len(goFiles), len(findings)))

	if len(findings) == 0 {
		sb.WriteString("No security issues detected.\n")
		return Result{
			Output:     sb.String(),
			Data:       map[string]any{"files": len(goFiles), "findings": []AuditFinding{}},
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}

	severityCounts := make(map[AuditSeverity]int)
	for _, f := range findings {
		severityCounts[f.Severity]++
	}
	sb.WriteString("### Summary\n")
	for _, sev := range []AuditSeverity{AuditCritical, AuditHigh, AuditMedium, AuditLow, AuditInfo} {
		if count := severityCounts[sev]; count > 0 {
			sb.WriteString(fmt.Sprintf("- **%s:** %d\n", sev, count))
		}
	}
	sb.WriteString("\n### Findings\n\n")

	for i, f := range findings {
		sb.WriteString(fmt.Sprintf("#### %d. [%s] %s\n", i+1, f.Severity, f.Category))
		sb.WriteString(fmt.Sprintf("**Location:** `%s:%d`\n", f.File, f.Line))
		if f.CWE != "" {
			sb.WriteString(fmt.Sprintf("**CWE:** %s\n", f.CWE))
		}
		sb.WriteString(fmt.Sprintf("**Finding:** %s\n", f.Message))
		if f.Exploitable != "" {
			sb.WriteString(fmt.Sprintf("**Exploitable:** %s\n", f.Exploitable))
		}
		if f.Suggestion != "" {
			sb.WriteString(fmt.Sprintf("**Fix:** %s\n", f.Suggestion))
		}
		if f.Snippet != "" {
			sb.WriteString(fmt.Sprintf("```go\n%s\n```\n", f.Snippet))
		}
		sb.WriteString("\n")
	}

	return Result{
		Output:     sb.String(),
		Data:       map[string]any{"files": len(goFiles), "findings": findings, "summary": severityCounts},
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

func runAuditDetector(detector func(*token.FileSet, ast.Node, string, int, *[]AuditFinding), files []string) []AuditFinding {
	var out []AuditFinding
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
		lineCount := len(strings.Split(string(src), "\n"))
		dir := filepath.Dir(file)
		ast.Inspect(f, func(n ast.Node) bool {
			detector(fset, n, dir, lineCount, &out)
			return true
		})
	}
	return out
}

func parseAuditSeverity(s string) AuditSeverity {
	switch strings.ToLower(s) {
	case "critical":
		return AuditCritical
	case "high":
		return AuditHigh
	case "medium":
		return AuditMedium
	case "low":
		return AuditLow
	default:
		return AuditInfo
	}
}

func compareAuditSeverity(a, b AuditSeverity) int {
	order := map[AuditSeverity]int{AuditCritical: 5, AuditHigh: 4, AuditMedium: 3, AuditLow: 2, AuditInfo: 1}
	return order[a] - order[b]
}

func getAuditDetectors(categories string) []func(*token.FileSet, ast.Node, string, int, *[]AuditFinding) {
	type auditDetector struct {
		category string
		fn       func(*token.FileSet, ast.Node, string, int, *[]AuditFinding)
	}
	// Category names mirror the Spec.Args description for the `categories`
	// argument. detectAuditInsecureRand + detectWeakCrypto both file under
	// "insecure-crypto" so users can request the crypto-hygiene bundle
	// with one keyword.
	all := []auditDetector{
		{"secrets", detectAuditHardcodedSecrets},
		{"sql-injection", detectAuditSQLInjection},
		{"cmd-injection", detectCommandInjection},
		{"path-traversal", detectPathTraversal},
		{"insecure-crypto", detectAuditInsecureRand},
		{"insecure-crypto", detectWeakCrypto},
		{"xss", detectXSS},
		{"unsafe-redirect", detectUnsafeRedirect},
		{"xxe", detectXXE},
	}
	if categories == "" {
		out := make([]func(*token.FileSet, ast.Node, string, int, *[]AuditFinding), 0, len(all))
		for _, d := range all {
			out = append(out, d.fn)
		}
		return out
	}
	enabled := make(map[string]bool)
	for _, c := range strings.Split(categories, ",") {
		key := strings.TrimSpace(strings.ToLower(c))
		if key != "" {
			enabled[key] = true
		}
	}
	out := make([]func(*token.FileSet, ast.Node, string, int, *[]AuditFinding), 0, len(all))
	for _, d := range all {
		if enabled[d.category] {
			out = append(out, d.fn)
		}
	}
	return out
}

func auditNodeString(fset *token.FileSet, n ast.Node) string {
	if n == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return ""
	}
	return buf.String()
}

// detectAuditHardcodedSecrets: CWE-798, CWE-259
func detectAuditHardcodedSecrets(fset *token.FileSet, n ast.Node, path string, lineCount int, out *[]AuditFinding) {
	lit, ok := n.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return
	}
	val := lit.Value
	if len(val) < 4 {
		return
	}

	secretPatterns := []struct {
		pattern *regexp.Regexp
		cwe     string
		msg     string
	}{
		{regexp.MustCompile(`(?i)['"]?(api[_-]?key|apikey|secret|password|passwd|pwd|token|bearer|auth)['"]?\s*[:=]\s*['"]?[a-zA-Z0-9+/=_-]{8,}`), "CWE-798", "Hardcoded credential detected"},
		{regexp.MustCompile(`(?i)['"]?(aws[_-]?(access[_-]?key|secret|session))['"]?\s*[:=]`), "CWE-798", "Hardcoded AWS credential detected"},
		{regexp.MustCompile(`['"]?sk[-_][a-zA-Z0-9]{20,}['"]?`), "CWE-798", "Hardcoded API key detected (Stripe-like)"},
		{regexp.MustCompile(`['"]?ghp_[a-zA-Z0-9]{36}['"]?`), "CWE-798", "Hardcoded GitHub token detected"},
		{regexp.MustCompile(`['"]?xox[baprs]-[0-9a-zA-Z-]{10,}['"]?`), "CWE-798", "Hardcoded Slack token detected"},
	}

	for _, sp := range secretPatterns {
		if sp.pattern.MatchString(val) {
			pos := fset.Position(n.Pos())
			*out = append(*out, AuditFinding{
				File:        pos.Filename,
				Line:        pos.Line,
				Severity:    AuditCritical,
				Category:    "hardcoded-secret",
				CWE:         sp.cwe,
				Message:     sp.msg,
				Exploitable: "Secret can be extracted from source code or binary.",
				Suggestion:  "Use environment variables or a secrets manager (Vault, AWS Secrets Manager).",
				Snippet:     val,
			})
			return
		}
	}
}

// detectAuditSQLInjection: CWE-89
func detectAuditSQLInjection(fset *token.FileSet, n ast.Node, path string, lineCount int, out *[]AuditFinding) {
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

	injectionPatterns := []string{"fmt.Sprintf", "fmt.Sprint", "strings.Join", "+", "fmt.Append"}
	isInjection := false
	for _, p := range injectionPatterns {
		if fn == p {
			isInjection = true
			break
		}
	}

	if !isInjection {
		return
	}

	sqlKeywords := []string{"SELECT", "INSERT", "UPDATE", "DELETE", "DROP", "CREATE", "ALTER", "EXEC", "UNION"}
	pos := fset.Position(n.Pos())
	callText := strings.ToUpper(auditNodeString(fset, call))
	for _, kw := range sqlKeywords {
		if strings.Contains(callText, kw) {
			*out = append(*out, AuditFinding{
				File:        pos.Filename,
				Line:        pos.Line,
				Severity:    AuditHigh,
				Category:    "sql-injection",
				CWE:         "CWE-89",
				Message:     "Potential SQL injection: string concatenation in SQL query.",
				Exploitable: "User input could be injected to manipulate database queries.",
				Suggestion:  "Use parameterized queries or prepared statements. Example: `db.Query(\"SELECT * FROM users WHERE id=$1\", userID)`",
			})
			return
		}
	}
}

// detectCommandInjection: CWE-78
func detectCommandInjection(fset *token.FileSet, n ast.Node, path string, lineCount int, out *[]AuditFinding) {
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

	dangerousFuncs := map[string]bool{
		"exec.Command":    true,
		"exec.LookPath":   true,
		"os.StartProcess": true,
		"syscall.Exec":    true,
		"os/exec.Command": true,
	}

	if !dangerousFuncs[fn] {
		return
	}

	for _, arg := range call.Args {
		if binLit, ok := arg.(*ast.BasicLit); ok && binLit.Kind == token.STRING {
			pos := fset.Position(n.Pos())
			*out = append(*out, AuditFinding{
				File:        pos.Filename,
				Line:        pos.Line,
				Severity:    AuditCritical,
				Category:    "command-injection",
				CWE:         "CWE-78",
				Message:     "Potential command injection: shell command with string concatenation.",
				Exploitable: "Attacker can execute arbitrary commands on the system.",
				Suggestion:  "Validate and sanitize all input. Use exec.Command with separate arguments instead of shell string.",
			})
			return
		}
	}
}

// detectPathTraversal: CWE-22
func detectPathTraversal(fset *token.FileSet, n ast.Node, path string, lineCount int, out *[]AuditFinding) {
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

	fileFuncs := map[string]bool{
		"os.Open":          true,
		"os.Create":        true,
		"os.OpenFile":      true,
		"ioutil.ReadFile":  true,
		"ioutil.WriteFile": true,
		"os.ReadFile":      true,
		"os.WriteFile":     true,
		"os.Rename":        true,
		"os.Remove":        true,
	}

	if !fileFuncs[fn] {
		return
	}

	for _, arg := range call.Args {
		if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			val := lit.Value
			if strings.Contains(val, "../") || strings.Contains(val, "..\\") {
				pos := fset.Position(n.Pos())
				*out = append(*out, AuditFinding{
					File:        pos.Filename,
					Line:        pos.Line,
					Severity:    AuditHigh,
					Category:    "path-traversal",
					CWE:         "CWE-22",
					Message:     "Potential path traversal: '..' in file path.",
					Exploitable: "Attacker can access files outside intended directory.",
					Suggestion:  "Validate path is within allowed directory. Use `path.Clean()` and check result.",
				})
				return
			}
		}
	}
}

// detectAuditInsecureRand: CWE-338
func detectAuditInsecureRand(fset *token.FileSet, n ast.Node, path string, lineCount int, out *[]AuditFinding) {
	sel, ok := n.(*ast.SelectorExpr)
	if !ok {
		return
	}
	if x, ok := sel.X.(*ast.Ident); ok && x.Name == "rand" {
		pos := fset.Position(n.Pos())
		*out = append(*out, AuditFinding{
			File:        pos.Filename,
			Line:        pos.Line,
			Severity:    AuditMedium,
			Category:    "insecure-random",
			CWE:         "CWE-338",
			Message:     "Using math/rand for security-sensitive purposes.",
			Exploitable: "Cryptographically weak random values can be predicted.",
			Suggestion:  "Use crypto/rand for security-sensitive random values.",
		})
	}
}

// detectXSS: CWE-79
func detectXSS(fset *token.FileSet, n ast.Node, path string, lineCount int, out *[]AuditFinding) {
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

	htmlWriteFuncs := []string{"fmt.Fprint", "io.WriteString", "w.Write"}
	for _, f := range htmlWriteFuncs {
		if fn == f {
			pos := fset.Position(n.Pos())
			*out = append(*out, AuditFinding{
				File:        pos.Filename,
				Line:        pos.Line,
				Severity:    AuditHigh,
				Category:    "xss",
				CWE:         "CWE-79",
				Message:     "Potential XSS: unescaped output to HTML writer.",
				Exploitable: "User input could be executed as JavaScript in browser.",
				Suggestion:  "Use html/template package or escape user input with html.EscapeString().",
			})
			return
		}
	}
}

// detectUnsafeRedirect: CWE-601
func detectUnsafeRedirect(fset *token.FileSet, n ast.Node, path string, lineCount int, out *[]AuditFinding) {
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

	redirectFuncs := map[string]bool{
		"http.Redirect":     true,
		"http.RedirectFunc": true,
	}

	if !redirectFuncs[fn] {
		return
	}

	if len(call.Args) > 1 {
		if _, ok := call.Args[1].(*ast.BasicLit); ok {
			pos := fset.Position(n.Pos())
			*out = append(*out, AuditFinding{
				File:        pos.Filename,
				Line:        pos.Line,
				Severity:    AuditMedium,
				Category:    "unsafe-redirect",
				CWE:         "CWE-601",
				Message:     "Redirect target may be user-controlled.",
				Exploitable: "Attacker can redirect victim to phishing or malware site.",
				Suggestion:  "Validate redirect URL is within allowed domain. Use allowlist approach.",
			})
		}
	}
}

// detectXXE: CWE-611
func detectXXE(fset *token.FileSet, n ast.Node, path string, lineCount int, out *[]AuditFinding) {
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

	if strings.Contains(fn, "xml.NewDecoder") || strings.Contains(fn, "xml.Decode") {
		pos := fset.Position(n.Pos())
		*out = append(*out, AuditFinding{
			File:        pos.Filename,
			Line:        pos.Line,
			Severity:    AuditHigh,
			Category:    "xxe",
			CWE:         "CWE-611",
			Message:     "XML parser may be vulnerable to XXE attack.",
			Exploitable: "Attacker can read local files or cause DoS via XML.",
			Suggestion:  "Disable external entity loading. Use xml.NewDecoder with no DTD processing.",
		})
	}
}

// detectWeakCrypto: CWE-327
func detectWeakCrypto(fset *token.FileSet, n ast.Node, path string, lineCount int, out *[]AuditFinding) {
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

	weakFuncs := map[string]string{
		"md5.New":       "MD5 is cryptographically broken (CWE-327)",
		"sha1.New":      "SHA-1 is deprecated for security purposes (CWE-327)",
		"des.NewCipher": "DES is insecure (56-bit key)",
	}

	if msg, ok := weakFuncs[fn]; ok {
		pos := fset.Position(n.Pos())
		*out = append(*out, AuditFinding{
			File:        pos.Filename,
			Line:        pos.Line,
			Severity:    AuditHigh,
			Category:    "weak-crypto",
			CWE:         "CWE-327",
			Message:     msg,
			Exploitable: "Weak cryptographic algorithms can be broken.",
			Suggestion:  "Use SHA-256 or stronger. For encryption, use AES-256-GCM or ChaCha20-Poly1305.",
		})
	}
}

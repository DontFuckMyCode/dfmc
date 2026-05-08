// security_audit.go — auto-run the security scanner after a write tool
// touches a sensitive path. Closes the dfmc_next_step.md item
// "Auto-run security scanner on sensitive code changes."
//
// Hooked into executeToolWithLifecycle's success path. Sensitive is a
// path-shape heuristic (auth/crypto/secret/token/key/provider/tls/
// oauth/password/cred), NOT a content scan, so the gate is cheap
// (single substring match per write). When the heuristic fires, we
// run security.Scanner.ScanPaths on just the changed file(s) and
// publish a `security:auto_audit` event with the finding counts. We
// never BLOCK the tool — the user can already see results in the
// event log and the audit tool itself; auto-blocking on a heuristic
// match would create more friction than it removes.
//
// Test files (_test.go / _test.py / .test.ts) are skipped by design:
// they routinely contain example secrets and stub credentials that
// would otherwise generate constant noise.

package engine

import (
	"path/filepath"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/security"
)

// sensitivePathTokens names path fragments that hint at security-
// relevant code. Match is case-insensitive, substring against the
// path components (so internal/auth/foo.go matches via "auth" but a
// random comment with "auth" in it does not — we never look at
// content here, only the path string).
var sensitivePathTokens = []string{
	"auth",
	"crypto",
	"secret",
	"security",
	"token",
	"keychain",
	"keystore",
	"provider",
	"tls",
	"oauth",
	"password",
	"cred",
	"sandbox",
	"env_scrub",
	"redact",
}

// writeTools enumerates tool names that mutate file content. When
// executeToolWithLifecycle reports success for one of these, we sniff
// the params for a path and check it against the sensitive heuristic.
// Read tools (read_file, grep_codebase, etc.) are skipped — they can't
// introduce a finding the scanner would flag.
var writeTools = map[string]struct{}{
	"write_file":    {},
	"edit_file":     {},
	"apply_patch":   {},
	"symbol_rename": {},
	"symbol_move":   {},
}

// maybeAuditSensitiveWrite runs the security scanner on the file(s)
// touched by a successful write tool, but only when the path looks
// security-sensitive. Best-effort: scanner errors and missing files
// are silently swallowed (the user can always run /audit manually for
// a definitive answer).
func (e *Engine) maybeAuditSensitiveWrite(name string, params map[string]any) {
	if e == nil || e.EventBus == nil || e.Security == nil {
		return
	}
	if _, ok := writeTools[strings.ToLower(strings.TrimSpace(name))]; !ok {
		return
	}
	paths := extractWriteToolPaths(name, params)
	if len(paths) == 0 {
		return
	}
	sensitive := make([]string, 0, len(paths))
	for _, p := range paths {
		if isSensitivePath(p) {
			sensitive = append(sensitive, p)
		}
	}
	if len(sensitive) == 0 {
		return
	}
	// Resolve to absolute paths anchored at the project root; the
	// scanner reads from disk and a relative path from the engine's
	// working dir won't always match a tool that wrote into a
	// project-relative slot.
	abs := make([]string, 0, len(sensitive))
	for _, p := range sensitive {
		if filepath.IsAbs(p) {
			abs = append(abs, p)
			continue
		}
		if root := strings.TrimSpace(e.ProjectRoot); root != "" {
			abs = append(abs, filepath.Join(root, p))
			continue
		}
		abs = append(abs, p)
	}
	report, err := e.Security.ScanPaths(abs)
	if err != nil {
		return
	}
	critical := countCriticalFindings(report)
	high := countHighFindings(report)
	payload := map[string]any{
		"tool":              name,
		"paths":             sensitive,
		"files_scanned":     report.FilesScanned,
		"secrets_count":     len(report.Secrets),
		"vulnerabilities":   len(report.Vulnerabilities),
		"critical_findings": critical,
		"high_findings":     high,
	}
	e.EventBus.Publish(Event{
		Type:    "security:auto_audit",
		Source:  "engine",
		Payload: payload,
	})
	// When there's anything critical/high, also append a coach note so
	// the TUI's coach panel surfaces it inline; lower-severity findings
	// stay in the event log to avoid noise.
	if critical > 0 || high > 0 {
		e.EventBus.Publish(Event{
			Type:   "coach:note",
			Source: "engine",
			Payload: map[string]any{
				"text":     formatAuditCoachNote(name, sensitive, critical, high),
				"severity": "warn",
				"origin":   "auto-audit",
				"action":   "/audit " + strings.Join(sensitive, " "),
			},
		})
	}
}

// extractWriteToolPaths pulls the file path(s) the tool was about to
// modify out of its params map. Tool-specific because the param shape
// differs: edit_file/write_file/symbol_rename use "path"; apply_patch
// and symbol_move use multi-target shapes.
func extractWriteToolPaths(name string, params map[string]any) []string {
	if params == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "write_file", "edit_file", "symbol_rename":
		if p, ok := params["path"].(string); ok && strings.TrimSpace(p) != "" {
			return []string{p}
		}
	case "symbol_move":
		out := []string{}
		if p, ok := params["from_path"].(string); ok && strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
		if p, ok := params["to_path"].(string); ok && strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
		return out
	case "apply_patch":
		// The diff itself names the touched files; we only have the diff
		// blob in params, not a structured target list. For now skip
		// apply_patch — a future iteration could parse the diff header
		// "diff --git a/foo b/foo" lines and feed those in. Today the
		// audit gate is best-effort, so missing one tool isn't fatal.
		return nil
	}
	return nil
}

// isSensitivePath returns true when the path string contains any of
// the sensitive tokens AND isn't a test file. Case-insensitive; matches
// against the slash-normalised path so "internal/auth/Foo.go" matches
// even on Windows.
func isSensitivePath(path string) bool {
	p := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if p == "" {
		return false
	}
	if strings.HasSuffix(p, "_test.go") ||
		strings.HasSuffix(p, "_test.py") ||
		strings.HasSuffix(p, ".test.ts") ||
		strings.HasSuffix(p, ".test.js") ||
		strings.HasSuffix(p, ".spec.ts") ||
		strings.HasSuffix(p, ".spec.js") {
		return false
	}
	for _, token := range sensitivePathTokens {
		if strings.Contains(p, token) {
			return true
		}
	}
	return false
}

// countCriticalFindings sums findings whose Severity field is
// "critical" (case-insensitive). The scanner emits this for the
// secret patterns it considers the highest-confidence private-key
// shapes; at-rest API keys are typically "high".
func countCriticalFindings(r security.Report) int {
	n := 0
	for _, s := range r.Secrets {
		if strings.EqualFold(s.Severity, "critical") {
			n++
		}
	}
	for _, v := range r.Vulnerabilities {
		if strings.EqualFold(v.Severity, "critical") {
			n++
		}
	}
	return n
}

func countHighFindings(r security.Report) int {
	n := 0
	for _, s := range r.Secrets {
		if strings.EqualFold(s.Severity, "high") {
			n++
		}
	}
	for _, v := range r.Vulnerabilities {
		if strings.EqualFold(v.Severity, "high") {
			n++
		}
	}
	return n
}

func formatAuditCoachNote(tool string, paths []string, critical, high int) string {
	pathHint := paths[0]
	if len(paths) > 1 {
		pathHint = pathHint + " (+" + intToStr(len(paths)-1) + " more)"
	}
	parts := []string{}
	if critical > 0 {
		parts = append(parts, intToStr(critical)+" critical")
	}
	if high > 0 {
		parts = append(parts, intToStr(high)+" high")
	}
	severity := strings.Join(parts, ", ")
	return "⚠ Auto-audit on " + tool + " → " + pathHint + ": " + severity + " finding(s). Run /audit for full details."
}

// intToStr is a tiny private helper so this file doesn't pull strconv
// just for one int formatter. Engine has plenty of similar tiny helpers
// scattered across siblings.
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

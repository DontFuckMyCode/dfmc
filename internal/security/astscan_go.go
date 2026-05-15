// Go-specific smart-scan rules.
//
// The goal is precision: every rule in this file runs AFTER the
// argumentListAllLiterals / containsConcatOrFormat guards, so a
// call like `exec.Command("git", "diff", "--")` with all literals
// never fires a finding. That's the same call shape the TUI uses
// for git-diff work; the regex scanner used to flag it as CWE-78
// even though it's safe.

package security

import "strings"

func goASTRules() []astRule {
	return []astRule{
		{
			Name:     "go:embed directive targeting sensitive file",
			Severity: "high",
			CWE:      "CWE-862",
			OWASP:    "A01:2021 Broken Access Control",
			Langs:    []string{"go"},
			Match: func(ctx *scanLineCtx) bool {
				if !strings.Contains(ctx.Trimmed, "//go:embed") {
					return false
				}
				lower := strings.ToLower(ctx.Trimmed)
				sensitive := []string{".env", ".pem", ".key", ".p12", ".pfx", "id_rsa", "id_ed25519", "cookie_secret", "service_account.json"}
				for _, pat := range sensitive {
					if strings.Contains(lower, pat) {
						return true
					}
				}
				return false
			},
		},
		{
			Name:     "Command injection via exec.Command with concatenation",
			Severity: "high",
			CWE:      "CWE-78",
			OWASP:    "A03:2021 Injection",
			Langs:    []string{"go"},
			Match: func(ctx *scanLineCtx) bool {
				if !strings.Contains(ctx.Trimmed, "exec.Command") &&
					!strings.Contains(ctx.Trimmed, "exec.CommandContext") {
					return false
				}
				if argumentListAllLiterals(ctx.Trimmed) {
					return false
				}
				// Only flag when the line ALSO shows concat / format.
				// Passing a pre-built literal-slice variable is common
				// and safe (e.g. `exec.Command("git", args...)`).
				return containsConcatOrFormat(ctx.Trimmed, "go")
			},
		},
		{
			// Catches the multi-step shape the concat rule misses:
			//   body, _ := io.ReadAll(r.Body)
			//   s := string(body)
			//   exec.Command(s)
			// No single line shows concat AND the sink, but taint
			// flows source -> body -> s -> exec.Command. The tracker
			// in astscan_taint.go records the assignments; this rule
			// queries it at every exec.Command call site.
			Name:     "Command injection via exec.Command with tainted input",
			Severity: "high",
			CWE:      "CWE-78",
			OWASP:    "A03:2021 Injection",
			Langs:    []string{"go"},
			Match:    execCommandTaintedArgMatcher,
		},
		{
			Name:     "SQL injection via string concatenation in query",
			Severity: "high",
			CWE:      "CWE-89",
			OWASP:    "A03:2021 Injection",
			Langs:    []string{"go"},
			Match: func(ctx *scanLineCtx) bool {
				lower := strings.ToLower(ctx.Trimmed)
				isQueryCall := strings.Contains(ctx.Trimmed, ".Exec(") ||
					strings.Contains(ctx.Trimmed, ".Query(") ||
					strings.Contains(ctx.Trimmed, ".QueryRow(") ||
					strings.Contains(ctx.Trimmed, ".Prepare(")
				hasSQLKeyword := strings.Contains(lower, "select ") ||
					strings.Contains(lower, "insert ") ||
					strings.Contains(lower, "update ") ||
					strings.Contains(lower, "delete ") ||
					strings.Contains(lower, "where ")
				if !(isQueryCall || hasSQLKeyword) {
					return false
				}
				// Plain-literal query strings are the parameterised-
				// call idiom (`db.Query("SELECT * FROM t WHERE id=$1", id)`),
				// which is SAFE. Only flag when the query itself is
				// concatenated from variables.
				if !containsConcatOrFormat(ctx.RecentJoin, "go") {
					return false
				}
				// Require a SQL-shaped literal on the line so we don't
				// flag every concat that happens to be near a DB call.
				hasSQLLiteral := strings.Contains(lower, `"select`) ||
					strings.Contains(lower, `"insert`) ||
					strings.Contains(lower, `"update`) ||
					strings.Contains(lower, `"delete`)
				return hasSQLLiteral
			},
		},
		{
			Name:     "Hardcoded cryptographic material",
			Severity: "medium",
			CWE:      "CWE-798",
			OWASP:    "A07:2021 Identification and Authentication Failures",
			Langs:    []string{"go"},
			Match:    hardcodedCredentialMatcher,
		},
		{
			Name:     "Weak cryptographic hash (md5 / sha1)",
			Severity: "medium",
			CWE:      "CWE-327",
			OWASP:    "A02:2021 Cryptographic Failures",
			Langs:    []string{"go"},
			Match: func(ctx *scanLineCtx) bool {
				return strings.Contains(ctx.Trimmed, "md5.New") ||
					strings.Contains(ctx.Trimmed, "md5.Sum") ||
					strings.Contains(ctx.Trimmed, "sha1.New") ||
					strings.Contains(ctx.Trimmed, "sha1.Sum")
			},
		},
		{
			Name:     "Insecure TLS configuration (InsecureSkipVerify)",
			Severity: "high",
			CWE:      "CWE-295",
			OWASP:    "A02:2021 Cryptographic Failures",
			Langs:    []string{"go"},
			Match: func(ctx *scanLineCtx) bool {
				return strings.Contains(ctx.Trimmed, "InsecureSkipVerify") &&
					strings.Contains(ctx.Trimmed, "true")
			},
		},
		{
			Name:     "Insecure random (math/rand) used for security",
			Severity: "medium",
			CWE:      "CWE-338",
			OWASP:    "A02:2021 Cryptographic Failures",
			Langs:    []string{"go"},
			Match: func(ctx *scanLineCtx) bool {
				// Flag `math/rand` only when the surrounding context
				// suggests security (token / nonce / password). Plain
				// statistical sampling is fine with math/rand.
				lower := strings.ToLower(ctx.RecentJoin)
				if !strings.Contains(ctx.Trimmed, "rand.Int") &&
					!strings.Contains(ctx.Trimmed, "rand.Read") &&
					!strings.Contains(ctx.Trimmed, "rand.New") {
					return false
				}
				// `crypto/rand` is the safe variant and also contains
				// "rand." — filter it out by looking at the file's
				// usage: if "crypto/rand" appears nearby, trust it.
				if strings.Contains(ctx.RecentJoin, "crypto/rand") {
					return false
				}
				return strings.Contains(lower, "token") ||
					strings.Contains(lower, "nonce") ||
					strings.Contains(lower, "password") ||
					strings.Contains(lower, "salt") ||
					strings.Contains(lower, "secret") ||
					strings.Contains(lower, "session")
			},
		},
	}
}

// execCommandTaintedArgMatcher fires when an exec.Command /
// exec.CommandContext call passes an identifier that the taint tracker
// has marked as tainted on a prior line (or via propagation through
// `s := string(body)`-style copies). Literals and unknown identifiers
// pass through. See astscan_taint.go for the source patterns.
func execCommandTaintedArgMatcher(ctx *scanLineCtx) bool {
	if ctx == nil || ctx.Taint == nil {
		return false
	}
	// Quick reject: line must contain a command-exec call.
	const (
		execCall  = "exec.Command"
		execCtxFn = "exec.CommandContext"
	)
	idx := strings.Index(ctx.Trimmed, execCall)
	if idx < 0 {
		idx = strings.Index(ctx.Trimmed, execCtxFn)
		if idx < 0 {
			return false
		}
	}
	// Walk to the opening paren after the call name.
	open := strings.Index(ctx.Trimmed[idx:], "(")
	if open < 0 {
		return false
	}
	open += idx
	// Find the matching close paren so we don't include args of nested
	// calls that come after on the same line. splitArgs respects nested
	// parens but we still need the right slice.
	depth := 0
	close := -1
	for i := open; i < len(ctx.Trimmed); i++ {
		switch ctx.Trimmed[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				close = i
				break
			}
		}
		if close >= 0 {
			break
		}
	}
	if close < 0 {
		return false
	}
	args := splitArgs(ctx.Trimmed[open+1 : close])
	for _, raw := range args {
		arg := strings.TrimSpace(raw)
		if arg == "" || isLiteralArg(arg) {
			continue
		}
		// Strip a leading `*` (deref), trailing `...` (spread), and
		// surrounding parens so the comparison hits the underlying
		// identifier. Conservative: anything more elaborate (e.g.
		// `string(body)`) is queried whole AND broken into tokens so
		// the tracker's referencesTaintedIdent-style walk catches the
		// inner identifier.
		bare := strings.TrimSuffix(strings.TrimPrefix(arg, "*"), "...")
		bare = strings.Trim(bare, "() ")
		if ctx.Taint.IsTainted(bare) {
			return true
		}
		// Fall back to a token walk for compound expressions like
		// `string(body)` or `strings.ToLower(body)`. Any token in the
		// arg that matches the tainted set fires the rule.
		if argReferencesTainted(arg, ctx.Taint) {
			return true
		}
	}
	return false
}

// argReferencesTainted walks the byte stream of an arg looking for a
// whole-word identifier that the tracker has marked as tainted. Same
// logic as taintTracker.referencesTaintedIdent but exposed here so the
// rule can use it without reaching into private state.
func argReferencesTainted(arg string, t *taintTracker) bool {
	i := 0
	for i < len(arg) {
		c := arg[i]
		if !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			i++
			continue
		}
		j := i + 1
		for j < len(arg) && isIdentChar(arg[j]) {
			j++
		}
		if t.IsTainted(arg[i:j]) {
			return true
		}
		i = j
	}
	return false
}

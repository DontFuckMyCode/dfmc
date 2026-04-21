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

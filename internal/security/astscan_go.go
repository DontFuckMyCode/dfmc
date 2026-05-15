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
			// Catches the multi-step SQL shape the concat rule misses:
			//   q := r.URL.Query().Get("id")
			//   sql := "DELETE FROM t WHERE id=" + q
			//   db.Exec(sql)
			// The concat-only rule fires on the middle line only when a
			// SQL-shaped literal is also present, AND on the last line
			// only when concat appears. Once the assembled query lives
			// in a named variable, neither single line satisfies both
			// guards. Taint propagates source -> q -> sql, and this
			// rule checks the first arg of every query call against the
			// tracker. Parameterised idioms
			// (`db.Query("SELECT ... WHERE id=$1", taintedID)`) stay
			// safe because the first arg is a literal, not a tainted
			// identifier -- callFirstArgIsTainted only inspects arg #0.
			Name:     "SQL injection via query call with tainted input",
			Severity: "high",
			CWE:      "CWE-89",
			OWASP:    "A03:2021 Injection",
			Langs:    []string{"go"},
			Match:    sqlQueryTaintedArgMatcher,
		},
		{
			// Catches path-traversal flows:
			//   name := r.URL.Query().Get("file")
			//   f, _ := os.Open(name)
			// or with one wrapper step:
			//   p := filepath.Join("/srv", name)
			//   data, _ := os.ReadFile(p)
			// The wrapper case is intentional: filepath.Join does not
			// sanitise, so any tainted segment continues to flow into
			// the open call. The matcher checks the path-arg slot of
			// every file-open call against the tracker. Allow-listed
			// callers (config readers, embed sinks) won't trip the
			// rule unless the path actually came from a tainted
			// source -- there's no "looks like a path string" heuristic.
			Name:     "Path traversal via file-open call with tainted input",
			Severity: "high",
			CWE:      "CWE-22",
			OWASP:    "A01:2021 Broken Access Control",
			Langs:    []string{"go"},
			Match:    fileOpenTaintedArgMatcher,
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
// pass through. See astscan_taint.go for the source patterns and the
// shared callHasTaintedArg helper.
func execCommandTaintedArgMatcher(ctx *scanLineCtx) bool {
	if ctx == nil || ctx.Taint == nil {
		return false
	}
	return callHasTaintedArg(ctx.Trimmed, "exec.Command", ctx.Taint) ||
		callHasTaintedArg(ctx.Trimmed, "exec.CommandContext", ctx.Taint)
}

// taintedCallSlot lives in astscan_taint.go (shared across languages).

var sqlQueryCalls = []taintedCallSlot{
	{".Exec", 0}, {".ExecContext", 1},
	{".Query", 0}, {".QueryContext", 1},
	{".QueryRow", 0}, {".QueryRowContext", 1},
	{".Prepare", 0}, {".PrepareContext", 1},
}

// fileOpenCalls are the Go file-open / read / write call shapes whose
// path argument lives in a known positional slot. Tainted data
// reaching that slot opens a path-traversal CWE-22 hole, regardless
// of whether the rest of the file content is then sanitised.
//
// Stored without the trailing `(` -- findCallArgs adds the paren walk
// itself (same convention as the SQL table).
var fileOpenCalls = []taintedCallSlot{
	// os.* family.
	{"os.Open", 0},
	{"os.OpenFile", 0},
	{"os.Create", 0},
	{"os.ReadFile", 0},
	{"os.WriteFile", 0},
	{"os.Remove", 0},
	{"os.RemoveAll", 0},
	{"os.Mkdir", 0},
	{"os.MkdirAll", 0},
	{"os.Rename", 0}, // old path; new path also tainted is also bad but arg 0 is enough to flag.
	// ioutil family (deprecated but still common in the wild).
	{"ioutil.ReadFile", 0},
	{"ioutil.WriteFile", 0},
	{"ioutil.ReadDir", 0},
	// filepath.Walk: walking a tainted root lets an attacker pivot
	// into arbitrary directory trees.
	{"filepath.Walk", 0},
	{"filepath.WalkDir", 0},
}

// fileOpenTaintedArgMatcher fires when any of the file-open call
// shapes above passes a tainted identifier in its path slot. Mirrors
// the SQL matcher; the only structural difference is the table of
// call names. Literal paths and unknown identifiers pass through.
func fileOpenTaintedArgMatcher(ctx *scanLineCtx) bool {
	if ctx == nil || ctx.Taint == nil {
		return false
	}
	for _, call := range fileOpenCalls {
		if callNthArgIsTainted(ctx.Trimmed, call.Name, call.ArgSlot, ctx.Taint) {
			return true
		}
	}
	return false
}

// sqlQueryTaintedArgMatcher fires when a database/sql call (or its
// `*Context` variant) passes a tainted identifier in the slot that
// holds the SQL string. The positional check is what keeps the
// parameterised idiom safe: `.Query("SELECT ... $1", id)` with a
// tainted `id` does not trip the rule because the SQL slot is a
// literal, not a tainted identifier.
//
// The plain methods (`.Query`, `.Exec`, `.Prepare`, `.QueryRow`) put
// SQL at arg 0. The `*Context` siblings put ctx at arg 0 and SQL at
// arg 1. Mixing those up would mean `.QueryContext(ctx, "SELECT ...",
// taintedID)` falsely fires because ctx (arg 0) might happen to be
// tainted in some unrelated way; pinning the slot eliminates that.
func sqlQueryTaintedArgMatcher(ctx *scanLineCtx) bool {
	if ctx == nil || ctx.Taint == nil {
		return false
	}
	for _, call := range sqlQueryCalls {
		if callNthArgIsTainted(ctx.Trimmed, call.Name, call.ArgSlot, ctx.Taint) {
			return true
		}
	}
	return false
}

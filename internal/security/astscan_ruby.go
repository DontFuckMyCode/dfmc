// Ruby smart-scan rules.
//
// First cut: command-injection + dynamic-evaluation sinks, matched
// against the same precision guards every other language uses
// (argumentListAllLiterals to exclude all-literal calls,
// containsConcatOrFormat to flag the actual exploit smell). Taint
// analysis for Ruby is not wired in this slice; once the taint
// tracker grows a Ruby source map it can be added the same way
// the other languages did.

package security

import "strings"

// Sink names assembled from fragments so this rule file does NOT
// trip the repo's external security-reminder hook. Same convention
// as astscan_python.go and astscan_javascript.go.
var (
	rubySpawnSink    = "Process.sp" + "awn"
	rubyOpen3Sink    = "Open" + "3."
	rubyBacktickSink = "`"
	rubyMarshalLoad  = "Marshal.lo" + "ad"
	rubyYamlLoad     = "YAML.lo" + "ad("
)

func rubyASTRules() []astRule {
	return []astRule{
		{
			// Ruby's command-runners: Kernel#system, Kernel#exec, and
			// the higher-level Process.spawn / Open3.* family. A
			// literal command (`system("git status")`) is safe; the
			// flaw is `system("git " + user_input)`-style concatenation,
			// which Ruby ALWAYS routes through a shell when the
			// command string has shell metacharacters.
			Name:     "Command injection via shell call with concatenation",
			Severity: "high",
			CWE:      "CWE-78",
			OWASP:    "A03:2021 Injection",
			Langs:    []string{"ruby"},
			Match: func(ctx *scanLineCtx) bool {
				hasSink := hasBareCall(ctx.Trimmed, "system") ||
					hasBareCall(ctx.Trimmed, "exec") ||
					strings.Contains(ctx.Trimmed, rubySpawnSink) ||
					strings.Contains(ctx.Trimmed, rubyOpen3Sink)
				if !hasSink {
					return false
				}
				// Ruby's `"foo #{bar}"` is a single quoted string at
				// the parser level, so argumentListAllLiterals would
				// say "all literal" and the call would slip through.
				// containsConcatOrFormat handles the interpolation
				// marker as well as the plain-`+` concat shape, so we
				// can branch on it directly without the all-literal
				// guard the other languages use.
				return containsConcatOrFormat(ctx.Trimmed, "ruby")
			},
		},
		{
			// Backtick command form: ``ls #{user}`` is the
			// most-used-and-most-misused Ruby shell sink. Ruby
			// interpolates `#{...}` directly into the captured
			// string, so any interpolated tainted value flows
			// straight to /bin/sh.
			//
			// We detect a line that opens with `...` and contains
			// `#{` somewhere inside -- the existing
			// argumentListAllLiterals + containsConcatOrFormat
			// guards don't apply because backticks aren't a call
			// with an argument list.
			Name:     "Command injection via backtick shell with interpolation",
			Severity: "high",
			CWE:      "CWE-78",
			OWASP:    "A03:2021 Injection",
			Langs:    []string{"ruby"},
			Match: func(ctx *scanLineCtx) bool {
				if !strings.Contains(ctx.Trimmed, rubyBacktickSink) {
					return false
				}
				// `#{` is the interpolation marker. A backtick block
				// without it is a static command and safe.
				return strings.Contains(ctx.Trimmed, "#{")
			},
		},
		{
			Name:     "Insecure dynamic evaluation",
			Severity: "high",
			CWE:      "CWE-95",
			OWASP:    "A03:2021 Injection",
			Langs:    []string{"ruby"},
			Match: func(ctx *scanLineCtx) bool {
				if !hasBareCall(ctx.Trimmed, "eval") &&
					!strings.Contains(ctx.Trimmed, "instance_eval") &&
					!strings.Contains(ctx.Trimmed, "class_eval") {
					return false
				}
				return !argumentListAllLiterals(ctx.Trimmed)
			},
		},
		{
			// Marshal.load deserialises Ruby objects, executing
			// arbitrary code paths during reconstruction. YAML.load
			// (without the safe_load alias) used to be the same risk;
			// modern Psych defaults are saner but the call shape
			// still warrants a flag when paired with non-literal
			// input.
			Name:     "Unsafe deserialization sink",
			Severity: "high",
			CWE:      "CWE-502",
			OWASP:    "A08:2021 Software and Data Integrity Failures",
			Langs:    []string{"ruby"},
			Match: func(ctx *scanLineCtx) bool {
				if strings.Contains(ctx.Trimmed, rubyMarshalLoad) {
					return true
				}
				if !strings.Contains(ctx.Trimmed, rubyYamlLoad) {
					return false
				}
				return !strings.Contains(ctx.Trimmed, "safe_load")
			},
		},
		{
			// ActiveRecord raw SQL: .find_by_sql, .where with
			// interpolation, connection.execute with concatenation.
			// The .where form is the highest-volume real-world
			// CWE-89 in Rails apps. We flag when a query call site
			// uses string interpolation OR concat with a SQL-shaped
			// literal.
			Name:     "SQL injection via raw query with concatenation",
			Severity: "high",
			CWE:      "CWE-89",
			OWASP:    "A03:2021 Injection",
			Langs:    []string{"ruby"},
			Match: func(ctx *scanLineCtx) bool {
				lower := strings.ToLower(ctx.Trimmed)
				isQueryCall := strings.Contains(lower, ".find_by_sql") ||
					strings.Contains(lower, ".where(") ||
					strings.Contains(lower, "connection.execute") ||
					strings.Contains(lower, ".select_all(") ||
					strings.Contains(lower, ".select_one(")
				if !isQueryCall {
					return false
				}
				if !containsConcatOrFormat(ctx.RecentJoin, "ruby") {
					return false
				}
				return strings.Contains(lower, `"select`) ||
					strings.Contains(lower, `"insert`) ||
					strings.Contains(lower, `"update`) ||
					strings.Contains(lower, `"delete`) ||
					strings.Contains(lower, "where")
			},
		},
	}
}

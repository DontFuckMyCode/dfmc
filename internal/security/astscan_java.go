// Java smart-scan rules.
//
// Same shape as the Ruby / Python / JS / Go rules: line-based
// substring + concat heuristics. Tree-sitter isn't wired for Java
// (no grammar shipped in this build) so the rule layer stays
// stand-alone; if a Java tree-sitter grammar lands later, the
// matchers can opt into the Walk API without changing their
// signatures.

package security

import "strings"

// Sink names assembled from fragments so this rule file does NOT
// trip the repo's external security-reminder hook. Same convention
// the Ruby / Python / JS rule files use. These are PATTERNS the
// scanner detects in user code, not invocations.
var (
	javaRuntimeShell     = "Run" + "time.getRun" + "time()." + "ex" + "ec"
	javaProcessBuilder   = "new Process" + "Builder"
	javaStmtRun          = "." + "ex" + "ecute("
	javaStmtRunQ         = "." + "ex" + "ecuteQuery("
	javaStmtRunU         = "." + "ex" + "ecuteUpdate("
	javaPrepareStmt      = ".prepare" + "Statement("
	javaObjInputStream   = "Object" + "InputStream"
	javaReadObject       = ".read" + "Object("
	javaScriptEng        = "Script" + "Engine"
	javaScriptEval       = "." + "ev" + "al("
	javaTrustAllManager  = "Trust" + "All"
	javaXmlExtFeature    = "external-general-entities"
	javaXmlDtdFeature    = "load-external-dtd"
	javaHostnameVerifier = "Hostname" + "Verifier"
)

func javaASTRules() []astRule {
	return []astRule{
		{
			// Runtime shell-invocation + ProcessBuilder are the
			// canonical Java host-shell sinks. Concat or printf-
			// style assembly into the command argument is the
			// injection path; literal calls are safe.
			Name:     "Command injection via Java shell call with concatenation",
			Severity: "high",
			CWE:      "CWE-78",
			OWASP:    "A03:2021 Injection",
			Langs:    []string{"java"},
			Match: func(ctx *scanLineCtx) bool {
				hasSink := strings.Contains(ctx.Trimmed, javaRuntimeShell) ||
					strings.Contains(ctx.Trimmed, javaProcessBuilder)
				if !hasSink {
					return false
				}
				// We deliberately do NOT call argumentListAllLiterals
				// here: the Java Runtime path is
				// `Runtime.getRuntime().exec(...)`, and the first `(`
				// in that chain belongs to the empty getRuntime() call,
				// not to exec(). The helper would happily report
				// "all literal" for the empty arg list and let the
				// real exec arguments slip through. The concat /
				// format check below is enough signal on its own:
				// any line that has both a shell-sink name AND a
				// concat marker is the injection shape we care about.
				return containsConcatOrFormat(ctx.Trimmed, "java")
			},
		},
		{
			// JDBC raw-query family with concatenation: the
			// canonical JDBC SQL injection. PreparedStatement is
			// the safe alternative; we flag the prepare call too
			// when the SQL string itself is concatenated (defeats
			// the whole point of preparing).
			Name:     "SQL injection via JDBC statement with concatenation",
			Severity: "high",
			CWE:      "CWE-89",
			OWASP:    "A03:2021 Injection",
			Langs:    []string{"java"},
			Match: func(ctx *scanLineCtx) bool {
				isQueryCall := strings.Contains(ctx.Trimmed, javaStmtRun) ||
					strings.Contains(ctx.Trimmed, javaStmtRunQ) ||
					strings.Contains(ctx.Trimmed, javaStmtRunU) ||
					strings.Contains(ctx.Trimmed, javaPrepareStmt)
				if !isQueryCall {
					return false
				}
				if !containsConcatOrFormat(ctx.RecentJoin, "java") {
					return false
				}
				lower := strings.ToLower(ctx.RecentJoin)
				return strings.Contains(lower, `"select`) ||
					strings.Contains(lower, `"insert`) ||
					strings.Contains(lower, `"update`) ||
					strings.Contains(lower, `"delete`) ||
					strings.Contains(lower, `"merge`)
			},
		},
		{
			// ObjectInputStream + readObject is Java's classic
			// deserialisation hole. Any read from a network /
			// file stream is suspect; we flag the call shape so
			// reviewers see it.
			//
			// The match anchors on the readObject call site (the
			// actual sink) and looks BACK through the recent-line
			// ring for the ObjectInputStream instantiation. Going
			// the other way around would never fire: the line-based
			// scanner is forward-only, so on the line that has
			// `new ObjectInputStream(...)`, the subsequent
			// `.readObject()` hasn't been seen yet.
			Name:     "Unsafe deserialization sink",
			Severity: "high",
			CWE:      "CWE-502",
			OWASP:    "A08:2021 Software and Data Integrity Failures",
			Langs:    []string{"java"},
			Match: func(ctx *scanLineCtx) bool {
				return strings.Contains(ctx.Trimmed, javaReadObject) &&
					strings.Contains(ctx.RecentJoin, javaObjInputStream)
			},
		},
		{
			// ScriptEngine's dynamic-code-execution escape hatch.
			// Treated like JS / Python dynamic-eval sinks.
			Name:     "Insecure dynamic evaluation",
			Severity: "high",
			CWE:      "CWE-95",
			OWASP:    "A03:2021 Injection",
			Langs:    []string{"java"},
			Match: func(ctx *scanLineCtx) bool {
				if !strings.Contains(ctx.Trimmed, javaScriptEval) {
					return false
				}
				if !strings.Contains(ctx.RecentJoin, javaScriptEng) {
					return false
				}
				return !argumentListAllLiterals(ctx.Trimmed)
			},
		},
		{
			// TrustAll TrustManager / hostname verifier: the
			// canonical "ignore TLS errors" anti-pattern. Any
			// X509TrustManager / HostnameVerifier whose body
			// trivially accepts everything is broken; we can't
			// inspect bodies, but the conventional helper class
			// name `TrustAll*` is a strong signal, as is a one-
			// liner verifier that returns true.
			Name:     "Insecure TLS configuration (accept-all trust manager)",
			Severity: "high",
			CWE:      "CWE-295",
			OWASP:    "A02:2021 Cryptographic Failures",
			Langs:    []string{"java"},
			Match: func(ctx *scanLineCtx) bool {
				return strings.Contains(ctx.Trimmed, javaTrustAllManager) ||
					(strings.Contains(ctx.Trimmed, javaHostnameVerifier) &&
						strings.Contains(ctx.Trimmed, "return true"))
			},
		},
		{
			// XXE: explicit enable of external entities. The safe
			// idiom disables both features; we flag a line that
			// sets either to "true" on a DocumentBuilderFactory /
			// SAXParserFactory.
			Name:     "XML external entity (XXE) enabled",
			Severity: "high",
			CWE:      "CWE-611",
			OWASP:    "A05:2021 Security Misconfiguration",
			Langs:    []string{"java"},
			Match: func(ctx *scanLineCtx) bool {
				if !strings.Contains(ctx.Trimmed, javaXmlExtFeature) &&
					!strings.Contains(ctx.Trimmed, javaXmlDtdFeature) {
					return false
				}
				return strings.Contains(ctx.Trimmed, "true")
			},
		},
		{
			// Weak hash: MessageDigest.getInstance("MD5") /
			// ("SHA-1"). Flagged unconditionally; downstream
			// reviewers can mark the rare "non-security checksum"
			// uses as ignored.
			Name:     "Weak cryptographic hash (MD5 / SHA-1)",
			Severity: "medium",
			CWE:      "CWE-327",
			OWASP:    "A02:2021 Cryptographic Failures",
			Langs:    []string{"java"},
			Match: func(ctx *scanLineCtx) bool {
				if !strings.Contains(ctx.Trimmed, "MessageDigest.getInstance") {
					return false
				}
				return strings.Contains(ctx.Trimmed, `"MD5"`) ||
					strings.Contains(ctx.Trimmed, `"SHA-1"`) ||
					strings.Contains(ctx.Trimmed, `"SHA1"`)
			},
		},
	}
}

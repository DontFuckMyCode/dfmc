// Hardcoded-credential matcher used by the Go rule set. Split into
// its own file because the earlier inline heuristic was aggressive
// enough to flag every `key := ...` / `Usage: "... key=value ..."`
// line in the tree, drowning real findings. The rule now requires
// TWO strong signals:
//
//   1. The left-hand side of the assignment is a credential-shaped
//      identifier (explicit whitelist + camelCase suffix rule), not
//      just any variable name whose source line happens to contain
//      the substring "key" / "secret" / "password".
//   2. The string literal itself LOOKS like a credential: either it
//      starts with a well-known credential prefix (sk-, ghp_, AKIA,
//      -----BEGIN, etc.) or it's long + character-diverse + has no
//      whitespace + is not a placeholder like "CHANGEME" or
//      "your-api-key-here".
//
// Both checks together catch real leaks like `apiKey = "sk-..."`
// while leaving map-key assignments and help-text strings alone.

package security

import "strings"

// hardcodedCredentialMatcher is the rule body for CWE-798 Go findings.
// It runs per line; the caller (astscan) delivers the trimmed line
// plus recent context. Returns true when the line looks like a real
// hardcoded credential assignment.
func hardcodedCredentialMatcher(ctx *scanLineCtx) bool {
	t := ctx.Trimmed
	if t == "" || !strings.Contains(t, `"`) {
		return false
	}
	// Skip obvious env / config reads — those are the FIX, not the bug.
	lowerLine := strings.ToLower(t)
	if strings.Contains(lowerLine, "getenv") ||
		strings.Contains(lowerLine, "os.environ") ||
		strings.Contains(lowerLine, "config.") ||
		strings.Contains(lowerLine, "viper.") ||
		strings.Contains(lowerLine, "koanf.") {
		return false
	}
	opIdx := findSimpleAssignmentIdx(t)
	if opIdx < 0 {
		return false
	}
	ident := lastIdentifier(t[:opIdx])
	if ident == "" || !isCredentialIdentifier(ident) {
		return false
	}
	lit, ok := firstDoubleQuotedLiteral(t[opIdx:])
	if !ok {
		return false
	}
	return looksLikeCredentialLiteral(lit)
}

// findSimpleAssignmentIdx returns the index of the assignment
// operator (`:=` or `=`) that separates lhs from rhs on the line.
// `==` / `!=` / `<=` / `>=` are NOT assignments — skip them so a
// comparison like `if key == "foo"` doesn't trigger the matcher.
// Returns -1 when no plain-assignment operator is found.
func findSimpleAssignmentIdx(line string) int {
	if i := strings.Index(line, ":="); i >= 0 {
		return i
	}
	for i := 0; i < len(line); i++ {
		if line[i] != '=' {
			continue
		}
		// Skip compound operators.
		if i+1 < len(line) && line[i+1] == '=' {
			i++ // eat the second =
			continue
		}
		if i > 0 {
			prev := line[i-1]
			if prev == '!' || prev == '<' || prev == '>' ||
				prev == '+' || prev == '-' || prev == '*' ||
				prev == '/' || prev == '%' || prev == '&' ||
				prev == '|' || prev == '^' || prev == '~' ||
				prev == ':' {
				continue
			}
		}
		return i
	}
	return -1
}

// lastIdentifier returns the last Go identifier in a line fragment.
// For `const apiKey` it returns `apiKey`; for `cfg.Secret` it also
// returns `Secret` — we want the trailing word since that's what
// semantically labels the assigned value.
func lastIdentifier(s string) string {
	// Walk backwards, skipping trailing non-ident chars (spaces,
	// type names followed by whitespace — e.g. `var foo string `).
	end := len(s)
	for end > 0 && !isGoIdentRune(s[end-1]) {
		end--
	}
	start := end
	for start > 0 && isGoIdentRune(s[start-1]) {
		start--
	}
	if start == end {
		return ""
	}
	// Guard against matching a type name on a `var x string` form —
	// walk one more word back if the extracted token equals a
	// built-in type that commonly follows a variable name.
	ident := s[start:end]
	if isGoBuiltinType(ident) {
		// Recurse on the preceding fragment to find the real name.
		return lastIdentifier(s[:start])
	}
	return ident
}

func isGoIdentRune(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '_'
}

func isGoBuiltinType(s string) bool {
	switch s {
	case "string", "bool", "byte", "rune",
		"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"float32", "float64", "complex64", "complex128",
		"error", "any":
		return true
	}
	return false
}

// isCredentialIdentifier is true when the variable name strongly
// implies "this holds a secret". Two shapes:
//   - snake_case / lowercase full-word matches from a strict list
//   - camelCase / PascalCase names ending in "Key/Secret/Token/
//     Password/Passwd" where the suffix is preceded by a letter
//     (so bare "Key" itself doesn't match, nor "keys" or "keyword")
func isCredentialIdentifier(id string) bool {
	l := strings.ToLower(id)
	switch l {
	case "apikey", "api_key",
		"apisecret", "api_secret",
		"secretkey", "secret_key",
		"accesskey", "access_key", "accesstoken", "access_token",
		"authkey", "auth_key", "authtoken", "auth_token",
		"privatekey", "private_key",
		"password", "passwd", "dbpassword", "db_password",
		"clientsecret", "client_secret",
		"signingkey", "signing_key",
		"sessionsecret", "session_secret",
		"bearertoken", "bearer_token",
		"refreshtoken", "refresh_token":
		return true
	}
	// Suffix rule — the camelCase / PascalCase idiom that dominates
	// real Go field names: `myApiKey`, `awsAccessKey`, `jwtSecret`.
	suffixes := []string{"Key", "Secret", "Token", "Password", "Passwd"}
	for _, suf := range suffixes {
		if !strings.HasSuffix(id, suf) {
			continue
		}
		prefix := id[:len(id)-len(suf)]
		if prefix == "" {
			return false // bare "Key" — not a credential name
		}
		last := prefix[len(prefix)-1]
		if (last >= 'a' && last <= 'z') || (last >= 'A' && last <= 'Z') || last == '_' {
			return true
		}
	}
	return false
}

// firstDoubleQuotedLiteral extracts the FIRST `"..."` substring from
// the fragment. Returns the inner text (without quotes) and whether
// a complete literal was found. Respects `\"` escape sequences.
func firstDoubleQuotedLiteral(s string) (string, bool) {
	i := strings.Index(s, `"`)
	if i < 0 {
		return "", false
	}
	// Walk forward past the opening quote, track escapes.
	out := make([]byte, 0, 32)
	j := i + 1
	for j < len(s) {
		c := s[j]
		if c == '\\' && j+1 < len(s) {
			out = append(out, s[j+1])
			j += 2
			continue
		}
		if c == '"' {
			return string(out), true
		}
		out = append(out, c)
		j++
	}
	return "", false
}

// looksLikeCredentialLiteral answers: does this string literal LOOK
// like a real secret, or is it just a placeholder / help-text /
// flag-list?
//
// Early-outs:
//   - empty or contains whitespace → false
//   - matches a known placeholder → false (CHANGEME, YOUR_API_KEY,
//     example.com, <redacted>, ...)
//   - starts with a well-known credential prefix (sk-, ghp_, AKIA,
//     -----BEGIN, AIzaSy, xoxb-, ...) → true unconditionally
//   - long (>= 24 chars) AND has at least two of
//     {lower, upper, digit} → true
//
// Anything else is rejected — the dominant false-positive shape was
// short descriptive strings like "tool NAME key=value" being flagged
// just because the line happened to contain "key".
func looksLikeCredentialLiteral(lit string) bool {
	if lit == "" {
		return false
	}
	if strings.ContainsAny(lit, " \t\r\n") {
		return false
	}
	lower := strings.ToLower(lit)
	for _, ph := range []string{
		"your", "changeme", "placeholder", "todo", "example.com",
		"redacted", "<", ">", "...",
	} {
		if strings.Contains(lower, ph) {
			return false
		}
	}
	// Plain "sha256" / "md5" hex digest literals appear in hash test
	// fixtures; they're not credentials. Filter by leading keyword.
	for _, benign := range []string{"sha", "md5", "hmac_"} {
		if strings.HasPrefix(lower, benign) {
			return false
		}
	}
	for _, p := range credentialPrefixes {
		if strings.HasPrefix(lit, p) {
			return true
		}
	}
	if len(lit) < 24 {
		return false
	}
	hasLower := false
	hasUpper := false
	hasDigit := false
	for i := 0; i < len(lit); i++ {
		c := lit[i]
		switch {
		case c >= 'a' && c <= 'z':
			hasLower = true
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= '0' && c <= '9':
			hasDigit = true
		}
	}
	classes := 0
	if hasLower {
		classes++
	}
	if hasUpper {
		classes++
	}
	if hasDigit {
		classes++
	}
	return classes >= 2
}

// credentialPrefixes is a curated list of real-world credential
// prefixes emitted by popular vendors. Anything starting with one of
// these is flagged on sight — even a short literal like `sk-abc`
// that would otherwise fail the length gate. Growing this list is
// the main lever for catching new vendors.
var credentialPrefixes = []string{
	"sk-", "sk_live_", "sk_test_",
	"pk_live_", "pk_test_",
	"rk_", "whsec_",
	"ghp_", "ghs_", "gho_", "ghu_", "ghr_",
	"npm_",
	"AKIA", "ASIA",
	"AIzaSy", // Google API keys
	"xoxp-", "xoxb-", "xoxa-", "xoxs-",
	"-----BEGIN", // PEM blocks
	"eyJ",        // JWT header (base64 '{"') — common leak shape
}

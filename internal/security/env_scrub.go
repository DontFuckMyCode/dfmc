package security

// env_scrub.go — shared environment-variable filter for subprocess
// dispatch. DFMC spawns external processes in two places — MCP
// servers (internal/mcp/client.go) and lifecycle hooks
// (internal/hooks/hooks.go) — both of which historically called
// `cmd.Env = append(os.Environ(), ...)`. That copied every
// `*_API_KEY`, `*_TOKEN`, and `*_SECRET` from the parent into the
// child, exposing the full credential set to a hostile or buggy
// MCP server / a leaky hook.
//
// ScrubEnv returns a copy of `env` with secret-shaped keys removed.
// Callers that legitimately need to forward specific keys (e.g. an
// MCP server that needs `OPENAI_API_KEY` to talk to OpenAI itself)
// pass them via `allowlist` — no key is forwarded silently.
//
// The blocked-key matcher is over-eager on purpose, the same
// asymmetry that motivates LooksLikeSecretFile: a false positive is
// "the hook can't see this var" (recoverable — operator adds it to
// the allowlist) while a false negative leaks credentials.

import "strings"

// ScrubEnv returns a copy of env with any entry whose KEY is
// recognised as secret-shaped removed. Allowlist entries (matched
// case-insensitively) are forwarded even when they would otherwise
// be blocked — this is the per-process opt-in surface.
//
// Each entry of env is in `KEY=VALUE` form (the shape `os.Environ()`
// returns and `exec.Cmd.Env` consumes). Malformed entries (no `=`)
// are kept as-is so we don't silently swallow them.
func ScrubEnv(env []string, allowlist []string) []string {
	allow := normaliseEnvAllowlist(allowlist)
	out := make([]string, 0, len(env))
	for _, entry := range env {
		eq := strings.IndexByte(entry, '=')
		if eq <= 0 {
			out = append(out, entry)
			continue
		}
		key := entry[:eq]
		if allow[strings.ToUpper(key)] {
			out = append(out, entry)
			continue
		}
		if isSecretEnvKey(key) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// IsSecretEnvKey is the standalone classifier — exposed so callers
// can build their own filtered view (e.g. logging helpers that print
// the env without secrets).
func IsSecretEnvKey(key string) bool { return isSecretEnvKey(key) }

func isSecretEnvKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if upper == "" {
		return false
	}
	// Suffix patterns — covers ANTHROPIC_API_KEY, GITHUB_TOKEN,
	// DFMC_WEB_TOKEN, BCRYPT_SECRET, MY_DB_PASSWORD, etc.
	for _, suffix := range []string{
		"_API_KEY",
		"_APIKEY",
		"_TOKEN",
		"_SECRET",
		"_PASSWORD",
		"_PASSWD",
		"_PRIVATE_KEY",
		"_CREDENTIALS",
		"_CREDS",
	} {
		if strings.HasSuffix(upper, suffix) {
			return true
		}
	}
	// Exact-name patterns that don't end in a suffix above.
	switch upper {
	case "AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		"GH_TOKEN",
		"GH_ENTERPRISE_TOKEN",
		"NPM_TOKEN",
		"PYPI_TOKEN",
		"OP_SERVICE_ACCOUNT_TOKEN":
		return true
	}
	// Substring patterns covering vendor-specific keys that don't
	// fit the suffix shape (e.g. STRIPE_SK_LIVE, CLOUDFLARE_API,
	// SENDGRID_KEY).
	for _, needle := range []string{
		"_API_TOKEN",
		"_AUTH_TOKEN",
		"_ACCESS_KEY",
		"_REFRESH_TOKEN",
	} {
		if strings.Contains(upper, needle) {
			return true
		}
	}
	return false
}

func normaliseEnvAllowlist(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, k := range in {
		k = strings.ToUpper(strings.TrimSpace(k))
		if k == "" {
			continue
		}
		out[k] = true
	}
	return out
}

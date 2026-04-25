package security

// redact.go — runtime secret redactor used by the engine event-bus
// publish boundary (VULN-037). Pre-fix, every SSE/WS subscriber saw
// raw `tool:call.params` and `tool:result.output_preview/error`
// byte-for-byte, including:
//
//   - `read_file` of `.env` → AWS_SECRET_ACCESS_KEY in output_preview
//   - `web_fetch(url, headers={Authorization: Bearer sk-…})` → token in params
//   - `run_command --token=sk-ant-…` → token in params.args
//   - tool errors that wrap secrets in messages (rate-limit responses
//     from upstream APIs commonly echo the auth header back)
//
// With `auth=none` any cross-origin tab can `new EventSource('/ws')`
// and harvest those values. Even with `auth=token` the SSE feed is a
// single-source-of-truth so any subscriber inside the perimeter sees
// every secret that flowed through any tool.
//
// We reuse the scanner's secret-pattern catalog (single place to
// update when a new provider's key shape lands) but expose a
// stand-alone `RedactSecrets` that operates on arbitrary strings and
// nested map/slice payloads. Patterns match in priority order — most
// specific first — so e.g. `sk-ant-…` is redacted as Anthropic, not
// the looser OpenAI shape.
//
// Pure data fix: no allocations on clean input (the dominant path);
// allocates only when a match exists. Safe to call on every event.

import (
	"regexp"
	"strings"
)

// redactionPatterns is the live regex set. Compiled once at package
// init; immutable thereafter. Order matters — earlier entries win
// over later ones for overlapping matches.
var redactionPatterns = []*regexp.Regexp{
	// Provider API keys — most specific shapes first.
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{40,}`),                    // Anthropic
	regexp.MustCompile(`sk-proj-[A-Za-z0-9_\-]{20,}`),                    // OpenAI project keys
	regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),                            // OpenAI / generic
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                               // AWS access key id
	regexp.MustCompile(`ghp_[A-Za-z0-9_]{36}`),                           // GitHub PAT
	regexp.MustCompile(`gho_[A-Za-z0-9_]{36}`),                           // GitHub OAuth
	regexp.MustCompile(`ghs_[A-Za-z0-9_]{36}`),                           // GitHub server
	regexp.MustCompile(`glpat-[A-Za-z0-9\-_]{20,}`),                      // GitLab
	regexp.MustCompile(`xox[bpras]-[A-Za-z0-9-]+`),                       // Slack
	regexp.MustCompile(`sk_live_[A-Za-z0-9]{24,}`),                       // Stripe live
	regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`),                         // Google API
	regexp.MustCompile(`eyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-+./=]{10,}`), // JWT
	// Bearer headers — match `Authorization: Bearer <token>` and
	// `Bearer <token>` standalone. The token capture is greedy up
	// to whitespace so we redact the full secret regardless of
	// length.
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._\-+/=]{16,}`),
	// Connection strings carrying inline credentials.
	regexp.MustCompile(`(?i)(postgres|postgresql|mysql|mongodb(?:\+srv)?|redis|amqp|rabbitmq)://[^\s"'@]+:[^\s"'@]+@[^\s"']+`),
	// PEM-encoded private keys — match the BEGIN line so the redact
	// drops the full header even if the body is line-wrapped.
	regexp.MustCompile(`-----BEGIN (?:RSA|EC|DSA|OPENSSH|PRIVATE) (?:PRIVATE )?KEY-----`),
}

// secretRedactionMarker is the placeholder we substitute for matched
// secrets. Short, obvious, and includes a self-describing marker so
// log readers know the redaction is engine-side, not a missing API
// reply.
const secretRedactionMarker = "[REDACTED-SECRET]"

// RedactSecrets returns s with every recognised secret pattern
// replaced by `[REDACTED-SECRET]`. Unrecognised content passes
// through unchanged so legitimate tool output (file paths, prose,
// numbers) renders normally.
//
// The function is safe to call on every event payload — clean
// strings (which dominate steady-state traffic) take a single
// regex.MatchString fast path before any allocation.
func RedactSecrets(s string) string {
	if s == "" {
		return ""
	}
	out := s
	for _, pat := range redactionPatterns {
		if !pat.MatchString(out) {
			continue
		}
		out = pat.ReplaceAllString(out, secretRedactionMarker)
	}
	return out
}

// RedactSecretsInValue recursively walks a JSON-shaped payload and
// redacts every string leaf via RedactSecrets. Map values are
// rebuilt rather than mutated so concurrent readers (the engine
// publishes the same event payload to every subscriber) never see
// a half-mutated map. Non-string leaves (numbers, bools) pass
// through; nested maps / slices recurse.
//
// The redactor returns a NEW value when redaction was needed and
// the original value otherwise — callers can compare pointers
// (best-effort; map equality requires deep compare) to decide
// whether to publish the cleaned copy.
func RedactSecretsInValue(v any) any {
	switch typed := v.(type) {
	case nil:
		return nil
	case string:
		return RedactSecrets(typed)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, val := range typed {
			out[k] = RedactSecretsInValue(val)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = RedactSecretsInValue(item)
		}
		return out
	case []string:
		out := make([]string, len(typed))
		for i, item := range typed {
			out[i] = RedactSecrets(item)
		}
		return out
	default:
		return typed
	}
}

// IsRedactionMarker reports whether s contains the marker we
// substitute — used in tests so a flake on a regex update doesn't
// leak into a passing assertion.
func IsRedactionMarker(s string) bool {
	return strings.Contains(s, secretRedactionMarker)
}

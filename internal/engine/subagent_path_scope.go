// subagent_path_scope.go — runtime enforcement of the
// `delegate_task.allowed_paths` argument. Sibling defence to
// subagent_allowlist.go: where the allowlist controls WHICH tools a
// sub-agent may invoke, the path scope controls WHICH FILES the
// allowed write tools may touch.
//
// Why limited to write tools: the allowlist already lets operators
// remove read tools entirely if they don't want a sub-agent reading
// anything. The high-leverage scope is "this agent can WRITE only
// inside internal/parsers/" — a model that reads broadly but mutates
// narrowly is the common useful shape. Restricting reads
// additionally would be straightforward (reuse the same gate, widen
// the tool list), but it is not what the
// AGENTIC_CODE_ASSISTANT_REPORT.md "Strict Scoped Agents" workstream
// is solving — it is solving "autonomy without hard scopes can
// mutate too broadly".
//
// Empty allowlist (the default when delegate_task is called without
// the arg) means "no restriction" — the gate is a no-op. Path
// matching is prefix-based after slash-normalising both sides:
// "internal/parsers" matches "internal/parsers/foo.go" and
// "internal/parsers/sub/bar.go" but not "internal/parsers_aux/x.go".
// Trailing slashes are tolerated. Glob support is intentionally
// absent — keeps the contract simple and the bypass surface small;
// if real-world cases need wildcards, the next iteration adds them
// behind a `_glob_` prefix or similar opt-in.

package engine

import (
	"context"
	"path/filepath"
	"strings"
)

type subagentPathScopeKey struct{}

// withSubagentPathScope attaches the allowed-write-path list to the
// context. Empty list short-circuits to a no-op so the legacy
// "no constraint" default keeps working. Each prefix is normalised
// (slash-form, no trailing slash, lowercased on Windows-insensitive
// segments handled by filepath.Clean → ToSlash) so a Windows host
// passing `internal\parsers` matches the same tools' `internal/parsers`
// argument.
func withSubagentPathScope(ctx context.Context, allow []string) context.Context {
	if len(allow) == 0 {
		return ctx
	}
	norm := make([]string, 0, len(allow))
	for _, raw := range allow {
		p := normalisePathPrefix(raw)
		if p == "" {
			continue
		}
		norm = append(norm, p)
	}
	if len(norm) == 0 {
		return ctx
	}
	return context.WithValue(ctx, subagentPathScopeKey{}, norm)
}

// subagentPathScopeFromContext returns the active allow-prefix list,
// or (nil, false) when no scope is set (gate is then a no-op).
func subagentPathScopeFromContext(ctx context.Context) ([]string, bool) {
	if ctx == nil {
		return nil, false
	}
	v := ctx.Value(subagentPathScopeKey{})
	if v == nil {
		return nil, false
	}
	out, ok := v.([]string)
	if !ok || len(out) == 0 {
		return nil, false
	}
	return out, true
}

// checkSubagentPathScope returns a non-empty refusal reason when the
// active write-tool call would touch a path outside the allowlist.
// Empty string means "permit" (or "scope not active"). The set of
// tools whose paths are inspected is intentionally narrow — see file
// header.
//
// Meta wrappers (tool_call / tool_batch_call) get peeled before the
// path check so a sub-agent cannot evade the gate by routing every
// write through tool_call(...). For tool_batch_call we check every
// inner entry; one offending path refuses the whole dispatch (same
// all-or-nothing contract as the allowlist gate, for the same
// "partial-batch leaks information" reason).
func checkSubagentPathScope(ctx context.Context, name string, params map[string]any) string {
	allow, active := subagentPathScopeFromContext(ctx)
	if !active {
		return ""
	}
	// Meta wrapper case: collect every inner (name, args) pair and
	// run the same path check against each.
	if inner := metaInnerToolCalls(name, params); len(inner) > 0 {
		for _, c := range inner {
			if denial := pathScopeRefusal(c.Name, c.Args, allow); denial != "" {
				return denial
			}
		}
		return ""
	}
	return pathScopeRefusal(name, params, allow)
}

// pathScopeRefusal applies the path check to one tool call. Returns
// "" when the call is permitted (no extracted paths or every path
// inside the allow list).
func pathScopeRefusal(name string, params map[string]any, allow []string) string {
	paths := extractWriteToolPaths(name, params)
	if len(paths) == 0 {
		return ""
	}
	for _, p := range paths {
		target := normalisePathPrefix(p)
		if target == "" {
			continue
		}
		if !pathScopeAllows(target, allow) {
			return "path " + p + " is outside allowed_paths " + strings.Join(allow, ", ")
		}
	}
	return ""
}

// pathScopeAllows is the prefix-match decision: target is allowed if
// it equals any prefix or sits underneath one. The "underneath" check
// requires a slash boundary so "internal/parsers" doesn't accidentally
// admit "internal/parsers_aux/x.go".
func pathScopeAllows(target string, allow []string) bool {
	for _, prefix := range allow {
		if target == prefix {
			return true
		}
		if strings.HasPrefix(target, prefix+"/") {
			return true
		}
	}
	return false
}

// normalisePathPrefix collapses Windows separators, trims trailing
// slashes, and runs filepath.Clean so callers can pass any of
// "internal/parsers", "internal\\parsers", "internal/parsers/" and
// have them all compare equal. Returns "" when the input is purely
// whitespace; callers must drop empties from their list before
// matching.
func normalisePathPrefix(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	// Normalise separators platform-independently: filepath.ToSlash is a
	// no-op on Linux (backslash isn't a separator there), so a Windows-style
	// allowed_path would not canonicalise on the CI runner.
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimRight(p, "/")
	if p == "" {
		return ""
	}
	// filepath.Clean uses OS separators; round-trip through ToSlash so
	// the result is canonical regardless of platform.
	p = filepath.ToSlash(filepath.Clean(p))
	return p
}

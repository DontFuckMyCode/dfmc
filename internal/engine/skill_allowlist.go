package engine

// skill_allowlist.go — runtime enforcement of `allowed_tools` declared
// on active skills. Sibling of subagent_allowlist.go which enforces
// the per-`delegate_task` allowed_tools arg; the two gates compose
// (a tool must satisfy BOTH lists when both are active).
//
// Why a second gate instead of unifying with subagent_allowlist:
// the lifetimes differ. The subagent allowlist scopes one delegated
// run; the skill allowlist scopes the entire turn (every tool call
// the model makes while a restricted skill set is active). Mixing
// them in one context key would let one path silently overwrite the
// other. Two keys, AND-composed at the gate, keeps the semantic
// transparent.
//
// Pre-fix: skill `allowed_tools` rendered into the system prompt as
// a "Scope guard" hint that the model could ignore — same prompt-
// only "guidance" pattern that bdef4ad fixed for sub-agents.
// Post-fix: when ALL active skills declare allowed_tools, the union
// becomes a hard dispatch-time gate (see skills.EffectiveAllowedTools
// for the multi-skill semantic).
//
// Empty / no-restriction (zero active skills, or any active skill
// omits allowed_tools) → gate is a no-op, every tool the engine
// knows is fair game. This preserves the legacy "no constraint"
// default for sessions where the user hasn't opted into restricted
// skills.

import (
	"context"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/skills"
)

type skillAllowlistKey struct{}

// withSkillAllowlist attaches the effective skill-derived allowlist
// to the context. The list is normalised to lowercase + duplicates
// removed (skills.EffectiveAllowedTools already does this) so the
// lookup in checkSkillAllowlist stays cheap.
//
// `enforced=false` → no-op (returns ctx unchanged). The caller passes
// the result of EffectiveAllowedTools directly so the decision about
// whether to engage the gate stays in one place (the skills package).
func withSkillAllowlist(ctx context.Context, allow []string, enforced bool) context.Context {
	if !enforced || len(allow) == 0 {
		return ctx
	}
	set := make(map[string]struct{}, len(allow))
	for _, name := range allow {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		set[key] = struct{}{}
	}
	if len(set) == 0 {
		return ctx
	}
	return context.WithValue(ctx, skillAllowlistKey{}, set)
}

// skillAllowlistFromContext returns the active set, or nil when no
// allowlist is attached. The bool reports whether enforcement is
// active so callers can branch without a nil-check race.
func skillAllowlistFromContext(ctx context.Context) (set map[string]struct{}, active bool) {
	if ctx == nil {
		return nil, false
	}
	v := ctx.Value(skillAllowlistKey{})
	if v == nil {
		return nil, false
	}
	s, ok := v.(map[string]struct{})
	if !ok || len(s) == 0 {
		return nil, false
	}
	return s, true
}

// checkSkillAllowlist returns a non-empty error string if `name` (or
// any of its meta-wrapped inner names) is outside the active skill
// allowlist. Empty string permits.
//
// Mirrors checkSubagentAllowlist's semantics: meta-tool wrappers
// (tool_call / tool_batch_call) are NOT auto-permitted at the outer
// dispatch — the meta dispatcher re-enters the gate with the inner
// backend tool name, but if a model passes a forbidden tool inside
// tool_call we want the refusal to happen at the OUTER call site
// (saves a dispatch round-trip and matches what subagent gate does).
// tool_search / tool_help are pure read-only discovery and always
// permitted.
//
// `innerNames` comes from metaInnerNames at the call site. If any
// inner name is outside the allowlist, the entire dispatch is
// refused — matches the all-or-nothing batch semantic in
// checkSubagentAllowlist.
func checkSkillAllowlist(ctx context.Context, name string, innerNames []string) string {
	set, active := skillAllowlistFromContext(ctx)
	if !active {
		return ""
	}
	outer := strings.ToLower(strings.TrimSpace(name))
	switch outer {
	case "tool_search", "tool_help":
		return ""
	}
	candidates := make([]string, 0, 1+len(innerNames))
	if outer != "tool_call" && outer != "tool_batch_call" {
		candidates = append(candidates, outer)
	}
	for _, inner := range innerNames {
		key := strings.ToLower(strings.TrimSpace(inner))
		if key == "" {
			continue
		}
		candidates = append(candidates, key)
	}
	if len(candidates) == 0 {
		return ""
	}
	for _, key := range candidates {
		if _, ok := set[key]; !ok {
			return "tool " + key + " not in active skill allowed_tools list"
		}
	}
	return ""
}

// WithActiveSkillsAllowlist is the public entrypoint Engine.Ask uses
// to attach the skill-derived gate to a context. Convenience over
// the two-step EffectiveAllowedTools + withSkillAllowlist dance so
// callers don't accidentally drop the enforced bool.
func WithActiveSkillsAllowlist(ctx context.Context, active []skills.Skill) context.Context {
	allow, enforced := skills.EffectiveAllowedTools(active)
	return withSkillAllowlist(ctx, allow, enforced)
}

package skills

// enforcement.go — derives the effective tool allowlist from a set
// of active skills so the engine can refuse calls outside the
// declared scope at dispatch time (not just in the prompt's textual
// "preferred tools" hint).
//
// Semantic — strict but composable:
//
//	1. If NO active skill declares `allowed_tools` (or the slice is
//	   empty after parsing), the gate is OFF. Same effect as today:
//	   builtins ship without Allowed and user-installed skills that
//	   omit it implicitly opt out of restriction.
//
//	2. If ANY active skill explicitly leaves `allowed_tools` empty
//	   the gate is OFF. An empty slice from an author who knows the
//	   field exists is a deliberate "I need every tool" — the
//	   permissive choice.
//	   NOTE: in practice the loader gives us the same []string{}
//	   whether the field was omitted vs. explicitly empty, so this
//	   case folds into rule 1. We keep the rule for clarity.
//
//	3. If EVERY active skill declares a non-empty `allowed_tools`,
//	   the gate is ON and the effective allowlist is the UNION of
//	   all the declared lists (composition: skill A needs Read +
//	   Grep, skill B needs Edit → effective is Read, Grep, Edit).
//
// Tool names are case-folded for comparison so a skill author
// writing "Read" and a tool registered as "read_file" don't lock
// themselves out by mismatched casing.
//
// The helper returns both the union slice (for diagnostics /
// "preferred tools" rendering) AND a boolean for the engine to
// branch on. A nil return + enforced=false is the canonical "no
// gate" signal.

import "strings"

// EffectiveAllowedTools builds the runtime tool allowlist for an
// active skill set. See package doc above for the union semantic.
// The returned slice is sorted, lowercase, deduplicated; the
// boolean tells the engine whether to engage the dispatch-time gate.
//
// Stand-alone helper (no engine state) so the engine, MCP handlers,
// and tests can all derive the same answer from the same inputs.
func EffectiveAllowedTools(active []Skill) (union []string, enforced bool) {
	if len(active) == 0 {
		return nil, false
	}
	// Walk every active skill once. If we find one that did not
	// declare allowed_tools (zero-length slice), the gate stays off
	// per rule 2 — we can return immediately without building the
	// union.
	seen := map[string]struct{}{}
	for _, skill := range active {
		if len(skill.Allowed) == 0 {
			return nil, false
		}
		for _, tool := range skill.Allowed {
			key := strings.ToLower(strings.TrimSpace(tool))
			if key == "" {
				continue
			}
			seen[key] = struct{}{}
		}
	}
	if len(seen) == 0 {
		// Every active skill declared allowed_tools but every entry
		// was whitespace — treat as no restriction rather than a
		// "deny everything" gate that would brick the session.
		return nil, false
	}
	union = make([]string, 0, len(seen))
	for k := range seen {
		union = append(union, k)
	}
	// Sort for stable output — matters for tests, prompt rendering,
	// and any caller that diffs the list across turns.
	sortStrings(union)
	return union, true
}

// sortStrings is a tiny inline sort to avoid adding the standard
// library "sort" import for a single call site. The list is small
// (handful of tool names) so insertion sort is fine and keeps the
// dependency graph clean.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// IsToolAllowedBySkills returns true when `name` is permitted under
// the given active skill set. When `enforced` from
// EffectiveAllowedTools is false, this always returns true (no
// restriction). When enforced, the lookup is case-insensitive.
//
// Meta tools (tool_search, tool_help, tool_call, tool_batch_call)
// are always permitted at this layer — the meta dispatcher itself
// re-enters the gate with the inner backend tool name, so blocking
// meta tools here would short-circuit legitimate dispatches before
// the inner check runs. Mirrors the subagent-allowlist semantic
// (see internal/engine/subagent_allowlist.go's checkSubagentAllowlist).
// Sub-agent spawning tools (delegate_task, etc.) are always blocked when
// enforced=true because they can invoke any tool (including destructive
// ones) and would bypass the per-skill allowlist. Restricting the
// sub-agent's effective allowlist to the same union is tracked as a
// follow-up (see audit VULN-NEW-1 remediation).
func IsToolAllowedBySkills(name string, allowed []string, enforced bool) bool {
	if !enforced || len(allowed) == 0 {
		return true
	}
	outer := strings.ToLower(strings.TrimSpace(name))
	// Hard-block: meta tools are always allowed through (re-entry
	// handles inner tool checks). Sub-agent tools are always blocked
	// when enforced — they can invoke any tool internally.
	switch outer {
	case "tool_search", "tool_help", "tool_call", "tool_batch_call":
		return true
	case "delegate_task", "subagent":
		return false
	}
	for _, t := range allowed {
		if strings.ToLower(strings.TrimSpace(t)) == outer {
			return true
		}
	}
	return false
}

package skills

// requires.go — skill dependency expansion. A skill may declare a
// list of skills that should activate alongside it (e.g. refactor
// requires onboard so the codebase context is loaded first). Cycles
// are detected and broken; missing dependencies are logged and
// skipped, never propagated as errors — a missing optional helper
// must not block the primary skill.
//
// Recognised YAML shapes for the `requires:` field:
//
//	requires:
//	  - onboard                            # plain string (skill name)
//	  - skill: audit                       # object form
//	    reason: "Check security implications"
//
// Used by ResolveForQuery (catalog.go) to expand the selected skill
// set after explicit / trigger / task resolution. Order is preserved:
// dependencies appear in the order they're listed, and a skill that
// is already explicitly selected wins precedence (no duplicate adds,
// no overrides).

import (
	"fmt"
	"log"
	"strings"
)

// Requirement is a parsed `requires` entry. Reason is informational —
// surfaced in `dfmc skill info` so users understand why dependencies
// were pulled in.
type Requirement struct {
	Skill  string `json:"skill" yaml:"skill"`
	Reason string `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// parseRequires normalises any of the accepted shapes into a flat
// []Requirement slice. Like parseTriggers, malformed entries are
// dropped with a warning rather than failing the whole load.
func parseRequires(raw any, skillName string) []Requirement {
	if raw == nil {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		if s, ok := raw.(string); ok {
			if name := strings.TrimSpace(s); name != "" {
				return []Requirement{{Skill: name}}
			}
		}
		return nil
	}
	out := make([]Requirement, 0, len(list))
	for _, item := range list {
		switch v := item.(type) {
		case string:
			if name := strings.TrimSpace(v); name != "" {
				out = append(out, Requirement{Skill: name})
			}
		case map[string]any:
			name := ""
			if raw, ok := v["skill"]; ok {
				name = strings.TrimSpace(fmt.Sprint(raw))
			}
			if name == "" {
				if raw, ok := v["name"]; ok {
					name = strings.TrimSpace(fmt.Sprint(raw))
				}
			}
			if name == "" {
				log.Printf("skills: skill %q has requires entry with empty skill name (skipping)", skillName)
				continue
			}
			reason := ""
			if r, ok := v["reason"]; ok {
				reason = strings.TrimSpace(fmt.Sprint(r))
			}
			out = append(out, Requirement{Skill: name, Reason: reason})
		}
	}
	return out
}

// expandRequires returns a flat ordered list of dependency skills
// for `seed`, walking the `requires` graph depth-first. The seeds
// themselves are NOT returned — callers append them in their own
// order. Cycles and self-references are silently broken; missing
// skills are logged at warn level.
func expandRequires(seeds []Skill, byName map[string]Skill) []Skill {
	if len(seeds) == 0 || len(byName) == 0 {
		return nil
	}
	out := make([]Skill, 0, len(seeds)*2)
	visited := make(map[string]bool, len(seeds)*2)
	// Mark the seeds as visited up-front so a seed that appears as a
	// dependency of a sibling is not re-added.
	for _, s := range seeds {
		visited[strings.ToLower(strings.TrimSpace(s.Name))] = true
	}

	var walk func(name string, fromSkill string)
	walk = func(name string, fromSkill string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			return
		}
		if visited[key] {
			return
		}
		dep, ok := byName[key]
		if !ok {
			log.Printf("skills: skill %q requires %q but it was not found in the catalog (skipping)", fromSkill, name)
			return
		}
		visited[key] = true
		// Depth-first: pull dep's own requires before adding dep itself,
		// so transitive prerequisites land before their dependants.
		for _, req := range dep.Requires {
			walk(req.Skill, dep.Name)
		}
		out = append(out, dep)
	}

	for _, s := range seeds {
		for _, req := range s.Requires {
			walk(req.Skill, s.Name)
		}
	}
	return out
}

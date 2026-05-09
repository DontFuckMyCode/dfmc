package skills

// explain.go — diagnostic preview of which skills would activate for
// a given query. Wraps ResolveForQuery with extra metadata about WHY
// each candidate scored where it did, so callers can render a
// "preview" view (CLI / TUI / MCP) without having to run a real
// chat turn.
//
// Use case: a user sets up a custom skill with `triggers:
// "security|vulnerability"` and wants to confirm "is my pattern
// firing for the queries I think it should?" before they spend
// tokens on a real run. ResolveForQuery would return the resolved
// set, but the trigger weight, the runner-up patterns, and the task
// fallback path are all hidden inside its closure. Explain surfaces
// the lot.
//
// Companion to ResolveForQuery, NOT a replacement: production code
// paths still call ResolveForQuery for the activation decision.
// Explain just narrates the same decision afterwards.

import (
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

// SkillExplain is one row in the diagnostic preview. Origin matches
// the values used in Selection.Origin: "explicit", "trigger", "task",
// "required". MatchedPattern + Weight are populated only when the
// activation reason was a trigger; everything else uses a synthetic
// reason string.
type SkillExplain struct {
	Name           string  `json:"name"`
	Origin         string  `json:"origin"`
	MatchedPattern string  `json:"matched_pattern,omitempty"`
	Weight         float64 `json:"weight,omitempty"`
	Reason         string  `json:"reason,omitempty"`
}

// SkillExplanation is the full diagnostic shape returned by Explain.
// CleanQuery is what's left of the user's query after explicit
// [[skill:X]] markers are stripped — that's the same string the
// trigger / task layers actually see, so previewing against it
// matches production behaviour exactly.
type SkillExplanation struct {
	Query        string         `json:"query"`
	CleanQuery   string         `json:"clean_query"`
	DetectedTask string         `json:"detected_task,omitempty"`
	Active       []SkillExplain `json:"active"`
	// NearMisses lists triggers that matched but lost (lower weight
	// than the winner, OR clear MinTriggerScore but were beaten).
	// Useful for tuning patterns: "my custom skill matched but the
	// builtin won — I need to bump my weight".
	NearMisses []SkillExplain `json:"near_misses,omitempty"`
	// SubThreshold lists triggers that matched but failed to clear
	// MinTriggerScore. Used when authors set a too-low weight by
	// mistake; this surface shows them why nothing fired.
	SubThreshold []SkillExplain `json:"sub_threshold,omitempty"`
}

// Explain runs ResolveForQuery and walks the catalog to build a
// human-readable diagnostic. Always succeeds — empty active list is
// a valid result (no skill matches).
func Explain(projectRoot, query string) SkillExplanation {
	clean := StripMarkers(query)
	task := promptlib.DetectTask(clean)
	sel := ResolveForQuery(projectRoot, query, task)
	all := Discover(projectRoot)

	out := SkillExplanation{
		Query:        query,
		CleanQuery:   clean,
		DetectedTask: task,
	}

	// Build the Active list in selection order.
	out.Active = make([]SkillExplain, 0, len(sel.Skills))
	for _, s := range sel.Skills {
		key := strings.ToLower(strings.TrimSpace(s.Name))
		entry := SkillExplain{
			Name:   s.Name,
			Origin: sel.Origin[key],
		}
		switch entry.Origin {
		case "trigger":
			pattern, weight := bestTriggerMatch(s.Triggers, clean)
			entry.MatchedPattern = pattern
			entry.Weight = weight
			entry.Reason = "regex trigger matched the query"
		case "explicit":
			entry.Reason = "user wrote [[skill:" + s.Name + "]]"
		case "task":
			entry.Reason = "task hint '" + task + "' maps to this skill"
		case "required":
			entry.Reason = "pulled in via another skill's `requires:` list"
		default:
			entry.Reason = "active"
		}
		out.Active = append(out.Active, entry)
	}

	// Walk the full catalog for near-misses and sub-threshold matches.
	// Skip skills already in Active — those have their own row.
	activeSet := map[string]struct{}{}
	for _, a := range out.Active {
		activeSet[strings.ToLower(strings.TrimSpace(a.Name))] = struct{}{}
	}
	if clean != "" {
		for _, s := range all {
			if _, ok := activeSet[strings.ToLower(strings.TrimSpace(s.Name))]; ok {
				continue
			}
			pattern, weight := bestTriggerMatch(s.Triggers, clean)
			if pattern == "" {
				continue
			}
			entry := SkillExplain{
				Name:           s.Name,
				MatchedPattern: pattern,
				Weight:         weight,
			}
			if weight >= MinTriggerScore {
				entry.Origin = "near_miss"
				entry.Reason = "matched but a higher-weighted trigger won"
				out.NearMisses = append(out.NearMisses, entry)
			} else {
				entry.Origin = "sub_threshold"
				entry.Reason = "matched but below MinTriggerScore — bump the weight to activate"
				out.SubThreshold = append(out.SubThreshold, entry)
			}
		}
	}

	// Stable ordering for the diagnostic surfaces — by descending
	// weight first, then alphabetic. Authors scanning for "why
	// didn't my pattern fire" want the closest miss at the top.
	sortByWeightDesc := func(in []SkillExplain) {
		sort.SliceStable(in, func(i, j int) bool {
			if in[i].Weight != in[j].Weight {
				return in[i].Weight > in[j].Weight
			}
			return strings.ToLower(in[i].Name) < strings.ToLower(in[j].Name)
		})
	}
	sortByWeightDesc(out.NearMisses)
	sortByWeightDesc(out.SubThreshold)

	return out
}

// bestTriggerMatch returns the highest-weighted trigger pattern from
// `triggers` that matches `query`. Returns ("", 0) when nothing
// matches. Uses the same case-insensitive matching the runtime gate
// applies.
func bestTriggerMatch(triggers []Trigger, query string) (string, float64) {
	bestPattern := ""
	bestWeight := 0.0
	for _, t := range triggers {
		if t.Pattern == nil {
			continue
		}
		if !t.Pattern.MatchString(query) {
			continue
		}
		if t.Weight > bestWeight {
			bestWeight = t.Weight
			bestPattern = t.Raw
		}
	}
	return bestPattern, bestWeight
}

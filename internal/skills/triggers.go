package skills

// triggers.go — auto-activation engine. Each skill may declare a list
// of regex patterns (with optional per-pattern weight). When a query
// has no explicit [[skill:name]] marker, ResolveForQuery walks the
// catalog, picks the highest-scoring trigger that exceeds the minimum
// threshold, and activates that skill. Compiles patterns once per
// Skill and caches the compiled form on the value.
//
// Recognised YAML shapes for the `triggers:` field:
//
//	triggers:
//	  - "security|vulnerability|audit"          # plain string, default weight
//	  - pattern: "CVE|exploit|penetration test" # object form
//	    weight: 1.0
//	  - "sql.?inject|xss:0.85"                  # inline weight via "<pattern>:<weight>"
//
// Patterns are matched case-insensitively against the cleaned query
// (skill markers stripped). A pattern compile error is logged once
// and the trigger is dropped — the rest of the catalog still works.

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
)

// Trigger is a compiled activation pattern attached to a Skill.
type Trigger struct {
	Pattern *regexp.Regexp `json:"-" yaml:"-"`
	Raw     string         `json:"pattern" yaml:"pattern"`
	Weight  float64        `json:"weight" yaml:"weight"`
}

// MinTriggerScore is the floor a candidate must clear to win
// auto-activation. Patterns with explicit weight at or above this
// threshold can fire; defaults to 0.6 so a "default" weighted skill
// (0.8) wins easily but a deliberately low-confidence trigger (0.5)
// stays dormant until something stronger isn't available.
const MinTriggerScore = 0.6

const defaultTriggerWeight = 0.8

// parseTriggers normalises any of the accepted YAML shapes into a
// flat []Trigger slice. Unknown / malformed entries are skipped with
// a warning log, never returned as errors — the catalog must keep
// loading even if one skill author writes garbage.
func parseTriggers(raw any, skillName string) []Trigger {
	if raw == nil {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		// Single-string trigger ("triggers: foo|bar") is not in the spec
		// but cheap to support.
		if s, ok := raw.(string); ok {
			if t, ok := compileTrigger(s, defaultTriggerWeight, skillName); ok {
				return []Trigger{t}
			}
		}
		return nil
	}
	out := make([]Trigger, 0, len(list))
	for _, item := range list {
		switch v := item.(type) {
		case string:
			pattern, weight := splitInlineWeight(v, defaultTriggerWeight)
			if t, ok := compileTrigger(pattern, weight, skillName); ok {
				out = append(out, t)
			}
		case map[string]any:
			pattern := ""
			if raw, ok := v["pattern"]; ok {
				pattern = strings.TrimSpace(fmt.Sprint(raw))
			}
			if pattern == "" {
				if raw, ok := v["match"]; ok {
					pattern = strings.TrimSpace(fmt.Sprint(raw))
				}
			}
			weight := defaultTriggerWeight
			if wv, ok := v["weight"]; ok {
				switch w := wv.(type) {
				case float64:
					weight = w
				case int:
					weight = float64(w)
				case string:
					if f, err := strconv.ParseFloat(strings.TrimSpace(w), 64); err == nil {
						weight = f
					}
				}
			}
			if t, ok := compileTrigger(pattern, weight, skillName); ok {
				out = append(out, t)
			}
		}
	}
	return out
}

// splitInlineWeight peels an optional ":<weight>" suffix off a plain
// string trigger. The pattern itself may legitimately contain colons
// (regex groups, alternations), so we only treat a trailing token as
// a weight if it parses cleanly as a float.
func splitInlineWeight(raw string, fallback float64) (string, float64) {
	raw = strings.TrimSpace(raw)
	idx := strings.LastIndex(raw, ":")
	if idx <= 0 {
		return raw, fallback
	}
	tail := strings.TrimSpace(raw[idx+1:])
	if tail == "" {
		return raw, fallback
	}
	w, err := strconv.ParseFloat(tail, 64)
	if err != nil {
		return raw, fallback
	}
	return strings.TrimSpace(raw[:idx]), w
}

func compileTrigger(pattern string, weight float64, skillName string) (Trigger, bool) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return Trigger{}, false
	}
	// Force case-insensitive match so authors don't have to remember (?i).
	expr := pattern
	if !strings.HasPrefix(expr, "(?i)") {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		log.Printf("skills: skill %q has invalid trigger pattern %q: %v (skipping)", skillName, pattern, err)
		return Trigger{}, false
	}
	if weight <= 0 {
		weight = defaultTriggerWeight
	}
	return Trigger{Pattern: re, Raw: pattern, Weight: weight}, true
}

// matchTriggers walks `catalog` and returns the name of the
// highest-scoring trigger match for `query` whose weight clears
// MinTriggerScore. Returns "" when nothing matches or when `query`
// is empty.
func matchTriggers(catalog []Skill, query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	bestName := ""
	bestScore := 0.0
	for _, skill := range catalog {
		for _, trig := range skill.Triggers {
			if trig.Pattern == nil {
				continue
			}
			if !trig.Pattern.MatchString(query) {
				continue
			}
			if trig.Weight < MinTriggerScore {
				continue
			}
			if trig.Weight > bestScore {
				bestScore = trig.Weight
				bestName = skill.Name
			}
		}
	}
	return bestName
}

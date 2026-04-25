package drive

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// globMatch reports whether pattern matches path using filepath.Match.
// It also tries the normalized (forward-slash) form of path so Windows
// backslashes don't break patterns written with forward slashes.
func globMatch(pattern, path string) bool {
	if matched, _ := filepath.Match(pattern, path); matched {
		return true
	}
	normalized := strings.ReplaceAll(path, "\\", "/")
	if matched, _ := filepath.Match(pattern, normalized); matched {
		return true
	}
	return false
}

// RoutingField encapsulates the routing-relevant fields from a drive.Todo.
// We use a plain struct instead of the full Todo type so the evaluator
// is decoupled from the scheduler's internal shape.
type RoutingField struct {
	ProviderTag  string
	WorkerClass  string
	Verification string
	Confidence   float64
	FileScope    []string
	Role         string
}

// MatchRule reports whether fields satisfy all non-zero criteria in rule.
// Empty/wildcard fields are ignored — all non-zero fields must match.
func MatchRule(f RoutingField, rule config.RoutingRule) bool {
	if rule.ProviderTag != "" {
		if !strings.EqualFold(strings.TrimSpace(f.ProviderTag), rule.ProviderTag) {
			return false
		}
	}
	if rule.WorkerClass != "" {
		if !strings.EqualFold(strings.TrimSpace(f.WorkerClass), rule.WorkerClass) {
			return false
		}
	}
	if rule.Verification != "" {
		if !strings.EqualFold(strings.TrimSpace(f.Verification), rule.Verification) {
			return false
		}
	}
	if rule.MinConfidence > 0 {
		if f.Confidence < rule.MinConfidence {
			return false
		}
	}
	if len(rule.FileScope) > 0 {
		matched := false
		for _, pattern := range rule.FileScope {
			for _, scope := range f.FileScope {
				matched = globMatch(pattern, scope)
				if matched {
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			return false
		}
	}
	if rule.Role != "" {
		if !strings.EqualFold(strings.TrimSpace(f.Role), rule.Role) {
			return false
		}
	}
	return true
}

// EvaluateRules returns the highest-priority RoutingRule that matches
// the given fields, or nil if no rule matches. Rules are sorted by
// Priority descending before evaluation.
func EvaluateRules(f RoutingField, rules []config.RoutingRule) *config.RoutingRule {
	if len(rules) == 0 {
		return nil
	}
	sorted := make([]config.RoutingRule, len(rules))
	copy(sorted, rules)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority > sorted[j].Priority
		}
		return false
	})
	for i := range sorted {
		if MatchRule(f, sorted[i]) {
			return &sorted[i]
		}
	}
	return nil
}

// RuleProfile extracts the profile name from a matched rule.
// Returns "" when rule is nil.
func RuleProfile(rule *config.RoutingRule) string {
	if rule == nil {
		return ""
	}
	return strings.TrimSpace(rule.Profile)
}

// RuleModel returns the model override from a matched rule, or "".
func RuleModel(rule *config.RoutingRule) string {
	if rule == nil {
		return ""
	}
	return strings.TrimSpace(rule.Model)
}

// todoToFields builds a RoutingField from a drive.Todo.
func TodoToRoutingField(t Todo) RoutingField {
	return RoutingField{
		ProviderTag:  t.ProviderTag,
		WorkerClass:  t.WorkerClass,
		Verification: string(t.Verification),
		Confidence:   t.Confidence,
		FileScope:    t.FileScope,
		Role:         "", // Role is set at dispatch time by NormalizeDriveExecution, not on Todo
	}
}
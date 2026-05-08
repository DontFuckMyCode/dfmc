package bridge

import (
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// NormalizeDriveExecution + per-field normalizers (normalizeRole,
// normalizeSkills, normalizeExecutionVerification, defaultAllowedTools)
// live in policy_normalize.go.

func SelectDriveProfile(req drive.ExecuteTodoRequest, profiles map[string]config.ModelConfig, fallback string) string {
	if picks := SelectDriveProfiles(req, profiles, fallback, 1); len(picks) > 0 {
		return picks[0]
	}
	return ""
}

func SelectDriveProfiles(req drive.ExecuteTodoRequest, profiles map[string]config.ModelConfig, fallback string, limit int) []string {
	if override := strings.TrimSpace(req.Model); override != "" {
		return []string{override}
	}
	if len(profiles) == 0 {
		fallback = strings.TrimSpace(fallback)
		if fallback == "" {
			return nil
		}
		return []string{fallback}
	}
	out := make([]string, 0, len(profiles))
	seen := map[string]struct{}{}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[strings.ToLower(name)]; ok {
			return
		}
		seen[strings.ToLower(name)] = struct{}{}
		out = append(out, name)
	}

	// Tag-based routing: if a provider_tag is set and matches a profile tag,
	// those profiles get first priority.
	tag := strings.TrimSpace(strings.ToLower(req.ProviderTag))
	if tag != "" {
		for name, prof := range profiles {
			if prof.TagMatches(tag) {
				add(name)
			}
		}
	}

	// Then fall back to vendor preference ordering.
	for _, vendor := range vendorPreference(req.Role, req.Verification) {
		add(pickProfileByVendor(profiles, vendor))
	}
	fallback = strings.TrimSpace(fallback)
	if fallback != "" {
		add(fallback)
	}
	remaining := make([]string, 0, len(profiles))
	for name := range profiles {
		remaining = append(remaining, name)
	}
	sort.SliceStable(remaining, func(i, j int) bool {
		left := profiles[remaining[i]]
		right := profiles[remaining[j]]
		if left.MaxContext != right.MaxContext {
			return left.MaxContext > right.MaxContext
		}
		return remaining[i] < remaining[j]
	})
	for _, name := range remaining {
		add(name)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func vendorPreference(role, verification string) []string {
	role = strings.ToLower(strings.TrimSpace(role))
	verification = strings.ToLower(strings.TrimSpace(verification))
	switch role {
	case "security_auditor", "code_reviewer", "synthesizer", "planner", "verifier":
		return []string{"anthropic", "openai", "google", "kimi", "zai", "alibaba", "deepseek", "minimax"}
	case "researcher":
		return []string{"google", "openai", "anthropic", "deepseek", "kimi", "zai", "alibaba", "minimax"}
	case "test_engineer", "debugger":
		return []string{"openai", "anthropic", "google", "deepseek", "kimi", "zai", "alibaba", "minimax"}
	default:
		if verification == "deep" {
			return []string{"anthropic", "openai", "google", "kimi", "zai", "alibaba", "deepseek", "minimax"}
		}
		return []string{"anthropic", "openai", "google", "deepseek", "kimi", "zai", "alibaba", "minimax"}
	}
}

func pickProfileByVendor(profiles map[string]config.ModelConfig, vendor string) string {
	bestName := ""
	bestScore := -1.0
	for name, prof := range profiles {
		score := vendorMatchScore(name, prof.AllModels(), vendor)
		if score <= 0 {
			continue
		}
		// Vendor match is the primary signal; prefer larger context windows
		combined := float64(score)*1_000_000 + float64(prof.MaxContext)
		if prof.CostPer1kTokens > 0 {
			combined /= prof.CostPer1kTokens
		}
		if combined > bestScore {
			bestScore = combined
			bestName = name
		}
	}
	return bestName
}

func vendorMatchScore(name string, models []string, vendor string) int {
	haystacks := []string{strings.ToLower(strings.TrimSpace(name))}
	for _, m := range models {
		haystacks = append(haystacks, strings.ToLower(strings.TrimSpace(m)))
	}
	match := func(terms ...string) int {
		score := 0
		for _, hay := range haystacks {
			for _, term := range terms {
				if strings.Contains(hay, term) {
					score++
				}
			}
		}
		return score
	}
	switch vendor {
	case "anthropic":
		return match("anthropic", "claude")
	case "openai":
		return match("openai", "gpt")
	case "google":
		return match("google", "gemini")
	case "deepseek":
		return match("deepseek")
	case "kimi":
		return match("kimi", "moonshot")
	case "zai":
		return match("zai", "glm")
	case "alibaba":
		return match("alibaba", "qwen", "dashscope")
	case "minimax":
		return match("minimax")
	default:
		return 0
	}
}

func cleanStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func containsAny(in string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(in, term) {
			return true
		}
	}
	return false
}

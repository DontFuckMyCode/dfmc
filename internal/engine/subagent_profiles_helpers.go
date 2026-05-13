// subagent_profiles_helpers.go — profile resolution + skill-text
// loading + fallback classification helpers used by the
// runSubagentProfiles loop. Sibling of subagent_profiles.go which
// keeps the runSubagentProfiles entry point and its retry-with-
// fallback orchestration.
//
// Splitting these out keeps subagent_profiles.go scoped to "what
// happens during one sub-agent invocation including the per-profile
// fallback retry chain" while this file owns the small classifiers
// and registries: which profiles support tools, what
// provider+model does a profile name resolve to, what skills text
// gets injected, what counts as a fallback-eligible error.

package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/skills"
)

func (e *Engine) normalizeSubagentProfiles(candidates []string, override string) ([]string, error) {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(candidates)+1)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		if !e.subagentProfileSupportsTools(name) {
			return
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	if override = strings.TrimSpace(override); override != "" {
		if _, _, err := e.resolveSubagentProfileTarget(override); err != nil {
			return nil, err
		}
		add(override)
	}
	for _, name := range candidates {
		if _, _, err := e.resolveSubagentProfileTarget(name); err != nil {
			return nil, err
		}
		add(name)
	}
	if len(out) == 0 {
		if !e.subagentProfileSupportsTools(e.provider()) {
			return nil, nil
		}
		out = append(out, e.provider())
	}
	return out, nil
}

func (e *Engine) subagentProfileSupportsTools(profile string) bool {
	if e == nil || e.Providers == nil || e.Tools == nil {
		return false
	}
	if len(e.Tools.BackendSpecs()) == 0 {
		return false
	}
	providerName, _, err := e.resolveSubagentProfileTarget(profile)
	if err != nil {
		return false
	}
	p, ok := e.Providers.Get(providerName)
	if !ok || p == nil {
		return false
	}
	return p.Hints().SupportsTools
}

func (e *Engine) resolveSubagentProfileTarget(profile string) (string, string, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return e.provider(), e.model(), nil
	}
	if e.Config != nil && e.Config.Providers.Profiles != nil {
		if cfg, ok := e.Config.Providers.Profiles[profile]; ok {
			return profile, strings.TrimSpace(cfg.Model), nil
		}
	}
	if e.Providers != nil {
		if p, ok := e.Providers.Get(profile); ok && p != nil {
			return p.Name(), p.Model(), nil
		}
	}
	return "", "", fmt.Errorf("unknown sub-agent model/profile override %q", profile)
}

// ensure skills import usage doesn't become unused in edits.
var _ = skills.Skill{}

func resolveSubagentSkills(projectRoot string, names []string) []skills.Skill {
	if len(names) == 0 {
		return nil
	}
	var out []skills.Skill
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if s, ok := skills.Lookup(projectRoot, name); ok {
			out = append(out, s)
		}
	}
	return out
}

func subagentSkillTexts(active []skills.Skill) []string {
	if len(active) == 0 {
		return nil
	}
	out := make([]string, 0, len(active))
	for _, s := range active {
		if text := strings.TrimSpace(s.SystemInstruction()); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func shouldFallbackSubagentError(err error) bool {
	if err == nil {
		return false
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func normalizeToolSource(raw string) Source {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return SourceAgent
	}
	return Source(raw)
}

func appendFallbackReason(reasons []string, err error) []string {
	text := strings.TrimSpace(errString(err))
	if text == "" {
		return reasons
	}
	return append(reasons, text)
}

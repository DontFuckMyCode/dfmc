package engine

import (
	"strings"
)

const defaultChatTier = "frontier"

func (e *Engine) tierPrimaryTarget(tier string) (string, string, bool) {
	if e == nil || e.Config == nil {
		return "", "", false
	}
	tier = strings.ToLower(strings.TrimSpace(tier))
	if tier == "" || e.Config.Routing.Tiers == nil {
		return "", "", false
	}
	cfg, ok := e.Config.Routing.Tiers[tier]
	if !ok {
		return "", "", false
	}
	return e.resolveModelRef(cfg.Primary)
}

func (e *Engine) resolveModelRef(ref string) (string, string, bool) {
	providerName, model, ok := splitProviderModelRef(ref)
	if !ok {
		return "", "", false
	}
	if e.Config != nil {
		if _, exists := e.Config.Providers.Profiles[providerName]; !exists && !strings.EqualFold(providerName, "offline") {
			return "", "", false
		}
	}
	return providerName, model, true
}

func splitProviderModelRef(ref string) (string, string, bool) {
	left, right, ok := strings.Cut(strings.TrimSpace(ref), ":")
	if !ok {
		return "", "", false
	}
	providerName := strings.TrimSpace(left)
	model := strings.TrimSpace(right)
	if providerName == "" || model == "" {
		return "", "", false
	}
	return providerName, model, true
}

func (e *Engine) modelForProvider(providerName string) string {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return ""
	}
	if strings.EqualFold(providerName, strings.TrimSpace(e.providerOverride)) && strings.TrimSpace(e.modelOverride) != "" {
		return strings.TrimSpace(e.modelOverride)
	}
	if provider, model, ok := e.tierPrimaryTarget(defaultChatTier); ok && strings.EqualFold(provider, providerName) {
		return model
	}
	if e.Config == nil {
		return ""
	}
	profile, ok := e.Config.Providers.Profiles[providerName]
	if !ok {
		return ""
	}
	return strings.TrimSpace(profile.Model)
}

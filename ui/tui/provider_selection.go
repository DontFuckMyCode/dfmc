package tui

// provider_selection.go — read-side provider/model accessors and the
// in-memory apply path for switching providers. Path resolution + scope
// rules live in provider_selection_paths.go; YAML write-side persistence
// lives in provider_selection_persist.go.
//
// Flow when the user picks a new provider/model from a panel:
//
//   applyProviderModelSelection
//     → eng.SetProviderModel + (optional) eng.SetPrimaryProvider
//     → persistProvidersPrimaryFallback (writes ~/.dfmc/config.yaml)
//     → m.notice = formatProviderSwitchNotice(...)
//
// The read-side (currentProvider, currentModel, providerProfile,
// providerPanelRows) prefers the engine's authoritative status snapshot
// but falls back to the cached m.status fields so the panel renders
// before the first round-trip completes.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
)

func (m Model) availableProviders() []string {
	if m.eng == nil || m.eng.Config == nil {
		return nil
	}
	names := make([]string, 0, len(m.eng.Config.Providers.Profiles))
	for name, profile := range m.eng.Config.Providers.Profiles {
		if !providerProfileLooksConfigured(name, profile) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// providerPanelRows builds the list of all registered provider profiles
// for the F2 providers sub-panel.
func (m Model) providerPanelRows() []theme.ProviderPanelRow {
	if m.eng == nil || m.eng.Config == nil {
		return nil
	}
	cfg := m.eng.Config.Providers
	primary := strings.TrimSpace(cfg.Primary)
	currentProvider := strings.TrimSpace(m.currentProvider())
	fallbacks := map[string]struct{}{}
	for _, name := range cfg.Fallback {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			fallbacks[name] = struct{}{}
		}
	}

	var rows []theme.ProviderPanelRow
	for name, profile := range cfg.Profiles {
		hasAPIKey := providerProfileHasCredential(profile)
		status := "ready"
		isPlaceholder := false
		if !providerProfileLooksConfigured(name, profile) || (providerProfileLooksLikeSeed(name, profile) && !hasAPIKey) {
			status = "no-key"
		}
		_, fallback := fallbacks[strings.ToLower(strings.TrimSpace(name))]
		rows = append(rows, theme.ProviderPanelRow{
			Name:           name,
			Active:         strings.EqualFold(name, currentProvider),
			Primary:        strings.EqualFold(name, primary),
			Fallback:       fallback,
			Models:         profile.AllModels(),
			FallbackModels: profile.FallbackModels,
			MaxContext:     profile.MaxContext,
			Protocol:       strings.TrimSpace(profile.Protocol),
			HasAPIKey:      hasAPIKey,
			Status:         status,
			IsPlaceholder:  isPlaceholder,
		})
	}
	// Also add the offline provider if not in profiles.
	hasOffline := false
	for name := range cfg.Profiles {
		if strings.EqualFold(name, "offline") {
			hasOffline = true
			break
		}
	}
	if !hasOffline {
		rows = append(rows, theme.ProviderPanelRow{
			Name:           "offline",
			Active:         strings.EqualFold("offline", currentProvider),
			Primary:        strings.EqualFold("offline", primary),
			Fallback:       false,
			Models:         []string{"offline-analyzer-v1"},
			FallbackModels: nil,
			MaxContext:     12000,
			Protocol:       "offline",
			HasAPIKey:      false,
			Status:         "ready",
			IsPlaceholder:  false,
		})
	}
	return rows
}

func (m Model) providerStatusPanelRows() []theme.ProviderPanelRow {
	rows := m.providerPanelRows()
	if len(rows) == 0 {
		return nil
	}
	keep := make([]theme.ProviderPanelRow, 0, len(rows))
	seen := map[string]struct{}{}
	add := func(row theme.ProviderPanelRow) {
		key := strings.ToLower(strings.TrimSpace(row.Name))
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		keep = append(keep, row)
	}
	for _, row := range rows {
		switch {
		case row.Active:
			add(row)
		case row.Fallback && row.Status == "ready" && m.isUserOwnedProvider(row.Name):
			add(row)
		}
	}
	sort.SliceStable(keep, func(i, j int) bool {
		return providerStatusRank(keep[i]) < providerStatusRank(keep[j]) ||
			(providerStatusRank(keep[i]) == providerStatusRank(keep[j]) &&
				strings.ToLower(keep[i].Name) < strings.ToLower(keep[j].Name))
	})
	return keep
}

func providerStatusRank(row theme.ProviderPanelRow) int {
	switch {
	case row.Active:
		return 0
	case row.Primary:
		return 1
	case row.Fallback:
		return 2
	case row.Status == "ready":
		return 3
	default:
		return 4
	}
}

func (m Model) isUserOwnedProvider(name string) bool {
	if m.eng == nil || m.eng.Config == nil {
		return false
	}
	profile, ok := m.eng.Config.Providers.Profiles[strings.TrimSpace(name)]
	if !ok {
		return false
	}
	if providerProfileHasCredential(profile) {
		return true
	}
	if providerProfileHasTag(profile, "my-provider") {
		return true
	}
	return strings.TrimSpace(profile.BaseURL) != "" &&
		strings.TrimSpace(profile.CatalogID) == "" &&
		!providerProfileLooksLikeSeed(name, profile)
}

func providerProfileHasCredential(profile config.ModelConfig) bool {
	return strings.TrimSpace(profile.APIKey) != "" ||
		strings.TrimSpace(profile.APIKeyEncrypted) != ""
}

func providerProfileHasTag(profile config.ModelConfig, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return false
	}
	for _, tag := range profile.Tags {
		if strings.ToLower(strings.TrimSpace(tag)) == want {
			return true
		}
	}
	return false
}

func providerProfileLooksLikeSeed(name string, profile config.ModelConfig) bool {
	if providerProfileHasCredential(profile) || providerProfileHasTag(profile, "my-provider") {
		return false
	}
	seed, ok := config.ModelsDevSeedProfiles()[strings.TrimSpace(name)]
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(profile.BaseURL), strings.TrimSpace(seed.BaseURL)) &&
		strings.EqualFold(strings.TrimSpace(profile.Protocol), strings.TrimSpace(seed.Protocol)) &&
		(strings.TrimSpace(profile.Model) == "" || strings.EqualFold(strings.TrimSpace(profile.Model), strings.TrimSpace(seed.Model)))
}

func (m Model) currentProvider() string {
	if providerName := strings.TrimSpace(m.status.Provider); providerName != "" {
		return providerName
	}
	if m.eng == nil {
		return ""
	}
	return strings.TrimSpace(m.eng.Status().Provider)
}

func (m Model) hydrateStatusProviderFromConfig() Model {
	if m.eng == nil || m.eng.Config == nil {
		return m
	}
	cfg := m.eng.Config.Providers
	providerName := strings.TrimSpace(m.status.Provider)
	if providerName == "" {
		providerName = strings.TrimSpace(cfg.Primary)
		m.status.Provider = providerName
	}
	if providerName == "" {
		return m
	}
	profile, ok := cfg.Profiles[providerName]
	if !ok {
		return m
	}
	if strings.TrimSpace(m.status.Model) == "" {
		m.status.Model = strings.TrimSpace(profile.Model)
	}
	if strings.TrimSpace(m.status.ProviderProfile.Name) == "" {
		m.status.ProviderProfile.Name = providerName
	}
	if strings.TrimSpace(m.status.ProviderProfile.Model) == "" {
		m.status.ProviderProfile.Model = strings.TrimSpace(profile.Model)
	}
	if strings.TrimSpace(m.status.ProviderProfile.Protocol) == "" {
		m.status.ProviderProfile.Protocol = strings.TrimSpace(profile.Protocol)
	}
	if strings.TrimSpace(m.status.ProviderProfile.BaseURL) == "" {
		m.status.ProviderProfile.BaseURL = strings.TrimSpace(profile.BaseURL)
	}
	if m.status.ProviderProfile.MaxTokens <= 0 {
		m.status.ProviderProfile.MaxTokens = profile.MaxTokens
	}
	if m.status.ProviderProfile.MaxContext <= 0 {
		m.status.ProviderProfile.MaxContext = profile.MaxContext
	}
	if m.status.ProviderProfile.CostPer1kTokens <= 0 {
		m.status.ProviderProfile.CostPer1kTokens = profile.CostPer1kTokens
	}
	if !m.status.ProviderProfile.Configured {
		m.status.ProviderProfile.Configured = providerProfileLooksConfigured(providerName, profile)
	}
	return m
}

func providerProfileLooksConfigured(name string, profile config.ModelConfig) bool {
	if strings.EqualFold(strings.TrimSpace(name), "offline") {
		return true
	}
	if providerProfileHasCredential(profile) {
		return true
	}
	if providerProfileLooksLikeSeed(name, profile) {
		return false
	}
	return strings.TrimSpace(profile.BaseURL) != ""
}

func (m Model) currentModel() string {
	if model := strings.TrimSpace(m.status.Model); model != "" {
		return model
	}
	if m.eng == nil {
		return ""
	}
	return strings.TrimSpace(m.eng.Status().Model)
}

func (m Model) defaultModelForProvider(name string) string {
	if m.eng == nil || m.eng.Config == nil {
		return ""
	}
	profile, ok := m.eng.Config.Providers.Profiles[strings.TrimSpace(name)]
	if !ok {
		return ""
	}
	return strings.TrimSpace(profile.Model)
}

func parseModelPersistArgs(args []string) (string, bool) {
	parts := make([]string, 0, len(args))
	persist := false
	for _, raw := range args {
		arg := strings.TrimSpace(raw)
		switch strings.ToLower(arg) {
		case "--persist", "--save":
			persist = true
		default:
			if arg != "" {
				parts = append(parts, arg)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, " ")), persist
}

func parseArgsWithPersist(args []string) ([]string, bool) {
	parts := make([]string, 0, len(args))
	persist := false
	for _, raw := range args {
		arg := strings.TrimSpace(raw)
		switch strings.ToLower(arg) {
		case "--persist", "--save":
			persist = true
		default:
			if arg != "" {
				parts = append(parts, arg)
			}
		}
	}
	return parts, persist
}

func (m Model) applyProviderModelSelection(providerName, model string) Model {
	providerName = strings.TrimSpace(providerName)
	model = strings.TrimSpace(model)
	if providerName == "" {
		return m
	}
	if m.eng != nil {
		if m.eng.Config != nil {
			if m.eng.Config.Providers.Profiles == nil {
				m.eng.Config.Providers.Profiles = map[string]config.ModelConfig{}
			}
			profile := m.eng.Config.Providers.Profiles[providerName]
			if model != "" {
				profile.Model = model
				profile = applyCatalogModelLimits(profile, model)
			}
			m.eng.Config.Providers.Profiles[providerName] = profile
		}
		m.eng.SetProviderModel(providerName, model)
		m.status = m.eng.Status()
		m.notice = formatProviderSwitchNotice(m.status.ProviderProfile)

		// Persist provider + model selection to config so it survives restart.
		if m.eng.Config != nil && strings.TrimSpace(m.eng.Config.Providers.Primary) != providerName {
			m.eng.SetPrimaryProvider(providerName)
		}
		if err := m.persistProvidersPrimaryFallback(); err != nil {
			m.notice += " (save failed: " + err.Error() + ")"
		}
	}
	return m
}

// formatProviderSwitchNotice produces a one-line confirmation after a
// provider/model switch. It names the profile, whether an endpoint and
// API key are configured, and flags the likely offline-fallback case up
// front so the user doesn't discover it only when a chat turn fails.
func formatProviderSwitchNotice(p engine.ProviderProfileStatus) string {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return ""
	}
	parts := []string{"provider → " + name}
	if model := strings.TrimSpace(p.Model); model != "" {
		parts = append(parts, "model: "+model)
	}
	if !p.Configured {
		if env := config.EnvVarForProvider(name); env != "" {
			parts = append(parts, fmt.Sprintf("⚠ no API key — set %s in .env or providers.profiles.%s.api_key (falling back to offline)", env, name))
		} else {
			parts = append(parts, fmt.Sprintf("⚠ no API key — set providers.profiles.%s.api_key in config.yaml (falling back to offline)", name))
		}
		return strings.Join(parts, " · ")
	}
	if base := strings.TrimSpace(p.BaseURL); base != "" {
		parts = append(parts, "endpoint: "+base)
	}
	return strings.Join(parts, " · ")
}

// providerProfileExists reports whether the named provider has an
// entry in the engine's Providers.Profiles map. This is broader than
// availableProviders() — it accepts unconfigured profiles too, so a
// user typing /provider <name> can flip to a known provider even
// before they've wired up an API key.
// allKnownProviders returns every provider name in Config.Providers.Profiles,
// configured or not, sorted alphabetically. Use this for surfaces
// (tab-completion, picker) where the user is asking to SEE the
// catalog; use availableProviders() when filtering for active routing.
func (m Model) allKnownProviders() []string {
	if m.eng == nil || m.eng.Config == nil {
		return nil
	}
	names := make([]string, 0, len(m.eng.Config.Providers.Profiles))
	for name := range m.eng.Config.Providers.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m Model) providerProfileExists(name string) bool {
	if m.eng == nil || m.eng.Config == nil {
		return false
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	for known := range m.eng.Config.Providers.Profiles {
		if strings.EqualFold(known, trimmed) {
			return true
		}
	}
	return false
}

func (m Model) providerProfile(name string) engine.ProviderProfileStatus {
	if m.eng == nil || m.eng.Config == nil {
		return engine.ProviderProfileStatus{Name: strings.TrimSpace(name)}
	}
	profile, ok := m.eng.Config.Providers.Profiles[strings.TrimSpace(name)]
	if !ok {
		return engine.ProviderProfileStatus{Name: strings.TrimSpace(name)}
	}
	return engine.ProviderProfileStatus{
		Name:            strings.TrimSpace(name),
		Model:           strings.TrimSpace(profile.Model),
		Protocol:        strings.TrimSpace(profile.Protocol),
		BaseURL:         strings.TrimSpace(profile.BaseURL),
		MaxTokens:       profile.MaxTokens,
		MaxContext:      profile.MaxContext,
		CostPer1kTokens: profile.CostPer1kTokens,
		Configured:      strings.TrimSpace(profile.APIKey) != "" || strings.TrimSpace(profile.BaseURL) != "",
	}
}

package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

const (
	providerViewList        = "list"
	providerViewDetail      = "detail"
	providerViewCatalog     = "catalog"
	providerViewCatalogForm = "catalog_form"
	providerViewTiers       = "tiers"
	providerViewSkills      = "skills"
)

var providerTierNames = []string{"frontier", "medium", "turbo", "weak"}

type catalogProviderItem struct {
	ID         string
	Name       string
	Endpoint   string
	Compatible string
	ModelCount int
}

func (m Model) loadProviderCatalogItems() Model {
	items, err := providerCatalogItems()
	if err != nil {
		m.providers.err = err.Error()
		m.providers.catalogItems = nil
		m.providers.catalogLoaded = true
		return m
	}
	m.providers.catalogItems = items
	m.providers.catalogLoaded = true
	m.providers.catalogScroll = clampScroll(m.providers.catalogScroll, len(items))
	return m
}

func providerCatalogItems() ([]catalogProviderItem, error) {
	catalog, err := config.LoadModelsDevCatalogCached(config.ModelsDevCachePath())
	if err != nil {
		return nil, err
	}
	items := make([]catalogProviderItem, 0, len(catalog))
	for id, provider := range catalog {
		refID := strings.TrimSpace(provider.ID)
		if refID == "" {
			refID = strings.TrimSpace(id)
		}
		if refID == "" {
			continue
		}
		compatible := configProtocolFromCatalog(provider)
		items = append(items, catalogProviderItem{
			ID:         refID,
			Name:       nonEmpty(strings.TrimSpace(provider.Name), refID),
			Endpoint:   strings.TrimSpace(provider.API),
			Compatible: compatible,
			ModelCount: len(provider.Models),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return items, nil
}

func configProtocolFromCatalog(provider config.ModelsDevProvider) string {
	switch strings.TrimSpace(provider.NPM) {
	case "@ai-sdk/anthropic":
		return "anthropic"
	case "@ai-sdk/openai":
		return "openai"
	case "@ai-sdk/openai-compatible":
		return "openai-compatible"
	case "@ai-sdk/google":
		return "google"
	default:
		return ""
	}
}

func (m Model) startCatalogProviderForm(item catalogProviderItem) Model {
	name := sanitizeProviderProfileName(item.ID)
	if name == "" {
		name = sanitizeProviderProfileName(item.Name)
	}
	name = m.uniqueProviderProfileName(name)
	m.providers.catalogRefID = item.ID
	m.providers.catalogFormField = 0
	m.providers.catalogFormName = name
	m.providers.catalogFormURL = item.Endpoint
	m.providers.catalogFormCompat = item.Compatible
	m.providers.catalogFormKey = ""
	m.providers.viewMode = providerViewCatalogForm
	m.notice = "provider form: edit name/endpoint/compatible/key; key is encrypted on save"
	return m
}

func (m Model) uniqueProviderProfileName(base string) string {
	base = sanitizeProviderProfileName(base)
	if base == "" {
		base = "provider"
	}
	if m.eng == nil || m.eng.Config == nil || m.eng.Config.Providers.Profiles == nil {
		return base
	}
	if _, exists := m.eng.Config.Providers.Profiles[base]; !exists {
		return base
	}
	for i := 2; ; i++ {
		name := fmt.Sprintf("%s-%d", base, i)
		if _, exists := m.eng.Config.Providers.Profiles[name]; !exists {
			return name
		}
	}
}

func sanitizeProviderProfileName(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func (m *Model) createProviderFromCatalog() error {
	if m.eng == nil || m.eng.Config == nil {
		return fmt.Errorf("engine not ready")
	}
	name := strings.TrimSpace(m.providers.catalogFormName)
	refID := strings.TrimSpace(m.providers.catalogRefID)
	if name == "" || refID == "" {
		return fmt.Errorf("provider name and catalog reference are required")
	}
	if _, exists := m.eng.Config.Providers.Profiles[name]; exists {
		return fmt.Errorf("provider %s already exists", name)
	}
	models := catalogModelsForRef(refID)
	defaultModel := ""
	if len(models) > 0 {
		defaultModel = models[0]
	}
	encryptedKey := ""
	key, changed := cleanProviderSecretInput(strings.TrimSpace(m.providers.catalogFormKey))
	if changed {
		m.notice = "key input: line breaks/control chars ignored"
	}
	if key != "" {
		var err error
		encryptedKey, err = config.EncryptSecret(key)
		if err != nil {
			return fmt.Errorf("encrypt key: %w", err)
		}
	}
	prof := config.ModelConfig{
		APIKey:          key,
		APIKeyEncrypted: encryptedKey,
		BaseURL:         strings.TrimSpace(m.providers.catalogFormURL),
		CatalogID:       refID,
		Model:           defaultModel,
		Models:          models,
		Protocol:        strings.TrimSpace(m.providers.catalogFormCompat),
		Tags:            []string{"my-provider", "catalog:" + refID},
	}
	prof = applyCatalogModelLimits(prof, defaultModel)
	m.eng.Config.Providers.Profiles[name] = prof
	if err := m.persistProviderProfileUserConfig(name, prof); err != nil {
		return err
	}
	return m.reloadEngineConfig()
}

func catalogModelsForRef(refID string) []string {
	refID = strings.TrimSpace(refID)
	if refID == "" {
		return nil
	}
	catalog, err := config.LoadModelsDevCatalogCached(config.ModelsDevCachePath())
	if err != nil {
		return nil
	}
	provider, ok := catalog[refID]
	if !ok {
		for key, candidate := range catalog {
			if strings.EqualFold(key, refID) || strings.EqualFold(candidate.ID, refID) {
				provider = candidate
				ok = true
				break
			}
		}
	}
	if !ok {
		return nil
	}
	models := make([]string, 0, len(provider.Models))
	for key, model := range provider.Models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			id = strings.TrimSpace(key)
		}
		if id != "" && !strings.EqualFold(strings.TrimSpace(model.Status), "deprecated") {
			models = append(models, id)
		}
	}
	sort.Strings(models)
	return models
}

func catalogModelForRef(refID, modelID string) (config.ModelsDevModel, bool) {
	refID = strings.TrimSpace(refID)
	modelID = strings.TrimSpace(modelID)
	if refID == "" || modelID == "" {
		return config.ModelsDevModel{}, false
	}
	catalog, err := config.LoadModelsDevCatalogCached(config.ModelsDevCachePath())
	if err != nil {
		return config.ModelsDevModel{}, false
	}
	provider, ok := catalog[refID]
	if !ok {
		for key, candidate := range catalog {
			if strings.EqualFold(key, refID) || strings.EqualFold(candidate.ID, refID) {
				provider = candidate
				ok = true
				break
			}
		}
	}
	if !ok {
		return config.ModelsDevModel{}, false
	}
	for key, model := range provider.Models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			id = strings.TrimSpace(key)
		}
		if strings.EqualFold(id, modelID) {
			if model.ID == "" {
				model.ID = id
			}
			return model, true
		}
	}
	return config.ModelsDevModel{}, false
}

func applyCatalogModelLimits(prof config.ModelConfig, modelID string) config.ModelConfig {
	model, ok := catalogModelForRef(prof.CatalogID, modelID)
	if !ok {
		return prof
	}
	if model.Limit.Context > 0 {
		prof.MaxContext = model.Limit.Context
	}
	if model.Limit.Output > 0 {
		prof.MaxTokens = model.Limit.Output
	}
	return prof
}

func (m Model) isMyProvider(name string) bool {
	if m.eng == nil || m.eng.Config == nil {
		return false
	}
	prof, ok := m.eng.Config.Providers.Profiles[name]
	if !ok {
		return false
	}
	if strings.TrimSpace(prof.CatalogID) != "" {
		return true
	}
	for _, tag := range prof.Tags {
		if strings.EqualFold(strings.TrimSpace(tag), "my-provider") {
			return true
		}
	}
	return false
}

func (m Model) providerModelRefs() []string {
	if m.eng == nil || m.eng.Config == nil {
		return nil
	}
	set := map[string]string{}
	for name, prof := range m.eng.Config.Providers.Profiles {
		if !m.isMyProvider(name) {
			continue
		}
		models := prof.AllModels()
		if strings.TrimSpace(prof.CatalogID) != "" {
			models = catalogModelsForRef(prof.CatalogID)
		}
		for _, model := range models {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			ref := name + ":" + model
			set[strings.ToLower(ref)] = ref
		}
	}
	out := make([]string, 0, len(set))
	for _, ref := range set {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

func (m Model) persistProviderProfileUserConfig(name string, prof config.ModelConfig) error {
	path, err := m.userConfigPath()
	if err != nil {
		return err
	}
	doc, err := readYAMLDoc(path)
	if err != nil {
		return err
	}
	if _, ok := doc["version"]; !ok {
		doc["version"] = 1
	}
	profileNode := ensureStringAnyMap(ensureStringAnyMap(ensureStringAnyMap(doc, "providers"), "profiles"), name)
	writeProviderProfileProjectConfig(profileNode, prof)
	if strings.TrimSpace(prof.APIKeyEncrypted) != "" {
		profileNode["api_key_enc"] = prof.APIKeyEncrypted
		delete(profileNode, "api_key")
	}
	if strings.TrimSpace(prof.CatalogID) != "" {
		profileNode["catalog_id"] = prof.CatalogID
	}
	return writeYAMLDoc(path, doc)
}

func (m Model) persistTierSelection(tier, slot, modelRef string) (string, error) {
	tier = strings.TrimSpace(tier)
	modelRef = strings.TrimSpace(modelRef)
	if tier == "" || modelRef == "" {
		return "", fmt.Errorf("tier and model are required")
	}
	path, err := m.userConfigPath()
	if err != nil {
		return "", err
	}
	doc, err := readYAMLDoc(path)
	if err != nil {
		return "", err
	}
	routingNode := ensureStringAnyMap(doc, "routing")
	tiersNode := ensureStringAnyMap(routingNode, "tiers")
	tierNode := ensureStringAnyMap(tiersNode, tier)
	if slot == "primary" {
		tierNode["primary"] = modelRef
	} else {
		fallbacks := stringSliceFromAny(tierNode["fallbacks"])
		for len(fallbacks) < 3 {
			fallbacks = append(fallbacks, "")
		}
		idx := 0
		switch slot {
		case "fallback2":
			idx = 1
		case "fallback3":
			idx = 2
		}
		fallbacks[idx] = modelRef
		tierNode["fallbacks"] = fallbacks
	}
	if err := writeYAMLDoc(path, doc); err != nil {
		return "", err
	}
	if m.eng != nil && m.eng.Config != nil {
		if m.eng.Config.Routing.Tiers == nil {
			m.eng.Config.Routing.Tiers = map[string]config.TierRouting{}
		}
		cfg := m.eng.Config.Routing.Tiers[tier]
		if slot == "primary" {
			cfg.Primary = modelRef
		} else {
			for len(cfg.Fallbacks) < 3 {
				cfg.Fallbacks = append(cfg.Fallbacks, "")
			}
			idx := 0
			switch slot {
			case "fallback2":
				idx = 1
			case "fallback3":
				idx = 2
			}
			cfg.Fallbacks[idx] = modelRef
		}
		m.eng.Config.Routing.Tiers[tier] = cfg
	}
	return path, nil
}

func (m Model) persistSkillModelSelection(skill, modelRef string) (string, error) {
	skill = strings.TrimSpace(skill)
	modelRef = strings.TrimSpace(modelRef)
	if skill == "" || modelRef == "" {
		return "", fmt.Errorf("skill and model are required")
	}
	path, err := m.userConfigPath()
	if err != nil {
		return "", err
	}
	doc, err := readYAMLDoc(path)
	if err != nil {
		return "", err
	}
	routingNode := ensureStringAnyMap(doc, "routing")
	skillsNode := ensureStringAnyMap(routingNode, "skill_models")
	skillsNode[skill] = modelRef
	if err := writeYAMLDoc(path, doc); err != nil {
		return "", err
	}
	if m.eng != nil && m.eng.Config != nil {
		if m.eng.Config.Routing.SkillModels == nil {
			m.eng.Config.Routing.SkillModels = map[string]string{}
		}
		m.eng.Config.Routing.SkillModels[skill] = modelRef
	}
	return path, nil
}

func (m Model) clearAllProviderKeys() (string, error) {
	path, err := m.userConfigPath()
	if err != nil {
		return "", err
	}
	doc, err := readYAMLDoc(path)
	if err != nil {
		return "", err
	}
	providersNode, ok := toStringAnyMap(doc["providers"])
	if !ok {
		return path, nil
	}
	profilesNode, ok := toStringAnyMap(providersNode["profiles"])
	if !ok {
		return path, nil
	}
	for _, raw := range profilesNode {
		if profile, ok := toStringAnyMap(raw); ok {
			delete(profile, "api_key")
			delete(profile, "api_key_enc")
		}
	}
	if m.eng != nil && m.eng.Config != nil {
		for name, prof := range m.eng.Config.Providers.Profiles {
			prof.APIKey = ""
			prof.APIKeyEncrypted = ""
			m.eng.Config.Providers.Profiles[name] = prof
		}
	}
	if err := writeYAMLDoc(path, doc); err != nil {
		return "", err
	}
	return path, nil
}

func readYAMLDoc(path string) (map[string]any, error) {
	doc := map[string]any{}
	data, err := os.ReadFile(path)
	if err == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := yaml.Unmarshal(data, &doc); err != nil {
				return nil, fmt.Errorf("parse config: %w", err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

func writeYAMLDoc(path string, doc map[string]any) error {
	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func stringSliceFromAny(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return nil
	}
}

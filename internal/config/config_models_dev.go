// config_models_dev.go — everything that talks to models.dev. The
// registry at https://models.dev/api.json is the upstream truth for
// provider IDs, current model IDs, context windows, and modality
// flags. We:
//
//   - fetch / cache the catalog (FetchModelsDevCatalog, Load / Save);
//   - merge it into a user's providers.profiles block without
//     clobbering API keys, custom base URLs, or explicit model pins
//     (MergeProviderProfilesFromModelsDev);
//   - seed sensible defaults for providers the user hasn't configured
//     yet (ModelsDevSeedProfiles);
//   - pick the "best" model per provider when the configured pin is
//     stale — tool-call capable first, then reasoning, then largest
//     context, then alphabetical — via selectModelsDevModel /
//     compareModelsDevModels.
//
// ModelsDevProviderAliases translates our local profile names
// ("kimi", "alibaba") to their registry IDs ("moonshotai",
// "alibaba"); protocolFromModelsDevProvider maps the NPM package
// shape back onto our internal protocol string.

package config

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const DefaultModelsDevAPIURL = "https://models.dev/api.json"

type ModelsDevCatalog map[string]ModelsDevProvider

type ModelsDevProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	API    string                    `json:"api,omitempty"`
	NPM    string                    `json:"npm,omitempty"`
	Env    []string                  `json:"env,omitempty"`
	Doc    string                    `json:"doc,omitempty"`
	Models map[string]ModelsDevModel `json:"models,omitempty"`
}

type ModelsDevModel struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Status      string          `json:"status,omitempty"`
	Reasoning   bool            `json:"reasoning"`
	ToolCall    bool            `json:"tool_call"`
	Modalities  ModelsDevModes  `json:"modalities"`
	Limit       ModelsDevLimits `json:"limit"`
	LastUpdated string          `json:"last_updated,omitempty"`
}

type ModelsDevModes struct {
	Input  []string `json:"input,omitempty"`
	Output []string `json:"output,omitempty"`
}

type ModelsDevLimits struct {
	Context int `json:"context,omitempty"`
	Input   int `json:"input,omitempty"`
	Output  int `json:"output,omitempty"`
}

type ModelsDevMergeOptions struct {
	RewriteBaseURL bool
}

func FetchModelsDevCatalog(ctx context.Context, apiURL string) (ModelsDevCatalog, error) {
	endpoint := strings.TrimSpace(apiURL)
	if endpoint == "" {
		endpoint = DefaultModelsDevAPIURL
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("models.dev error status %d", resp.StatusCode)
	}
	out := ModelsDevCatalog{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func LoadModelsDevCatalog(path string) (ModelsDevCatalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := ModelsDevCatalog{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func SaveModelsDevCatalog(path string, catalog ModelsDevCatalog) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func MergeProviderProfilesFromModelsDev(existing map[string]ModelConfig, catalog ModelsDevCatalog, opts ModelsDevMergeOptions) map[string]ModelConfig {
	out := maps.Clone(existing)
	for name, seed := range ModelsDevSeedProfiles() {
		prof := out[name]
		if strings.TrimSpace(prof.APIKey) == "" {
			prof.APIKey = seed.APIKey
		}
		if strings.TrimSpace(prof.Model) == "" {
			prof.Model = seed.Model
		}
		if prof.MaxTokens <= 0 {
			prof.MaxTokens = seed.MaxTokens
		}
		if prof.MaxContext <= 0 {
			prof.MaxContext = seed.MaxContext
		}
		if strings.TrimSpace(prof.Protocol) == "" {
			prof.Protocol = seed.Protocol
		}
		if strings.TrimSpace(prof.BaseURL) == "" {
			prof.BaseURL = seed.BaseURL
		}
		out[name] = prof
	}

	if len(catalog) == 0 {
		return out
	}
	for name, providerID := range ModelsDevProviderAliases() {
		provider, ok := catalog[providerID]
		if !ok {
			continue
		}
		current := out[name]
		selected, ok := selectModelsDevModel(provider, current.Model, name)
		if !ok {
			continue
		}
		current.Model = selected.ID
		if selected.Limit.Output > 0 {
			current.MaxTokens = selected.Limit.Output
		}
		if selected.Limit.Context > 0 {
			current.MaxContext = selected.Limit.Context
		}
		if protocol := protocolFromModelsDevProvider(provider); protocol != "" {
			current.Protocol = protocol
		}
		if opts.RewriteBaseURL {
			current.BaseURL = strings.TrimSpace(provider.API)
		} else if strings.TrimSpace(current.BaseURL) == "" && strings.TrimSpace(provider.API) != "" {
			current.BaseURL = strings.TrimSpace(provider.API)
		}
		out[name] = current
	}
	return out
}

func ModelsDevProviderAliases() map[string]string {
	return map[string]string{
		"anthropic": "anthropic",
		"openai":    "openai",
		"google":    "google",
		"deepseek":  "deepseek",
		"kimi":      "moonshotai",
		"minimax":   "minimax",
		"zai":       "zai",
		"alibaba":   "alibaba",
	}
}

func ModelsDevSeedProfiles() map[string]ModelConfig {
	return map[string]ModelConfig{
		"anthropic": {Model: "claude-sonnet-4-6", MaxTokens: 64000, MaxContext: 1000000, Protocol: "anthropic"},
		"openai":    {Model: "gpt-5.4", MaxTokens: 128000, MaxContext: 1050000, Protocol: "openai"},
		"google":    {Model: "gemini-3.1-pro-preview-customtools", MaxTokens: 65536, MaxContext: 1048576, Protocol: "google"},
		"deepseek":  {Model: "deepseek-chat", BaseURL: "https://api.deepseek.com/v1", MaxTokens: 8192, MaxContext: 131072, Protocol: "openai-compatible"},
		"kimi":      {Model: "kimi-k2.5", BaseURL: "https://api.moonshot.ai/v1", MaxTokens: 262144, MaxContext: 262144, Protocol: "openai-compatible"},
		"minimax":   {Model: "MiniMax-M2.7", BaseURL: "https://api.minimax.io/anthropic/v1", MaxTokens: 131072, MaxContext: 204800, Protocol: "anthropic"},
		"zai":       {Model: "glm-5.1", BaseURL: "https://api.z.ai/api/paas/v4", MaxTokens: 131072, MaxContext: 200000, Protocol: "openai-compatible"},
		"alibaba":   {Model: "qwen3.5-plus", BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", MaxTokens: 65536, MaxContext: 1000000, Protocol: "openai-compatible"},
	}
}

func protocolFromModelsDevProvider(provider ModelsDevProvider) string {
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

func selectModelsDevModel(provider ModelsDevProvider, currentModel, providerName string) (ModelsDevModel, bool) {
	if model, ok := lookupModelsDevModel(provider, currentModel); ok {
		return model, true
	}
	if model, ok := lookupModelsDevModel(provider, ModelsDevSeedProfiles()[providerName].Model); ok {
		return model, true
	}
	candidates := make([]ModelsDevModel, 0, len(provider.Models))
	for _, model := range provider.Models {
		if strings.EqualFold(strings.TrimSpace(model.Status), "deprecated") {
			continue
		}
		if !containsFold(model.Modalities.Input, "text") || !containsFold(model.Modalities.Output, "text") {
			continue
		}
		candidates = append(candidates, model)
	}
	if len(candidates) == 0 {
		return ModelsDevModel{}, false
	}
	slices.SortFunc(candidates, compareModelsDevModels)
	return candidates[0], true
}

func lookupModelsDevModel(provider ModelsDevProvider, id string) (ModelsDevModel, bool) {
	target := strings.TrimSpace(id)
	if target == "" {
		return ModelsDevModel{}, false
	}
	for _, model := range provider.Models {
		if strings.EqualFold(model.ID, target) {
			return model, true
		}
	}
	return ModelsDevModel{}, false
}

func compareModelsDevModels(a, b ModelsDevModel) int {
	if a.ToolCall != b.ToolCall {
		if a.ToolCall {
			return -1
		}
		return 1
	}
	if a.Reasoning != b.Reasoning {
		if !a.Reasoning {
			return -1
		}
		return 1
	}
	if a.Limit.Context != b.Limit.Context {
		if a.Limit.Context > b.Limit.Context {
			return -1
		}
		return 1
	}
	return strings.Compare(strings.ToLower(strings.TrimSpace(a.ID)), strings.ToLower(strings.TrimSpace(b.ID)))
}

func containsFold(items []string, target string) bool {
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), target) {
			return true
		}
	}
	return false
}

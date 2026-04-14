package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultDirName = ".dfmc"
	DefaultVersion = 1
)

type Config struct {
	Version   int             `yaml:"version"`
	Providers ProvidersConfig `yaml:"providers"`
	Routing   RoutingConfig   `yaml:"routing"`
	Context   ContextConfig   `yaml:"context"`
	Memory    MemoryConfig    `yaml:"memory"`
	Security  SecurityConfig  `yaml:"security"`
	Tools     ToolsConfig     `yaml:"tools"`
	Hooks     HooksConfig     `yaml:"hooks"`
	Plugins   PluginsConfig   `yaml:"plugins"`
	TUI       TUIConfig       `yaml:"tui"`
	Web       WebConfig       `yaml:"web"`
	Remote    RemoteConfig    `yaml:"remote"`
	Project   ProjectConfig   `yaml:"project"`
}

type ProvidersConfig struct {
	Primary  string                 `yaml:"primary"`
	Fallback []string               `yaml:"fallback"`
	Profiles map[string]ModelConfig `yaml:"profiles"`
}

type ModelConfig struct {
	APIKey     string `yaml:"api_key,omitempty"`
	BaseURL    string `yaml:"base_url,omitempty"`
	Model      string `yaml:"model,omitempty"`
	MaxTokens  int    `yaml:"max_tokens,omitempty"`
	MaxContext int    `yaml:"max_context,omitempty"`
	Protocol   string `yaml:"protocol,omitempty"`
	Region     string `yaml:"region,omitempty"`
}

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

type RoutingConfig struct {
	Rules []RoutingRule `yaml:"rules"`
}

type RoutingRule struct {
	Condition string `yaml:"condition"`
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model,omitempty"`
}

type ContextConfig struct {
	MaxFiles         int    `yaml:"max_files"`
	MaxTokensTotal   int    `yaml:"max_tokens_total"`
	MaxTokensPerFile int    `yaml:"max_tokens_per_file"`
	MaxHistoryTokens int    `yaml:"max_history_tokens"`
	Compression      string `yaml:"compression"`
	IncludeTests     bool   `yaml:"include_tests"`
	IncludeDocs      bool   `yaml:"include_docs"`
}

type MemoryConfig struct {
	Enabled               bool    `yaml:"enabled"`
	MaxEpisodic           int     `yaml:"max_episodic"`
	MaxSemantic           int     `yaml:"max_semantic"`
	ConsolidationInterval string  `yaml:"consolidation_interval"`
	DecayRate             float64 `yaml:"decay_rate"`
}

type SecurityConfig struct {
	SecretDetection bool          `yaml:"secret_detection"`
	VulnScanning    bool          `yaml:"vuln_scanning"`
	DepAudit        bool          `yaml:"dep_audit"`
	Sandbox         SandboxConfig `yaml:"sandbox"`
}

type SandboxConfig struct {
	AllowShell bool   `yaml:"allow_shell"`
	AllowNet   bool   `yaml:"allow_network"`
	Timeout    string `yaml:"timeout"`
	MaxOutput  string `yaml:"max_output"`
}

type ToolsConfig struct {
	Enabled []string           `yaml:"enabled"`
	Shell   ShellToolConfig    `yaml:"shell"`
	Extra   map[string]any     `yaml:"extra,omitempty"`
	Params  map[string]string  `yaml:"params,omitempty"`
	Flags   map[string]bool    `yaml:"flags,omitempty"`
	Limits  map[string]float64 `yaml:"limits,omitempty"`
}

type ShellToolConfig struct {
	BlockedCommands []string `yaml:"blocked_commands"`
	Timeout         string   `yaml:"timeout"`
}

type HooksConfig struct {
	Entries map[string][]HookEntry `yaml:",inline"`
}

type HookEntry struct {
	Name      string `yaml:"name"`
	Condition string `yaml:"condition,omitempty"`
	Command   string `yaml:"command"`
}

type PluginsConfig struct {
	Directory string   `yaml:"directory"`
	Enabled   []string `yaml:"enabled"`
}

type TUIConfig struct {
	Theme      string `yaml:"theme"`
	VimKeys    bool   `yaml:"vim_keys"`
	ShowTokens bool   `yaml:"show_tokens"`
}

type WebConfig struct {
	Port        int    `yaml:"port"`
	Host        string `yaml:"host"`
	Auth        string `yaml:"auth"`
	OpenBrowser bool   `yaml:"open_browser"`
}

type RemoteConfig struct {
	Enabled  bool   `yaml:"enabled"`
	GRPCPort int    `yaml:"grpc_port"`
	WSPort   int    `yaml:"ws_port"`
	Auth     string `yaml:"auth"`
}

type ProjectConfig struct {
	Name        string   `yaml:"name"`
	Languages   []string `yaml:"languages"`
	Exclude     []string `yaml:"exclude"`
	Conventions struct {
		Naming            string `yaml:"naming"`
		MaxFunctionLength int    `yaml:"max_function_length"`
		MaxFileLength     int    `yaml:"max_file_length"`
	} `yaml:"conventions"`
}

type LoadOptions struct {
	GlobalPath  string
	ProjectPath string
	CWD         string
}

func Load() (*Config, error) {
	return LoadWithOptions(LoadOptions{})
}

func LoadWithOptions(opts LoadOptions) (*Config, error) {
	cfg := DefaultConfig()

	globalPath := opts.GlobalPath
	if globalPath == "" {
		globalPath = filepath.Join(UserConfigDir(), "config.yaml")
	}
	if err := loadYAML(globalPath, cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load global config: %w", err)
	}

	projectPath := opts.ProjectPath
	if projectPath == "" {
		projectRoot := FindProjectRoot(opts.CWD)
		if projectRoot != "" {
			projectPath = filepath.Join(projectRoot, DefaultDirName, "config.yaml")
		}
	}
	if projectPath != "" {
		if err := loadYAML(projectPath, cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("load project config: %w", err)
		}
	}

	cfg.applyEnv()
	cfg.Providers.Profiles = MergeProviderProfilesFromModelsDev(cfg.Providers.Profiles, nil, ModelsDevMergeOptions{})
	if catalog, err := LoadModelsDevCatalog(ModelsDevCachePath()); err == nil {
		cfg.Providers.Profiles = MergeProviderProfilesFromModelsDev(cfg.Providers.Profiles, catalog, ModelsDevMergeOptions{})
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func loadYAML(path string, out *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func (c *Config) applyEnv() {
	envToProvider := map[string]string{
		"ANTHROPIC_API_KEY": "anthropic",
		"OPENAI_API_KEY":    "openai",
		"GOOGLE_AI_API_KEY": "google",
		"DEEPSEEK_API_KEY":  "deepseek",
		"KIMI_API_KEY":      "kimi",
		"MOONSHOT_API_KEY":  "kimi",
		"MINIMAX_API_KEY":   "minimax",
		"ZAI_API_KEY":       "zai",
		"ALIBABA_API_KEY":   "alibaba",
	}
	if c.Providers.Profiles == nil {
		c.Providers.Profiles = map[string]ModelConfig{}
	}
	for envName, providerName := range envToProvider {
		val := strings.TrimSpace(os.Getenv(envName))
		if val == "" {
			continue
		}
		prof := c.Providers.Profiles[providerName]
		prof.APIKey = val
		c.Providers.Profiles[providerName] = prof
	}
}

func UserConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return DefaultDirName
	}
	return filepath.Join(home, DefaultDirName)
}

func FindProjectRoot(start string) string {
	cwd := start
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return ""
		}
	}

	markers := []string{
		DefaultDirName,
		".git",
		"go.mod",
		"package.json",
		"Cargo.toml",
		"pyproject.toml",
	}

	dir := cwd
	for {
		for _, marker := range markers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return cwd
}

func (c *Config) DataDir() string {
	return filepath.Join(UserConfigDir(), "data")
}

func ModelsDevCachePath() string {
	return filepath.Join(UserConfigDir(), "cache", "models.dev.json")
}

func (c *Config) PluginDir() string {
	if strings.TrimSpace(c.Plugins.Directory) != "" {
		return c.Plugins.Directory
	}
	return filepath.Join(UserConfigDir(), "plugins")
}

func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func MergeProviderProfilesFromModelsDev(existing map[string]ModelConfig, catalog ModelsDevCatalog, opts ModelsDevMergeOptions) map[string]ModelConfig {
	out := map[string]ModelConfig{}
	for name, prof := range existing {
		out[name] = prof
	}
	for name, seed := range modelsDevSeedProfiles() {
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
	for name, providerID := range modelsDevProviderAliases() {
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

func modelsDevProviderAliases() map[string]string {
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

func modelsDevSeedProfiles() map[string]ModelConfig {
	return map[string]ModelConfig{
		"anthropic": {Model: "claude-sonnet-4-6", MaxTokens: 64000, MaxContext: 1000000, Protocol: "anthropic"},
		"openai":    {Model: "gpt-5.4", MaxTokens: 128000, MaxContext: 1050000, Protocol: "openai"},
		"google":    {Model: "gemini-3.1-pro-preview-customtools", MaxTokens: 65536, MaxContext: 1048576, Protocol: "google"},
		"deepseek":  {Model: "deepseek-chat", BaseURL: "https://api.deepseek.com", MaxTokens: 8192, MaxContext: 131072, Protocol: "openai-compatible"},
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
	if model, ok := lookupModelsDevModel(provider, modelsDevSeedProfiles()[providerName].Model); ok {
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

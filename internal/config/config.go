package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	APIKey    string `yaml:"api_key,omitempty"`
	BaseURL   string `yaml:"base_url,omitempty"`
	Model     string `yaml:"model,omitempty"`
	MaxTokens int    `yaml:"max_tokens,omitempty"`
	Region    string `yaml:"region,omitempty"`
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

// config_types.go — pure data definitions for the YAML config tree.
// Every struct here maps 1:1 onto a block under .dfmc/config.yaml.
// Behaviour lives elsewhere: Load / Save / path helpers in
// config.go, env hydration in config_env.go, models.dev integration
// in config_models_dev.go, defaults in defaults.go, validation in
// validator.go.
//
// Extracted to separate files to keep this file readable:
//   config_routing.go  — RoutingConfig, RoutingRule, PipelineConfig, PipelineStep
//   config_memory.go   — MemoryConfig
//   config_model.go    — ModelConfig struct and its accessor methods

package config

type Config struct {
	Version     int                       `yaml:"version"`
	Providers   ProvidersConfig           `yaml:"providers"`
	Routing     RoutingConfig             `yaml:"routing"`
	Pipelines   map[string]PipelineConfig `yaml:"pipelines"`
	Context     ContextConfig             `yaml:"context"`
	Memory      MemoryConfig              `yaml:"memory"`
	Security    SecurityConfig            `yaml:"security"`
	Tools       ToolsConfig               `yaml:"tools"`
	Agent       AgentConfig               `yaml:"agent"`
	Hooks       HooksConfig               `yaml:"hooks"`
	Plugins     PluginsConfig             `yaml:"plugins"`
	TUI         TUIConfig                 `yaml:"tui"`
	Web         WebConfig                 `yaml:"web"`
	Remote      RemoteConfig              `yaml:"remote"`
	Project     ProjectConfig             `yaml:"project"`
	Coach       CoachConfig               `yaml:"coach"`
	Intent      IntentConfig              `yaml:"intent"`
	AST         ASTConfig                 `yaml:"ast"`
	MCP         MCPConfig                 `yaml:"mcp"`
	Telegram    TelegramConfig            `yaml:"telegram"`
	DataDirPath string                    `yaml:"data_dir"`
}

// TelegramConfig holds Telegram bot integration settings.
type TelegramConfig struct {
	Enabled      bool    `yaml:"enabled"`
	Token        string  `yaml:"token"`
	AllowedUsers []int64 `yaml:"allowed_users"` // Telegram user IDs that can chat with this instance
	SessionName  string  `yaml:"session_name"`  // Display name for this DFMC instance (e.g. "work", "home")
}

type ASTConfig struct {
	CacheSize int `yaml:"cache_size"`
}

type CoachConfig struct {
	Enabled  bool `yaml:"enabled"`
	MaxNotes int  `yaml:"max_notes"`
}

type IntentConfig struct {
	Enabled          bool   `yaml:"enabled"`
	Provider         string `yaml:"provider"`
	Model            string `yaml:"model"`
	TimeoutMs        int    `yaml:"timeout_ms"`
	FailOpen         bool   `yaml:"fail_open"`
	MaxSnapshotChars int    `yaml:"max_snapshot_chars"`
}

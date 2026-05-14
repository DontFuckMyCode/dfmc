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
	ProjectRoot string                    `yaml:"project_root"` // set at load time via FindProjectRoot
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
	Codemap     CodemapConfig             `yaml:"codemap"`
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

// CodemapConfig controls how the codemap engine parses and indexes source files.
type CodemapConfig struct {
	// Parallel enables concurrent file parsing (uses runtime.NumCPU workers).
	Parallel bool `yaml:"parallel"`
	// WorkerCount overrides the number of parallel workers. 0 = runtime.NumCPU().
	WorkerCount int `yaml:"worker_count"`
	// MaxFileSizeMB skips files larger than this threshold.
	MaxFileSizeMB int `yaml:"max_file_size_mb"`
	// IgnorePatterns are glob patterns excluded from parsing.
	IgnorePatterns []string `yaml:"ignore_patterns"`
}

type CoachConfig struct {
	Enabled  bool `yaml:"enabled"`
	MaxNotes int  `yaml:"max_notes"`
}

type IntentConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	TimeoutMs int    `yaml:"timeout_ms"`
	FailOpen  bool   `yaml:"fail_open"`
	// FailClosed, when true, changes the error-reporting behavior of
	// the intent layer. Instead of returning Fallback(raw)+nil (fail-open
	// semantic), the router returns Fallback(raw)+err so callers can
	// distinguish classifier errors from ordinary fallback routing and
	// emit a structured intent:error event. The routing decision itself
	// is still safe (Fallback); only the error channel changes. Default
	// is false to preserve backward compatibility.
	FailClosed       bool `yaml:"fail_closed"`
	MaxSnapshotChars int  `yaml:"max_snapshot_chars"`
}

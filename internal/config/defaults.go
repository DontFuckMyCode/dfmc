package config

import "path/filepath"

func DefaultConfig() *Config {
	return &Config{
		Version: DefaultVersion,
		Providers: ProvidersConfig{
			Primary:  "anthropic",
			Fallback: []string{"openai", "deepseek"},
			Profiles: map[string]ModelConfig{
				"anthropic": {Model: "claude-sonnet-4-6", MaxTokens: 16384},
				"openai":    {Model: "gpt-5.4", MaxTokens: 8192},
				"google":    {Model: "gemini-3.1-pro", MaxTokens: 8192},
				"deepseek":  {Model: "deepseek-chat", MaxTokens: 8192},
				"kimi":      {Model: "kimi-k2.5", MaxTokens: 8192},
				"minimax":   {Model: "minimax-m2.7", MaxTokens: 8192},
				"zai":       {Model: "glm-5", MaxTokens: 8192},
				"alibaba":   {Model: "qwen3.5-plus", MaxTokens: 8192},
				"generic":   {Model: "qwen3.5-coder", BaseURL: "http://localhost:11434/v1", MaxTokens: 8192},
			},
		},
		Context: ContextConfig{
			MaxFiles:         50,
			MaxTokensPerFile: 2000,
			Compression:      "standard",
			IncludeTests:     false,
			IncludeDocs:      true,
		},
		Memory: MemoryConfig{
			Enabled:               true,
			MaxEpisodic:           1000,
			MaxSemantic:           5000,
			ConsolidationInterval: "30m",
			DecayRate:             0.01,
		},
		Security: SecurityConfig{
			SecretDetection: true,
			VulnScanning:    true,
			DepAudit:        true,
			Sandbox: SandboxConfig{
				AllowShell: true,
				AllowNet:   false,
				Timeout:    "30s",
				MaxOutput:  "100KB",
			},
		},
		Tools: ToolsConfig{
			Enabled: []string{"file_ops", "shell", "git", "search", "code_edit", "web"},
			Shell: ShellToolConfig{
				BlockedCommands: []string{"rm -rf /", "mkfs", "dd if="},
				Timeout:         "30s",
			},
		},
		Hooks: HooksConfig{Entries: map[string][]HookEntry{}},
		Plugins: PluginsConfig{
			Directory: filepath.Join(UserConfigDir(), "plugins"),
			Enabled:   []string{},
		},
		TUI: TUIConfig{
			Theme:      "dark",
			VimKeys:    true,
			ShowTokens: true,
		},
		Web: WebConfig{
			Port:        7777,
			Host:        "127.0.0.1",
			Auth:        "none",
			OpenBrowser: true,
		},
		Remote: RemoteConfig{
			Enabled:  false,
			GRPCPort: 7778,
			WSPort:   7779,
			Auth:     "token",
		},
	}
}

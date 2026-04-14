package config

import "path/filepath"

func DefaultConfig() *Config {
	profiles := modelsDevSeedProfiles()
	profiles["generic"] = ModelConfig{
		Model:      "qwen3.5-coder",
		BaseURL:    "http://localhost:11434/v1",
		MaxTokens:  8192,
		MaxContext: 128000,
		Protocol:   "openai-compatible",
	}
	return &Config{
		Version: DefaultVersion,
		Providers: ProvidersConfig{
			Primary:  "anthropic",
			Fallback: []string{"openai", "deepseek"},
			Profiles: profiles,
		},
		Context: ContextConfig{
			MaxFiles:         50,
			MaxTokensTotal:   16000,
			MaxTokensPerFile: 2000,
			MaxHistoryTokens: 1200,
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

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
			SymbolAware:      true,
			GraphDepth:       2,
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
		Agent: AgentConfig{
			// Sustained-loop defaults. Earlier defaults (25/5/7) were tuned
			// for short Q&A turns and choked on real refactor work — the
			// model would land in a synthesis nudge after 5 tool calls and
			// be forbidden from calling more after 7, so multi-step
			// orchestration like "read N files, edit M, verify, edit
			// again" stopped halfway. Modern context windows + provider
			// pricing easily support the higher numbers below; users on
			// tight budgets can override per-project in .dfmc/config.yaml.
			MaxToolSteps:                60,
			MaxToolTokens:               250000,
			MaxToolResultChars:          3200,
			MaxToolResultDataChars:      1200,
			ParallelBatchSize:           4,
			ToolRoundSoftCap:            15,
			ToolRoundHardCap:            30,
			BudgetHeadroomDivisor:       7,
			ElasticToolTokensRatio:      0.60,
			ElasticToolResultCharsRatio: 1.0 / 40.0,
			ElasticToolDataCharsRatio:   1.0 / 100.0,
			// "auto" = autonomous park→compact→resume inside the same Ask
			// call. The cumulative ResumeMaxMultiplier ceiling still bounds
			// total work. Set to "off" in CI / cost-sensitive contexts to
			// require an explicit /continue between budgets.
			AutonomousResume:   "auto",
			AutonomousPlanning: "auto",
			ToolReasoning:      "auto",
			ContextLifecycle: ContextLifecycleConfig{
				Enabled:                   true,
				AutoCompactThresholdRatio: 0.7,
				KeepRecentRounds:          3,
				HandoffBriefMaxTokens:     500,
				AutoHandoffThresholdRatio: 0.9,
			},
		},
		Hooks: HooksConfig{
			AllowProject: false,
			Entries:      map[string][]HookEntry{},
		},
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
		Coach: CoachConfig{
			Enabled:  true,
			MaxNotes: 3,
		},
		Intent: IntentConfig{
			Enabled:          true,
			TimeoutMs:        1500,
			FailOpen:         true,
			MaxSnapshotChars: 2000,
		},
	}
}

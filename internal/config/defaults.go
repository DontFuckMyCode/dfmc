package config

import "path/filepath"

func DefaultConfig() *Config {
	profiles := ModelsDevSeedProfiles()
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
			Primary:  "minimax",
			Fallback: []string{"openai", "deepseek"},
			Profiles: profiles,
		},
		Context: ContextConfig{
			// Sized for modern 200K-1M providers (Opus 4.7, GPT-5.4,
			// Sonnet 4.6) where prompt caching subsidizes stable
			// prefixes. The earlier 50-files × 320-tokens "file scrap"
			// failure mode came from a tiny per-file budget, not from
			// the file count itself; here we keep the count generous
			// and the per-file budget meaningful so injects are
			// useful evidence, not unreadable slices.
			MaxFiles:         20,
			MaxTokensTotal:   12000,
			MaxTokensPerFile: 2000,
			// History budget — the work narrative the model threads
			// across turns. Prompt caching on the conversation prefix
			// makes carrying ~24K of history cheap on modern
			// providers; capping low (the old 2K) starved the
			// reasoning thread and forced the model to re-discover
			// context every round.
			MaxHistoryTokens:   24000,
			MaxHistoryMessages: 120,
			Compression:        "standard",
			// AutoIncludeFiles=false: a tool-using model should
			// retrieve its own context via grep_codebase / find_symbol
			// / read_file when it needs files, NOT have N random
			// scraps dumped into the prompt before the question even
			// runs. Pre-loading hurts signal-to-noise (the model sees
			// 20 partial files it didn't ask for) AND wastes the
			// cached prefix on stuff that may be irrelevant to the
			// current turn. Users who DO want a specific file pulled
			// in opt back in per-turn with `[[file:path]]` markers or
			// `#ctx-files` / `#context:on` flags in the prompt.
			AutoIncludeFiles: false,
			IncludeTests:     false,
			IncludeDocs:      true,
			SymbolAware:      true,
			GraphDepth:       2,
		},
		Memory: MemoryConfig{
			Enabled:                true,
			MaxEpisodic:            1000,
			MaxSemantic:            5000,
			ConsolidationInterval:  "30m",
			DecayRate:              0.01,
			EnableLLMUpdate:        true,
			LLMUpdateProvider:      "",
			LLMUpdateModel:         "",
			LLMUpdatePrompt:        "Based on the following conversation turn, suggest up to {max_entries} memory entries to store. Each entry should be a concise fact or insight relevant to future sessions. Respond in JSON format: [{\"type\": \"episodic\"|\"semantic\", \"content\": \"...\", \"relevance\": 0.0-1.0}].\n\nTurn:\n{turn_content}\n\nSuggested entries:",
			LLMUpdateTimeoutMs:     10000,
			LLMUpdateMaxEntries:    5,
			LLMUpdateMinConfidence: 0.5,
			LLMUpdateEnabled:       true,
			LLMUpdateThreshold:     0.6,
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
			// RequireApprovalNetwork defaults to ["*"] so any non-user source
			// (web, ws, mcp) is gated by default. Operators who trust their
			// local browser tab can set this to [] to disable the network gate,
			// or to specific tool names. The agent-loop RequireApproval stays
			// empty so existing automation isn't broken by default.
			RequireApprovalNetwork: []string{"*"},
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
			MetaCallBudget:              64,
			MetaDepthLimit:              4,
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
			// "auto" = the engine self-resumes after a final assistant
			// answer if the model didn't emit `[done: true]`. The first
			// `[next: …]` action becomes the next user prompt. Capped by
			// MaxAutoContinueIterations so a runaway model can't burn a
			// whole budget on its own. Set to "off" for legacy behavior
			// (user types each follow-up manually).
			AutoContinue:              "auto",
			MaxAutoContinueIterations: 5,
			ContextLifecycle: ContextLifecycleConfig{
				Enabled:                   true,
				AutoCompactThresholdRatio: 0.7,
				KeepRecentRounds:          3,
				HandoffBriefMaxTokens:     500,
				AutoHandoffThresholdRatio: 0.9,
			},
			ToolDefaultTimeoutSeconds: 30,
			ReadSnapshotCap:           256,
			RecentFailureCap:          256,
			// ChatAutoDecompose: routes multi-step chat prompts to Drive if the task
			// looks like it needs multiple steps. Single-step questions stay with Ask().
			ChatAutoDecompose: true,
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
			Theme:                 "dark",
			VimKeys:               true,
			ShowTokens:            true,
			ShowStatsPanel:        true,
			ToolStripExpanded:     true,
			GitDiffTimeoutSeconds: 30,
			MouseCapture:          true,
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
		Telegram: TelegramConfig{
			Enabled:      false,
			Token:        "",
			AllowedUsers: []int64{},
			SessionName:  "dfmc",
		},
	}
}

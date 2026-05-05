package config

type ProvidersConfig struct {
	Primary  string                 `yaml:"primary"`
	Fallback []string               `yaml:"fallback"`
	Profiles map[string]ModelConfig `yaml:"profiles"`
}

type ContextConfig struct {
	MaxFiles         int    `yaml:"max_files"`
	MaxTokensTotal   int    `yaml:"max_tokens_total"`
	MaxTokensPerFile int    `yaml:"max_tokens_per_file"`
	MaxHistoryTokens int    `yaml:"max_history_tokens"`
	Compression      string `yaml:"compression"`
	IncludeTests     bool   `yaml:"include_tests"`
	IncludeDocs      bool   `yaml:"include_docs"`
	SymbolAware      bool   `yaml:"symbol_aware"`
	GraphDepth       int    `yaml:"graph_depth"`
}

type SecurityConfig struct {
	SecretDetection bool          `yaml:"secret_detection"`
	VulnScanning    bool          `yaml:"vuln_scanning"`
	DepAudit        bool          `yaml:"dep_audit"`
	Sandbox         SandboxConfig `yaml:"sandbox"`
}

type SandboxConfig struct {
	AllowCommand *bool  `yaml:"allow_command,omitempty"`
	AllowShell   bool   `yaml:"allow_shell"`
	AllowNet     bool   `yaml:"allow_network"`
	Timeout      string `yaml:"timeout"`
	MaxOutput    string `yaml:"max_output"`
}

type ToolsConfig struct {
	Enabled                []string           `yaml:"enabled"`
	Shell                  ShellToolConfig    `yaml:"shell"`
	Extra                  map[string]any     `yaml:"extra,omitempty"`
	Params                 map[string]string  `yaml:"params,omitempty"`
	Flags                  map[string]bool    `yaml:"flags,omitempty"`
	Limits                 map[string]float64 `yaml:"limits,omitempty"`
	RequireApproval        []string           `yaml:"require_approval,omitempty"`
	RequireApprovalNetwork []string           `yaml:"require_approval_network,omitempty"`
}

type ShellToolConfig struct {
	BlockedCommands []string `yaml:"blocked_commands"`
	Timeout         string   `yaml:"timeout"`
}

type AgentConfig struct {
	MaxToolSteps                int                    `yaml:"max_tool_steps"`
	MaxToolTokens               int                    `yaml:"max_tool_tokens"`
	MaxToolResultChars          int                    `yaml:"max_tool_result_chars"`
	MaxToolResultDataChars      int                    `yaml:"max_tool_result_data_chars"`
	ParallelBatchSize           int                    `yaml:"parallel_batch_size"`
	MetaCallBudget              int                    `yaml:"meta_call_budget"`
	MetaDepthLimit              int                    `yaml:"meta_depth_limit"`
	ToolRoundSoftCap            int                    `yaml:"tool_round_soft_cap"`
	ToolRoundHardCap            int                    `yaml:"tool_round_hard_cap"`
	BudgetHeadroomDivisor       int                    `yaml:"budget_headroom_divisor"`
	ElasticToolTokensRatio      float64                `yaml:"elastic_tool_tokens_ratio"`
	ElasticToolResultCharsRatio float64                `yaml:"elastic_tool_result_chars_ratio"`
	ElasticToolDataCharsRatio   float64                `yaml:"elastic_tool_data_chars_ratio"`
	ResumeMaxMultiplier         int                    `yaml:"resume_max_multiplier"`
	ToolReasoning               string                 `yaml:"tool_reasoning"`
	AutonomousResume            string                 `yaml:"autonomous_resume"`
	AutonomousPlanning          string                 `yaml:"autonomous_planning"`
	ContextLifecycle            ContextLifecycleConfig `yaml:"context_lifecycle"`
	ToolDefaultTimeoutSeconds   int                    `yaml:"tool_default_timeout_seconds"`
	ToolTimeouts                map[string]int         `yaml:"tool_timeouts"`
	RangeCachePerPath           int                    `yaml:"range_cache_per_path"`
	RetryWindowSize             int                    `yaml:"retry_window_size"`
}

type ContextLifecycleConfig struct {
	Enabled                   bool    `yaml:"enabled"`
	AutoCompactThresholdRatio float64 `yaml:"auto_compact_threshold_ratio"`
	KeepRecentRounds          int     `yaml:"keep_recent_rounds"`
	HandoffBriefMaxTokens     int     `yaml:"handoff_brief_max_tokens"`
	AutoHandoffThresholdRatio float64 `yaml:"auto_handoff_threshold_ratio"`
}

type HooksConfig struct {
	AllowProject bool                   `yaml:"allow_project,omitempty"`
	Entries      map[string][]HookEntry `yaml:",inline"`
}

type HookEntry struct {
	Name      string   `yaml:"name"`
	Condition string   `yaml:"condition,omitempty"`
	Command   string   `yaml:"command"`
	Args      []string `yaml:"args,omitempty"`
	Shell     *bool    `yaml:"shell,omitempty"`
}

type PluginsConfig struct {
	Directory string   `yaml:"directory"`
	Enabled   []string `yaml:"enabled"`
}

type TUIConfig struct {
	Theme                 string `yaml:"theme"`
	VimKeys               bool   `yaml:"vim_keys"`
	ShowTokens            bool   `yaml:"show_tokens"`
	ToolStripExpanded     bool   `yaml:"tool_strip_expanded"`
	GitDiffTimeoutSeconds int    `yaml:"git_diff_timeout_seconds"`
	MouseCapture          bool   `yaml:"mouse_capture"`
}

type WebConfig struct {
	Port           int      `yaml:"port"`
	Host           string   `yaml:"host"`
	Auth           string   `yaml:"auth"`
	OpenBrowser    bool     `yaml:"open_browser"`
	AllowedOrigins []string `yaml:"allowed_origins"`
	AllowedHosts   []string `yaml:"allowed_hosts"`
	TrustedProxies []string `yaml:"trusted_proxies"`
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

type MCPConfig struct {
	Servers []MCPServerConfig `yaml:"servers"`
}

type MCPServerConfig struct {
	Name    string            `yaml:"name"`
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
}

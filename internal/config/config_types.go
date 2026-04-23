// config_types.go — pure data definitions for the YAML config tree.
// Every struct here maps 1:1 onto a block under .dfmc/config.yaml.
// Behaviour lives elsewhere: Load / Save / path helpers in
// config.go, env hydration in config_env.go, models.dev integration
// in config_models_dev.go, defaults in defaults.go, validation in
// validator.go. The only methods colocated here are the tiny
// ModelConfig accessors (BestModel, AllModels) — they're pure field
// readers that keep the backward-compat single-Model alias behaviour
// next to the field it reads.

package config

type Config struct {
	Version   int             `yaml:"version"`
	Providers ProvidersConfig           `yaml:"providers"`
	Routing   RoutingConfig             `yaml:"routing"`
	Pipelines map[string]PipelineConfig `yaml:"pipelines"`
	Context   ContextConfig   `yaml:"context"`
	Memory    MemoryConfig    `yaml:"memory"`
	Security  SecurityConfig  `yaml:"security"`
	Tools     ToolsConfig     `yaml:"tools"`
	Agent     AgentConfig     `yaml:"agent"`
	Hooks     HooksConfig     `yaml:"hooks"`
	Plugins   PluginsConfig   `yaml:"plugins"`
	TUI       TUIConfig       `yaml:"tui"`
	Web       WebConfig       `yaml:"web"`
	Remote    RemoteConfig    `yaml:"remote"`
	Project   ProjectConfig   `yaml:"project"`
	Coach     CoachConfig     `yaml:"coach"`
	Intent    IntentConfig    `yaml:"intent"`
	AST       ASTConfig       `yaml:"ast"`
}

// ASTConfig holds runtime knobs for the AST engine. Today only the
// parse-cache capacity is exposed — large monorepos may want to raise
// it (the working set can easily exceed the 10K default once codemap
// rebuilds + on-demand find_symbol lookups overlap), and embedded
// deployments may want to lower it to keep RSS predictable.
type ASTConfig struct {
	// CacheSize bounds the LRU parse cache (entries). Zero falls back
	// to the package default. Values <= 0 are treated as "use default"
	// rather than "disable", since disabling caching would make codemap
	// rebuilds 100x slower without warning.
	CacheSize int `yaml:"cache_size"`
}

// CoachConfig governs the background "tiny-touches" observer that publishes
// coach:note events at the end of each agent turn. Rule-based today, the
// default Enabled=true costs nothing (microseconds, zero network). Set
// Enabled=false to silence the TUI coach role entirely.
type CoachConfig struct {
	Enabled  bool `yaml:"enabled"`
	MaxNotes int  `yaml:"max_notes"`
}

// IntentConfig governs the state-aware request normalizer that runs before
// every Ask. Given a compact snapshot of engine state (parked? last tool?
// last assistant turn?), a cheap LLM rewrites the user's raw input into an
// unambiguous, fully contextualized prompt and decides whether to resume
// the parked agent or start a fresh turn. Designed to be fail-open: any
// timeout/error/missing provider falls back to passing the raw message
// through, so a flaky intent layer never blocks the user.
//
// Provider/Model: when empty, the router picks the cheapest available
// completion-capable provider in this priority order: anthropic (Haiku),
// openai (gpt-4o-mini), gemini (flash), then anything else. If none of
// those are configured the layer is silently disabled — better to skip
// than to block on the offline placeholder.
type IntentConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	TimeoutMs int    `yaml:"timeout_ms"`
	// FailOpen when true (default) lets the raw user message pass through
	// the engine when the intent LLM errors or times out. Set false in
	// hardened environments where you'd rather surface the failure than
	// silently degrade — useful for debugging.
	FailOpen bool `yaml:"fail_open"`
	// MaxSnapshotChars caps the size of the engine-state snapshot string
	// sent to the intent LLM. Larger snapshots give better context but cost
	// more tokens per turn. 0 falls back to 2000 chars (~500 tokens).
	MaxSnapshotChars int `yaml:"max_snapshot_chars"`
}

// AgentConfig bounds the native tool loop so a runaway model can't drain a
// budget. All fields have defaults in DefaultConfig; zero values fall back.
type AgentConfig struct {
	// MaxToolSteps caps the number of model<->tool round-trips.
	MaxToolSteps int `yaml:"max_tool_steps"`
	// MaxToolTokens is the hard token budget across all provider calls in a
	// single agent loop. Zero disables the budget.
	MaxToolTokens int `yaml:"max_tool_tokens"`
	// MaxToolResultChars trims the text output sent back as tool_result.
	MaxToolResultChars int `yaml:"max_tool_result_chars"`
	// MaxToolResultDataChars trims the JSON `data` payload of tool_result.
	MaxToolResultDataChars int `yaml:"max_tool_result_data_chars"`
	// ParallelBatchSize caps the concurrency of tool_batch_call dispatch.
	// Reserved for S4; not consumed yet.
	ParallelBatchSize int `yaml:"parallel_batch_size"`

	// MetaCallBudget caps the cumulative number of backend tool calls any
	// single agent turn may dispatch via the meta tools (tool_call /
	// tool_batch_call) combined. 0 falls back to 64. Raise for long-running
	// orchestration sessions that fan out a lot (e.g. Drive runs with many
	// subagents); the ceiling guards against runaway planner loops that
	// produce pathological batches.
	MetaCallBudget int `yaml:"meta_call_budget"`
	// MetaDepthLimit caps how deeply meta tools can nest in a single turn.
	// 0 falls back to 4. Nesting beyond this is almost always a model error
	// (meta-in-meta) so the ceiling should rarely need raising.
	MetaDepthLimit int `yaml:"meta_depth_limit"`

	// ToolRoundSoftCap is the round count at which the loop injects a single
	// synthesis nudge ("you have enough context, answer now"). Tuned below
	// MaxToolSteps so a model stuck in a read-read-read loop gets one firm
	// redirect before the hard cap takes tool_use away. 0 falls back to 5.
	ToolRoundSoftCap int `yaml:"tool_round_soft_cap"`
	// ToolRoundHardCap flips ToolChoice to "none" for every subsequent call,
	// forcing natural-language text. 0 falls back to 7. Spaced two rounds
	// above the soft cap so the nudge has room to land.
	ToolRoundHardCap int `yaml:"tool_round_hard_cap"`
	// BudgetHeadroomDivisor reserves 1/N of MaxTokens as a safety margin
	// before each round starts so the post-round gate can't lose budget
	// races. 0 falls back to 7 (~14% headroom).
	BudgetHeadroomDivisor int `yaml:"budget_headroom_divisor"`
	// ElasticToolTokensRatio is the share of provider_max_context the native
	// tool loop may spend across the whole run when the provider exposes a
	// context window. 0 falls back to 0.60.
	ElasticToolTokensRatio float64 `yaml:"elastic_tool_tokens_ratio"`
	// ElasticToolResultCharsRatio caps a single tool_result text payload as a
	// share of provider_max_context. 0 falls back to 1/40 (~2.5%).
	ElasticToolResultCharsRatio float64 `yaml:"elastic_tool_result_chars_ratio"`
	// ElasticToolDataCharsRatio caps the JSON sidecar of a single tool_result
	// as a share of provider_max_context. 0 falls back to 1/100 (1%).
	ElasticToolDataCharsRatio float64 `yaml:"elastic_tool_data_chars_ratio"`

	// ResumeMaxMultiplier caps cumulative agent work across every
	// /continue (or natural-language "devam") of a single root ask. Each
	// resume gets a fresh MaxToolSteps budget, but cumulative steps and
	// tokens accumulate; once they pass MaxToolSteps × N (this value),
	// further resumes are refused. 0 falls back to 10 — enough for a
	// 600-step / ~2.5M-token sustained orchestration session. Raise
	// per-project (e.g. 30) for unattended overnight runs; tighten to 1
	// or 2 in CI environments that must hard-stop after one budget.
	ResumeMaxMultiplier int `yaml:"resume_max_multiplier"`

	// ToolReasoning controls the per-tool-call self-narration surface:
	// every tool's JSON schema gets an optional virtual `_reason` field
	// the model can fill with a one-sentence why ("reading config to
	// locate the API key"). The engine strips it before dispatch and
	// publishes a tool:reasoning event so UIs can render the why above
	// each tool result.
	//
	// Values: "auto" / "on" / "" / "true" → enabled. "off" / "false" /
	// "no" / "0" → disabled (no event publish; the schema still includes
	// the field as a no-op so models that already learned the convention
	// don't fail). Default "auto" = on.
	ToolReasoning string `yaml:"tool_reasoning"`

	// AutonomousResume controls whether a budget-exhausted park triggers
	// an automatic compact-and-resume from inside the same Ask call -
	// the user sees one continuous response instead of having to type
	// /continue (or "devam") between every park. Bounded by the same
	// ResumeMaxMultiplier ceiling so a runaway loop can't go forever.
	// Set to "off" / "false" to require an explicit user resume each
	// time. Empty / unset / "on" / "auto" all enable autonomy. Default
	// is autonomous because the manual-resume UX bleeds the cache and
	// breaks flow on every park; the cumulative ceiling already protects
	// against cost runaway.
	AutonomousResume string `yaml:"autonomous_resume"`

	// AutonomousPlanning controls the deterministic preflight that runs on
	// fresh native-tool asks before the model's first round. When enabled,
	// DFMC splits obviously multi-part requests up-front, seeds the session
	// todo list, and injects a dynamic system note nudging the model toward
	// orchestrate/delegate fan-out instead of serializing everything in one
	// giant loop. Empty / unset / "on" / "auto" enable it; "off" / "false"
	// / "no" / "0" disable it.
	AutonomousPlanning string `yaml:"autonomous_planning"`

	// ContextLifecycle governs offline auto-compaction of in-loop history so
	// token spend stays flat even across many tool rounds. Strictly offline -
	// no extra LLM call - to honour DFMC's token-miser promise.
	ContextLifecycle ContextLifecycleConfig `yaml:"context_lifecycle"`
}

type ContextLifecycleConfig struct {
	// Enabled toggles auto-compaction. Default true; set false to always send
	// the full rolling history to the provider.
	Enabled bool `yaml:"enabled"`
	// AutoCompactThresholdRatio is the share of the provider's context window
	// (estimated tokens / provider_max_context) above which auto-compact
	// fires. 0.7 means compact when >70% of the window is in use.
	AutoCompactThresholdRatio float64 `yaml:"auto_compact_threshold_ratio"`
	// KeepRecentRounds is the number of most-recent tool rounds preserved
	// verbatim when compacting. Older rounds collapse into a single summary.
	KeepRecentRounds int `yaml:"keep_recent_rounds"`
	// HandoffBriefMaxTokens caps the size of the auto-new-session handoff
	// brief injected as seed context when auto-new-session fires.
	HandoffBriefMaxTokens int `yaml:"handoff_brief_max_tokens"`
	// AutoHandoffThresholdRatio is the share of the provider's context window
	// above which — even after compaction — a fresh conversation is started
	// seeded with a handoff brief. Must be strictly above
	// AutoCompactThresholdRatio so compaction always gets the first try.
	// Default 0.9 (trip when >90% of the window is still in use).
	AutoHandoffThresholdRatio float64 `yaml:"auto_handoff_threshold_ratio"`
}

type ProvidersConfig struct {
	Primary  string                 `yaml:"primary"`
	Fallback []string               `yaml:"fallback"`
	Profiles map[string]ModelConfig `yaml:"profiles"`
}

type ModelConfig struct {
	APIKey      string   `yaml:"api_key,omitempty"`
	BaseURL     string   `yaml:"base_url,omitempty"`
	Models      []string `yaml:"models,omitempty"`           // ordered list, first = preferred
	FallbackModels []string `yaml:"fallback_models,omitempty"` // tried in order when preferred model fails
	Model      string   `yaml:"model,omitempty"`            // deprecated single-model alias (backward compat)
	MaxTokens   int      `yaml:"max_tokens,omitempty"`
	MaxContext  int      `yaml:"max_context,omitempty"`
	Protocol    string   `yaml:"protocol,omitempty"`
	Region      string   `yaml:"region,omitempty"`
	HTTPTimeout int      `yaml:"http_timeout,omitempty"` // response header timeout in seconds; 0 uses default (180s)
}

// BestModel returns the preferred model: Models[0] if set, otherwise Model.
// This preserves backward compat where only Model (singular) was available.
func (c ModelConfig) BestModel() string {
	if len(c.Models) > 0 {
		return c.Models[0]
	}
	return c.Model
}

// AllModels returns the full ordered model list: Models if set,
// otherwise a single-element list of Model (for backward compat).
func (c ModelConfig) AllModels() []string {
	if len(c.Models) > 0 {
		return c.Models
	}
	if c.Model != "" {
		return []string{c.Model}
	}
	return nil
}

type RoutingConfig struct {
	Rules []RoutingRule `yaml:"rules"`
}

type RoutingRule struct {
	Condition string `yaml:"condition"`
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model,omitempty"`
}

// PipelineConfig is a named ordered chain of provider+model steps.
// When active, the engine walks the steps in order on failure.
type PipelineConfig struct {
	Steps []PipelineStep `yaml:"steps"`
}

type PipelineStep struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

type ContextConfig struct {
	MaxFiles         int    `yaml:"max_files"`
	MaxTokensTotal   int    `yaml:"max_tokens_total"`
	MaxTokensPerFile int    `yaml:"max_tokens_per_file"`
	MaxHistoryTokens int    `yaml:"max_history_tokens"`
	Compression      string `yaml:"compression"`
	IncludeTests     bool   `yaml:"include_tests"`
	IncludeDocs      bool   `yaml:"include_docs"`
	// SymbolAware enables codemap-driven retrieval (extract identifiers
	// from the query, resolve via AST symbol nodes, walk import graph
	// to pull sibling files). Default on via BuildDefault.
	SymbolAware bool `yaml:"symbol_aware"`
	// GraphDepth bounds how many hops the import-graph walker takes
	// from each resolved seed file. 0 disables expansion; 2 is the
	// default and captures direct sibling files without pulling in
	// every transitive dependency.
	GraphDepth int `yaml:"graph_depth"`
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
	// AllowCommand is the preferred spelling for the run_command kill-switch.
	// When false, DFMC disables the entire run_command tool, not just shell
	// interpreters. The legacy allow_shell key is still accepted for backward
	// compatibility and maps to the same canonical boolean.
	AllowCommand *bool `yaml:"allow_command,omitempty"`
	// AllowShell is the legacy alias for AllowCommand. Keep it wired so older
	// configs keep working, but prefer allow_command in docs and examples.
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
	// RequireApproval is the list of tool names that require human
	// approval before the engine dispatches them — only consulted for
	// non-user sources (agent, subagent). User-initiated CallTool
	// invocations bypass the gate, since the user already typed the
	// command. "*" matches every tool. Empty list disables the gate.
	RequireApproval []string `yaml:"require_approval,omitempty"`
}

type ShellToolConfig struct {
	BlockedCommands []string `yaml:"blocked_commands"`
	Timeout         string   `yaml:"timeout"`
}

type HooksConfig struct {
	// AllowProject opts into loading hook entries from the project's
	// `.dfmc/config.yaml`. Default false: repository-local shell hooks are
	// treated as untrusted until the operator enables them from a global
	// config they control.
	AllowProject bool                   `yaml:"allow_project,omitempty"`
	Entries      map[string][]HookEntry `yaml:",inline"`
}

type HookEntry struct {
	Name      string `yaml:"name"`
	Condition string `yaml:"condition,omitempty"`
	// Command is either the full shell command (legacy/default mode) or the
	// executable path/name when Args or Shell=false are set.
	Command string `yaml:"command"`
	// Args enables shell-free hook execution. When non-empty, DFMC invokes
	// Command directly as argv instead of routing through sh/cmd unless
	// Shell=true is set explicitly for backwards compatibility.
	Args []string `yaml:"args,omitempty"`
	// Shell controls whether DFMC wraps Command in the platform shell.
	// Nil preserves the historical default (true unless Args are present).
	Shell *bool `yaml:"shell,omitempty"`
}

type PluginsConfig struct {
	Directory string   `yaml:"directory"`
	Enabled   []string `yaml:"enabled"`
}

type TUIConfig struct {
	Theme      string `yaml:"theme"`
	VimKeys    bool   `yaml:"vim_keys"`
	ShowTokens bool   `yaml:"show_tokens"`
	// MouseCapture toggles bubbletea's mouse-event capture. When true
	// the wheel scrolls the transcript natively but the terminal's
	// drag-to-select / right-click-copy is disabled — most terminals
	// let Shift+drag bypass this. Default is true because wheel-scroll
	// is what people reach for in a full-screen TUI; users who prefer
	// native drag-to-select can flip it off with /mouse or set
	// tui.mouse_capture: false in .dfmc/config.yaml.
	MouseCapture bool `yaml:"mouse_capture"`
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

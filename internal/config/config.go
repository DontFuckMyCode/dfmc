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
	"strconv"
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
	// an automatic compact-and-resume from inside the same Ask call —
	// the user sees one continuous response instead of having to type
	// /continue (or "devam") between every park. Bounded by the same
	// ResumeMaxMultiplier ceiling so a runaway loop can't go forever.
	// Set to "off" / "false" to require an explicit user resume each
	// time. Empty / unset / "on" / "auto" all enable autonomy. Default
	// is autonomous because the manual-resume UX bleeds the cache and
	// breaks flow on every park; the cumulative ceiling already protects
	// against cost runaway.
	AutonomousResume string `yaml:"autonomous_resume"`

	// ContextLifecycle governs offline auto-compaction of in-loop history so
	// token spend stays flat even across many tool rounds. Strictly offline —
	// no extra LLM call — to honour DFMC's token-miser promise.
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
	// MouseCapture toggles bubbletea's mouse-event capture. When true
	// the wheel scrolls the transcript natively but the terminal's
	// drag-to-select / right-click-copy is disabled — most terminals
	// let Shift+drag bypass this. Default is false so copy/paste
	// "just works" out of the box; users who prefer wheel scroll can
	// opt in.
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
	globalHooks := cloneHooksConfig(cfg.Hooks)
	allowProjectHooks := cfg.Hooks.AllowProject

	projectPath := opts.ProjectPath
	projectRoot := FindProjectRoot(opts.CWD)
	if projectPath == "" {
		if projectRoot != "" {
			projectPath = filepath.Join(projectRoot, DefaultDirName, "config.yaml")
		}
	} else if projectRoot == "" {
		projectRoot = filepath.Dir(filepath.Dir(projectPath))
	}
	if projectPath != "" {
		if err := loadYAML(projectPath, cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("load project config: %w", err)
		}
		if !allowProjectHooks {
			cfg.Hooks = globalHooks
		}
	}

	loadDotEnv(projectRoot)
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

func cloneHooksConfig(in HooksConfig) HooksConfig {
	out := HooksConfig{
		AllowProject: in.AllowProject,
		Entries:      map[string][]HookEntry{},
	}
	for event, entries := range in.Entries {
		cp := make([]HookEntry, len(entries))
		copy(cp, entries)
		out.Entries[event] = cp
	}
	return out
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

func loadDotEnv(root string) {
	root = strings.TrimSpace(root)
	if root == "" {
		return
	}
	path := filepath.Join(root, ".env")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if i == 0 {
			line = strings.TrimPrefix(line, "\uFEFF")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		value = parseDotEnvValue(value)
		_ = os.Setenv(key, value)
	}
}

func parseDotEnvValue(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'") {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
		return strings.Trim(value, "\"'")
	}
	if idx := strings.Index(value, " #"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return value
}

// providerAPIEnvVars maps env var name → provider profile name. Used both
// to hydrate APIKey from the environment and to tell the user WHICH env
// var is expected when a profile falls back to offline.
var providerAPIEnvVars = map[string]string{
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

// EnvVarForProvider returns the canonical env var name for a provider
// profile (e.g. "anthropic" → "ANTHROPIC_API_KEY"). Returns "" for
// unknown names. When multiple env vars map to the same provider
// (e.g. KIMI / MOONSHOT), the canonical one is returned.
func EnvVarForProvider(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return ""
	}
	canonical := map[string]string{
		"anthropic": "ANTHROPIC_API_KEY",
		"openai":    "OPENAI_API_KEY",
		"google":    "GOOGLE_AI_API_KEY",
		"deepseek":  "DEEPSEEK_API_KEY",
		"kimi":      "KIMI_API_KEY",
		"minimax":   "MINIMAX_API_KEY",
		"zai":       "ZAI_API_KEY",
		"alibaba":   "ALIBABA_API_KEY",
	}
	return canonical[key]
}

func (c *Config) applyEnv() {
	if c.Providers.Profiles == nil {
		c.Providers.Profiles = map[string]ModelConfig{}
	}
	for envName, providerName := range providerAPIEnvVars {
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
		"deepseek":  {Model: "deepseek-chat", BaseURL: "https://api.deepseek.com/v1", MaxTokens: 8192, MaxContext: 131072, Protocol: "openai-compatible"},
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

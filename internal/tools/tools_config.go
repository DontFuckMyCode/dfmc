package tools

// tools_config.go — the subset of config that tools.Engine actually
// consumes. Splitting this out of the full config.Config accomplishes
// three things:
//
//  1. Documents exactly which fields the tools package depends on.
//     Adding a new config field in any section compiles fine but has
//     no effect on the tools engine unless it is added here — this
//     surfaces breakage immediately rather than silently ignoring it.
//
//  2. Breaks the coupling between tools.Engine and the entire config
//     tree. Previously New(cfg) took config.Config, coupling the package
//     to every config section. Now it takes ToolsConfigSubset.
//
//  3. Enables targeted config replay: when the config hot-reloads,
//     tools.Engine can receive just the changed subset rather than a
//     full Config struct that might have unrelated mutations.
//
// The zero value is a valid default config (no disabled tools, no
// custom timeouts, standard limits) so tests that call New(ToolsConfigSubset{})
// without providing explicit config work without adjustment.

// ToolsConfigSubset is the config surface area tools.Engine reads.
// It is a flat structural copy of the relevant fields from the
// top-level config.Config, not a reference. This makes the dependency
// explicit and prevents indirect mutations of the full Config from
// silently affecting tool behaviour.
type ToolsConfigSubset struct {
	Tools      ToolsSection
	Agent      AgentSection
	Security   SecuritySection
	WebFetch   WebFetchSection
	Providers  ProviderSection
	AgentRetry AgentRetrySection
}

// ToolsSection covers the Tools config group.
type ToolsSection struct {
	Disabled        []string
	Enabled         []string
	Limits          map[string]float64
	RequireApproval []string
	Layers          []string
	Shell           ShellSection
}

// ShellSection is the Tools.Shell subsection.
type ShellSection struct {
	Timeout         string // unparsed duration string, parsed by tools.Engine at registration time
	BlockedCommands []string
	OutputCap       int64 // bytes; 0 means use package constant (4 MiB)
}

// AgentSection is the Agent config group.
type AgentSection struct {
	ReadSnapshotCap           int
	RecentFailureCap          int
	OrchestrateAutoSubtasks   int
	ParallelBatchSize         int
	ToolTimeouts              map[string]int
	ToolDefaultTimeoutSeconds int
	MaxToolSteps              int
	MaxToolTokens             int
	MaxToolResultChars        int
	MaxToolResultDataChars    int
	AutonomousResume          string
	AutonomousPlanning        string
	ToolReasoning             string
}

// SecuritySection mirrors config.SecurityConfig for the fields tools read.
type SecuritySection struct {
	Sandbox SandboxSection
}

// SandboxSection is the Security.Sandbox subsection.
type SandboxSection struct {
	AllowShell bool
	Timeout    string // unparsed duration string
	MaxOutput  string // unparsed, parsed as int64 by tools.Engine
}

// WebFetchSection mirrors config.WebFetchConfig.
type WebFetchSection struct {
	AllowedHosts []string
}

// ProviderSection mirrors the provider fields needed by project_info.
type ProviderSection struct {
	Primary  string
	Fallback []string
	Profiles map[string]ProviderProfile
}

// ProviderProfile mirrors a single provider profile.
type ProviderProfile struct {
	Model string
}

// AgentRetrySection mirrors the retry window config from Agent.
type AgentRetrySection struct {
	RetryWindowSize int
}

// ToToolsConfigSubset converts a full Config to the subset. The
// ConfigLike parameter type makes the wiring explicit at compile time:
// passing **config.Config (the May 2026 typo where the caller already
// held a *Config and reached for & out of habit) no longer goes through
// silently as "any" and lands in a zero-valued default branch.
func ToToolsConfigSubset(cfg ConfigLike) ToolsConfigSubset {
	return toToolsConfigSubsetFromConfig(cfg)
}

func toToolsConfigSubsetFromConfig(c ConfigLike) ToolsConfigSubset {
	if c == nil {
		return ToolsConfigSubset{}
	}
	profiles := map[string]ProviderProfile{}
	for name, p := range c.ProvidersProfiles() {
		profiles[name] = ProviderProfile{Model: p}
	}
	return ToolsConfigSubset{
		Tools: ToolsSection{
			Disabled:        c.ToolsDisabled(),
			Enabled:         c.ToolsEnabled(),
			Limits:          c.ToolsLimits(),
			RequireApproval: c.ToolsRequireApproval(),
			Layers:          c.ToolsLayers(),
			Shell: ShellSection{
				Timeout:         c.ToolsShellTimeout(),
				OutputCap:       c.ToolsShellOutputCap(),
				BlockedCommands: c.ToolsShellBlockedCommands(),
			},
		},
		Agent: AgentSection{
			ReadSnapshotCap:           c.AgentReadSnapshotCap(),
			RecentFailureCap:          c.AgentRecentFailureCap(),
			OrchestrateAutoSubtasks:   c.AgentOrchestrateAutoSubtasks(),
			ParallelBatchSize:         c.AgentParallelBatchSize(),
			ToolTimeouts:              c.AgentToolTimeouts(),
			ToolDefaultTimeoutSeconds: c.AgentToolDefaultTimeoutSeconds(),
			MaxToolSteps:              c.AgentMaxToolSteps(),
			MaxToolTokens:             c.AgentMaxToolTokens(),
			MaxToolResultChars:        c.AgentMaxToolResultChars(),
			MaxToolResultDataChars:    c.AgentMaxToolResultDataChars(),
			AutonomousResume:          c.AgentAutonomousResume(),
			AutonomousPlanning:        c.AgentAutonomousPlanning(),
			ToolReasoning:             c.AgentToolReasoning(),
		},
		Security: SecuritySection{
			Sandbox: SandboxSection{
				AllowShell: c.SecuritySandboxAllowShell(),
				Timeout:    c.SecuritySandboxTimeout(),
				MaxOutput:  c.SecuritySandboxMaxOutput(),
			},
		},
		WebFetch: WebFetchSection{
			AllowedHosts: c.WebFetchAllowedHosts(),
		},
		Providers: ProviderSection{
			Primary:  c.ProvidersPrimary(),
			Fallback: c.ProvidersFallback(),
			Profiles: profiles,
		},
		AgentRetry: AgentRetrySection{
			RetryWindowSize: c.AgentRetryWindowSize(),
		},
	}
}

// ConfigLike is the subset of the full config.Config interface that
// tools.Engine reads. Defined here to avoid importing config from the
// tools package. The concrete config.Config implements it.
type ConfigLike interface {
	ToolsDisabled() []string
	ToolsEnabled() []string
	ToolsLimits() map[string]float64
	ToolsRequireApproval() []string
	ToolsLayers() []string
	ToolsShellTimeout() string
	ToolsShellOutputCap() int64
	ToolsShellBlockedCommands() []string
	AgentReadSnapshotCap() int
	AgentRecentFailureCap() int
	AgentOrchestrateAutoSubtasks() int
	AgentParallelBatchSize() int
	AgentToolTimeouts() map[string]int
	AgentToolDefaultTimeoutSeconds() int
	AgentMaxToolSteps() int
	AgentMaxToolTokens() int
	AgentMaxToolResultChars() int
	AgentMaxToolResultDataChars() int
	AgentAutonomousResume() string
	AgentAutonomousPlanning() string
	AgentToolReasoning() string
	SecuritySandboxAllowShell() bool
	SecuritySandboxTimeout() string
	SecuritySandboxMaxOutput() string
	WebFetchAllowedHosts() []string
	ProvidersPrimary() string
	ProvidersFallback() []string
	ProvidersProfiles() map[string]string
	AgentRetryWindowSize() int
}

package config

// config_tools_interface.go — implements tools.ConfigLike for Config.
// This file bridges config and tools packages without creating an
// import cycle. The interface is defined in internal/tools to keep the
// tools package free of config imports; Config implements it here so
// tools.New() can receive the full Config without casting.
//
// If you add a new field to ToolsConfigSubset or ConfigLike in the
// tools package, add the corresponding accessor here.

// ToolsDisabled returns the tools.disabled list from config.
func (c *Config) ToolsDisabled() []string {
	return c.Tools.Disabled
}

// ToolsEnabled returns the tools.enabled list from config.
func (c *Config) ToolsEnabled() []string {
	return c.Tools.Enabled
}

// ToolsLimits returns the tools.limits map from config.
func (c *Config) ToolsLimits() map[string]float64 {
	return c.Tools.Limits
}

// ToolsLayers returns the tools.layers list from config.
func (c *Config) ToolsLayers() []string {
	return c.Tools.Layers
}

// ToolsRequireApproval returns the tools.require_approval list from config.
func (c *Config) ToolsRequireApproval() []string {
	return c.Tools.RequireApproval
}

// ToolsShellTimeout returns the shell timeout as a raw string.
// tools.Engine parses it at registration time, not here.
func (c *Config) ToolsShellTimeout() string {
	return c.Tools.Shell.Timeout
}

// ToolsShellOutputCap returns the per-invocation output cap in bytes.
// Zero means use the built-in default (4 MiB).
func (c *Config) ToolsShellOutputCap() int64 {
	return c.Tools.Shell.OutputCap
}

// ToolsShellBlockedCommands returns the list of blocked shell commands.
func (c *Config) ToolsShellBlockedCommands() []string {
	return c.Tools.Shell.BlockedCommands
}

// AgentReadSnapshotCap is the agent.read_snapshot_cap field.
func (c *Config) AgentReadSnapshotCap() int {
	return c.Agent.ReadSnapshotCap
}

// AgentRecentFailureCap is the agent.recent_failure_cap field.
func (c *Config) AgentRecentFailureCap() int {
	return c.Agent.RecentFailureCap
}

// AgentOrchestrateAutoSubtasks is the agent.orchestrate_auto_subtasks field.
func (c *Config) AgentOrchestrateAutoSubtasks() int {
	return c.Agent.OrchestrateAutoSubtasks
}

// AgentParallelBatchSize is the agent.parallel_batch_size field.
func (c *Config) AgentParallelBatchSize() int {
	return c.Agent.ParallelBatchSize
}

// AgentToolTimeouts is the agent.tool_timeouts override map.
func (c *Config) AgentToolTimeouts() map[string]int {
	return c.Agent.ToolTimeouts
}

// AgentToolDefaultTimeoutSeconds is the default tool timeout in seconds.
func (c *Config) AgentToolDefaultTimeoutSeconds() int {
	return c.Agent.ToolDefaultTimeoutSeconds
}

// AgentMaxToolSteps is the agent.max_tool_steps field.
func (c *Config) AgentMaxToolSteps() int {
	return c.Agent.MaxToolSteps
}

// AgentMaxToolTokens is the agent.max_tool_tokens field.
func (c *Config) AgentMaxToolTokens() int {
	return c.Agent.MaxToolTokens
}

// AgentMaxToolResultChars is the agent.max_tool_result_chars field.
func (c *Config) AgentMaxToolResultChars() int {
	return c.Agent.MaxToolResultChars
}

// AgentMaxToolResultDataChars is the agent.max_tool_result_data_chars field.
func (c *Config) AgentMaxToolResultDataChars() int {
	return c.Agent.MaxToolResultDataChars
}

// AgentAutonomousResume is the agent.autonomous_resume field.
func (c *Config) AgentAutonomousResume() string {
	return c.Agent.AutonomousResume
}

// AgentAutonomousPlanning is the agent.autonomous_planning field.
func (c *Config) AgentAutonomousPlanning() string {
	return c.Agent.AutonomousPlanning
}

// AgentToolReasoning is the agent.tool_reasoning field.
func (c *Config) AgentToolReasoning() string {
	return c.Agent.ToolReasoning
}

// SecuritySandboxAllowShell is the security.sandbox.allow_shell flag.
func (c *Config) SecuritySandboxAllowShell() bool {
	return c.Security.Sandbox.AllowShell
}

// SecuritySandboxTimeout is the raw timeout string from config.
// tools.Engine parses it at registration time.
func (c *Config) SecuritySandboxTimeout() string {
	return c.Security.Sandbox.Timeout
}

// SecuritySandboxMaxOutput is the raw max_output string from config.
// tools.Engine parses it as int64 at registration time.
func (c *Config) SecuritySandboxMaxOutput() string {
	return c.Security.Sandbox.MaxOutput
}

// WebFetchAllowedHosts is the allowed hosts list for web fetch.
func (c *Config) WebFetchAllowedHosts() []string {
	return c.WebFetch.AllowedHosts
}

// ProvidersPrimary returns the primary provider name.
func (c *Config) ProvidersPrimary() string {
	return c.Providers.Primary
}

// ProvidersFallback returns the fallback provider names.
func (c *Config) ProvidersFallback() []string {
	return c.Providers.Fallback
}

// ProvidersProfiles returns a map of profile name to model name.
func (c *Config) ProvidersProfiles() map[string]string {
	out := make(map[string]string, len(c.Providers.Profiles))
	for name, p := range c.Providers.Profiles {
		out[name] = p.Model
	}
	return out
}

// AgentRetryWindowSize is the agent.retry_window_size field.
func (c *Config) AgentRetryWindowSize() int {
	return c.Agent.RetryWindowSize
}
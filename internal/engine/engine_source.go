// Tool-call Source taxonomy.
//
// Every tool invocation in the engine is tagged with a Source so the
// approval gate can distinguish privileged callers (a real keystroke
// from the human at the TUI/CLI) from network-reachable callers
// (HTTP/WS/MCP) that the user has no direct chance to inspect before
// the call fires. The gate condition in executeToolWithLifecycle
// short-circuits when source == SourceUser; every other Source goes
// through requiresApproval.
//
// String values are kept stable because they appear in:
//   - Hook payloads (hooks.Payload["tool_source"])
//   - tool:denied events on the EventBus (payload["source"])
//   - ApprovalRequest.Source on the Approver contract
//   - persisted denial logs in RecordDenial
// Any rename is a contract change for downstream consumers.

package engine

// Source identifies who initiated a tool call. The string form is
// stable wire format — see the "stable" note in the package comment.
// SourceUser, SourceWeb, SourceWS, SourceMCP, SourceCLI are defined in
// engine_tools.go alongside the Source type itself.

const (
	// SourceAgent is the canonical tag for tool calls originated by the
	// native agent loop on the user's behalf. Goes through the approval
	// gate (when configured) and pre/post_tool hooks.
	SourceAgent Source = "agent"
	// SourceSubagent is the tag for tool calls originated by a sub-agent
	// spawned via delegate_task / orchestrate. Same gating as SourceAgent
	// but distinguishable in approval prompts and denial logs.
	SourceSubagent Source = "subagent"
)

// IsUser reports whether s is the privileged-user source. Centralises
// the "skip the gate" predicate so future Source additions don't have
// to remember to update every comparison site.
func (s Source) IsUser() bool { return s == SourceUser }

// IsPrivileged reports whether s bypasses the approval gate entirely.
// Today: SourceUser (real keystroke at TUI/CLI) and SourceCLI (operator
// running `dfmc tool run` themselves). Mirrors the predicate inside
// requiresApproval so callers can short-circuit gate work without
// duplicating string comparisons.
func (s Source) IsPrivileged() bool { return s == SourceUser || s == SourceCLI }

// String renders the Source for hook/event payloads.
func (s Source) String() string { return string(s) }

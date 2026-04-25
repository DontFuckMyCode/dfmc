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
// SourceAgent and SourceSubagent are added here; SourceCLI is in engine_tools.go.
// SourceUser, SourceWeb, SourceWS, SourceMCP are already defined in engine_tools.go.

// IsUser reports whether s is the privileged-user source. Centralises
// the "skip the gate" predicate so future Source additions don't have
// to remember to update every comparison site.
func (s Source) IsUser() bool { return s == SourceUser }

// String renders the Source for hook/event payloads.
func (s Source) String() string { return string(s) }

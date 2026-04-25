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

// SourceAgent — agent loop running inside the engine on behalf of
// a model response. Goes through the approval gate.
const SourceAgent Source = "agent"

// SourceSubagent — delegate_task / orchestrate child loop. Same
// gate as SourceAgent but distinct so denials can name the
// orchestrator vs the orchestrated.
const SourceSubagent Source = "subagent"

// IsUser reports whether s is the privileged-user source. Centralises
// the "skip the gate" predicate so future Source additions don't have
// to remember to update every comparison site.
func (s Source) IsUser() bool { return s == SourceUser }

// String renders the Source for hook/event payloads.
func (s Source) String() string { return string(s) }

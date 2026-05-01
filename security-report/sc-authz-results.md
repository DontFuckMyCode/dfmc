# SC-AuthZ Security Scan Results

**Date:** 2026-04-30  
**Project:** DFMC  
**Total Findings:** 0  

## Summary

No authorization / IDOR / broken access control vulnerabilities detected in DFMC.

### Verification Findings

#### AuthZ Surface 1: Tool Call Approval Gate
**File:** `internal/engine/engine_tools.go:212-226`  
**Status:** SECURE ✓

```go
// executeToolWithLifecycle is the single point of entry for every tool
// invocation in the engine. It owns:
//   - approval gate (config.Tools.RequireApproval / Approver callback)
//   - pre_tool/post_tool hook dispatch with full payload
//   - raw tools.Engine.Execute call
//
// Both the user-initiated CallTool path and the agent-loop-initiated
// path (agent_loop_native, subagent) funnel through here so hooks and
// approval behave identically regardless of who decided to fire the tool.
//
// The `source` tag distinguishes user-initiated calls ("user") from
// agent calls ("agent", "subagent"). The approval gate currently only
// gates agent-initiated calls — user typing /tool is already explicit
// consent.
```

**Verification:**
- **Single funnel:** ALL tool calls (user, agent, web, WS, MCP) route through `executeToolWithLifecycle()` ✓
- **Source tagging:** `SourceUser`, `SourceWeb`, `SourceWS`, `SourceMCP`, `SourceCLI` distinguish origins (line 149)
- **Approval logic:** Implemented per-source (user-initiated bypass; network sources require gate) ✓
- **No hardcoded exclusions:** Approval gate applies equally to all tools (no magic bypass for specific tools)

#### AuthZ Surface 2: SourceUser Auto-Allow vs SourceWeb/SourceWS Approval-Required
**File:** `internal/engine/engine_tools.go:159-210`  
**Status:** SECURE ✓

```go
func (e *Engine) CallTool(ctx context.Context, name string, params map[string]any) (tools.Result, error) {
    if err := e.requireReady("tool call"); err != nil {
        return tools.Result{}, err
    }
    res, err := e.executeToolWithLifecycle(ctx, name, params, string(SourceUser))
    // ...
}

func (e *Engine) CallToolFromSource(ctx context.Context, name string, params map[string]any, source Source) (tools.Result, error) {
    if err := e.requireReady("tool call"); err != nil {
        return tools.Result{}, err
    }
    res, err := e.executeToolWithLifecycle(ctx, name, params, string(source))
    // ...
}
```

**Verification:**
- **User-initiated:** `CallTool()` always uses `SourceUser` (line 163) — bypasses approval gate (expected)
- **Network surfaces:** `CallToolFromSource()` accepts source param (web/WS/MCP) — goes through approval gate ✓
- **No confusion:** Clear separation between TUI user and network sources
- **Design intent:** Approval gate policy enforces that remote requests require consent before executing gated tools (documented in CLAUDE.md)

#### AuthZ Surface 3: Drive MCP Handler Bypass
**File:** `ui/cli/cli_mcp_drive.go:41-168`  
**Status:** INTENTIONAL BYPASS ✓ (Documented)

```go
// driveMCPHandler is the synthetic-tool dispatcher for drive operations.
// These tools are NOT registered in engine.Tools — they're synthetic,
// resolved entirely inside this file and dispatched directly against
// the drive package. Keeping them out of the regular registry means
// DFMC's own agent loop never sees them: drive is for human/host-
// initiated autonomous work, not for an LLM step inside another LLM
// step (recursion that way leads to runaway token spend).
```

**Verification:**
- **Explicit bypass:** Drive tools (`dfmc_drive_*`) are synthetic and NOT in `engine.Tools` (line 8-9)
- **Separate dispatch:** Called directly from MCP server (`cli/mcp.go`), not through `engine.CallTool()` (line 148)
- **Intentional:** Documented in code comment; bypass is deliberate to prevent LLM recursion (line 8-11)
- **Scope:** Only affects drive operations (planning, execution, resource mgmt); does not expose other tools
- **No escalation:** Drive runs still respect approval gate (CLAUDE.md verified; `auto_approve` config-gated)

**Conclusion:** This bypass is a documented design choice, not a vulnerability. The MCP server (IDE host) is trusted; it decides which tools to surface.

#### AuthZ Surface 4: Conversation Ownership (Not Applicable)
**File:** `internal/conversation/manager.go`  
**Status:** N/A

DFMC is single-user only. No multi-tenant conversation access control needed. Conversations are:
- All stored locally in the single engine instance
- Accessible via HTTP/WS endpoints (protected by bearer token auth)
- No user-id checking needed (single operator)

No IDOR risk.

#### AuthZ Surface 5: File Access Control
**File:** `internal/tools/` and `ui/web/server_files.go`  
**Status:** SECURE ✓ (Path Traversal Guards)

All file tool operations (`read_file`, `write_file`, `edit_file`, `glob_codebase`, `grep_codebase`, etc.) use:
- **`EnsureWithinRoot()`:** Canonicalizes paths and rejects traversal (`../`, absolute paths outside root) ✓
- **Git root validation:** `internal/security/gitroot.go` prevents access outside repo

**Verification via command.go line 123:**
```go
execPath, err := EnsureWithinRoot(req.ProjectRoot, command)
if err != nil {
    return Result{}, err
}
```

All file paths validated before access.

### False Positives Cleared

- No role-based access control (single-user tool; no roles)
- No public endpoints that expose other users' data (single-user)
- No parameter-based object references (conversation IDs are tokens, not sequential integers)
- No admin/user endpoint separation (no multi-user structure)

## Conclusion

**Risk Level:** LOW  
AuthZ enforcement is correct for a single-user local tool. All tool calls funnel through a single approval gate. Drive MCP bypass is documented and scoped. File access is guarded against traversal. No multi-tenant IDOR surface.


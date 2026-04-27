# sc-privilege-escalation — DFMC

**Scope.** DFMC has no internal RBAC; classic "user role bypass" findings do
not apply. The privilege boundary is the **tool approval gate**
(`executeToolWithLifecycle` in `internal/engine/engine_tools.go`). The LLM is
the lower-privileged actor; the user is the higher-privileged actor whose
explicit consent (or pre-configured policy) gates destructive tool calls.
Privilege escalation in this codebase = ways for an LLM (or remote MCP /
HTTP caller) to bypass the approval gate, trick a user into auto-approving
something they wouldn't have, or hijack a hook script with weak permissions.

The single mandated entry point is `executeToolWithLifecycle`. Discovery
confirmed every tool path funnels through it:

| Path | Tagged source | Goes through lifecycle? |
|------|---------------|--------------------------|
| `engine.CallTool` (CLI/TUI/web `/api/v1/tools`, MCP regular tools) | `"user"` | Yes — but `source=="user"` skips approval gate |
| `runNativeToolLoop` agent loop sequential dispatch | `"agent"` | Yes |
| `executeToolCallsParallel` agent loop fan-out | `"agent"` | Yes (`agent_loop_parallel.go:112`) |
| `runSubagentProfiles` (delegate_task / orchestrate / Drive sub-agent) | `"subagent"` | Yes (sub-agent runs `runNativeToolLoop`) |
| `driveMCPHandler.Call` (MCP `dfmc_drive_*` tools) | n/a | **Bypasses by design** — control-plane only; spawned drive sub-agents still hit the lifecycle |
| `web.handleWorkspaceApply` (`POST /api/v1/workspace/apply`) | n/a | **Bypasses** — direct `git apply` shell-out (called out in sc-csrf-results CSRF-002) |

The architecture review and prior phase reports
(`sc-authz-results.md`, `sc-business-logic-results.md`,
`sc-mass-assignment-results.md`, `sc-cmdi-results.md`,
`sc-csrf-results.md`) already document the major bypass paths
(`source="user"` hardcoded for HTTP/WS/MCP, `RequireApproval` defaults
empty, `auto_approve` allowlist not validated, `workspace/apply` skips
`CallTool`). This skill focuses on **two privesc-specific gaps not
already enumerated** and confirms the rest.

---

## Findings

### Finding: PRIVESC-001
- **Title:** `AllowedTools` for sub-agents is hint-only, not enforced — user-believed sandbox is fictitious
- **Severity:** Medium
- **Confidence:** 95
- **File:** `D:\Codebox\PROJECTS\DFMC\internal\engine\subagent.go:31-56`,
  `D:\Codebox\PROJECTS\DFMC\internal\engine\subagent_profiles.go:60-138`,
  `D:\Codebox\PROJECTS\DFMC\internal\tools\delegate.go:73-119`,
  `D:\Codebox\PROJECTS\DFMC\internal\tools\builtin_specs.go:408`
- **Vulnerability Type:** CWE-269 (Improper Privilege Management); CWE-1357 / CWE-682 (User Confusion: Functionality Misrepresentation)
- **Description:**
  The `delegate_task` and `orchestrate` tools (and the Drive runner's per-TODO
  spawner) accept an `allowed_tools` array. The advertised semantics — visible
  in `builtin_specs.go:408` (`"allowed_tools restricts the sub-agent to a
  subset. For read-only surveys pass [\"read_file\",\"grep_codebase\",\"ast_query\",\"glob\"] — keeps it cheap and safe."`) — promise a sandbox.

  In reality `AllowedTools` is plumbed only as a **prompt hint** in
  `buildSubagentPrompt` (subagent.go:47-51, "Preferred tools: ...") and as an
  event-payload field for telemetry. Greps confirm it is **never consulted by
  any runtime gate**:
  - Not by `executeToolWithLifecycle` (engine_tools.go:218 — the gate only
    consults `RequireApproval`)
  - Not by `tools.Engine.Execute` (no per-request allowlist filter)
  - Not by the registered tools themselves
  - Not by `runNativeToolLoop` or any of its phase helpers

  A sub-agent spawned with `allowed_tools: ["read_file","grep_codebase"]`
  has the **same tool authority** as the parent: it can call `run_command`,
  `write_file`, `edit_file`, `apply_patch`, `git_commit`, `web_fetch` — every
  registered tool — and the only gate it hits is the same engine-level
  `RequireApproval` list (which defaults empty per AUTHZ-002). The
  string-match pattern `if len(req.AllowedTools) > 0 { … }` in subagent.go is
  a documentation-only branch.

  This is fictional sandboxing. The doc string in `builtin_specs.go:408`
  ("keeps it cheap and safe") is actively misleading: a parent LLM that
  delegates to a "read-only" sub-agent has no actual safety property. The
  spec text influences how cautious operators construct delegation calls, so
  the gap matters in practice — operators who would have hand-approved each
  destructive call may auto-approve "read-only sub-agent" runs that aren't
  actually read-only.
- **Impact:**
  - LLM-controlled escalation: a parent agent can spawn a sub-agent with a
    narrow `allowed_tools` to convince the user/operator the sub-task is
    safe, then the sub-agent calls `run_command` / `write_file` anyway.
    Combined with `Drive.AutoApprove` (LOGIC-007) this is an unattended write
    path no operator audited.
  - Operator surprise: a Drive plan emitted by the planner declares
    `allowed_tools: ["read_file"]` for a "research" TODO; the operator skims
    the plan, sees read-only, runs it. The TODO actually executes
    `run_command rm ...` because the field is decorative.
- **Remediation:**
  Either:
  1. **Enforce.** Pass `AllowedTools` through `tools.SubagentRequest` →
     `runNativeToolLoop` and have `executeToolWithLifecycle` reject any tool
     name not in the allowlist when `source=="subagent"` AND a non-empty
     allowlist was set. The denial event already exists
     (`tool:denied`); plumb the new reason `subagent allowlist`.
  2. **Or rename and re-document** `allowed_tools` to `preferred_tools`
     everywhere (delegate.go, builtin_specs.go, drive_adapter.go, planner
     output schema, web `/api/v1/drive` body) and explicitly state in the
     spec text that this is a soft prompt nudge, not an enforced sandbox.

  Option 1 is preferred — the field's name and documented semantics promise
  enforcement, and operators are already relying on it.
- **References:**
  - https://cwe.mitre.org/data/definitions/269.html
  - https://cwe.mitre.org/data/definitions/1357.html

---

### Finding: PRIVESC-002
- **Title:** `hooks.CheckConfigPermissions` is dead code — group/world-writable config silently grants RCE
- **Severity:** Medium
- **Confidence:** 95
- **File:** `cmd/dfmc/main.go`, `ui/cli/cli_doctor.go`, `internal/engine/engine.go`
- **Status:** **RESOLVED** (2026-04-26)
- **File:** `D:\Codebox\PROJECTS\DFMC\internal\hooks\hooks.go:300-314` (defined),
  no call sites anywhere in the tree
- **Vulnerability Type:** CWE-732 (Incorrect Permission Assignment for
  Critical Resource); CWE-269 (Improper Privilege Management); CWE-1188
  (Insecure Default Initialization of Resource)
- **Description:**
  `internal/hooks/hooks.go:303` defines `CheckConfigPermissions(configPath
  string) string`, which warns when DFMC's config file is group- or
  world-writable (mode `0020` or `0002`). The doc comment on it is explicit:
  "warns if the DFMC config file is group or world-writable, which would
  allow an attacker who can write to the config to achieve arbitrary code
  execution via hook commands."

  Greps across the entire tree show **zero call sites** outside the function
  definition itself and one prose mention in
  `security-report/sc-cmdi-results.md:110`:

  ```text
  $ grep -rn "CheckConfigPermissions" .
  internal/hooks/hooks.go:300:// CheckConfigPermissions warns ...
  internal/hooks/hooks.go:303:func CheckConfigPermissions(configPath string) string {
  security-report/sc-cmdi-results.md:110:  ... `CheckConfigPermissions` warns on ...
  ```

  No code path in `cmd/dfmc/main.go`, `internal/config/`, `internal/engine/`,
  `ui/cli/cli_doctor.go`, `ui/cli/hooks_cli.go`, or any subcommand wires it
  in. `dfmc doctor`, `dfmc hooks`, and `Engine.Init` all bypass the check.

  The threat model the function exists to mitigate is real (CMDI-003 in
  `sc-cmdi-results.md` calls it out: shell-wrapped hooks fire on
  `user_prompt_submit` and every `pre_tool`/`post_tool` event, so any party
  who can write to `~/.dfmc/config.yaml` gets RCE on the next user turn or
  tool call). Without the warning hook actually firing, the user gets no
  notification when their config has lax permissions — the only safety net
  the codebase implements for this attack class is silently inactive.

  Privilege impact:
  - **On Unix/macOS:** A multi-user host where `~/.dfmc/` was created with
    `umask 002` (group writable) — common on shared dev boxes, CI workers,
    or systems with a permissive default umask — lets any unprivileged
    member of the user's primary group inject hook commands and inherit the
    user's full DFMC tool authority.
  - **On Windows:** Inherited ACLs from the parent directory may be more
    permissive than expected; the same code-path skip applies.
  - **Across DFMC instances:** an attacker who briefly wins write access
    (race during `dfmc init`, restored backup with bad perms, sync-tool
    artifact) can persist a hook entry that fires on every subsequent
    `dfmc ask` until the user notices.
- **Impact:**
  Any party with write access to the config file gains the privileges of
  the user running DFMC: arbitrary shell commands run on every prompt
  submission, every tool call, every session start. Effectively a
  privilege-escalation persistence mechanism. The check that exists to
  surface this attack is dead code, so operators have no early-warning
  signal.
- **Remediation:**
  Wire `hooks.CheckConfigPermissions` into:
  1. **`cmd/dfmc/main.go` startup** — call it on the resolved global
     config path immediately after `config.Load`. Print the warning to
     stderr on every invocation; do not fail-close (warnings are
     non-blocking by design and consistent with the "best-effort hooks"
     stance documented in `hooks.go`).
  2. **`dfmc doctor`** — surface as a dedicated row in the diagnostic
     output. Operators run doctor specifically to find this kind of
     misconfiguration.
  3. **`Engine.Init`** — log via `EventBus` so the TUI/web can surface a
     security badge in the runtime card.

  Optional: introduce a strict-mode flag (`security.refuse_writable_config:
  true` in `~/.dfmc/config.yaml`) that converts the warning to a startup
  error for operators who want fail-closed.
- **References:**
  - https://cwe.mitre.org/data/definitions/732.html
  - https://cwe.mitre.org/data/definitions/269.html
  - Cross-ref: `security-report/sc-cmdi-results.md` CMDI-003

---

## Confirmations (no new finding, already covered)

These privesc-relevant patterns were verified during discovery and are
already documented in earlier phase reports — included here so the
privesc lens is complete.

1. **HTTP/WS/MCP `engine.CallTool` hardcodes `source="user"`** — the
   approval gate skips for any HTTP-initiated call, no matter how
   destructive. (`engine_tools.go:120`,
   `ui/web/server_tools_skills.go:167`, `ui/web/server_ws.go:260`,
   `ui/cli/cli_mcp.go:134`.) Already AUTHZ-001.

2. **`Config.Tools.RequireApproval` defaults to empty** — even when
   the gate would fire, no tool is actually gated. (`approver.go:98-117`,
   `internal/config/defaults.go`.) Already AUTHZ-002.

3. **`auto_approve` accepts arbitrary strings + `"*"` wildcard** —
   `POST /api/v1/drive`, `dfmc_drive_start` (MCP), `dfmc drive
   --auto-approve`. No allowlist validation; an authenticated caller can
   pass `auto_approve: ["*"]` to flip off the approval gate for the
   entire run. (`ui/web/server_drive.go:47`,
   `ui/cli/cli_mcp_drive.go:80`,
   `internal/drive/driver.go:166`.) Already LOGIC-007 / MASS-003.

4. **`POST /api/v1/workspace/apply` bypasses `engine.CallTool`** — applies
   patches via direct `git apply` shell-out, skipping
   `executeToolWithLifecycle` entirely (no approval gate, no hooks, no
   `EnsureReadBeforeMutation`). The CSRF/SSRF surface is documented; the
   privesc angle is the same — a tool-equivalent write path with weaker
   gating than `apply_patch`. (`ui/web/server_workspace.go:54-96`.)
   Already CSRF-002.

5. **TUI approval modal does NOT have an "approve all" / "trust this
   session" toggle** — verified in `ui/tui/update.go:438-494` and
   `ui/tui/approver.go`. Each tool call presents a fresh y/n with the
   tool name, source, and parameters. No category-change re-prompt is
   needed because there is no auto-approve session state. Positive
   finding — no change required.

6. **Sub-agent path goes through the lifecycle helper.** Verified
   `runSubagentProfiles → runNativeToolLoop → executeAndAppendToolBatch
   → executeToolWithLifecycle` (with `source="subagent"`). The
   `BeginAutoApprove` Drive scope wraps the engine's approver and unblocks
   any tool in the configured allowlist (`drive_adapter.go:319-395`), but
   the lifecycle helper itself is reached. Combined with PRIVESC-001, the
   `allowed_tools` per-TODO field is decorative; only the run-wide
   `auto_approve` actually changes runtime behaviour.

7. **No `os.Chmod` to 0o777 / 0o666 anywhere; no `os.Chown`.** The single
   `os.Chmod` site (`internal/tools/fileutil.go:40`) takes `perm` from
   the caller, and every caller passes `0o644` (`builtin.go:73`,
   `builtin_edit.go:85`, `apply_patch.go:162`). No path widens
   permissions on `~/.dfmc/`. Positive finding.

---

## Summary

- 2 new findings: PRIVESC-001 (Medium), PRIVESC-002 (Medium).
- All previously documented privesc-equivalent issues
  (HTTP `source="user"`, empty `RequireApproval`, unbounded
  `auto_approve`, `workspace/apply` bypass) re-verified; cross-references
  point to the owning phase report.
- TUI approval UX is sound — no auto-approve / trust-session toggles to
  exploit.
- No filesystem permission widening on DFMC-owned files.

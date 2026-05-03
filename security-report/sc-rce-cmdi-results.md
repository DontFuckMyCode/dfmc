# sc-rce + sc-cmdi Results

## Findings

### [High] `BeginAutoApprove` restoration is not stack-safe under concurrent Drive runs — post-drive auto-approve leak

- **File**: `internal/engine/drive_adapter.go:285-297`
- **Description**: `BeginAutoApprove` snapshots the current approver into a local `prev`, installs an override wrapper, and `defer release()` restores `prev` via `SetApprover`. The restoration does NOT verify that the currently-installed approver is still the wrapper this call installed. When `Driver.Run` / `Driver.Resume` from concurrent Drive runs unwind, the last caller to call `release()` restores its own `prev`, clobbering whatever approver the other concurrent run installed.

  Net effect: after two Drive runs complete concurrently, the slot may hold a zombie wrapper from run A that auto-approves using run A's allowlist (including `["*"]` if set), affecting unrelated subsequent `/chat` or `/tool` requests until another caller explicitly calls `SetApprover`.

- **Impact**: Privilege escalation — a user who runs two concurrent Drive tasks with `auto_approve: ["*"]` can leave the engine in a state where all future tool calls in that process are auto-approved with no user action required.

- **Evidence**:
  ```go
  func (r *driveRunner) BeginAutoApprove(tools []string) func() {
      prev := r.e.approver()          // snapshot read
      override := newDriveAutoApprover(prev, tools, "drive")
      r.e.SetApprover(override)       // install
      return func() {
          r.e.SetApprover(prev)       // unconditional restore — no ownership check
      }
  }
  ```
  `Driver.Run` and `Driver.Resume` both call `BeginAutoApprove` with `defer release()`, but they can execute concurrently for different run IDs in the same process.

- **Mitigation**: Use an opaque token returned by `SetApprover` so `release` only restores when the slot still owns that token. Alternatively, gate Drive runs with a process-wide single-flight semaphore to guarantee nested LIFO restoration ordering.

### [Informational] `driveMCPHandler` bypasses `engine.CallTool` — documented intentional design

- **File**: `ui/cli/cli_mcp_drive.go:148`
- **Description**: The six `dfmc_drive_*` synthetic tools are dispatched via `driveMCPHandler.Call` directly, not `engine.CallTool`. This means they do NOT pass through the approval gate, pre/post hooks, or panic guard.

- **Impact**: None — explicitly intentional per CLAUDE.md design documentation.

- **Mitigation**: No change needed. Design is deliberate.

---

## No Issues Found

| Surface | Review |
|---------|--------|
| `run_command` tool | `detectShellMetacharacter` + `detectShellSubstitutionArg` + `hasScriptRunnerWithEvalFlag` + `isBlockedShellInterpreter` + `ensureCommandAllowed` + argv-only `exec.CommandContext`. No shell bypass. |
| Git tools (`git_runner.go`) | `blockedGitArg` blocklist, `rejectGitFlagInjection` for CVE-2018-17456, argv-only `exec.CommandContext`. |
| Git worktree tools | `rejectGitFlagInjection` on `path`/`ref`/`new_branch`, `EnsureWithinRoot` on `path`. |
| Plugin execution (`pluginexec/client.go`) | `resolveArgv` with explicit type control, `buildEnv` with minimal allowlist + secret scrub, bounded stdout/stderr capture (16 MiB frame cap), argv-only `exec.CommandContext`. |
| WASM plugins (`pluginexec/wasm.go`) | wazero sandbox, no host functions exposed, only "run" export callable, memory bounds checked on read/write. |
| MCP client (`mcp/client.go`) | `exec.Command` argv-only, `security.ScrubEnv` on parent environment before forking, explicit `env_passthrough` opt-in for operator-chosen keys. |
| Hooks (`hooks/hooks.go`) | `sanitizeEnvValue` quotes all payload values (Unix single-quote, Windows `%%` doubling), secret keys scrubbed from parent env, process-group isolation, 1 MiB output cap per stream, panic guard on observer. |
| Path containment (`tools/engine.go:EnsureWithinRoot`) | Two-layer: syntactic (`filepath.Abs` + `filepath.Rel`) catches `..` traversal; symbolic (`filepath.EvalSymlinks` on both root and path) catches committed symlink escapes. Dangling symlinks walk to nearest existing ancestor and re-validate. |
| Secret scrubbing | `ScrubEnv` with deny-by-suffix (`*_API_KEY`, `*_TOKEN`, `*_SECRET`, `AWS_SECRET_ACCESS_KEY`, etc.) + allowlist opt-in. |
| `auto_approve` memory exhaustion | Capped at 5000 entries (`server_drive.go:78`). |
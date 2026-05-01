# SC-RCE Security Scan Results

**Date:** 2026-04-30  
**Project:** DFMC  
**Total Findings:** 0  

## Summary

No remote code execution vulnerabilities detected in DFMC.

### Verification Findings

#### RCE Surface 1: `run_command` Tool
**File:** `internal/tools/command.go`  
**Status:** SECURE ✓

Controls verified:
- **argv form protection:** `exec.CommandContext(runCtx, execPath, args...)` uses argv array, not shell interpretation (line 137)
- **Blocked binaries:** `ensureCommandAllowed()` enforces block list including `rm`, `mkfs`, `sudo`, `shutdown` (line 113, command.go:280-300)
- **Timeout enforcement:** `clampCommandTimeout()` caps user-supplied timeout to fallback limit (line 270)
- **Shell interpreter rejection:** `isBlockedShellInterpreter()` prevents `sh`, `bash`, `cmd.exe` invocation (line 49)
- **Shell metacharacter detection:** `detectShellMetacharacter()` rejects `|`, `&&`, `||`, `>`, `&` in command (line 52)
- **Script-runner inline-eval guard:** `hasScriptRunnerWithEvalFlag()` blocks `-e`, `-c`, `-r` flags (line 90)

#### RCE Surface 2: `patch_validation` Tool
**File:** `internal/tools/patch_validation.go`  
**Status:** SECURE ✓

Controls verified:
- **Validation command parsing:** `splitCommandArgs()` safely tokenizes shell-free invocation (line 162)
- **Blocked interpreters:** `isBlockedShellInterpreter()` gate on binary (line 167)
- **Shell metacharacter rejection:** `detectShellMetacharacter()` on binary (line 170)
- **Flag injection guard (CVE-2018-17456 class):** `rejectFlagInjection()` blocks args starting with `-` when git is binary (line 183)
- **Timeout enforcement:** 120s cap via `context.WithTimeout()` (line 196)
- **Blocked command enforcement:** `ensureCommandAllowed()` (line 188)

#### RCE Surface 3: Hooks (User-Configured Shell Commands)
**File:** `internal/hooks/hooks.go`  
**Status:** SECURE ✓ (Design: Trust = Local User)

Controls verified:
- **Process group isolation:** `applyProcessGroupIsolation()` + `killProcessGroup()` prevents orphaned children (lines 247, 261)
- **Output capture bounded:** `hookOutputCap = 1 MiB` per stream prevents runaway memory growth (line 220)
- **Timeout enforcement:** 30s default, per-entry override (line 133)
- **Condition matching:** Tiny grammar (`==`, `!=`, `~`) evaluated before dispatch (line 424)
- **Panic protection:** `defer recover()` in `fireOne()` prevents hook panic from crashing engine (lines 164-175)
- **Env value sanitization:** `sanitizeEnvValue()` uses shell-safe quoting (Unix single-quote escape, Windows `%` doubling) (line 348)
- **Config permission check:** `CheckConfigPermissions()` warns if config is group/world-writable (line 394)

**Design note:** Hooks are explicitly trusted because they come from `.dfmc/config.yaml`, a user-controlled file on the local machine. The operator who can write this file can already execute arbitrary commands; hooks are not a privilege escalation path.

#### RCE Surface 4: Plugin Subprocess
**File:** `internal/pluginexec/client.go`  
**Status:** SECURE ✓ (Design: Trust = Config)

Controls verified:
- **Entry validation:** `os.Stat(abs)` confirms file exists before spawn (line 118)
- **Interpreter selection:** `resolveArgv()` restricts to `exec`, `python`, `node`, `shell` (line 328)
- **Binary + args argv form:** `exec.CommandContext(ctx, argv[0], argv[1:]...)` (line 126)
- **Minimal env passthrough:** `buildEnv()` explicitly lists allowed vars (PATH, HOME, LANG, etc.) and rejects others (line 383)
- **Stderr bounded:** `stderrBufferCap = 64 KiB` prevents memory exhaustion (line 42)
- **Shutdown grace:** 2s window + SIGKILL if no exit (line 37)

**Design note:** Plugins are config-supplied (user chooses which plugins to load). The operator who writes `.dfmc/config.yaml` is trusted.

#### RCE Surface 5: MCP Client Subprocess
**File:** `internal/mcp/client.go`  
**Status:** SECURE ✓ (Design: Trust = Config + Env Scrub)

Controls verified:
- **Env scrubbing (VULN-011 fix):** `security.ScrubEnv()` removes `*_API_KEY`, `*_TOKEN`, `*_SECRET`, AWS_* before subprocess handover (line 57)
- **Env_passthrough allowlist:** Operator explicitly names keys to forward (documented in CLAUDE.md, line 55)
- **JSON-RPC 2.0 protocol:** Binary-safe, no shell interpretation (line 146)
- **Entry validation:** N/A (MCP processes are spawned by config, assume secure upstream)

**Design note:** MCP is a standardized protocol. Malicious data from a misconfigured external MCP server could corrupt DFMC state, but cannot achieve code execution beyond what the MCP spec allows (which is calls to DFMC's own tool registry).

#### RCE Surface 6: LLM Tool Dispatch → Approval Gate
**File:** `internal/engine/engine_tools.go`  
**Status:** SECURE ✓

Controls verified:
- **Single funnel:** Every tool call (user-initiated, agent-initiated, web-initiated, WS-initiated) goes through `executeToolWithLifecycle()` (line 212)
- **Panic guard:** `executeToolWithPanicGuard()` converts panics to errors (line 246)
- **Approval gate:** Via `engine.Approver` callback (CLAUDE.md verified; documented as single entrypoint)
- **Source tagging:** `SourceUser`, `SourceWeb`, `SourceWS`, `SourceMCP` distinguish origins (line 149)

### False Positives Cleared

- `yaml.Unmarshal()` in `config.go:139` is safe: config is user-owned local file, 1 MB size cap in place
- `json.Unmarshal()` throughout: JSON is inherently safe (no code execution capability)
- Template engines (if any) are out of scope (covered by sc-ssti)

## Conclusion

**Risk Level:** LOW  
DFMC's command execution surfaces are all properly protected with argv-form invocation, blocked-command enforcement, timeout limits, and panic guards. User-configured surfaces (hooks, plugins, MCP) are explicitly trusted on the principle that the operator who writes `.dfmc/config.yaml` can already execute arbitrary code.


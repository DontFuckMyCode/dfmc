# sc-cmdi — Command Injection (CWE-77 / CWE-78 / CWE-88)

**Target:** `D:\Codebox\PROJECTS\DFMC` (Go 1.25). Re-audited 2026-04-29.

## Counts: Critical 0 · High 1 · Medium 1 · Low 1 · Total 3

DFMC's tool surface universally uses argv-style `exec.Command*` (no `sh -c`), eliminating classic CWE-78 shell-metachar injection at the process boundary. Prior-sweep critical/high findings against `patch_validation`, `benchmark`, and `gh_runner` are all remediated. The remaining gaps are around the indirection surface of `run_command`'s blocked-binary list, plus two by-design config-trust surfaces (hooks, MCP) that are documented but worth restating because the blast radius is "everything the user can type."

---

## CMDI-001 — High — `run_command` blocked-binary list bypassed by indirection wrappers

**Severity:** High · **Confidence:** 90 · **CWE:** CWE-78

- **File:** `D:\Codebox\PROJECTS\DFMC\internal\tools\command.go:343-364`
- **Sink:** `exec.CommandContext(runCtx, execPath, args...)` (line 137)

`isBlockedBinary` (line 343) refuses `rm`, `sudo`, `dd`, `mkfs`, `shutdown`, `pkexec`, `killall`, `pkill`, etc. by canonicalised binary basename. None of the standard indirection wrappers — `env`, `nice`, `nohup`, `taskset`, `xargs`, `chroot`, `stdbuf`, `setsid`, `time` — appear on the blocklist. Each of these takes a child binary as its first non-flag argument, so an LLM tool call of the shape

```json
{"command":"env","args":["sudo","whoami"]}
{"command":"nohup","args":["rm","-rf","/some/path"]}
{"command":"xargs","args":["-I{}","sh","{}"]}   // sh in args is also unblocked
```

passes:
- `isBlockedShellInterpreter(command)` (line 49) — `env` is not a shell.
- `detectShellMetacharacter(command)` (line 432) — no metachars in the binary slot.
- `isBlockedBinary("env")` (line 303) — false.
- `isBlockedShellInterpreter(arg)` loop over args (lines 93-97) — catches `sh`/`bash`/`pwsh` but only when the arg's basename is exactly a shell interpreter; `sudo` / `rm` / `mkfs` etc. as args are not screened.
- `hasScriptRunnerWithEvalFlag` (line 628) — fires only on the (`node`,`-e`)/(`python`,`-c`)/(`perl`,`-e`)/(`ruby`,`-e`)/(`php`,`-r`) pairs.

Net effect: the LLM-issued tool call is converted to `exec.CommandContext("env","sudo","whoami")` and the parent process forks `sudo` despite `sudo` being a top-of-list blocked binary. Same shape works on Windows via `start.exe` / `cmd.exe /c` (cmd.exe IS blocked by the shell-interpreter check, but `start` is not — and `start.exe rm.exe foo` runs `rm.exe`).

**Remediation:** in `ensureCommandAllowed`, after the binary-name check, walk the `args` array applying `isBlockedBinary` to each token until the first one beginning with `-` (heuristic: argv pre-flag region is the "child binary" position for env-style wrappers). Or maintain a small allow-list of legitimate first-token wrappers and reapply the binary-name check to `args[0]`.

---

## CMDI-002 — Medium — Hooks shell-mode is the documented "code-exec via config" surface

**Severity:** Medium (by-design, blast-radius high) · **Confidence:** 95 · **CWE:** CWE-77

- **File:** `D:\Codebox\PROJECTS\DFMC\internal\hooks\hooks.go:122-127, 287-305`

`useShell` defaults to true: any hook entry providing only `command:` (the documented happy-path shape in YAML) is wrapped in `sh -c` / `cmd.exe /C`. An attacker who can write to `~/.dfmc/config.yaml`, or to the project-level `.dfmc/config.yaml` while `Hooks.AllowProject=true`, achieves arbitrary local code execution on every `pre_tool` / `user_prompt_submit` event.

Mitigations already present:
- `internal/config/config.go:64` clones `globalHooks` and discards project hooks unless `AllowProject=true` AND `isProjectConfigSecure(projectPath)` (group/world-writable refused).
- `internal/hooks/hooks.go:394-405` `CheckConfigPermissions` warns (does not block) on group/world-writable global config.
- `hookEnv` (line 311) passes payload through `sanitizeEnvKey` + `sanitizeEnvValue` so `DFMC_*` env values cannot break out of the single-quote wrap (Unix) / `^`-escape sequence (Windows).

Residual risk: a malicious npm/pip postinstall script that appends to `~/.dfmc/config.yaml` (which the user already trusts) would land RCE on the next session. Not exploitable from LLM-injected input directly because hook bodies are static config.

**Remediation:** flip `useShell` default to false, require explicit `shell: true` opt-in per entry, and document the consequence in the hooks YAML reference.

---

## CMDI-003 — Low — `validation_command` 120000ns timeout is degenerate (cosmetic)

**Severity:** Low · **Confidence:** 95 · **CWE:** N/A (correctness, not injection)

- **File:** `D:\Codebox\PROJECTS\DFMC\internal\tools\patch_validation.go:190`

```go
runCtx, cancel := context.WithTimeout(ctx, 120000)
```

`context.WithTimeout` takes a `time.Duration`, which is nanoseconds. `120000` is therefore 120 microseconds, not the apparently-intended 120 seconds (`120 * time.Second`). The validation command times out instantly. Not a security issue — the harden-pass for this tool (lines 166-189) blocks shell interpreters / metacharacters / script-runner eval flags / git flag-injection / configured blocked commands BEFORE the exec — but the resulting tool is functionally broken, which is worth flagging from a defence-in-depth perspective: a broken validation step encourages users to disable it and fall back to less-safe ad-hoc `run_command` calls.

**Remediation:** `context.WithTimeout(ctx, 120 * time.Second)` (or read from `req.Params["timeout_ms"]` mirroring `resolveCommandTimeout`).

---

## Re-verified (no longer findings)

| Prior finding | Status |
|---|---|
| CMDI-001-prior — `validation_command` runs ANY binary | **Mitigated.** `patch_validation.go:166-189` now applies the full guard suite. `project_root` override removed (test pins this at `patch_validation_test.go:257-258`). |
| CMDI-002-prior — `benchmark.target` flag-injection | **Mitigated.** `benchmark.go:114-117` refuses `-`-prefix `target` and inserts `--` separator before exec. |
| CMDI-005-prior — `gh_runner` flag-injection one-sided | **Mitigated.** `gh_runner.go:106-141` `rejectGHFlagInjection` now refuses `@path`, `--body-file`, `--input`, `--input-file`, `--field=@`, shell substitution `$(`/`` ` ``/`${`, and path traversal `../`. |
| Git tools `--upload-pack=cmd` (CVE-2018-17456) | **Mitigated** by `rejectGitFlagInjection` (`internal/tools/git_runner.go:128`) on every ref/path/branch user value. Tests pin this for `git_status`, `git_diff`, `git_branch`, `git_log`, `git_blame`, `git_commit`, `git_worktree_*`. |
| `hookEnv` env-injection | **Mitigated** by `sanitizeEnvKey` (alphanumerics-only key normalisation) + `sanitizeEnvValue` (single-quote wrap on Unix; `%`/`"`/`\\`/`!`/`^` escaping on Windows) at `internal/hooks/hooks.go:325-389`. Pinned by `hooks_test.go`. |

## Conclusion

Single open vector worth raising: `run_command` indirection-wrapper bypass (CMDI-001 above). The hooks/MCP config-trust surfaces remain by-design.

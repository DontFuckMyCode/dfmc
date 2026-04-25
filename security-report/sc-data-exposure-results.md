# sc-data-exposure — DFMC Results

Skill: `sc-data-exposure` (Sensitive Data Exposure / Information Disclosure).
Target: `D:\Codebox\PROJECTS\DFMC` (Go 1.25). Scoped per
`security-report/architecture.md`. Skipped `bin/`, `vendor/`, `node_modules/`,
`.dfmc/`, `.git/`, `security-report/`.

CWE families covered:
- CWE-200 — General information exposure
- CWE-209 — Error message information leak
- CWE-532 — Insertion of sensitive info into log/file
- CWE-538 — File and directory information exposure
- CWE-552 — Files / directories accessible to external parties

---

## Finding: EXPOSE-001 — Web file API serves project secrets verbatim

- **Title:** `GET /api/v1/files/{path...}` returns raw bytes of `.env`, `id_rsa`, `credentials.json`, etc. without redaction
- **Severity:** **High**
- **Confidence:** 95
- **File:** `D:\Codebox\PROJECTS\DFMC\ui\web\server_files.go:45-105`
- **Vulnerability Type:** CWE-200 / CWE-552 (Files Accessible to External Parties)
- **Description:**
  `handleFileContent` resolves a caller-supplied path via
  `resolvePathWithinRoot` (which only blocks escapes outside the project
  root) and then returns the raw file content as JSON
  (`"content": string(data)`, line 103). The TUI explicitly maintains
  `looksLikeSecretFile` in `ui/tui/secret_redact.go` to refuse rendering
  `.env`, `*.pem`, `id_rsa`, `credentials*`, `.netrc`, etc., but the
  web file handler imports nothing equivalent and skips the check.
  The directory walk in `listFiles` (line 107) skips `.git`/`.dfmc`/
  `node_modules`/`vendor`/`dist`/`bin` from *listings* but the content
  endpoint accepts arbitrary in-root paths, so a caller can request
  `.env`, `.dfmc/dfmc.db`, `.dfmc/conversations/<id>.jsonl`, etc.
  directly. The project root `.env` (auto-loaded at startup,
  `internal/config/config.go:62-70`) is the canonical home for
  ANTHROPIC/OPENAI/DEEPSEEK/KIMI/ZAI/ALIBABA/MINIMAX/GOOGLE_AI API keys.
- **Impact:** Any authenticated web client (or unauthenticated when
  `auth=none`, which the bind-host normalisation forces to loopback —
  but loopback is also where casual `curl` lives) can fetch all
  provider API keys, the bbolt store, and the full conversation log
  history with one request.
- **Remediation:** Reuse `looksLikeSecretFile` (or move it to a shared
  package) and have `handleFileContent` substitute a `"redacted":
  true` response for any path matching the secret-shape predicate.
  Additionally consider blacklisting `.dfmc/`, `.git/`, `.env*`, and
  any `.pem`/`.key`/`.p12` extensions at the handler level the same
  way the listing walker already does.
- **References:**
  - https://cwe.mitre.org/data/definitions/200.html
  - https://cwe.mitre.org/data/definitions/552.html

---

## Finding: EXPOSE-002 — `Config.Save` writes `~/.dfmc/config.yaml` (plaintext API keys) with mode 0o644

- **Title:** Provider API keys persisted to a world-readable config file
- **Severity:** **High**
- **Confidence:** 95
- **File:** `D:\Codebox\PROJECTS\DFMC\internal\config\config.go:175-187`
- **Vulnerability Type:** CWE-732 / CWE-200 (Incorrect default permissions on a file containing secrets)
- **Description:**
  `Config.Save` writes the merged YAML config tree — which contains
  every provider profile's plaintext `APIKey` field
  (`internal/config/config_types.go`) — with `os.WriteFile(path, data,
  0o644)`. On any multi-user POSIX host this means every other local
  account can read the user's Anthropic/OpenAI/DeepSeek/Kimi/Z.ai/
  Alibaba/MiniMax/Google AI keys via `cat ~/.dfmc/config.yaml`. The
  parent dir is also created at 0o755 (`config.go:176`).
  The same pattern is reused for the models.dev cache
  (`config_models_dev.go:125`, also 0o644) — that one carries no
  secrets so it's not an exposure on its own, but it shows the mode
  is a project-wide convention that should be tightened where keys
  are stored.
- **Impact:** Local privilege escalation surface: a non-privileged
  user on a shared host can read another user's LLM credentials and
  reuse them against the corresponding paid APIs.
- **Remediation:**
  - Save the file with `0o600` and the parent directory with `0o700`.
  - When reading, log a warning if the file is found with broader
    permissions (so users who already had a permissive file get a
    nudge to tighten).
  - Optionally split secrets out of `config.yaml` into a separate
    `credentials.yaml` written 0o600, mirroring `~/.aws/credentials`
    /`~/.netrc` conventions.
- **References:**
  - https://cwe.mitre.org/data/definitions/732.html
  - https://cwe.mitre.org/data/definitions/200.html

---

## Finding: EXPOSE-003 — `/ws` SSE stream broadcasts raw tool parameters and outputs

- **Title:** `tool:call` / `tool:result` events ship `params` and `output_preview` verbatim over the event bus
- **Severity:** **Medium**
- **Confidence:** 75
- **File:**
  - `D:\Codebox\PROJECTS\DFMC\internal\engine\agent_loop_events.go:100-118`
  - `D:\Codebox\PROJECTS\DFMC\internal\engine\agent_loop_events.go:120-168`
  - `D:\Codebox\PROJECTS\DFMC\ui\web\server_chat.go:116-168` (`handleWebSocket` — the SSE forwarder for `/ws`)
- **Vulnerability Type:** CWE-200 (Information Disclosure)
- **Description:**
  `publishNativeToolCall` puts the unfiltered `trace.Call.Input` map
  into the event payload as `params` (line 109). `publishNativeToolResultWithPayload`
  publishes `output_preview` (the first 180 chars of tool output, line 139)
  and a raw `error` field (line 156). `handleWebSocket` (server_chat.go)
  then forwards these payloads byte-for-byte to every SSE subscriber:
  ```
  writeSSE(w, flusher, map[string]any{
      "type":    "event",
      "event":   ev.Type,
      "source":  ev.Source,
      "payload": ev.Payload,   // raw — no redaction
      ...
  })
  ```
  Concrete leakage paths:
  - `read_file` of `.env`/`id_rsa`/`credentials.json` puts the secret
    bytes into `output_preview` (capped at 180 chars but more than
    enough to leak an `sk-...` key).
  - `web_fetch` with an `Authorization: Bearer ...` header passed in
    `params.headers` echoes the bearer token over the stream.
  - `run_command` with a flag like `--token=sk-...` is reflected in
    `params.args`.
  - Tool errors that wrap a secret in their message
    (`"401 from https://api.anthropic.com using x-api-key=sk-...`")
    land verbatim in the `error` payload.
  The endpoint is bearer-token gated when `Web.Auth=token`, which is
  the default for the operator-facing `dfmc remote start`
  configuration; the gating mitigates external attackers but does
  not prevent leakage to any process or user that already holds the
  token (e.g., other terminals on the same workstation, or a CI
  worker that needs to call the JSON API but shouldn't see the
  user's `.env`).
- **Impact:** Authenticated read-only API consumers gain access to
  anything any tool call has touched: file contents, secrets in
  command-line args, bearer tokens used for outbound requests.
- **Remediation:**
  - Run a redactor over `tool:call.payload.params` and
    `tool:result.payload.output_preview`/`error` before publishing.
    Reuse the secret regex catalog from `internal/security/scanner.go`
    so detection stays in one place.
  - For known-sensitive parameter names (`headers.Authorization`,
    `args` slots like `--token=`, `--password=`, `Bearer `), replace
    the value with `***REDACTED***` before publish.
  - Consider a per-tool allowlist: `read_file` outputs are typically
    fine to surface; `web_fetch` headers and `run_command` args
    deserve scrubbing.
- **References:**
  - https://cwe.mitre.org/data/definitions/200.html
  - https://cwe.mitre.org/data/definitions/532.html

---

## Finding: EXPOSE-004 — Hook subprocesses inherit full parent environment

- **Title:** User-defined `pre_tool`/`post_tool`/`session_*` hook commands receive every parent env var, including `*_API_KEY`
- **Severity:** **Medium**
- **Confidence:** 70
- **File:** `D:\Codebox\PROJECTS\DFMC\internal\hooks\hooks.go:197`
- **Vulnerability Type:** CWE-200 / CWE-526 (Cleartext storage of sensitive info in env vars accessible by spawned children)
- **Description:**
  Hook dispatch sets `cmd.Env = append(os.Environ(), hookEnv(event,
  payload)...)`. Every hook shell command therefore sees
  `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`,
  `KIMI_API_KEY`, `ZAI_API_KEY`, `ALIBABA_API_KEY`, `MINIMAX_API_KEY`,
  `GOOGLE_AI_API_KEY`, `DFMC_WEB_TOKEN`, `DFMC_REMOTE_TOKEN`, etc.
  Project-level hooks are gated behind `hooks.allow_project=true` at
  the global level, which is a strong mitigation for "clone hostile
  repo, run dfmc, get your keys exfiltrated" — but a benign hook
  authored by the user themselves (e.g., a "log every tool call to
  a file" hook copied from a snippet repo) would silently see the
  full env, and any compromise of the hook command source would
  leak everything.
- **Impact:** A hook author (or anyone who can edit the hook file)
  gains read access to every API key the engine has on hand. Hooks
  executed during a session can also be modified by other DFMC
  processes if the global config is writable.
- **Remediation:**
  - Filter `os.Environ()` before passing to hooks: drop everything
    matching `*_API_KEY`, `*_TOKEN`, `*_SECRET`, `DFMC_*_TOKEN`.
    Pass only an allowlist of env vars the user explicitly opts into
    via `hooks.entries[].env`.
  - If full inheritance is intentional for compatibility, document
    it loudly in the hook config schema and surface a one-line
    advisory in `dfmc doctor` whenever any hook is configured.
- **References:**
  - https://cwe.mitre.org/data/definitions/200.html
  - https://cwe.mitre.org/data/definitions/526.html

---

## Finding: EXPOSE-005 — External MCP server subprocesses inherit full parent environment

- **Title:** `mcp.NewClient` spawns user-configured MCP servers with the entire DFMC env
- **Severity:** **Medium**
- **Confidence:** 80
- **File:** `D:\Codebox\PROJECTS\DFMC\internal\mcp\client.go:35-48`
- **Vulnerability Type:** CWE-200 / CWE-526
- **Description:**
  ```go
  cmd := exec.Command(command, args...)
  envVars := make([]string, len(os.Environ()))
  copy(envVars, os.Environ())
  for k, v := range env {
      envVars = append(envVars, k+"="+v)
  }
  cmd.Env = envVars
  ```
  Every external MCP server configured under `mcp.servers` in
  config.yaml inherits the full parent environment including all
  provider API keys. The architecture report
  (`security-report/architecture.md` §9 "What's NOT present")
  acknowledges "No sandboxing of external MCP server subprocesses",
  so this is a known shape — but it remains a real exposure vector:
  a malicious or buggy MCP server that the user installs can read
  its own `os.Environ()` and either log the keys or exfiltrate them
  (it already has network capability since MCP is over stdio + the
  server's own outbound calls).
- **Impact:** An attacker who can convince a user to register a
  hostile MCP server gains every LLM key on the workstation in one
  hop. Same threat model as a malicious `npm install` of a package
  whose postinstall script reads env vars.
- **Remediation:**
  - Default to a minimal env: `PATH`, `HOME`, `USER`, `LANG`,
    `LC_ALL`, plus the per-server overrides explicitly declared in
    config (`env: {KEY: value}`). Never leak provider API keys
    unless the user lists them per server.
  - Add `mcp.servers[].env_passthrough: [LIST]` so the user can
    opt a specific server into specific env vars (most MCP servers
    don't need any of the LLM keys).
  - Surface a one-line advisory at server start
    (`mcp:client:env_inherited` event with a count of inherited
    secret-shaped names) so power users can audit which servers
    were given which keys.
- **References:**
  - https://cwe.mitre.org/data/definitions/200.html
  - https://cwe.mitre.org/data/definitions/526.html

---

## Finding: EXPOSE-006 — Tool panic guard returns full Go runtime stack to the model

- **Title:** `executeToolWithPanicGuard` puts up to 2 KiB of stack trace into the agent-visible error
- **Severity:** **Low**
- **Confidence:** 60
- **File:** `D:\Codebox\PROJECTS\DFMC\internal\engine\engine_tools.go:174-216`
- **Vulnerability Type:** CWE-209 (Error Message Information Leak)
- **Description:**
  When any tool panics, the recovered handler builds an error string
  via `fmt.Errorf("tool %s panicked: %v\n%s", name, r, truncateStackForError(stack))`,
  preserving the first 2048 bytes of the runtime stack. That error
  is surfaced as a `tool:panicked` event payload AND fed back to the
  LLM as a `tool_result` (the loop's existing error path). The stack
  reveals Go file paths under the build environment, function names,
  goroutine IDs, and any in-flight argument addresses — enough for
  an attacker who can induce panics (e.g., a malformed tool input
  via a prompt-injected MCP server reply) to fingerprint the
  installed DFMC build, infer the tree-sitter version, and identify
  vendored dependencies that may have known CVEs.
  Comments explicitly say this is intentional ("a crash dump in the
  conversation log lets us file a real bug report"). The trade-off
  is reasonable for first-party debugging; problematic when the
  agent loop is driven by an attacker-controllable model output.
- **Impact:** Limited information disclosure. Leaks build paths,
  package layout, dependency-version hints. No credentials or user
  data unless a tool panicked while holding them on the stack
  (possible but rare in normal operation).
- **Remediation:**
  - Keep the full stack in the local error log + `tool:panicked`
    event for debugging.
  - For the agent-loop-visible error returned from `Execute`, strip
    to just `tool %s panicked: %v` — the model doesn't benefit from
    seeing Go file paths, and removing them shrinks the prompt-
    injection echo channel.
  - Toggle full-stack visibility via a `verbose`/`debug` config flag
    so first-party debugging stays available behind an explicit
    opt-in.
- **References:**
  - https://cwe.mitre.org/data/definitions/209.html

---

## Finding: EXPOSE-007 — Config validator echoes the placeholder API-key value in the error message

- **Title:** `validator.go` interpolates `profile.APIKey` into the validation error
- **Severity:** **Low**
- **Confidence:** 70
- **File:** `D:\Codebox\PROJECTS\DFMC\internal\config\validator.go:60-62`
- **Vulnerability Type:** CWE-209 (Error Message Information Leak) / CWE-532
- **Description:**
  ```go
  if isLikelyPlaceholder(profile.APIKey) {
      return fmt.Errorf("providers.profiles.%q: api_key %q looks like an unfilled placeholder — replace it with your actual key or remove the line", name, profile.APIKey)
  }
  ```
  The guard only fires when the key matches `<…>`-style placeholder
  syntax (`isLikelyPlaceholder`), so in practice a real
  `sk-ant-XXXXXXXXXX` will not trip it. However, this error message
  is the only place in the validator that interpolates a key value
  at all, and any downstream caller that shows config-validation
  errors (e.g., `dfmc doctor`, the web `/api/v1/doctor` endpoint
  via `Config.Validate()` — `ui/web/server_admin.go:127-128`) will
  surface that string verbatim. If a future change broadens the
  guard or duplicates the pattern for a real-key validation, the
  echo will become a real key disclosure.
- **Impact:** Today: the placeholder string itself is leaked, which
  is benign. Tomorrow: easy footgun if the pattern is copied without
  audit.
- **Remediation:**
  - Don't interpolate the actual key. Use a length/shape descriptor
    instead: `api_key looks like a <%d-char value matching <…>
    placeholder pattern>; replace it with your actual key`.
  - Add a unit test asserting that no `Config.Validate()` error
    string contains the literal `APIKey` value, so a future
    regression is caught at CI time.
- **References:**
  - https://cwe.mitre.org/data/definitions/209.html

---

## Phase 2: Verification Notes

Verified the following positive controls (no action needed):

- **`/api/v1/config` redacts secrets**
  (`ui/web/server_admin.go:96-107`, `sanitizeConfigValueForWeb`,
  `isSensitiveConfigPath`). Returns `***REDACTED***` for every leaf
  whose key is one of `api_key|apikey|secret|secret_key|client_secret|password|passphrase|token|*_token`.
- **`dfmc config show` (CLI) redacts by default**
  (`ui/cli/cli_config.go:482-529`, `sanitizeConfigValue`); raw
  values only with explicit `--raw` flag.
- **`Engine.Status()` does NOT include API keys**
  (`internal/engine/engine_passthrough.go:29-68` — only
  `provider`/`model`/`base_url`/`max_tokens`/`max_context` are
  exposed; `APIKey` field never copied).
- **Security scanner stores only redacted matches**
  (`internal/security/scanner.go:179-200, 294-300`,
  `redact()` keeps first/last 4 chars + asterisks). The TUI
  Security panel shows already-redacted strings so the comment
  "secrets are shown with redacted matches" is accurate (verified at
  `ui/tui/security.go:218-224`).
- **TUI Files panel refuses to preview secret-shaped paths**
  (`ui/tui/secret_redact.go`). Comprehensive list of basenames and
  extensions; the omission is the parallel web endpoint (EXPOSE-001).
- **Storage uses 0o600 for the bbolt file**
  (`internal/storage/store.go:71`). Conversation JSONL files are
  created via `os.CreateTemp` (default 0o600 on POSIX) and renamed
  atomically. Parent dirs are 0o755 (acceptable — listings only).
- **Bearer tokens flow in headers, not URLs**
  (`ui/web/server.go:397-419`, `ui/web/static/index.html:693-695`).
  No tokens land in `?token=`-style query strings.
- **`isLikelyPlaceholder` correctly rejects real keys**: the guard
  only matches `<…>`-bracketed placeholders, so a real `sk-ant-…`
  key cannot fall into the echo path on the current code (see
  EXPOSE-007 for the future-regression risk).

---

## Severity Summary

| Severity | Count |
|---|---|
| Critical | 0 |
| **High** | **2** (EXPOSE-001, EXPOSE-002) |
| Medium | 3 (EXPOSE-003, EXPOSE-004, EXPOSE-005) |
| Low | 2 (EXPOSE-006, EXPOSE-007) |

**Total findings: 7.**

Top fixes, ordered by risk-reduction-per-line-of-code:
1. EXPOSE-002 — change `Config.Save` to `0o600`. One-line patch.
2. EXPOSE-001 — wire `looksLikeSecretFile` into `handleFileContent`.
   Few lines; reuses existing TUI helper.
3. EXPOSE-003 — redact tool-event payloads before SSE publish.
   Larger but high-leverage; pairs naturally with the existing
   security scanner regex catalog.
4. EXPOSE-005 / EXPOSE-004 — env minimisation for hooks and MCP
   subprocesses; opt-in passthrough list.
5. EXPOSE-006 — strip stack from agent-visible panic error;
   keep verbose-mode escape hatch.
6. EXPOSE-007 — drop key interpolation from the validator error;
   add CI guard.

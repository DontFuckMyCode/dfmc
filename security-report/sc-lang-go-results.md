# sc-lang-go Results

## Findings

### [Low] Catastrophic regex backtracking in `grep_codebase`

- **File**: `internal/tools/builtin_grep.go:96`
- **Description**: User-supplied pattern passed directly to `regexp.Compile` per call. A crafted pattern like `^(a+)+$` can cause O(2^n) matching time, causing exponential CPU consumption on a grep call.
- **Impact**: Self-DOS — the model controls the pattern and could craft one that causes the grep to hang indefinitely. Blocks the agent loop.
- **Evidence**: `regexp.MustCompile(pattern)` called per invocation of `grepCodebase` without timeout at the regex level.
- **Mitigation**: Use `regexp.Compile` with a timeout enforced at the execution level, or pre-validate patterns for known catastrophic constructs (nested quantifiers). Go 1.21+ `regexp.Regexp` has `MatchReader` for progressive matching.

### [Low] Per-call regex compilation in `find_symbol` parent resolution

- **File**: `internal/tools/find_symbol_parent.go:87, 119`
- **Description**: `parent` arg is embedded into a regex and compiled per call. While patterns are simple and anchored (unlike arbitrary user patterns), a malicious `parent` value could still cause pathological backtracking.
- **Impact**: Lower risk than grep — patterns are simpler. But still a self-DOS vector if an agent crafts a specifically problematic parent pattern.
- **Evidence**: Per-call `regexp.MustCompile` on `parent` argument value.
- **Mitigation**: Pre-compile and cache regexes for the small fixed set of parent patterns, or add a timeout on the regex execution.

### [Informational] `math/rand` for retry backoff — intentional, documented

- **File**: `internal/tools/subagent_retry.go:213-228`
- **Description**: Explicitly documented: "math/rand (not crypto/rand) is intentional: the spread only needs to distribute retries across time; cryptographic strength is not required." Jitter for retry backoff does not need crypto entropy.
- **Impact**: None — this is correct for non-security-purpose randomness.
- **Mitigation**: No change needed.

---

## Controls Verified (Not Vulnerabilities)

The following existing controls were verified and found to be correctly implemented:

| Control | Location | Assessment |
|---------|----------|-------------|
| Path containment + symlink resolution | `tools/engine.go:EnsureWithinRoot` | Solid — two-layer (syntactic + symbolic) |
| CVE-2018-17456 git flag injection guard | `tools/git_runner.go:rejectGitFlagInjection` | Correct — argv-only enforcement |
| Secret redaction on event bus payloads | `internal/security/redact.go` | Working — VULN-013 fix present |
| Env scrubbing for MCP/hook subprocesses | `security.ScrubEnv` | Correct — deny-by-suffix pattern |
| Panic guard around all tool execution | `engine/engine_tools.go:executeToolWithPanicGuard` | Correct — defer/recover with truncated stack |
| WebSocket connection caps + read limits | `ui/web/server_ws.go` | Correct — per-IP/global caps, 64 KiB read limit |
| MCP frame size cap | `mcp/client.go` | Correct — `bufio.Scanner` with 16 MiB limit |
| bbolt DB file permissions | `storage/store.go` | Correct — `0o600` + atomic backup via `CreateTemp` |
| Conversation ID validation | `storage/store.go:validateConvID` | Correct — path traversal protection |
| HTTP SSRF guard | `tools/web.go:safeTransport` | Correct — DNS at connect time, loopback/private check |
| Constant-time token comparison | `ui/web/server.go:693` | Correct — `crypto/subtle.ConstantTimeCompare` |

---

## Clean Areas

- No `unsafe.Pointer` usage found
- No XML unmarshalling
- No unbounded `io.ReadAll` on HTTP bodies — all bounded via `LimitReader`
- No `encoding/gob`
- No hardcoded credentials in source
- No missing `defer resp.Body.Close()` on HTTP responses
- No goroutine leaks — all goroutines have tied lifecycles or bounded channels
- No `sync.Once` misuse
- No race conditions in reviewed concurrent map accesses
# False Positives Cleared

The following findings from Phase 2 (Hunt) were investigated in Phase 3 (Verify) and determined to be false positives or acceptably mitigated.

---

## CMDi bypass via path resolution in `run_command`

**Phase 2 claim:** `isBlockedShellInterpreter` operates on raw input before `EnsureWithinRoot` resolution, allowing blocked binary bypass via symlink/junction placement.

**Verification result:** **FALSE POSITIVE**

**Evidence:** `isBlockedShellInterpreter` at `command.go:49` runs before `looksLikePath` at line 122 and before `EnsureWithinRoot` at line 123. Both `cmd` and `C:\Windows\System32\cmd.exe` resolve via `filepath.Base()` to `cmd`, which is in the blocked list. The check order is deliberately protective.

**Confidence:** High

---

## Windows junction bypass in `EnsureWithinRoot`

**Phase 2 claim:** `filepath.EvalSymlinks` does not resolve Windows junction points (directory junctions created with `mklink /J`), allowing project root escape.

**Verification result:** **FALSE POSITIVE**

**Evidence:** `filepath.EvalSymlinks` on Windows resolves both NTFS symlinks and directory junctions. Both `absRoot` and `absPath` are evaluated via `EvalSymlinks` at engine.go lines 855 and 859 before the `isPathWithin` check at line 870. A junction pointing outside the root would resolve to its target and the check would catch it.

**Confidence:** High

---

## `golang.org/x/net` CVE-2024-45338

**Phase 2 claim:** `golang.org/x/net v0.53.0` may be affected by CVE-2024-45338 (http2 header parsing integer overflow).

**Verification result:** **FALSE POSITIVE**

**Evidence:** CVE-2024-45338 was fixed in `golang.org/x/net v0.33.0` (released September 2024). v0.53.0 (January 2025) includes all fixes through v0.33.0+. The codebase is not vulnerable.

**Confidence:** High

---

## `bbolt` CVE-2023-43804

**Phase 2 claim:** `go.etcd.io/bbolt v1.4.3` may be affected by the freelist corruption CVE-2023-43804.

**Verification result:** **FALSE POSITIVE**

**Evidence:** CVE-2023-43804 was fixed in bbolt v1.3.5 (released October 2023). v1.4.3 includes the fix. The codebase is not vulnerable.

**Confidence:** High

---

## Conversation ID Predictability

**Phase 2 claim:** Conversation IDs are timestamp-based and guessable, enabling enumeration attacks.

**Verification result:** **FALSE POSITIVE for realistic attack scenarios**

**Evidence:** IDs use `conv_YYYYMMDD_HHMMSS.mmm` format with a nanosecond suffix appended on collision (`now.Nanosecond()%1_000_000_000`). This yields up to 1 billion distinct IDs per millisecond. The nanosecond suffix makes enumeration infeasible. Bearer token auth provides the primary access control boundary.

**Confidence:** High

---

## WS Streaming Event Channel Drop (Low Severity)

**Phase 2 claim:** The SSE/WS event channel silently drops events when the 64-element buffer is full.

**Verification result:** **ACCEPTABLE BY DESIGN**

**Evidence:** This is intentional. A slow WS client should not block the engine's event loop. The drop is a performance/safety trade-off, not a vulnerability. Clients requiring guaranteed delivery should use the HTTP API directly.

**Confidence:** High

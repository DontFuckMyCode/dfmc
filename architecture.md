# DFMC Architecture

DFMC ("Don't Fuck My Code") is a code intelligence assistant distributed as a single Go binary. It combines local code analysis (AST + codemap + security heuristics) with a multi-provider LLM router that falls back to an offline provider when API keys are missing or calls fail. Three UIs (CLI, bubbletea TUI, embedded Web API) all drive the same `engine.Engine`.

**Module path:** `github.com/dontfuckmycode/dfmc`. Go 1.25. CGO required for tree-sitter AST bindings.

---

## 1. System Architecture

```
                            +-----------------------------------------+
                            |              main.go                    |
                            |         engine.New(cfg)                |
                            |            engine.Init()                |
                            +--------------------+--------------------+
                                             |
                    +------------------------+------------------------+
                    |                        |                        |
            +-------v--------+    +----------v----------+    +------v-------+
            |  ui/cli/cli.go  |    |  ui/tui/tui.go      |    |ui/web/server.go|
            |   Run() dispatch|    | bubbletea Model      |    |http mux + SSE  |
            +-------+---------+    +----------+----------+    +------+-------+
                    |                         |                        |
                    +-------------------------+------------------------+
                                          | *engine.Engine
                        +------------------+------------------+
                        |                  |                  |
              +---------v--------+ +-------v--------+ +------v--------+
              | internal/engine   | | internal/tools  | |internal/provider|
              |   Engine{}        | |  Tools.Engine    | |    Router{}     |
              | owns all subsystems| |  40+ backend     | |primary+fallback |
              |                   | |  + 4 meta tools  | |  + offline stub |
              +-------------------+ +------------------+ +----------------+
```

### 1.1 Engine as the Hub

`internal/engine/engine.go` owns all subsystems and is passed (by pointer) into all three UIs. The Engine type is split across sibling files:

| File | Responsibility |
|------|---------------|
| `engine.go` | Construction, lifecycle (`Init`/`Shutdown`), state machine |
| `engine_tools.go` | `CallTool`, tool approval, pre/post hooks, panic guard |
| `engine_context.go` | Context budgeting, chunk building, reserve breakdown |
| `engine_prompt.go` | System prompt assembly, PromptRuntime resolver |
| `engine_ask.go` | `Ask`/`AskRaced`/`AskWithMetadata`/`StreamAsk`, history budgeting |
| `engine_intent.go` | Intent layer glue: builds Snapshot, runs `Intent.Evaluate` |
| `engine_passthrough.go` | `Status`, Memory/Conversation/Provider passthrough |
| `engine_analyze.go` | `AnalyzeWithOptions`, dead-code/complexity passes |
| `agent_loop_native.go` | Provider-native tool loop |
| `agent_parking.go` | Park/resume state freeze |

### 1.2 Subsystem Ownership

```
Engine
+-- *ast.Engine          (tree-sitter or regex fallback)
+-- *codemap.Engine      (symbol/dependency graph)
+-- *ctxmgr.Manager      (context ranking + compression)
+-- *provider.Router     (multi-provider with fallback cascade)
+-- *tools.Engine        (40+ backend tools + 4 meta tools)
+-- *memory.Store        (working + episodic + semantic tiers)
+-- *conversation.Manager (JSONL-persisted, branching)
+-- *hooks.Dispatcher    (lifecycle event hooks)
+-- *intent.Router       (state-aware intent classification)
+-- *storage.Store       (bbolt handle)
+-- *security.Scanner    (static analysis heuristics)
+-- EventBus             (fan-out to TUI/Web/remote)
```

### 1.3 Engine State Machine

```
StateCreated -> StateInitializing -> StateReady -> StateServing -> StateShuttingDown -> StateStopped
```

`StateServing` is set when `StartServing()` is called (by the web server or CLI serve command). `StateShuttingDown` gates the agent loop so a `Shutdown()` mid-round parks the agent instead of racing with bbolt close.

---

## 2. Tool Execution Flow

Every tool invocation -- user-initiated (`CallTool`), agent-initiated, or subagent-initiated -- funnels through `executeToolWithLifecycle` in `engine_tools.go`. This is the only place where approval gate, pre/post hooks, and panic guard all fire together.

```
CallTool / agent loop / subagent
          |
          v
executeToolWithLifecycle(ctx, name, params, source)
          |
          +-- [source != "user"] --> requiresApproval(name)? --> askToolApproval --> deny/log
          |
          +-- Hooks.Fire(EventPreTool)  <- outer tool (meta or backend)
          +-- Hooks.Fire(EventPreTool)  <- inner tools (fan-out for meta wrappers)
          |
          v
executeToolWithPanicGuard
          |
          +-- defer/recover -- any panic becomes a regular error
          |                   + runtime:panic event + truncated stack (2 KiB cap)
          |
          v
tools.Engine.Execute(ctx, name, req)
          |
          +-- normalizeToolParams (alias resolution: old->old_string, text->content)
          +-- ExtractReason -- publish tool:reasoning event, strip _reason field
          +-- readBeforeMutationMode -- strict/lenient/none gate
          |       EnsureReadBeforeMutation (strict) --> hash check against snapshot
          +-- tool.Execute(ctx, req) -- real work
          +-- trackFailure / clearFailure (3-retry gate per unique key)
          +-- compressToolOutput (byte/truncation limits)
          +-- recordReadSnapshot (LRU, 256 entry cap)
          |
          v
Result + Hooks.Fire(EventPostTool)  <- outer + inner fan-out
          |
          v
tool:complete | tool:error | tool:panicked | tool:denied events
```

### 2.1 Read-Before-Mutation Gate

Tools that mutate existing files enforce a prior `read_file` snapshot:

| Tool | Mode | Check |
|------|------|-------|
| `edit_file` | `readGateLenient` | Prior snapshot required; hash drift tolerated (edit_file has its own anchor validation) |
| `write_file` | `readGateStrict` | Prior snapshot + hash equality |
| `apply_patch` | `readGateStrict` (per file, via `Engine.EnsureReadBeforeMutation`) | Prior snapshot + hash equality |
| All others | `readGateNone` | No gate |

The snapshot is the SHA-256 of the file content at the time of the last `read_file` call. For `read_file` itself the hash comes from `content_sha256` in the result Data (full-file hash, not the returned slice hash) to avoid TOCTOU drift.

### 2.2 Meta-Tool Layer

The model only sees 4 meta tools (in `internal/tools/meta.go`). Backend tools are discovered on demand.

```
tool_search(query, limit?)   -> ranked backend tool list (meta tools filtered out)
tool_help(name)              -> full spec + JSON schema for one backend tool
tool_call(name, args)        -> dispatch ONE backend tool
tool_batch_call(calls[])      -> dispatch N backend tools in parallel (bounded by ParallelBatchSize)

Auto-unwrap: tool_call{name:"tool_call", args:{name:"<backend>", args:{...}}}
  -> peels the wrapper, dispatches the inner call, prepends a hint.

Refusal: tool_call/tool_batch_call cannot dispatch other meta tools.
  The refusal message names the correct shape so the model self-corrects in one round.
```

Meta-tool budget (64 calls/turn, depth 4) is seeded once at the agent-loop boundary via `SeedMetaToolBudgetWithLimits`. Nested calls share one counter rather than each getting a fresh allowance.

### 2.3 Param-Name Aliasing

`tools/engine.go normalizeToolParams` rewrites known typo aliases before every Execute:

- `edit_file`: `old` -> `old_string`, `new` -> `new_string`
- `write_file`: `text`/`body`/`data` -> `content`

This lets weaker models use JS/Python edit-tool conventions without failing.

---

## 3. Agent Loop Flow (Provider-Native)

When the active provider supports native tool calling (Anthropic, OpenAI, OpenAI-compatible), the engine uses the native tool loop (`agent_loop_native.go`) instead of a plain completion. The model sees only the 4 meta tools; meta-inside-meta is refused with a self-teaching hint.

```
askWithNativeTools(ctx, question)
          |
          +-- ClearParkedAgent (fresh question abandons stale parked loop)
          +-- ensureIndexed (prime codemap if not yet built)
          +-- buildContextChunks (retrieval pipeline)
          +-- buildNativeToolSystemPromptBundle (system prompt + tool descriptors)
          +-- maybeAutoKickoffAutonomy (supervisor DAG --> kickoff TODOs)
          |
          v
runNativeToolLoop(seed, limits, "ask")
          |
          +-- per-step loop (1 -> MaxSteps):
                |
                +-- [engine shutting down?] --> parkNativeToolLoop(ParkReasonShuttingDown)
                |
                +-- drainAgentNotes --> inject [user btw] notes as messages
                |
                +-- maybeCompactNativeLoopHistory (reactive, 0.7 threshold)
                |
                +-- proactiveCompact (step > RoundSoftCap --> threshold 0.5)
                |
                +-- preflightBudget --> park-or-recover before next round burns tokens
                |
                +-- [len(traces) >= RoundSoftCap?] --> synthesis nudge (stop gathering, answer now)
                |
                +-- [len(traces) >= RoundHardCap?] --> ToolChoice:none (final guardrail)
                |
                +-- Providers.Complete(ctx, req) --> resp with tool_calls[]
                |
                +-- [resp has no tool_calls + no text?] --> handleEmptyTurn (nudge or give up)
                |
                +-- [resp has no tool_calls + text] --> final answer, record, coach, return
                |
                +-- executeAndAppendToolBatch (parallel bounded fan-out via tool_batch_call)
                |
                +-- injectTrajectoryHints (coach-aware hints after batch)
                |
                +-- postStepBudget --> compact-or-park if tokens near MaxTokens
                |
                +-- [step == MaxSteps?] --> parkNativeToolLoop(ParkReasonStepCap)
```

### 3.1 Tool Result Processing

`executeAndAppendToolBatch` dispatches all tool calls in the response through `executeToolWithLifecycle`. Results are accumulated as `nativeToolTrace` entries (tool call + result/error + provider/model + step + time). The trace feeds:
- Coach hints (trajectory analysis for circle detection)
- Parked state (for `/continue` resume)
- Telemetry events (`agent:loop:tool_result`)

### 3.2 Budget Auto-Recovery

When `preflightBudget` or `postStepBudget` detects token overflow risk:
1. Compact the message history (drop oldest rounds, keep system prompt + recent context)
2. Retry the same provider/model once
3. If still overflow after compaction: `parkNativeToolLoop(ParkReasonBudgetExhausted)`

Auto-recovery is capped at `maxBudgetAutoRecoveries = 1` per invocation to prevent compact->fill->compact loops.

### 3.3 Autonomous Resume

When `autonomous_resume = "auto"` (default), the wrapper around `askWithNativeTools` detects a budget-exhausted park and immediately re-enters the loop after compacting. The user sees one continuous answer instead of a "press Enter to resume" banner. `resume_max_multiplier` (default 10) caps total work: `MaxSteps x multiplier` steps and `MaxTokens x multiplier` tokens per ask.

---

## 4. Intent Classification Flow

The intent layer (`internal/intent/router.go`) runs a small classifier before every `Ask`. It decides whether the user's turn is a **resume** (continuing a parked agent), a **new** question, or a **clarify** (too vague to act on).

```
User submits "devam et" / "fix it" / ...
          |
          v
Engine.routeIntent(ctx, raw)
          |
          +-- Intent.Evaluate(ctx, raw, Snapshot)
          |     |
          |     +-- buildClassifierMessages(snapshot.Render(), raw)
          |     |     -> system: intent system prompt
          |     |     -> user: ENGINE_STATE:\n<snapshot>\n\nUSER_MESSAGE:\n<raw>
          |     |
          |     +-- provider.Complete(ctx, req)  [timeout: 1500ms, ToolChoice:none]
          |     |
          |     +-- parseDecision(JSON) --> Decision{Intent, EnrichedRequest, Reasoning, FollowUpQuestion}
          |
          +-- [Intent == "resume" && HasParkedAgent?] --> ResumeAgent(ctx, note)
          |
          +-- [Intent == "clarify" && FollowUpQuestion != ""] --> echo question to user (no main model call)
          |
          +-- [Intent == "new"] --> standard Ask path (enriched prompt replaces raw)
```

**Snapshot** (`snapshot.go`) is a compact view of engine state:

```
PARKED_AGENT: yes|no
  summary: "parked at step 7 -- refactor tui.go"
  step: 7 (cumulative: 23)
  last_tool: edit_file
  parked_age: 47s
ACTIVE_MODEL: anthropic/claude-3-5-sonnet
USER_TURNS: 14
RECENT_TOOLS: read_file, grep_codebase, edit_file, run_command
LAST_ASSISTANT:
  [last assistant text, truncated to fit max_chars]
```

**Decision types:**
- `resume`: user is continuing a parked loop. The note from this turn is appended to the parked state.
- `new`: fresh question. Any parked state is abandoned.
- `clarify`: ambiguous input with no anchoring state. User sees `FollowUpQuestion` directly.

**Fail-open:** Any error (timeout, no provider, JSON parse failure) returns `Fallback(raw)` with `Source: "fallback"`. The engine never blocks on the intent layer.

---

## 5. Drive Autonomous Loop

`dfmc drive "<task>"` (CLI) and `/drive <task>` (TUI) run a self-driving plan->execute loop:

```
Driver.Run(ctx, task)
          |
          +-- NewRun(task) --> Run{id, Status: RunPlanning, Todos: []}
          |
          +-- fire EventRunStart
          |
          +-- BeginAutoApprove(autoApprove scope)
          |
          v
        Plan Stage
          |
          v
runPlanner --> Runner.PlannerCall (raw LLM call, no tools, no history)
          |
          +-- plannerSystemPrompt + task --> JSON{todos:[{id,title,detail,depends_on,file_scope,...}]}
          +-- parsePlannerOutput --> []Todo
          +-- validateTodos (unique ids, valid deps, no cycles)
          +-- applySupervisorPlan (auto-survey/verify wrapping)
          |
          v
        Execute Stage (executeLoop)
          |
          +-- while !runFinished(todos):
                |
                +-- readyBatch(todos, MaxParallel) --> indices of runnable TODOs
                |     Conflict checks: file_scope intersection with Running TODOs
                |
                +-- ExecuteTodo(ctx, todo) for each picked index (parallel, bounded)
                |     --> engine.RunSubagent(todo.Detail, provider_tag, ...)
                |     --> supervisor.Task with context snapshot + todo metadata
                |
                +-- per-TODO result --> update todo.Status (Done/Blocked/Skipped)
                |
                +-- skipBlockedDescendants (propagate Blocked dep --> Skipped)
                |
                +-- persist(run) after every transition (bbolt)
                |
                +-- fire drive:todo:start/done/blocked/skipped events
          |
          v
        Finalize
          |
          +-- RunDone:     all TODOs terminal
          +-- RunFailed:   MaxFailedTodos consecutive Blocked, or planner error
          +-- RunStopped:  MaxWallTime exceeded or ctx cancelled (resumable)
          |
          +-- EventRunDone/Stopped/Failed + persist
```

**Per-TODO provider routing:** `provider_tag` ("plan" | "code" | "review" | "test" | "research") is looked up in `Config.Routing` to select a named provider profile for the executor sub-agent. Unmapped tags fall back to the engine default.

**Scheduler conflict rules (`scheduler.go`):**
1. Deps all Done -> eligible
2. File scope conflict with Running TODO -> skip
3. File scope conflict within batch -> only first (input order) wins
4. Empty scope + non-readonly -> exclusive slot (runs alone)
5. `kind=verify` / `worker_class=reviewer|tester|security` -> exclusive slot

**Resume:** `Driver.Resume(ctx, runID)` resets Running TODOs to Pending, re-runs from persisted state. Works for `RunStopped` (wall-time/cancel) and in-progress crashes.

---

## 6. Context Injection Pipeline

```
User question
          |
          v
contextBuildOptions (task profile, explicit file markers, provider max_context)
          |
          +-- task detection: security | debug | review | refactor | general
          +-- file_scale / per_file_scale / total_scale from profile
          +-- provider limit - reserve breakdown = available for context
          +-- GraphDepth, Compression, IncludeTests/Docs from config
          |
          v
ctxmgr.Manager.BuildWithOptions(query, opts)
          |
          +-- tokenizeQuery (alphanumeric + underscore terms, >= 3 chars)
          +-- graph.Nodes() scoring (path/name substring --> +2.0, symbol name --> +3.0)
          +-- symbol-aware pass: resolve identifiers --> seed files (+4.0 + strength)
          +-- graph walk: expandViaGraph(seeds, GraphDepth) --> +1.5/hop
          +-- hotspot scoring (+1.0 per hotspot file)
          +-- sort by score, filter by IncludeTests/Docs
          +-- buildChunkForBudget (compress, token-count, line window)
          |
          v
[]types.ContextChunk
          |
          +-- --> buildContextChunks --> e.lastContextSnapshot (for task-attached reuse)
          +-- --> buildSystemPrompt --> system prompt bundle with context files injected
          +-- --> buildRequestMessages --> trimmed conversation history
```

**Reserve breakdown** (`contextReserveBreakdown`):
- Prompt tokens (system prompt estimate)
- History tokens (conversation budget)
- Response tokens (MaxTokens / response reserve)
- Tool tokens (baseToolReserveTokens = 512)

**Prompt library** (`internal/promptlib`): Composes system prompt from `defaults/*.yaml` + `~/.dfmc/prompts` + `.dfmc/prompts`. Renders `PromptBundle` (cacheable prefix + dynamic tail) so providers that support prompt caching (Anthropic) can emit `cache_control` annotations.

---

## 7. Provider Routing / Failover

```
Providers.Complete(ctx, req)
          |
          +-- ResolveOrder(requested) --> [requested?, primary, ...fallback..., offline]
          |     (offline always last -- it always has an answer)
          |
          +-- filterToolCapable(order, requested) -- strip providers that lack
          |     SupportsTools when req.Tools is non-empty (silence would yield
          |     a tool-less offline reply to a live tool-using task)
          |
          v
        Per-provider loop:
          |
          +-- [provider has multiple models?] --> completeWithProviderRetry chain
          |     +-- on ErrContextOverflow: compact messages, retry same model
          |
          +-- completeWithThrottleRetry (429/503 --> Retry-After or backoff, max 3 retries)
          |
          +-- [error?] --> next provider; [success?] --> return

Providers.CompleteRaced(ctx, req, candidates)
          +-- resolveRaceTargets (dedupe + normalize)
          +-- fire all candidates concurrently (context cancellation as winner signal)
          +-- return first success; cancel losers
```

**Offline provider:** Always registered as the final fallback. `NewOfflineProvider()` returns a placeholder that implements `Complete` with a canned "offline mode" response -- no actual LLM call. When API keys are missing, the primary provider resolves to a placeholder, so the cascade silently falls through to offline.

**Throttle handling:** `ErrProviderThrottled` carries `RetryAfter` if the provider sets the header; otherwise exponential backoff (1s, 2s, 4s). Respects `ctx.Done()` during wait -- agent loop cancellation aborts retries immediately.

---

## 8. Conversation / Message Flow

```
User message
          |
          v
buildRequestMessages (question, chunks, systemPrompt)
          |
          +-- historyBudgetForRequest
          |     = min(conversationHistoryBudget, providerLimit - responseReserve - usedByRequest)
          |
          +-- trimmedConversationMessages(budget)
          |     <- backward walk from latest message
          |     <- truncate at budget boundary
          |     <- peel leading assistant turns (anthropic: messages[0] must be user)
          |
          +-- [omitted messages > 0 && summaryBudget > 0]
          |     --> buildHistorySummary(omitted, summaryBudget)
          |     --> merged into oldest kept user turn (preserves role alternation)
          |
          +-- append current user message --> []provider.Message
          |
          v
Providers.Complete(ctx, req) --> resp
          |
          v
recordInteraction(question, answer, providerName, model, tokenCount, chunks)
          |
          +-- Conversation.AddMessage(user) + AddMessage(assistant)
          |     +-- SaveActive() --> JSON + JSONL on disk (atomic rename + fsync)
          |
          +-- Memory.SetWorkingQuestionAnswer
          +-- Memory.TouchFile(each context chunk)
          +-- Memory.AddEpisodicInteraction(projectRoot, question, answer, 0.7)
```

**Branching:** `Conversation.BranchCreate(name)` copies the current branch's messages into a new branch. `BranchSwitch(name)` changes the active branch. `UndoLast()` removes the last user+assistant pair (or just the assistant if there's no preceding user).

---

## 9. Slash Command Dispatch Chain

```
User types "/review src/foo.go"
          |
          v
TUI: submitChatQuestion --> executeChatCommand
CLI: runChatSlash --> runSkillShortcut

executeChatCommand(cmd string, args string) --> Model + tea.Cmd
          |
          +-- detectSlashCommand --> SlashCommandItem{template, description}
          |
          +-- renderSlashCommandPreview (tool call preview for actions)
          |
          +-- [quick-action?] --> executeQuickAction (no LLM call)
          |     list_dir, read_file, grep_codebase, run_command shortcuts
          |
          +-- [intent-gated?] --> autoToolIntentFromQuestion --> autoToolIntentGatedTool
          |
          +-- [ask mode?] --> submitToEngine (AskWithMetadata or StreamAsk)
```

| Slash Command | Action |
|---------------|--------|
| `/help` | Print registered tool catalog |
| `/tools` | Toggle `m.ui.toolStripExpanded` (collapsed by default) |
| `/keylog` | Toggle `m.ui.keyLogEnabled` (DFMC_KEYLOG env var) |
| `/mouse` | Toggle mouse capture mode |
| `/continue` | Resume parked agent loop |
| `/reasoning` | Toggle coach reasoning verbosity |
| `/compact` | Force history compaction |
| `/clear` | Clear transcript, keep conversation |
| `/undo` | Conversation.UndoLast() |
| `/branch` | BranchCreate / BranchSwitch |
| `/drive` | Drive.Run(task) |
| `/skill` | runSkillShortcut |
| `/{tool}` | Quick-action tool calls (list_dir, read_file, grep, run_command, etc.) |

---

## 10. Hooks System

`internal/hooks/hooks.go` dispatches user-configured shell commands on lifecycle events. Hooks are **best-effort**: failure never blocks a tool call or user turn.

```
Hooks.Fire(ctx, event, payload) --> int (hooks that actually ran)
          |
          +-- entries := d.entries[event]
          +-- conditionMatches? (tool_name == X, tool_name ~ file, etc.)
          |
          +-- runOne(ctx, event, compiledHook, payload) --> Report
                +-- timeout (default 30s, per-entry override)
                +-- applyProcessGroupIsolation (kill children on timeout)
                +-- cmd.Env = os.Environ() + DFMC_EVENT=... + DFMC_<KEY>=<value>
                +-- CombinedOutput (bounded at 1 MiB per stream)
                +-- observer(Report) --> engine EventBus --> hook:run event
```

**Events:**

| Event | Payload keys |
|-------|--------------|
| `user_prompt_submit` | `DFMC_PROMPT`, `DFMC_PROVIDER`, `DFMC_MODEL`, `DFMC_PROJECT_ROOT` |
| `pre_tool` | `DFMC_TOOL_NAME`, `DFMC_TOOL_SOURCE`, `DFMC_PROJECT_ROOT` |
| `post_tool` | `DFMC_TOOL_NAME`, `DFMC_TOOL_SOURCE`, `DFMC_TOOL_SUCCESS`, `DFMC_TOOL_DURATION_MS`, `DFMC_PROJECT_ROOT` |
| `session_start` | `DFMC_PROJECT_ROOT` |
| `session_end` | `DFMC_PROJECT_ROOT` |

**Condition grammar:** `tool_name == apply_patch`, `tool_name != run_command`, `tool_name ~ file`

---

## 11. Key Types

### Engine (`engine.go`)
```go
type Engine struct {
    Config      *config.Config
    Storage     *storage.Store
    EventBus    *EventBus        // fan-out to TUI/Web/remote
    ProjectRoot string

    AST         *ast.Engine
    CodeMap     *codemap.Engine
    Context     *ctxmgr.Manager
    Providers   *provider.Router
    Tools       *tools.Engine
    Memory      *memory.Store
    Conversation *conversation.Manager
    Security    *security.Scanner
    Hooks       *hooks.Dispatcher
    Intent      *intent.Router

    // Lock ordering: agentMu -> mu (never hold mu while holding agentMu)
    mu         sync.RWMutex  // general state
    state      EngineState
    agentMu    sync.Mutex   // parked agent + subagent state

    lastContextSnapshot *ctxmgr.ContextSnapshot
    agentParked         *parkedAgentState
    subagentInFlight    int
    subagentStashed     *parkedAgentState
}
```

### Provider Router (`provider/router.go`)
```go
type Router struct {
    primary   string                 // default provider name
    fallback  []string               // fallback chain
    providers map[string]Provider    // registered providers
}
type ThrottleNotice struct {
    Provider string; Attempt int; Wait time.Duration; Stream bool; Err error
}
```

### Tools Engine (`tools/engine.go`)
```go
type Engine struct {
    registry        map[string]Tool   // backend + meta tools
    readSnapshots   map[string]string // path -> sha256(content at read time)
    readSnapshotLRU []string          // LRU order for eviction
    delegateTool    *DelegateTaskTool  // engine.RunSubagent wired here
    orchestrateTool *OrchestrateTool
    reasoningPublisher ReasoningPublisher  // (toolName, reason) -> engine event
    taskStore       *taskstore.Store
}
```

### Intent (`intent/intent.go` + `router.go`)
```go
type Intent   string  // "resume" | "new" | "clarify"
type Decision struct {
    Intent           Intent
    EnrichedRequest  string   // rewritten prompt
    Reasoning        string   // short trace
    FollowUpQuestion string   // only for clarify
    Source           string   // "llm" | "fallback"
    Latency          time.Duration
}
type Router struct {
    cfg    config.IntentConfig  // Enabled, Provider, Model, TimeoutMs, FailOpen
    lookup ProviderLookup       // func(name) (Provider, bool)
}
```

### Drive (`drive/driver.go` + `types.go`)
```go
type Run struct {
    ID        string; Status RunStatus
    Task      string; Plan *Plan; Todos []Todo
    CreatedAt time.Time; EndedAt time.Time; Reason string
}
type Todo struct {
    ID string; Title string; Detail string
    DependsOn []string; FileScope []string
    ProviderTag string; WorkerClass string; Skills []string
    AllowedTools []string; Labels []string
    Verification string; ReadOnly bool; Confidence float64
    Status TodoStatus; Error string; BlockedReason string
    Origin string; Kind string; ParentID string
}
type Driver struct {
    runner Runner; store *Store; publisher Publisher; cfg Config
}
```

### Context Manager (`context/manager.go`)
```go
type Manager struct {
    codemap *codemap.Engine
    prompts *promptlib.Library
}
type BuildOptions struct {
    MaxFiles, MaxTokensTotal, MaxTokensPerFile int
    Compression string; IncludeTests, IncludeDocs bool
    SymbolAware bool; GraphDepth int
    Strategy RetrievalStrategy  // general | security | debug | review | refactor
}
```

---

## 12. Event Bus

`internal/engine/event_bus.go` provides a publish-subscribe fan-out. All UIs and Drive subscribe to receive engine and agent events for real-time rendering.

**Key events:**

| Event | Payload |
|-------|---------|
| `engine:initializing/ready/serving/shutdown/stopped` | -- |
| `engine:shutdown_error` | `{stage, error}` |
| `memory:degraded` | `{reason}` |
| `index:start/progress/done/error/cancelled` | varies |
| `context:built/error` | `{files, tokens, budget, task, reasons}` |
| `provider:complete` | `{provider, model, tokens}` |
| `provider:race:complete/failed` | `{winner, candidates, model, tokens, duration_ms}` |
| `agent:loop:start/thinking/final/parked/error/interrupted/shutdown_parked` | varies |
| `agent:loop:budget_exhausted` | `{step, max_tool_steps, max_tool_tokens, tokens_used}` |
| `agent:note:injected` | `{step, note}` |
| `tool:complete/error/panicked/denied/reasoning` | varies |
| `hook:run` | `{event, name, command, exit_code, duration_ms, err}` |
| `intent:decision` | `Decision` struct |
| `drive:run:start/done/stopped/failed/warning` | varies |
| `drive:todo:start/done/blocked/skipped/retry` | varies |

---

## 13. Config Hierarchy

```
config.Load() merges (in order, later wins):
  1. Built-in defaults (internal/config/defaults.go)
  2. ~/.dfmc/config.yaml (global overrides)
  3. <project>/.dfmc/config.yaml (project overrides)
  4. Environment variables (ANTHROPIC_API_KEY, OPENAI_API_KEY, ...)
  5. CLI flags (--provider, --model, --project, ...)

.dfc/config.yaml is gitignored -- it contains API keys.
.project/ holds design specs (SPECIFICATION.md, IMPLEMENTATION.md) -- also gitignored.
```

**Degraded startup:** Commands that don't need a full engine (`help`, `version`, `doctor`, `completion`, `man`, `update`) are allow-listed in `allowsDegradedStartup`. When `engine.Init` fails with `ErrStoreLocked`, these commands still run so the user can diagnose the lock conflict.

---

## 14. CGO / AST Backend

**Tree-sitter bindings** (`tree-sitter-go`, `-javascript`, `-typescript`, `-python`) require CGO. With `CGO_ENABLED=0` the build succeeds but AST silently falls back to the regex extractor in `internal/ast/backend_stub.go`.

```
ast.NewWithCacheSize(cacheSize)
  +-- CGO_ENABLED=1 --> tree_sitter bindings (real AST)
  +-- CGO_ENABLED=0 --> backend_stub.go (regex fallback, no AST)

dfmc doctor --> reports ast_backend: tree-sitter | regex
```

The `ast_backend: regex` result means AST features will be degraded -- code search via `grep_codebase` still works, but `find_symbol` and `codemap` have reduced precision.

---

## 15. Read-Gate Summary

```
edit_file   --> readGateLenient   --> prior snapshot (hash drift tolerated)
write_file  --> readGateStrict    --> prior snapshot + hash equality
apply_patch --> readGateStrict    --> per-file via Engine.EnsureReadBeforeMutation
new files   --> exempt            --> no prior read needed
```

The purpose: prevent the model from editing a file that has changed since it was read (another tool, an editor, a formatter). For `edit_file` the tool's own exact-string anchor check already catches drift, so the gate is lenient. For `write_file` and `apply_patch` there is no per-call anchor -- full overwrite and line-numbered hunks silently lose concurrent changes without the hash check.

---

## 16. Interaction Between Subsystems

```
+---------------------------------------------------------------------+
|                          Engine.Ask                                 |
|                                                                     |
|  1. routeIntent --> Intent.Evaluate(Snapshot) --> resume / new / clarify |
|                                                                     |
|  2a. [resume] --> ResumeAgent --> runNativeToolLoop --> CallTool --> execute |
|                                                                     |
|  2b. [new]    --> buildContextChunks --> buildSystemPrompt           |
|                 --> Providers.Complete(req)                          |
|                 --> recordInteraction --> Conversation + Memory        |
|                                                                     |
|  3. [tool call from loop] --> executeToolWithLifecycle              |
|       --> Hooks(pre_tool) --> Tools.Execute --> Hooks(post_tool)     |
|       --> Context.Invalidate(file) --> CodeMap refreshed on next build|
|                                                                     |
|  4. result --> model sees tool_result --> next completion call       |
|                                                                     |
|  5. [budget low] --> compactHistory --> retry same provider          |
|     [budget exhausted after compact] --> parkNativeToolLoop         |
|     [parked] --> user types /continue --> ResumeAgent resumes       |
+---------------------------------------------------------------------+
|                    Engine.Init --> Shutdown                          |
|                                                                     |
|  Init:                                                               |
|    storage.Open --> AST.New + CodeMap.New + Tools.New                |
|    Memory.New + Memory.Load (degraded flag on failure)               |
|    provider.NewRouter --> attachProviderObservers                    |
|    intent.NewRouter (fail-open, nil-safe)                           |
|    hooks.New --> fire session_start                                  |
|    indexCodebase (async, writes to CodeMap)                          |
|                                                                     |
|  Shutdown (order matters!):                                          |
|    cancel indexCtx --> indexWG.Wait (join background goroutines)     |
|    fire session_end hooks (5s timeout)                              |
|    Conversation.SaveActive --> Memory.Persist --> Tools.Close --> Storage|
+---------------------------------------------------------------------+
|                      Drive / Planner / Scheduler                     |
|                                                                     |
|  Driver.Run --> runPlanner (no tools, no history, single LLM call)   |
|    --> []Todo (validated, dependency graph)                         |
|                                                                     |
|  Scheduler.readyBatch(todos, limit):                               |
|    depsAllDone? --> file_scope conflicts? --> lane capacity?         |
|    --> batch of runnable TODO indices                               |
|                                                                     |
|  ExecuteTodo --> engine.RunSubagent(detail, provider_tag)            |
|    --> supervisor.Task --> tools.TaskStore (bbolt)                    |
|    --> context snapshot attached to task                             |
|    --> result --> todo.Status = Done|Blocked|Skipped                |
|    --> persist(run) --> EventBus.drive:*                            |
+---------------------------------------------------------------------+
```

---

## 17. TUI Panel State Architecture

All TUI panel state lives in `panel_states.go` structs, not flat fields on `Model`:

```
Model
+-- chat chatState              (composer + transcript + stream)
+-- program *tea.Program         (live handle for runtime commands)
+-- gitInfo / sessionStart       (workspace metadata)
+-- ui uiToggles                 (overlay/mode flags)
+-- patchView patchViewState     (patch tab + diff snapshot)
+-- workflow workflowPanelState
+-- filesView filesViewState
+-- toolView toolViewState
+-- slashMenu slashMenuState
+-- inputHistory inputHistoryState
+-- commandPicker commandPickerState
+-- activity activityPanelState  (timestamped firehose)
+-- *diagnosticPanelsState      (cold panels: Memory, CodeMap, Conversations, ...)
+-- agentLoop agentLoopState    (native loop telemetry)
+-- telemetry sessionTelemetry   (running counters)
+-- intent intentState           (latest intent decision)
+-- pendingApproval *pendingApproval  (modal, y/n/Esc)
```

The `uiToggles` struct holds runtime flags driven by slash commands and ctrl-keys. Cold diagnostic panels are lazily constructed via `ensureDiagnostics()` so they don't consume memory until visited.

---

## 18. Web Server Routes

```
GET  /                      --> renderWorkbenchHTML (go:embed static/index.html)
GET  /healthz               --> {status: ok}

GET  /api/v1/status         --> engine.Status()
GET  /api/v1/commands/{name} --> command detail
POST /api/v1/chat           --> handleChat (SSE stream)
POST /api/v1/ask            --> handleAsk (non-streaming, supports race mode)
GET  /api/v1/codemap        --> handleCodeMap
GET  /api/v1/providers      --> provider list
GET  /api/v1/tools/{name}   --> tool spec
POST /api/v1/tools/{name}   --> tool exec

GET/POST /api/v1/conversation/*  --> conversation CRUD
GET  /api/v1/memory            --> memory search
GET/POST /api/v1/prompts/*     --> prompt library
GET/POST /api/v1/magicdoc      --> magicdoc show/update

GET/POST /api/v1/drive/*      --> Drive.Run / Drive.Resume / Drive.Stop
GET/POST /api/v1/tasks/*     --> task store CRUD

GET /ws                      --> handleWebSocket (SSE stream for live updates)

Rate limiting: 30 req/s per IP, burst 60.
Bearer token auth: enabled when auth=token (DFMC_WEB_TOKEN env var).
Body size cap: 4 MiB per POST/PUT/PATCH.
```

---

## 19. File Path Normalization

All file paths in tool results are made project-relative before being stored in conversation logs or memory:

```
Tools.Execute --> res.Data["path"] = PathRelativeToRoot(projectRoot, absPath)
                +-- filepath.Rel(absRoot, absPath) --> forward-slash normalized
                   Falls back to absPath on cross-volume (Windows)

EnsureWithinRoot(root, path):
  1. Lexical: filepath.Abs + filepath.Rel -- rejects .. prefix
  2. Symbolic: filepath.EvalSymlinks on both root and path
     --> re-check containment after symlink resolution
     --> for non-existent path: resolve nearest existing ancestor,
         then re-check containment
```

This prevents absolute host paths (`C:\Users\...`) from leaking into conversation JSONL and episodic memory.

---

## 20. Notable Design Decisions

| Decision | Rationale |
|----------|-----------|
| `executeToolWithLifecycle` is the only tool entry point | Ensures approval gate + hooks + panic guard fire for every call, including subagent-initiated ones |
| Intent layer is fail-open | The engine must never be blocked by the intent classifier; any failure falls back to raw input routing |
| Offline provider is always last in ResolveOrder | It always has an answer; racing it wastes tokens |
| `tool_reasoning` strip is unconditional | Even when the publisher is nil the strip runs so tools never see `_reason` as unexpected input |
| Read-gate uses full-file hash for `read_file` | The returned `Output` is a line window, not the full file; hashing the window would produce a slice-hash that never matches `fileContentHash` at the strict gate |
| History trim peels leading assistant turns | Anthropic/compat rejects messages arrays that start with assistant; peeling keeps alternation valid |
| Park saves cumulative steps/tokens | Without this, each park->resume cycle resets `CumulativeSteps` and the autonomous ceiling never trips |
| Hook output capped at 1 MiB per stream | Prevents runaway hooks (`tail -f`, infinite progress dots) from growing buffers until OOM |
| Meta tool budget seeded once at loop boundary | Double-seeding wins (first call wins), so nested expansion shares one counter instead of each dispatch getting a fresh allowance |
| Drive persist after every state transition | Crash or restart loses at most one in-flight transition; no explicit checkpoint needed |
| Supervisor plan wraps planner TODOs | Auto-survey adds `file_scope`-empty discovery TODOs before each modification; auto-verify adds terminal review TODOs |
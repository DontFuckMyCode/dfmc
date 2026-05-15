# DFMC — Next Steps: Specification, Implementation, Tasks & Autonomous Coding

**Project:** D:/Codebox/PROJECTS/DFMC  
**Generated:** 2026-05-02  
**Status:** Analysis + Roadmap  
**Files scanned:** 1,037 Go files | **Symbols:** 10,377

---

## 1. Current State of the System

DFMC is a deterministic coding assistant that goes beyond simple LLM chat. It combines AST-based code analysis, a code graph (codemap), security scanning, and a multi-agent orchestration layer to support autonomous coding workflows. The system is built entirely in Go and runs as a local CLI tool with a Web-based UI.

### 1.1 Core Capabilities — What Exists Today

#### AST Engine (`internal/ast/`)
Tree-sitter-backed parser with language-specific scanners for Go, JavaScript, and Python. Provides:
- Symbol lookup (definition, references)
- AST-based query (`ast_query` tool)
- Syntax tree traversal and pattern matching

#### Code Graph (`internal/codemap/`)
Builds a project-level graph from source files. Supports:
- Dependency graph analysis (nodes, edges, cycle detection)
- Project-level metrics (file counts, symbol counts)
- `codemap` tool for direct graph queries

#### Security Scanning (`internal/security/`)
Regex + AST-based security scanner with three dedicated tools:
- **`audit`** — find security issues (hardcoded secrets, SQL injection, command injection, SSRF patterns)
- **`redact.go`** — runtime secret redaction before event-bus publish (VULN-037 fix)
- **`env_scrub.go`** — subprocess env isolation, blocking secret-shaped keys (VULN-011 fix)
- **`safe_http.go`** — SSRF guard with DNS rebinding TOCTOU fix (pinned IP validation)

#### Task Planning & Execution (`internal/drive/`, `internal/supervisor/`, `internal/taskstore/`)
Three-layer system for task management:

| Layer | Package | Role |
|-------|---------|------|
| **High-level planning** | `drive/planner.go` | Converts free-form task description into a JSON DAG of TODOs (minimal LLM call, no tool loop) |
| **Execution orchestration** | `supervisor/coordinator.go` | Owns the execution of a run's task graph, manages worker goroutines, handles dispatch and result propagation |
| **Persistence** | `taskstore/store.go` | bbolt-backed storage for task records |
| **Execution agent** | `supervisor/executor.go` | ExecuteTaskFunc adapter called by coordinator |
| **Drive agent** | `drive/driver.go` | Drive task execution with parallel subagent support |

#### Tool Registry (`internal/tools/`)
Backend tool registry exposing:
- **File operations:** `read_file`, `write_file`, `edit_file`, `apply_patch`, `list_dir`
- **Search:** `grep_codebase`, `glob`, `find_symbol`, `codemap`, `ast_query`
- **Shell:** `run_command`
- **Git:** `git_status`, `git_diff`, `git_log`, `git_blame`, `git_branch`, `git_commit`
- **Web:** `web_fetch`, `web_search`
- **Planning:** `task_split`, `orchestrate`, `delegate_task`, `todo_write`
- **Reasoning:** `think`

#### Multi-Agent System
- **`orchestrate`** — DAG fan-out to multiple subagents with `race` mode for 2-3 provider candidates
- **`delegate_task`** — spawn a single subagent for a specific task
- **`todo_write`** — visible task state management

---

## 2. Architecture Overview

```
┌────────────────────────────────────────────────────────────┐
│  main() → Run()                                            │
├────────────────────────────────────────────────────────────┤
│  1. bbolt storage open                                     │
│  2. AST engine + Codemap + Context manager init            │
│  3. Tools engine (task store, subagent runner, codemap)     │
│  4. External MCP clients load + bridge tools              │
│  5. Memory + Conversation manager load                     │
│  6. Security scanner + LangIntel registry                 │
│  7. Provider router + observers                           │
│  8. Hook dispatcher init                                  │
│  9. Background context + intent router                     │
│ 10. Project root resolve + codebase indexer start          │
│ 11. session_start fire → engine:ready publish             │
└────────────────────────────────────────────────────────────┘

┌────────────────────────────────────────────────────────────┐
│  USER REQUEST                                              │
│  ↓                                                         │
│  task_split (drive/planner.go) → JSON DAG of TODOs        │
│  ↓                                                         │
│  BuildExecutionPlan (supervisor) → ExecutionPlan          │
│  ↓                                                         │
│  Supervisor.Run() → Worker dispatch                        │
│  ↓                                                         │
│  executeTask (drive/driver.go) → subagent execution        │
│  ↓                                                         │
│  supervisor/coordinator_dispatch.go → propagate results    │
│  ↓                                                         │
│  taskstore/store.go (bbolt persistence)                    │
└────────────────────────────────────────────────────────────┘
```

---

## 3. Specification → Implementation → Tasks Pipeline

### 3.1 The Pipeline (Current + Missing)

Today the system follows this flow:

```
User Intent
    ↓
task_split (LLM) → JSON DAG (TodoNode graph)
    ↓
drive/planner.go → DriveTODOs + Dependencies
    ↓
supervisor/BuildExecutionPlan → ExecutionPlan
    ↓
supervisor/executor.go → ExecuteTaskFunc
    ↓
drive/driver.go → DriveAgent execution
    ↓
taskstore (bbolt) → persistence
```

### 3.2 What is Missing or Incomplete

#### ❌ No explicit SPEC.md → TODO.md → IMPL.md lifecycle

The system can generate a task DAG from a natural language description, but there is no dedicated workflow for:
1. **Specification extraction** — reading a `SPEC.md` (or a design doc) and extracting concrete requirements
2. **TODO.md derivation** — converting requirements into a prioritized, dependency-ordered task list
3. **Implementation tracking** — mapping each TODO to specific file(s) + function(s) + verification criteria
4. **Spec validation** — comparing `IMPL.md` against `SPEC.md` to confirm all requirements are met

#### ❌ Task state persistence across sessions

`taskstore/store.go` exists (bbolt-backed), but the interface is basic. There is no:
- Task resumption after restart
- Task history / audit trail
- Task priority queue management
- Task dependency revision when upstream tasks change

#### ❌ No autonomous coding loop

The system executes tasks but does not have a self-contained:
- **Plan → Implement → Test → Review → Revise** cycle
- No self-verification against spec
- No regression detection between task completions
- No autonomous retry on failure

#### ❌ Subagent context isolation is weak

`delegate_task` and `orchestrate` can fan out to subagents, but:
- Subagents share the same context budget (no per-agent isolation)
- No per-subagent tool access control
- No subagent memory persistence (each subagent starts fresh)
- No coordination protocol between subagents (beyond DAG ordering)

---

## 4. Specification Extraction

### 4.1 What Exists

The `drive/planner.go` system prompt instructs the planner LLM to produce a structured JSON DAG from a free-form task. This is the closest thing to a specification system today, but it is:
- Input: natural language task
- Output: task graph (not a structured spec document)

No dedicated tool reads an existing `SPEC.md` file and converts it into actionable tasks.

### 4.2 What Needs to Be Built

```
SPEC.md (input document with requirements)
    ↓ [spec_parser tool]
Requirement list: [req_001, req_002, ...]
    ↓ [spec_to_todo tool]
TODO.md: prioritized, dependency-ordered task list
    ↓ [todo_to_task tool]
Drive TODOs → ExecutionPlan → Supervisor execution
    ↓ [impl_check tool]
IMPL.md: implementation checklist with pass/fail per requirement
```

**Proposed new tools:**

| Tool | Input | Output | Status |
|------|-------|--------|--------|
| `spec_parse` | `SPEC.md` file path | Structured requirement list (JSON) | Not built |
| `spec_to_todo` | Requirement list | `TODO.md` with priority + dependencies | Not built |
| `spec_validate` | `SPEC.md` + implementation | Per-requirement pass/fail report | Not built |
| `todo_to_task` | `TODO.md` | `DriveTODOs` → `ExecutionPlan` ready | Not built |

---

## 5. Implementation Extraction

### 5.1 What Exists

`codemap` tool can enumerate files and their relationships. `ast_query` can extract symbols, kinds, and positions. But there is no tool that:
- Maps a requirement to specific file(s) that need to change
- Tracks implementation status per requirement
- Identifies missing pieces (functions, interfaces, tests)

### 5.2 What Needs to Be Built

```
Requirement: "Implement HTTPS-only mode for web server"
    ↓ [impl_trace tool]
Files to change: ui/web/server.go, internal/config/config.go
Symbols to add: TLSConfig, force_https middleware
    ↓ [impl_generate tool]
Scaffold implementation: stub functions with TODO comments
    ↓ [impl_verify tool]
Check: Does implementation match requirement?
```

---

## 6. Task Derivation & Management

### 6.1 Current State

`drive/planner.go` generates a JSON DAG:

```json
{
  "todos": [
    {"id": "1", "content": "Add SSRF guard to HTTP client", "priority": "high"},
    {"id": "2", "content": "Write tests for SSRF guard", "priority": "high", "depends_on": ["1"]},
    {"id": "3", "content": "Update documentation", "priority": "low", "depends_on": ["2"]}
  ]
}
```

`supervisor/types.go` defines `TaskState`: `pending → running → done / blocked / skipped / verifying / waiting / external_review`

`supervisor/coordinator.go` manages the run loop with worker dispatch.

`taskstore/store.go` provides bbolt-backed persistence.

### 6.2 Gaps

| Gap | Description |
|-----|-------------|
| **Task resumption** | No mechanism to resume a partially completed task DAG after a restart |
| **Task revision** | When a user changes a TODO, the DAG is not recalculated; upstream tasks may become invalid |
| **Task prioritization** | Planner produces "priority" field but supervisor does not use it for scheduling order |
| **Task cancellation** | No graceful cancellation of in-flight tasks when user aborts |
| **Task retry** | Failed tasks do not automatically retry; no backoff strategy |
| **Task delegation** | `delegate_task` exists but subagent state is not tracked in `taskstore` |
| **Task verification** | `TaskVerifying` state exists but verification logic is not implemented in coordinator |

### 6.3 Proposed Additions

```go
// Enhanced TaskStore interface
type TaskStore interface {
    // Current
    Save(t *Task) error
    Get(id TaskID) (*Task, error)
    ListByRun(runID string) ([]*Task, error)
    Delete(id TaskID) error
    
    // Missing
    ResumeState() (*TaskGraph, error)  // Pick up from last checkpoint
    UpdateDeps(id TaskID, newDeps []TaskID) error  // Revise dependencies
    Retry(id TaskID, attempt int) error  // Track retry count
    SetResult(id TaskID, result TaskResult) error  // Store implementation trace
}
```

---

## 7. Autonomous Coding Loop

### 7.1 Current State

Today the system executes tasks but does not self-verify. The loop is:

```
User → task_split → Supervisor.Run() → Drive execution → done
```

There is no feedback loop that:
- Compares implementation against specification
- Runs tests and adjusts on failure
- Flags incomplete work for human review

### 7.2 Proposed Autonomous Loop

```
┌─────────────────────────────────────────────────────────────┐
│  FOR each task in ExecutionPlan (topological order)         │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ 1. PLAN    → Read spec, derive implementation steps  │   │
│  │ 2. WRITE   → edit_file / write_file                 │   │
│  │ 3. TEST    → run_command (go test ./...)           │   │
│  │ 4. REVIEW  → audit / codemap check                  │   │
│  │ 5. REVISE  → if test fails, retry (max 3 attempts) │   │
│  │ 6. COMMIT  → git_commit on success                  │   │
│  └─────────────────────────────────────────────────────┘   │
│  ↓                                                         │
│  spec_validate → generate IMPL.md                          │
│  ↓                                                         │
│  if all tasks pass → fire event:run:complete               │
│  else → fire event:run:incomplete + report                 │
└─────────────────────────────────────────────────────────────┘
```

### 7.3 Self-Verification Criteria

Each task in the DAG should carry:

```go
type Task struct {
    // ... existing fields ...
    
    Verification struct {
        Criteria   string      // e.g., "go test ./internal/security/ passes"
        Tool       string      // e.g., "run_command"
        Args       map[string]string
        MaxRetries int         // default 3
        OnFail     TaskState   // e.g., TaskBlocked or TaskExternalReview
    }
}
```

---

## 8. Subagent System

### 8.1 Current State

- `orchestrate` — fan out DAG to multiple subagents (with `race` mode)
- `delegate_task` — spawn single subagent
- `todo_write` — track task state

Subagents are managed by the `drive/driver.go` system. Each subagent runs in a goroutine with its own context budget. However:
- No per-subagent memory
- No per-subagent tool access control
- No subagent-to-subagent communication protocol
- Subagent results are aggregated but not reconciled

### 8.2 Proposed Subagent Memory & Isolation

```go
type SubagentSession struct {
    ID       SessionID
    AgentID  string          // which worker class (coder, reviewer, etc.)
    Context  context.Context // isolated budget
    Tools    []string        // allowed tool subset
    Memory   *MemoryStore    // bbolt-backed session memory
    Result   *SubagentResult
}
```

Each subagent gets:
- Isolated context budget (not shared with parent)
- Allowed tool subset (configurable per agent class)
- Own memory store (survives across tool calls within the session)
- Structured result format for aggregation

---

## 9. Security Scanning Integration

### 9.1 Current State

`internal/security/scanner.go` provides regex-based scanning. `audit` tool exists and is usable. Security findings are reported but not automatically tied to tasks.

### 9.2 Proposed Integration

When a task modifies code that touches a security-sensitive area (auth, crypto, network, env handling), the autonomous loop should automatically run the security scanner and block the task if a critical finding is present:

```
task: "Add OAuth2 support to HTTP client"
    ↓
drive detects: auth-related code change
    ↓
auto-run: audit --scope=auth --path=changed_files
    ↓
if critical findings → task state = TaskExternalReview
    ↓
report to user: "Security review required before merge"
```

---

## 10. Roadmap — Priority Order

### Phase 1: Specification Lifecycle (Weeks 1–2)
- [ ] **`spec_parse` tool** — Parse `SPEC.md` into structured requirements (AST + regex on Markdown)
- [ ] **`spec_to_todo` tool** — Convert requirements to `TODO.md` with priority and dependencies
- [ ] **`spec_validate` tool** — Compare `IMPL.md` against `SPEC.md` requirements

### Phase 2: Task Persistence Enhancement (Weeks 2–3)
- [ ] Task resumption — save execution state to bbolt, resume after restart
- [ ] Task revision — allow DAG recomputation when upstream changes
- [ ] Task cancellation — graceful abort of in-flight tasks
- [ ] Task retry — exponential backoff on failure (max 3 attempts)
- [ ] Verification criteria per task — `Task.Verification` struct in `supervisor/types.go`

### Phase 3: Autonomous Loop (Weeks 3–5)
- [ ] Plan → Implement → Test → Review → Revise cycle implementation
- [ ] Self-verification against spec per task
- [ ] Auto-run security scanner on sensitive code changes
- [ ] `IMPL.md` generation on successful task completion

### Phase 4: Subagent Isolation (Weeks 4–6)
- [ ] Per-subagent context budget (not shared)
- [ ] Per-subagent allowed tool subset
- [ ] bbolt-backed subagent memory store
- [ ] Subagent-to-subagent communication protocol (for synthesis tasks)

### Phase 5: Spec-Driven Codemap (Weeks 5–7)
- [ ] `codemap` integration with spec requirements — map each requirement to affected files
- [ ] `impl_trace` tool — trace requirement to implementation symbols
- [ ] `impl_generate` tool — scaffold implementation from requirement

---

## 11. Summary

| Area | Current State | Readiness |
|------|--------------|-----------|
| AST engine | ✅ Complete | Production-ready |
| Codemap | ✅ Complete | Production-ready |
| Security scanning | ✅ Complete | Production-ready |
| Task splitting | ✅ Complete | Production-ready |
| Task DAG execution | ✅ Mostly complete | Works, missing verification + retry |
| Specification extraction | ❌ Not built | Requires new tools |
| Implementation extraction | ❌ Not built | Requires new tools |
| Spec → TODO → IMPL lifecycle | ❌ Not built | Requires Phase 1 |
| Task persistence | ⚠️ Partial | Basic bbolt, no resume/retry |
| Autonomous coding loop | ❌ Not built | Requires Phase 3 |
| Subagent memory/isolation | ❌ Not built | Requires Phase 4 |
| Security + task integration | ❌ Not built | Requires Phase 3 |

**DFMC is a strong foundation.** The AST, codemap, security, and orchestration layers are solid. The missing pieces are the specification-driven workflow (spec → todo → impl → validate) and the autonomous self-verification loop. These are buildable on top of the existing architecture without disrupting the current system.

---

## 12. Immediate Next Steps

1. **Audit current `task_split` output format** — confirm it can be extended to carry `Verification` criteria
2. **Build `spec_parse` tool** — parse Markdown spec files into structured JSON
3. **Add task retry logic to `supervisor/coordinator_dispatch.go`** — handle `TaskVerifying` state with max retries
4. **Add task resume to `taskstore/store.go`** — checkpoint after each task completion, resume on restart
5. **Integrate `audit` tool into autonomous loop** — auto-run on auth/net/crypto code changes

---

*Report generated by DFMC security audit + architecture analysis. For questions or deeper analysis of a specific component, request a targeted review.*
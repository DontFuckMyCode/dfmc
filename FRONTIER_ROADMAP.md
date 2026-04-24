# DFMC Frontier Roadmap

This roadmap defines how DFMC can evolve from a strong local-first coding assistant into a frontier-grade coding agent comparable in daily usability to Claude Code, Codex CLI, Gemini CLI, and OpenCode, while keeping DFMC's differentiator: deterministic local intelligence that does not depend entirely on remote models.

## Product Goal

Build an agent that can:

- plan and execute large coding tasks with parallel sub-agents
- manage todos, task graphs, and execution state across long sessions
- adapt context strategy to the task, provider, and current uncertainty
- route work across multiple providers and models intentionally
- gain reusable capabilities via first-class skills
- operate with a richer, more disciplined system-prompt stack
- use a broader and more reliable tool surface
- lean on AST, codemap, deterministic analyzers, and security scanners instead of outsourcing reasoning to the model alone

## Current Strengths

DFMC already has strong foundations:

- shared core engine across CLI, TUI, web, remote, and MCP
- provider router with fallback and offline mode
- bounded provider-native tool loop
- sub-agent delegation and autonomous drive mode
- AST, codemap, context budgeting, prompt library, and security scanning
- conversation, memory, and drive persistence
- event bus and live observability

The next step is not to rebuild the product, but to unify these pieces into a more opinionated, higher-agency runtime.

## Phase 1: Skills As First-Class Runtime Capabilities

Status: started

Objective:

- move skills from "prompt snippets" to "runtime overlays"
- let CLI/Web/TUI use the same skill catalog
- allow project/global YAML skills to alter system behavior, not just user text

Shipped in this pass:

- shared `internal/skills` catalog
- built-in skills unified into one source of truth
- project/global YAML skills loaded through the same catalog
- Agent Skills `SKILL.md` format support (`.dfmc/skills/<name>/SKILL.md` and `.SKILL.md` files)
- explicit `[[skill:name]]` activation syntax
- task-aware auto-selection for some modes (`security -> audit`, etc.)
- skill overlays injected into the system prompt (prepended for budget priority)
- skill-scoped tool allow/prefer enforcement surfaced in tool result feedback
- skill composition (`[[skill:review]] + [[skill:audit]]`) — multiple explicit skills resolve correctly
- CLI and web skill execution routed through the shared runtime
- CLI skill install/export workflow (`dfmc skill install <path>`, `dfmc skill export <name>`)
- TUI `/skill` list and show powered by the shared catalog (all 14 builtins + custom skills)
- 7 new builtins: `api`, `backend`, `frontend`, `security`, `performance`, `git`, `architecture`

Next hardening in this phase:

- (none — Phase 1 complete)

## Phase 2: Execution Supervisor

Objective:

- introduce a real supervisor above the current agent loop and drive runner
- manage parallel workers, dependencies, budgets, and escalation coherently

Target capabilities:

- one root task -> execution graph -> worker assignment
- worker classes: survey, edit, verify, synthesize, security, test
- dynamic fan-out when the task is broad enough
- worker budget leasing from a root budget
- retry policy by failure class, not simple repetition
- explicit handoff summaries between workers
- supervisor-level confidence and completion checks

Concrete implementation areas:

- `internal/supervisor` package
- unify `delegate_task`, `orchestrate`, and `drive` under one scheduler model
- root-run state persisted independently from one-off chats
- event model for worker lifecycle and supervisor decisions

## Phase 3: Task, Todo, And State Management

Objective:

- make long-running work stateful and inspectable
- let DFMC manage multiple active strands cleanly

Target capabilities:

- hierarchical task trees, not just flat todos
- durable task metadata: owner, scope, files, status, confidence, verification state
- resumable task contexts
- explicit blocked/waiting/external-review states
- user-visible task dashboard in TUI/web
- task snapshots and branchable execution histories

Concrete implementation areas:

- expand `todo_write` into a structured task-state tool/runtime
- persist task trees in bbolt alongside drive runs
- expose tasks over CLI/web/MCP
- add task-aware prompt context and summaries

## Phase 4: Context Lifecycle V2

Objective:

- make context selection adaptive, cheap, and durable across long tasks
- preserve signal while reducing token waste

Target capabilities:

- context tiers: repository brief, task brief, active files, recent edits, evidence stack
- execution-memory summaries between rounds and between sub-agents
- uncertainty-aware retrieval: more exploration when confidence is low
- context snapshots attached to tasks and workers
- context invalidation when files change
- retrieval strategies that differ for review/debug/refactor/generate/security

Concrete implementation areas:

- richer `internal/context` retrieval policies
- file-change-aware invalidation hooks
- task-scoped context stores
- compression modes tuned per task class and provider window

## Phase 5: Multi-Provider And Multi-Model Strategy Engine

Objective:

- move from simple fallback to intentional routing

Target capabilities:

- per-phase routing: planner / explorer / coder / verifier / auditor / summarizer
- provider scoring by latency, reliability, tool support, context window, and cost
- canary racing for high-value single-shot queries
- model choice driven by task and confidence, not just configured default
- route explain/review/security/test work to specialized profiles
- traceable provider decisions in the UI

Concrete implementation areas:

- add routing policy engine above `internal/provider.Router`
- integrate with supervisor and drive execution
- enrich provider metadata with capability tags
- expose route decisions in event payloads and status

## Phase 6: Prompt Stack V2

Objective:

- turn prompting into a more explicit execution contract

Target capabilities:

- layered system prompts: product core, execution policy, skill overlay, role overlay, task overlay, provider/tool policy
- separate planner, worker, verifier, auditor, summarizer prompt families
- stronger anti-loop and anti-fabrication behaviors
- explicit done criteria and verification requirements per worker type
- prompt debug view in TUI/web

Concrete implementation areas:

- expand `internal/promptlib/defaults`
- add worker-type prompt axes
- add structured prompt diagnostics
- add prompt regression tests for important runtime modes

## Phase 7: Tool Surface Expansion

Objective:

- reduce how often the model must "infer" from raw text
- make the tool plane more semantically useful and more deterministic

High-value additions:

- symbol rename / symbol move tools
- semantic code search across AST nodes
- test discovery and targeted test execution tools
- dependency graph and impact-analysis tools
- patch validation and rollback helpers
- structured git-commit/PR/change-summary tools
- richer filesystem and diagnostics tools
- benchmark/perf tools
- config/schema inspection tools

Design rule:

- prefer tools that return structured data over raw prose
- prefer deterministic helpers over model-only reasoning when the answer can be computed

## Phase 8: Deterministic Intelligence Plane

Objective:

- make DFMC stronger than "just an LLM shell"

Target capabilities:

- stronger AST-backed navigation and transformation
- richer codemap semantics: ownership, hotspots, public/private boundaries, call adjacency
- deterministic review hints before the model speaks
- deterministic bug triage hints
- deterministic security heuristics with lower false-positive rates
- incremental scan mode for changed files and current tasks

Concrete implementation areas:

- deeper AST support per language
- codemap enrichment with call/ownership metadata
- security scanner split into fast heuristic + deeper per-language analyzers
- deterministic reviewer and verifier helpers feeding the prompt stack

## Phase 9: Verification Plane

Objective:

- make "done" mean "checked"

Target capabilities:

- explicit verification tasks attached to edits
- targeted build/test/lint commands suggested automatically
- mutation-to-verification mapping
- failure triage that routes back into debug/refactor loops
- verifier workers in supervisor mode

Concrete implementation areas:

- verification planner attached to tool traces
- stronger test/package detection
- richer `run_command` presets and command recommendation engine
- verifier prompt family and event surface

## Phase 10: UX And Product Fit

Objective:

- make the advanced runtime feel simple, not complex

Target capabilities:

- clear task/workflow panels in TUI and web
- model/provider routing transparency
- sub-agent inspection with concise summaries
- skill catalog with examples and install flows
- "why this tool / why this provider / why this context" visibility
- interruption, resume, and branch controls that feel natural

## Suggested Build Order

1. skills runtime
2. supervisor skeleton
3. structured task state
4. provider strategy engine
5. context lifecycle v2
6. prompt stack v2
7. tool surface expansion
8. deterministic intelligence plane upgrades
9. verification plane
10. UX integration across TUI/web/MCP

## Non-Negotiables

- DFMC must remain useful offline or under degraded provider conditions.
- Deterministic analysis should grow, not shrink.
- New autonomy must stay bounded by explicit budgets and approval rules.
- Every advanced capability should surface observability events.
- The same core behavior should remain reachable from CLI, TUI, web, and MCP.

## Short-Term Next Slice

After the skill-runtime upgrade, the highest-leverage next implementation slice is:

1. introduce a supervisor abstraction above `drive` and `delegate_task`
2. promote todos into structured persisted tasks
3. add per-worker provider routing
4. add verifier workers and task-linked validation

That sequence would move DFMC materially closer to a frontier-grade coding agent without sacrificing its local, deterministic edge.

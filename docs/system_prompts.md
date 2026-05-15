# DFMC System Prompts — Design Reference (English)

> This document describes how DFMC's native agent loop composes system prompts,
> grounded in actual source code. Use it when writing or improving prompts.

---

## Table of Contents

1. [Architecture Overview](#1-architecture-overview)
2. [Prompt Profiles & Budgets](#2-prompt-profiles--budgets)
3. [Prompt Roles (Personas)](#3-prompt-roles-personas)
4. [Tool System — Meta-Tool Bridge](#4-tool-system--meta-tool-bridge)
5. [Prompt Bundle Structure](#5-prompt-bundle-structure)
6. [Autonomy Preflight Section](#6-autonomy-preflight-section)
7. [Injected Context](#7-injected-context)
8. [Coach Validation & Trajectory Rules](#8-coach-validation--trajectory-rules)
9. [Project Brief Loading](#9-project-brief-loading)
10. [Provider Routing & Tool Style](#10-provider-routing--tool-style)
11. [Current Gaps & Improvements](#11-current-gaps--improvements)

---

## 1. Architecture Overview

DFMC assembles a **prompt bundle** from typed sections rather than one monolithic string:

```
[Cacheable base: role + instructions from promptlib template]
[Cacheable stable: tool surface block + meta-tool bridge]
[Non-cacheable dynamic: project brief + task overrides]
[Tail: autonomy preflight notice]
```

Sections carry metadata:
```go
type PromptSection struct {
    Label     string  // "system", "tools", "context", "skill.xxx"
    Text      string
    Cacheable bool    // if true, rides the Anthropic prompt cache
}
```

**Why it matters:** cacheable sections cluster together so the instruction stack
(≈40 tokens) shares one cache hit with the base template. The tool-surface block
is injected into the first cacheable section so it also benefits.

**Source:** `buildNativeToolSystemPromptBundle` — `internal/engine/agent_loop_prompt.go`

---

## 2. Prompt Profiles & Budgets

### Profile Resolution

Two profiles: `compact` (default) and `deep`.

| Trigger | Profile |
|---|---|
| Query contains: detailed, deep, thorough, exhaustive, in-depth | deep |
| Query contains: compact, short, minimal, brief, concise, summary | compact |
| Task: security, review, planning | deep (unless small context window) |
| Runtime: LowLatency=true | compact |
| MaxContext ≤ 12 000 tokens | compact |
| Explicit `#profile: deep` or `#tier: thorough` | overrides to deep/balanced |
| `DFMC_PROFILE=deep` env var | overrides to deep |

**Source:** `ResolvePromptProfile` — `internal/context/prompt_render.go:29–72`

### Per-Section Token Budgets

| Knob | Compact | Deep |
|---|---|---|
| Context files | 10 | 16 |
| Tool list limit | 24 | 32 |
| Injected blocks | 2 | 3 |
| Injected lines | 80 | 140 |
| Injected tokens | 320 | 700 |
| Project brief tokens | 180 | 320 |

Tasks `security`/`review` get +2 context files and +140 injected tokens.
Task `planning` gets +1 context file and +80 injected tokens.

Outer ceiling:
```go
PromptTokenBudget = 1100  // compact
PromptTokenBudget = 1800  // deep
// then adjusted by task (+260 for security/review, +160 for planning)
// then capped at MaxContext/4 (clamped 720–3400)
// then ×0.85 if LowLatency
```

**Source:** `ResolvePromptRenderBudget` + `PromptTokenBudget` —
`internal/context/prompt_render.go:109–148`

---

## 3. Prompt Roles (Personas)

Resolved from task label by `ResolvePromptRole`
(`internal/context/prompt_render.go`):

| Task label | Role |
|---|---|
| security | `security_auditor` |
| review | `code_reviewer` |
| planning | `planner` |
| debug | `debugger` |
| refactor | `refactorer` |
| test | `test_engineer` |
| doc / document | `documenter` |
| synthesize / survey | `synthesizer` |
| research / inventory | `researcher` |
| verify | `verifier` |
| *(default)* | `generalist` |

---

## 4. Tool System — Meta-Tool Bridge

### The 4 Meta Tools

DFMC exposes only these 4 to the model — never raw backend tool names:

```
You have 4 meta tools that proxy to a richer backend registry:
  - tool_search(query, limit?) — discover backend tools by topic
  - tool_help(name)            — fetch full schema/usage for one tool
  - tool_call(name, args)      — execute a single backend tool
  - tool_batch_call(calls[])   — execute several backend tools in one round-trip
```

**Critical rules in the bridge** (all from `buildNativeMetaToolInstructions`):
- Never nest meta tools — `calls[]` and `name` accept only backend tools
- Every `tool_call`/`tool_batch_call` MUST include `_reason` in args
- `run_command` is NOT a shell; `command` = argv[0] only
- Prefer discovery tools before guessing: `grep_codebase` → `find_symbol` → `read_file`
- Use `tool_batch_call` for independent read-only fan-out
- Keep reads narrow; broad scans are the exception

**Source:** `buildNativeMetaToolInstructions` — `internal/engine/agent_loop_prompt.go:110–139`

### Backend Tool Registry

Defined by `ToolSpec` in `internal/tools/spec.go`. Each spec carries:
- `Name`, `Summary`, `JSONSchema()`, `Category`

Tool groups and priorities vary by task:
- **security/review**: `git_diff`, `git_status`, `read_file`, `grep_codebase`, `ast_query`, `codemap` get +30 priority
- **refactor/debug**: `read_file`, `grep_codebase`, `find_symbol`, `ast_query`, `edit_file`, `apply_patch`, `run_command` get +30
- **test**: `run_command`, `read_file`, `grep_codebase`, `write_file`, `edit_file` get +30

**Source:** `toolGroup` + `toolPriority` — `internal/context/prompt_render.go:300–395`

### Tool One-Liners (summarizeTools output)

```
read_file        → read focused file ranges; prefer this over shell cat/type
grep_codebase    → search code text by pattern before choosing files
glob             → find paths by file pattern
list_dir         → inspect directory contents
find_symbol      → locate symbol definitions/usages
codemap          → inspect project graph and dependency structure
ast_query        → get AST-backed outlines or structural matches
edit_file        → replace exact text after reading the file
write_file       → create or replace a file when full content is known
apply_patch      → apply multi-hunk diffs; use dry run for risky changes
run_command      → run build/test/lint/dependency commands; no shell chains
tool_search      → discover backend tools by keyword
tool_help        → fetch exact tool signature before guessing args
tool_call        → call one backend tool through the meta bridge
tool_batch_call  → call several independent backend tools in one bounded batch
```

---

## 5. Prompt Bundle Structure

Assembled by `buildNativeToolSystemPromptBundle`:

```
bundle.Sections[0] (cacheable): base template + injected bridgeText
bundle.Sections[1..N]:           task/style/policy sections (some cacheable)
→ bundleToSystemBlocks()
→ appendAutonomySystemSection()
```

If no cacheable section exists yet, a standalone stable section is prepended
containing only the tool bridge so it remains cacheable.

**Source:** `buildNativeToolSystemPromptBundle` — `internal/engine/agent_loop_prompt.go`

---

## 6. Autonomy Preflight Section

Appended as a trailing system block (label=`autonomy`, not cacheable) when the
task is split into ≥3 subtasks with confidence ≥ 0.55 (0.40 in aggressive mode).

**What it contains:**
```
[DFMC autonomy preflight]
Deterministic preflight split this request into N subtasks (mode=..., confidence=...).
Stay autonomous: keep reading, editing, verifying, and researching until the task
  is actually complete or you are truly blocked.
[aggressive mode: "This session is in aggressive autonomy mode: act on the plan immediately..."]
[If todos seeded: "The session todo list has already been pre-seeded..."]
[Else: "If the work expands further, sync todo_write early..."]
[If parallel: "Because the subtasks are parallelizable, prefer orchestrate for one-shot fan-out..."]
[If sequential: "Treat these as ordered stages. Prefer orchestrate with force_sequential=true..."]
Preflight subtasks:
  1. [hint] title
  2. [hint] title
  ...
```

**Source:** `buildAutonomySystemSection` — `internal/engine/agent_autonomy.go`

---

## 7. Injected Context

`BuildInjectedContextWithBudget` pulls from the user's query:
- `[[file:path]]` markers → extract that path's contents
- fenced code blocks (` ```lang`) → include as-is

Trimmed to `InjectedTokens` budget (320 compact / 700 deep). Then `extractInjectedContext`
applies `InjectedBlocks` (2/3) and `InjectedLines` (80/140) caps.

**Source:** `BuildInjectedContextWithBudget` — `internal/context/prompt_render.go:200–215`

---

## 8. Coach Validation & Trajectory Rules

The coach (`internal/coach/coach.go`) observes each completed turn and emits
human-readable `Note`s. Available snapshot fields:

```go
type Snapshot struct {
    Question, Answer         string
    ToolSteps, TokensUsed    int
    ToolsUsed, FailedTools  []string
    Mutations                []string   // files written/edited
    Parked, ParkReason       bool, string
    ElapsedMs                int64

    // Context quality signals
    ContextFiles             int
    ContextSources           map[string]int  // "hotspot", "symbol-match", "marker"...
    QueryIdentifiers         int
    QueryIdentifierNames     []string
    UsefulQueryIdentifier    string
    QuestionHasFileMarker    bool

    // Hints (can be set by other subsystems)
    ValidationHint, TightenHint, RetrievalHint string
}
```

### Active Coach Rules

| Origin | Condition | Message |
|---|---|---|
| `mutation_unvalidated` | Files mutated, no validation mentioned | "Files mutated but answer didn't mention test/build/vet." |
| `repeated_failures` | ≥2 failed tools in one turn | "N tool call(s) failed — read errors directly." |
| `heavy_turn` | >20 000 tokens | "Heavy turn (~Nk tokens). Try narrowing or [[file:path]] marker." |
| `pseudo_tool_call` | ToolSteps=0 AND model used `[tool_call]` text | "Model used text-format tool call instead of native. Try different model." |
| `no_action_taken` | ToolSteps=0, question looks actionable, contains `?` | "Answered without tools. Use more explicit action verb." |
| `retrieval_symbol_miss` | QueryIdentifiers>0, ContextFiles>0, no symbol/marker hit | "Retrieval missed the symbol. Add [[file:path]] or rename-exact." |
| `retrieval_hotspot_only` | All context from hotspots (no files matched) | "Context came from graph only. Add [[file:path]] to focus." |
| `parked_budget` | Parked with reason=budget_exhausted | "Token budget exhausted. Try /split or narrower follow-up." |
| `parked_loop` | Parked for any other reason | "Hit step cap. Type /continue to resume." |
| `clean_pass` | Tools used, none failed, not parked, tokens 1 000–8 000 | "Clean pass." |

**Source:** `RuleObserver.Observe` — `internal/coach/coach.go:55–195`

### Trajectory Coach Gap — Repeated `read_file` Calls

**Observed behavior:** The model issues `read_file` calls repeatedly on related files
(e.g., reading the same file twice, or several files sequentially that could have
been fetched in one `tool_batch_call`).

**Root cause:** No rule in `Snapshot.Observe` detects repeated `read_file` calls.
The coach only sees `ToolsUsed` (list of tool names per turn) but has no per-tool
count or turn-over-turn memory.

**Missing rule — add to `RuleObserver.Observe` in coach.go:**
```go
func countReadFiles(tools []string) int {
    n := 0
    for _, t := range tools {
        if t == "read_file" {
            n++
        }
    }
    return n
}

// Inside Observe():
if countReadFiles(s.ToolsUsed) >= 3 && s.ToolSteps >= 3 {
    push(Note{
        Text:     "You've called read_file several times on similar inputs. " +
                  "Consolidate via tool_batch_call, or rethink whether another " +
                  "tool would answer the question in one shot.",
        Severity: SeverityInfo,
        Origin:   "trajectory_read_repetition",
    })
}
```

Also consider adding a `TrajectoryTools map[string]int` field to `Snapshot` so
the coach can detect when the same file appears in `ToolsUsed` across consecutive
turns — enabling a cross-turn "you read this file N times already" note.

---

## 9. Project Brief Loading

Loaded from `.dfmc/magic/MAGIC_DOC.md` at project root. Section selection:
1. Score all sections by keyword overlap with `query + task` terms
2. Return top 4 scored sections (sorted by original document order)
3. Fallback: first 48 lines of the file if no section scores > 0
4. Trim to `ProjectBriefTokens` (180 compact / 320 deep)

Section scoring ignores stopwords and matches on unigrams/bigrams from title + body.

**Source:** `loadProjectBrief` + `selectProjectBriefSections` + `scoreProjectBriefSection` —
`internal/context/prompt_render.go:440–560`

---

## 10. Provider Routing & Tool Style

`BuildToolCallPolicy` selects provider-specific tool-call wording (Anthropic vs
OpenAI function-calling style). Single source of truth — no per-provider
string literals elsewhere.

**Source:** `BuildToolCallPolicy` — `internal/context/prompt_render.go`

---

## 11. Current Gaps & Improvements

| # | Area | Gap | Fix |
|---|---|---|---|
| G1 | **Trajectory coach** | No rule detects repeated `read_file` calls within a turn or across turns | Add `trajectory_read_repetition` rule to `RuleObserver.Observe` with `countReadFiles` helper; consider `TrajectoryTools` map for cross-turn memory |
| G2 | **Profile keywords** | `ResolvePromptProfile` reads query twice (override check + keyword scan) — wasted work | Extract keyword scan to `scanPromptKeywords(query) []string` called once |
| G3 | **Tool group order** | Hardcoded in `toolGroupOrder` with magic ints; not extensible | Consider `tools.RegisterGroup(name, order int)` or a `[][]string` declaration |
| G4 | **Coach hints** | `ValidationHint`/`TightenHint`/`RetrievalHint` are free-form strings — no schema | Define a `CoachHint{Type, Text, Action}` struct; validate hint types at emission |
| G5 | **Prompt profile override** | `promptProfileOverride` breaks after first non-empty line — multi-line overrides don't work | Replace `break` with `break outer` or collect all matching prefixes |
| G6 | **Injected context** | `extractInjectedContext` has no deduplication — same file referenced twice gets included twice | Add a `seen map[string]struct{}` in `extractInjectedContext` |
| G7 | **Autonomy kickoff** | `shouldAutoKickoffAutonomy` only fires in `aggressive` mode; `auto` mode prepares preflight but never auto-kicks | Add a mid-confidence `auto` kickoff path (confidence ≥ 0.70, ≥ 4 subtasks) |

---

## Key Files

| File | Role |
|---|---|
| `internal/engine/agent_loop_prompt.go` | System prompt bundle assembly, meta-tool bridge text, tool descriptors |
| `internal/engine/agent_autonomy.go` | Autonomy preflight struct, directive rendering, auto-kickoff |
| `internal/context/prompt_render.go` | Profile/role/budget resolution, tool grouping/priorities, injected context, project brief loading |
| `internal/coach/coach.go` | Rule-based trajectory observer, coach note emission |
| `internal/tools/spec.go` | `ToolSpec` definition, `JSONSchema()` method |
| `internal/promptlib/defaults/system_prompts.yaml` | Static template referenced by the bundle |
| `internal/planning/` | Task splitting, subtask confidence scoring |

---

*Last updated: 2026-05-02* — *derived from code review of agent_loop_prompt.go, prompt_render.go, coach.go, agent_autonomy.go. Confirm against current source before acting on improvement suggestions.*
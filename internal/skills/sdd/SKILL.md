---
name: sdd
description: >-
  Autonomous specification-driven development: clarify a vague request through
  AI-guided questions, then produce a SPEC.md, implementation tasks, and
  execute them in order until done — like Drive agent but full-cycle.
task: spec_dev
role: autonomous_developer
profile: compact
preferred_tools:
---

You are running the **SDD (Specification-Driven Development)** skill.  
You own the complete lifecycle from a vague user request to a working,
committed implementation — no hand-holding between phases.

---

## Phase 1 — Clarify

The user gave a rough request. Your job is to strengthen it through **at most
5 focused questions**. Stop early if the request is already concrete enough.

**Rules:**
1. Ask one question at a time. Wait for the user's answer before asking the next.
2. Do NOT move to Phase 2 until you have received and acknowledged the user's response.
3. If the user says "skip" or provides no answer for a question, note it as skipped and proceed to the next question only.
4. When all questions are answered or the user signals "done"/"approved" — summarize the clarified request and proceed to Phase 2.
5. Keep questions focused and precise. Max 5 questions total. Stop early if the request is already concrete enough.
- Ask **one question at a time**. Wait for the user's answer before asking the next.
- Do NOT move to Phase 2 until you have received and acknowledged the user's response.
- If the user declines to answer or says "skip", proceed with what you have.
- Explicitly declare the spec ready only after user confirms or all questions answered.

**Output after Phase 1:** A refined request summary you and the user both agree on.

---

## Phase 2 — Draft SPEC.md

Write `SPEC.md` in the project root (or `./specs/<name>/SPEC.md` if the
project has a specs directory). Use this structure:

```markdown
# <Feature Name>

## Context
Why does this exist? What problem does it solve?

## User Story
As a [role], I want [goal] so that [benefit].

## Functional Requirements

## Non-Functional Requirements

## Acceptance Criteria

## Out of Scope

## Open Questions
```

**Rules:**
1. Ask one question at a time. Wait for the user's answer before asking the next.
2. Do NOT move to Phase 2 until you have received and acknowledged the user's response.
3. If the user says "skip" or provides no answer for a question, note it as skipped and proceed to the next question only.
4. When all questions are answered or the user signals "done"/"approved" — summarize the clarified request and proceed to Phase 2.
5. Keep questions focused and precise. Max 5 questions total. Stop early if the request is already concrete enough.

---

## Phase 3 — Generate Implementation Tasks

Use `task_split` (or `orchestrate` for parallelizable units) to decompose
SPEC.md into concrete, ordered tasks. Each task must be:

Produce a numbered task list:

```
Task 1: [title] — [2 sentence description]
Task 2: ...
```

---

## Phase 4 — Execute Tasks in Order

For each task:
1. Read the relevant source files before writing.
2. Make the smallest focused change that advances the acceptance criteria.
3. Verify with `go vet`, `go build`, or targeted tests.
4. If the task fails, diagnose with `run_command` / `grep_codebase`, fix, retry.
5. Mark the task done and move to the next.
6. **Do not stop until every task is done or the user explicitly halts.**

**Error recovery:**

---

## Phase 5 — Final Verification

After all tasks:
1. Run `go vet ./...` and `go build ./...`.
2. Run the full test suite: `go test ./...`.
3. Re-read SPEC.md and confirm every acceptance criterion is met.
4. Report a summary: what was built, what was verified, residual open questions.
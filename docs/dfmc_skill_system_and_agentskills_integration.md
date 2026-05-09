# DFMC Skill System & AgentSkills.io Integration

> **Status (2026-05-09):** Triggers, requires, version/author/tags/compatibility metadata implemented. See "What ships today" below — the bulk of this document beyond that section is forward-looking design notes that have NOT shipped (input/output schema, hot-reload, agentskillsignore, incremental watcher).

## Overview

DFMC's skill system provides an extensible, trigger-based mechanism for activating specialized code assistance workflows. The system is designed to be compatible with the [AgentSkills.io](https://agentskills.io) specification, supporting YAML frontmatter metadata, tool permission matrices, and trigger-based skill activation.

## What ships today

| Feature | Status | Location |
|---------|--------|----------|
| SKILL.md format (YAML frontmatter + markdown body) | ✅ | `catalog_loader.go` |
| Native DFMC YAML format (`<name>.yaml`) | ✅ | `catalog_loader.go` |
| `name`, `description`, `system_prompt` / markdown body | ✅ | `catalog.go` (Skill struct) |
| `allowed-tools` (space-separated) and `allowed_tools` (list) | ✅ | `catalog_loader.go` |
| `preferred_tools`, `task`, `role`, `profile` | ✅ | `catalog.go` |
| `.dfmc/skills/<name>/SKILL.md` directory bundles | ✅ | `catalog.go` `Discover` |
| `[[skill:name]]` explicit query markers | ✅ | `catalog_render.go` |
| `triggers:` auto-activation (regex, weighted) | ✅ NEW | `triggers.go` |
| `requires:` skill dependencies (cycle-safe) | ✅ NEW | `requires.go` |
| `version:` field | ✅ NEW | parsed in `applyExtendedFields` |
| `metadata.author`, `metadata.tags` | ✅ NEW | parsed in `applyExtendedFields` |
| `compatibility:` field (informational) | ✅ NEW | parsed in `applyExtendedFields` |
| Project + global skill discovery, builtins win | ✅ | `catalog.go` `Discover` |
| `dfmc skill list / info / run / export / install` | ✅ | `ui/cli/cli_skill.go` |
| `Selection.Origin` map (why each skill activated) | ✅ NEW | `catalog.go` |
| Builtin skills carry triggers (review/debug/audit/...) | ✅ NEW | `catalog_builtin.go` |
| Author-side lint (`ValidateSkillFile`, `ValidateSkillBytes`) | ✅ NEW | `validate.go` |
| `dfmc skill validate <path>` (alias `lint`) | ✅ NEW | `ui/cli/cli_skill.go` |
| `dfmc skill install` auto-validates before copy | ✅ NEW | `ui/cli/cli_skill.go` |
| `/skill validate <path>` slash command (TUI) | ✅ NEW | `ui/tui/slash_skills.go` |
| `/skill show <name>` shows version/triggers/requires/etc. | ✅ NEW | `ui/tui/slash_skills.go` |
| `POST /api/v1/skills/validate` web endpoint | ✅ NEW | `ui/web/server_tools_skills.go` |
| `compatibility:` engine-version check (semver) | ⚠ partial | parsed + stored on `Skill.Compatibility`; runtime gate (`compatibility.go`) was a separate WIP that disappeared from disk between sessions |
| Skill scaffolding (`RenderSkillTemplate`) | ✅ NEW | `scaffold.go` |
| `dfmc skill new <name> [--simple] [--global] [--force]` | ✅ NEW | `ui/cli/cli_skill.go` |
| `[auto]` badge in `/skill list` and `dfmc skill list` for trigger-armed skills | ✅ NEW | `ui/tui/slash_skills.go`, `ui/cli/cli_skill.go` |
| MCP exposure (`dfmc_skill_list/show/validate/run/explain`) | ✅ NEW | `ui/cli/cli_mcp_skill.go` |
| Activation preview / dry-run (`skills.Explain`) | ✅ NEW | `explain.go` |
| `dfmc skill explain <query>` + `/skill explain <query>` | ✅ NEW | `ui/cli/cli_skill.go`, `ui/tui/slash_skills.go` |
| `POST /api/v1/skills/explain` web endpoint | ✅ NEW | `ui/web/server_tools_skills.go` |
| `{active_skills}` system-prompt var carries origin badges | ✅ NEW | `internal/context/skill_aggregator.go` |
| Hard tool-restriction enforcement (allowed_tools at dispatch time) | ✅ NEW | `internal/skills/enforcement.go`, `internal/engine/skill_allowlist.go` |
| Multi-skill allowlist composition (UNION when all declare; OFF when any omits) | ✅ NEW | `EffectiveAllowedTools` in `enforcement.go` |

### Resolution priority

`ResolveForQuery` activates skills in this order:

1. **Explicit `[[skill:name]]` markers** (user wins).
2. **Trigger match** — highest-weighted regex hit ≥ `MinTriggerScore` (0.6).
3. **Task-hint fallback** (existing `skillForTask` map: `review` → review, `security` → audit, etc.).
4. **`requires:` expansion** — depth-first dependency walk on the resolved set; transitive prerequisites land before their dependants.

## Not yet implemented (design notes below)

- `inputs` / `outputs` schema validation — DFMC has no structured tool I/O surface yet
- Skill hot-reload — session-scoped CLI/TUI processes don't churn enough to justify a watcher
- `agentskillsignore` exclusion file — skill volume is too low to need it
- Engine-level `compatibility:` enforcement — currently parsed and shown in `skill info`, but not version-checked
- Hard tool-restriction enforcement — `allowed_tools` is currently a textual hint in the system prompt, not a dispatch-level filter
- Incremental file watcher / AST cache integration

The remainder of this document is forward-looking design that has NOT shipped. Treat as a planning artifact, not a current-state spec.

## Current Implementation

### Skill Catalog Structure

```
internal/skills/
├── catalog.go           — Skill discovery and lookup
├── catalog_loader.go    — SKILL.md parsing (YAML + markdown)
├── catalog_builtin.go    — 10 built-in skills (audit, debug, doc, etc.)
├── catalog_render.go    — System prompt decoration
└── catalog_test.go
```

### Skill File Format (SKILL.md)

```yaml
---
name: skill-name
description: "What this skill does"
allowed-tools: grep_codebase ast_query read_file
preferred-tools: ast_query
compatibility: "dfmc>=2.0.0"
metadata:
  author: Team Name
  tags: [tag1, tag2]
---

# System prompt body (markdown)
You are a specialized expert. When analyzing code, follow these steps...
```

### Skill Resolution Flow

```go
func (c *Catalog) ResolveForQuery(query string, taskHint string) *Skill {
    // 1. Explicit skill reference: [[skill:name]]
    if name := extractExplicitSkill(query); name != "" {
        return c.Lookup(name)
    }

    // 2. Trigger-based matching (TODO: implement)
    if skillName, score := c.triggerMatcher.Match(query); score > 0.5 {
        return c.Lookup(skillName)
    }

    // 3. Task hint fallback
    if taskHint != "" {
        return c.Lookup(taskHint)
    }

    return nil
}
```

---

## AgentSkills.io Compatibility

### Currently Supported

| Feature | Status | Location |
|---------|--------|----------|
| SKILL.md format (YAML frontmatter + markdown) | ✅ | `catalog_loader.go:46-126` |
| `name` field | ✅ | `catalog_loader.go` |
| `description` field | ✅ | Built-in skills |
| `allowed-tools` (space-separated) | ✅ | `catalog_loader.go:95-105` |
| `.dfmc/skills/<name>/SKILL.md` directory format | ✅ | `catalog.go:107-113` |
| `name` ≠ filename mismatch warning | ✅ | `catalog_loader.go:88-92` |
| `compatibility` + `metadata` | ✅ | Appended to description |
| Project + global skill discovery | ✅ | `catalog.go:60-106` |
| `preferred-tools` + `allowed-tools` list | ✅ | `Skill` struct |

### Missing / Incomplete

| Feature | Priority | Description |
|---------|----------|-------------|
| `triggers` field | P0 | Trigger-based automatic skill activation |
| `inputs` / `outputs` schema | P1 | Type validation for skill I/O |
| `version` field | P1 | Skill versioning and breaking change detection |
| Skill dependencies (`requires`) | P1 | One skill can depend on another |
| Hot-reload | P2 | Reload skills when files change |
| `agentskillsignore` support | P2 | Exclude directories from discovery |
| `compatibility` engine enforcement | P2 | Actually check version constraints |

---

## Proposed Enhancement: Triggers System

### YAML Format

```yaml
---
name: security-audit
description: Deep security analysis
allowed-tools: grep_codebase ast_query read_file

triggers:
  - pattern: "security|vulnerability|audit"
    weight: 0.9
  - pattern: "CVE|exploit|penetration.test"
    weight: 1.0
  - pattern: "sql.?inject|xss|secret|api.?key"
    weight: 0.85
---

You are a security expert...
```

### Trigger Matcher Implementation

```go
// internal/skills/triggers.go
package skills

import (
    "regexp"
    "strings"
    "sync"
)

// Trigger represents a condition that activates a skill
type Trigger struct {
    Pattern *regexp.Regexp
    Weight  float64
    Raw     string
}

// TriggerMatcher matches user queries against skill triggers
type TriggerMatcher struct {
    mu    sync.RWMutex
    index map[string][]Trigger // skillName → triggers
}

// RegisterSkillTriggers adds triggers for a skill
func (m *TriggerMatcher) RegisterSkillTriggers(name string, triggers []string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    for _, t := range triggers {
        // Format: "pattern" or "pattern:0.9" (with weight)
        parts := strings.SplitN(t, ":", 2)
        pattern := parts[0]
        weight := 0.8 // default

        if len(parts) == 2 {
            w, err := strconv.ParseFloat(parts[1], 64)
            if err == nil {
                weight = w
            }
        }

        re, err := regexp.Compile(pattern)
        if err != nil {
            return fmt.Errorf("invalid trigger pattern %q: %w", pattern, err)
        }

        m.index[name] = append(m.index[name], Trigger{
            Pattern: re,
            Weight:  weight,
            Raw:     t,
        })
    }
    return nil
}

// Match finds the best matching skill for a query
func (m *TriggerMatcher) Match(query string) (skillName string, score float64) {
    m.mu.RLock()
    defer m.mu.RUnlock()

    query = strings.ToLower(query)

    for name, triggers := range m.index {
        for _, t := range triggers {
            if t.Pattern.MatchString(query) {
                if t.Weight > score {
                    score = t.Weight
                    skillName = name
                }
            }
        }
    }

    return
}
```

---

## Proposed Enhancement: Skill Dependencies

### YAML Format

```yaml
---
name: refactor-safe
triggers:
  - pattern: "refactor|restructure|reorganize"

requires:
  - skill: onboard
    reason: "Understand project structure first"
  - skill: audit
    reason: "Check security implications"
---

You are a safe refactoring expert...
```

### Dependency Resolution

```go
// internal/skills/requires.go
type SkillDependency struct {
    Skill  string
    Reason string
}

// ResolveDependencies returns all required skills in order
func (c *Catalog) ResolveDependencies(skillName string) ([]*Skill, error) {
    skill := c.Lookup(skillName)
    if skill == nil {
        return nil, fmt.Errorf("skill not found: %s", skillName)
    }

    var result []*Skill
    visited := make(map[string]bool)

    var resolve func(s *Skill) error
    resolve = func(s *Skill) error {
        if visited[s.Name] {
            return nil // cycle prevention
        }
        visited[s.Name] = true

        for _, dep := range s.Dependencies {
            depSkill := c.Lookup(dep.Skill)
            if depSkill == nil {
                return fmt.Errorf("required skill %q not found for %q", dep.Skill, s.Name)
            }
            if err := resolve(depSkill); err != nil {
                return err
            }
            result = append(result, depSkill)
        }
        return nil
    }

    if err := resolve(skill); err != nil {
        return nil, err
    }

    return result, nil
}
```

---

## Proposed Enhancement: Input/Output Schema

### YAML Format

```yaml
---
name: security-audit

inputs:
  - name: target
    type: path
    required: true
    description: "Directory or file to audit"
  - name: severity
    type: enum
    values: [low, medium, high, critical]
    default: high
  - name: include_deps
    type: boolean
    default: false

outputs:
  - name: findings
    type: array
    description: "List of security findings"
    schema:
      type: object
      properties:
        severity: { type: string }
        cwe_id: { type: string }
        file: { type: string }
        line: { type: integer }
        description: { type: string }
        remediation: { type: string }
  - name: score
    type: number
    description: "Overall security score 0-100"
---
```

### Validation

```go
// internal/skills/validate.go
var (
    requiredFields = []string{"name", "description"}
    allowedFields  = []string{
        "name", "description", "version", "compatibility",
        "allowed-tools", "preferred-tools", "triggers",
        "inputs", "outputs", "requires",
    }
)

func ValidateSkill(name string, data map[string]any) []ValidationError {
    var errs []ValidationError

    for _, field := range requiredFields {
        if v, ok := data[field]; !ok || v == "" {
            errs = append(errs, ValidationError{
                Field:   field,
                Message: fmt.Sprintf("required field %q is missing or empty", field),
            })
        }
    }

    // Validate inputs
    if inputs, ok := data["inputs"].([]any); ok {
        for i, input := range inputs {
            if m, ok := input.(map[string]any); ok {
                errs = append(errs, validateInput(m, i)...)
            }
        }
    }

    // Validate outputs
    if outputs, ok := data["outputs"].([]any); ok {
        for i, output := range outputs {
            if m, ok := output.(map[string]any); ok {
                errs = append(errs, validateOutput(m, i)...)
            }
        }
    }

    // Validate allowed-tools
    if tools, ok := data["allowed-tools"].(string); ok {
        for _, tool := range strings.Fields(tools) {
            if !isValidTool(tool) {
                errs = append(errs, ValidationError{
                    Field:   "allowed-tools",
                    Message: fmt.Sprintf("unknown tool: %q", tool),
                })
            }
        }
    }

    return errs
}
```

---

## Proposed Enhancement: Skill Hot-Reload

```go
// internal/skills/hotreload.go
package skills

import (
    "log"
    "sync"
    "time"
)

type HotReload struct {
    catalog  *Catalog
    watcher  *watcher.Watcher
    mu       sync.RWMutex
    modified map[string]time.Time
}

func NewHotReload(catalog *Catalog, skillsDir string) *HotReload {
    hr := &HotReload{
        catalog:  catalog,
        modified: make(map[string]time.Time),
    }

    hr.watcher = watcher.New([]string{skillsDir}, func(path string, op fsnotify.Op) {
        hr.reloadSkill(path)
    })
    hr.watcher.Start()

    return hr
}

func (hr *HotReload) reloadSkill(path string) {
    hr.mu.Lock()
    defer hr.mu.Unlock()

    // Debounce: skip if recently reloaded
    if last, ok := hr.modified[path]; ok {
        if time.Since(last) < 500*time.Millisecond {
            return
        }
    }

    skillName := extractSkillName(path)
    if skillName == "" {
        return
    }

    hr.catalog.RemoveSkill(skillName)
    if err := hr.catalog.LoadSkillFile(path); err != nil {
        log.Printf("Failed to reload skill %q: %v", skillName, err)
        return
    }

    hr.modified[path] = time.Now()
    log.Printf("Skill reloaded: %s", skillName)
}

func (hr *HotReload) Stop() {
    hr.watcher.Stop()
}
```

---

## Incremental File Watcher

### Purpose

Currently, every DFMC command re-parses the entire project. The incremental watcher enables:

1. **AST Cache** — Parse only changed files, not entire project
2. **Codemap Cache** — Update only affected nodes, not full graph rebuild
3. **Skill Hot-Reload** — Reload skills when SKILL.md files change

### Architecture

```go
// internal/watcher/watcher.go
package watcher

import "github.com/fsnotify/fsnotify"

type FileChange struct {
    Path    string
    Op      fsnotify.Op
    ModTime time.Time
}

type Watcher struct {
    mu       sync.RWMutex
    fsnotify *fsnotify.Watcher
    paths    []string
    onChange func(string, fsnotify.Op)
    stop     chan struct{}
}

func New(paths []string, onChange func(string, fsnotify.Op)) (*Watcher, error) {
    w, err := fsnotify.NewWatcher()
    if err != nil {
        return nil, err
    }

    for _, p := range paths {
        if err := w.Add(p); err != nil {
            w.Close()
            return nil, err
        }
    }

    return &Watcher{
        fsnotify: w,
        paths:    paths,
        onChange: onChange,
        stop:     make(chan struct{}),
    }, nil
}

func (w *Watcher) Start() {
    go w.run()
}

func (w *Watcher) run() {
    for {
        select {
        case event, ok := <-w.fsnotify.Events:
            if !ok {
                return
            }
            // Ignore chmod (permissions only)
            if event.Op == fsnotify.Chmod {
                continue
            }
            w.onChange(event.Name, event.Op)

        case err := <-w.fsnotify.Errors:
            if err != nil {
                log.Printf("Watcher error: %v", err)
            }

        case <-w.stop:
            return
        }
    }
}

func (w *Watcher) Stop() {
    close(w.stop)
    w.fsnotify.Close()
}
```

### Debouncer

```go
// internal/watcher/debouncer.go
package watcher

import (
    "sync"
    "time"
)

type Debouncer struct {
    mu      sync.Mutex
    pending map[string]*time.Timer
    delay   time.Duration
}

func NewDebouncer(delay time.Duration) *Debouncer {
    return &Debouncer{
        pending: make(map[string]*time.Timer),
        delay:   delay,
    }
}

func (d *Debouncer) Debounce(key string, fn func()) {
    d.mu.Lock()
    defer d.mu.Unlock()

    if t, ok := d.pending[key]; ok {
        t.Stop()
    }

    d.pending[key] = time.AfterFunc(d.delay, func() {
        d.mu.Lock()
        delete(d.pending, key)
        d.mu.Unlock()
        fn()
    })
}
```

---

## Incremental AST Updates

```go
// internal/ast/incremental.go
package ast

import (
    "crypto/sha256"
    "os"
)

// IncrementalParse parses only changed files
func (e *Engine) IncrementalParse(path string) error {
    // 1. Check if file has changed
    info, err := os.Stat(path)
    if err != nil {
        return err
    }

    currentHash := hashFile(path)
    if cached, ok := e.cache.Get(path); ok && cached.Hash == currentHash {
        return nil // No change, use cached
    }

    // 2. Parse the file
    tree, err := e.ParseFile(path)
    if err != nil {
        return err
    }

    // 3. Update symbols
    symbols := e.ExtractSymbols(tree, path)
    e.cache.Set(path, &CacheEntry{
        Tree:   tree,
        Hash:   currentHash,
        Symbols: symbols,
        ModTime: info.ModTime(),
    })

    // 4. Update dependent files (imports)
    for _, imp := range symbols.Imports {
        if dep, ok := e.findDependency(imp); ok {
            e.IncrementalParse(dep) // Recursive update
        }
    }

    return nil
}

func hashFile(path string) string {
    data, _ := os.ReadFile(path)
    return fmt.Sprintf("%x", sha256.Sum256(data))
}
```

---

## Incremental Codemap Updates

```go
// internal/codemap/incremental.go
package codemap

// IncrementalUpdate updates only affected nodes
func (g *Graph) IncrementalUpdate(path string) error {
    // 1. Find affected nodes
    affected := g.findAffectedNodes(path)
    if len(affected) == 0 {
        return nil
    }

    // 2. Update only affected nodes
    for _, node := range affected {
        if err := g.rebuildNode(node); err != nil {
            return err
        }
    }

    // 3. Recalculate metrics for affected region
    region := g.findRegion(affected)
    g.recalculateMetrics(region)

    return nil
}

func (g *Graph) findAffectedNodes(path string) []*Node {
    var result []*Node

    for _, node := range g.Nodes {
        if contains(node.Files, path) {
            result = append(result, node)
        }

        // Also include nodes that import this file
        for _, dep := range node.Dependencies {
            if dep == path {
                result = append(result, node)
                break
            }
        }
    }

    return result
}
```

---

## Cache Invalidation Coordination

```go
// internal/cache/invalidator.go
package cache

type Invalidator struct {
    mu          sync.Mutex
    astCache    *ast.Cache
    codemapCache *codemap.Cache
}

func (ci *Invalidator) Invalidate(path string) {
    ci.mu.Lock()
    defer ci.mu.Unlock()

    // Invalidate AST cache
    ci.astCache.Invalidate(path)

    // Find and invalidate affected codemap nodes
    affected := ci.codemapCache.AffectedNodes(path)
    for _, node := range affected {
        ci.codemapCache.Invalidate(node)
    }
}

func (ci *Invalidator) InvalidateAll() {
    ci.mu.Lock()
    defer ci.mu.Unlock()

    ci.astCache.Clear()
    ci.codemapCache.Clear()
}
```

---

## Skill → Tool Permission Matrix

| Skill | grep_codebase | ast_query | find_symbol | read_file | glob | codemap | run_command |
|-------|---------------|-----------|-------------|-----------|------|---------|-------------|
| audit | ✅ | | | ✅ | ✅ | | |
| debug | ✅ | ✅ | ✅ | ✅ | | | |
| doc | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| explain | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| generate | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| onboard | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| refactor | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| review | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| test | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | |
| security-audit | ✅ | ✅ | | ✅ | ✅ | | |

---

## Proposed New Skills

### 1. security-audit

```yaml
# .dfmc/skills/security-audit/SKILL.md
---
name: security-audit
version: 1.0.0
compatibility: "dfmc>=2.0.0"
description: |
  Deep security analysis for Go/TypeScript codebases.
  Detects SQL injection, XSS, hardcoded secrets, and more.

allowed-tools: grep_codebase ast_query find_symbol read_file glob
preferred-tools: ast_query grep_codebase

triggers:
  - pattern: "security|vulnerability|audit"
    weight: 0.9
  - pattern: "CVE|exploit|penetration.test"
    weight: 1.0

inputs:
  - name: target
    type: path
    required: true
    description: "Directory or file to audit"
  - name: severity
    type: enum
    values: [low, medium, high, critical]
    default: high

outputs:
  - name: findings
    type: array
    description: "List of security findings"
  - name: score
    type: number
    description: "Overall security score 0-100"
---

You are a senior security expert specializing in application security.

## Your Capabilities

1. **SQL Injection Detection**
   - Find string concatenation in SQL queries
   - Identify unsafe Sprintf/Format usage
   - Check for parameterized query usage

2. **XSS Prevention Analysis**
   - Detect innerHTML, writeln with user input
   - Find missing output encoding

3. **Hardcoded Secrets**
   - Detect API keys, passwords, tokens in code
   - Check for proper env variable usage

Output format:
## Finding: [SEVERITY] [CWE-ID]
- File: `path:line`
- Description: ...
- Remediation: ...
```

### 2. refactor-safe

```yaml
# .dfmc/skills/refactor-safe/SKILL.md
---
name: refactor-safe
description: Safe refactoring with impact analysis
allowed-tools: ast_query codemap find_symbol read_file

requires:
  - skill: onboard
    reason: "Understand project structure first"
  - skill: audit
    reason: "Check security implications"

triggers:
  - pattern: "refactor|restructure|reorganize"
    weight: 0.85
---

You are a safe refactoring expert. Before making changes:
1. Understand the codebase structure (uses onboard skill)
2. Check for security impacts (uses audit skill)
3. Analyze dependency graph (codemap tool)
4. Plan minimal reversible changes
```

### 3. test-generate

```yaml
# .dfmc/skills/test-generate/SKILL.md
---
name: test-generate
description: Generate tests for coverage gaps
allowed-tools: ast_query find_symbol read_file glob

triggers:
  - pattern: "test|coverage|spec|specify|it\\.should"
    weight: 0.8
---

You are a test generation expert. For any code:
1. Identify edge cases and boundary conditions
2. Follow existing test patterns in the codebase
3. Generate meaningful test cases with clear assertions
4. Ensure proper mocking/stubbing
```

---

## File Structure Changes

```
internal/skills/
├── catalog.go           # Existing — update ResolveForQuery
├── catalog_loader.go    # Existing — add triggers/inputs/outputs parsing
├── catalog_builtin.go  # Existing
├── catalog_render.go    # Existing
├── catalog_test.go     # Existing
├── triggers.go          # NEW — trigger matching engine
├── requires.go         # NEW — skill dependencies
├── validate.go          # NEW — input/output/schema validation
├── hotreload.go        # NEW — skill hot-reload
└── discover.go          # NEW — agentskillsignore support

internal/watcher/             # NEW DIRECTORY
├── watcher.go           # fsnotify wrapper
├── debouncer.go         # Event debouncing
└── coordinator.go        # AST + Codemap sync

internal/ast/
├── engine.go            # Existing
├── cache.go             # Existing
└── incremental.go       # NEW — incremental parse API

internal/codemap/
├── engine.go            # Existing
├── cache.go             # Existing
└── incremental.go       # NEW — incremental update

internal/cache/               # NEW DIRECTORY
└── invalidator.go      # NEW — cache invalidation coordination
```

---

## Priority Order

### Phase 1: Skills Infrastructure (1-2 weeks)
1. `triggers` field parsing + matching engine
2. Skill hot-reload (watcher integration)
3. Skill validation (name required, allowed-tools check)

### Phase 2: Schema & Dependencies (1 week)
4. `inputs`/`outputs` schema + validation
5. Skill dependencies (`requires` field)
6. `compatibility` engine-level enforcement

### Phase 3: Caching & Watcher (2-3 weeks)
7. Incremental AST parser
8. Incremental Codemap updates
9. Cache invalidation coordination

### Phase 4: New Skills (ongoing)
10. security-audit skill
11. refactor-safe skill
12. test-generate skill
13. migrate skill
14. performance skill

### Phase 5: Ecosystem (2-3 weeks)
15. `agentskillsignore` support
16. Skill metrics/telemetry
17. Skill version + breaking change detection
18. Skill marketplace (remote registry)

---

## References

- [AgentSkills.io Specification](https://agentskills.io)
- DFMC Skill Catalog: `internal/skills/catalog.go`
- Skill Loader: `internal/skills/catalog_loader.go`
- Codemap Engine: `internal/codemap/engine.go`
- AST Engine: `internal/ast/engine.go`
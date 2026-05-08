# Tool Calling Security & Orchestration Enhancement Suggestions

> **Purpose**: Document gaps in current tool calling safety mechanisms and propose concrete enhancements to prevent hallucinations, argument misuse, and runtime failures.

---

## 1. Current Protection Mechanisms (Already Implemented)

### 1.1 DAG Validation — `orchestrate_dag.go:187`

The `ValidateDAG` function provides strong structural guarantees:

| Check | Behavior on Violation |
|-------|----------------------|
| Duplicate stage ID | `ErrDuplicateStageID` — hard error |
| Unknown stage reference | `ErrUnknownDependency` — hard error |
| Self-loop (A depends on A) | `ErrSelfDependency` — hard error |
| Cycle detection (Kahn's topological sort) | `ErrCycleDetected` — hard error |

**Code location**: `dfmc/orchestration/orchestrate_dag.go:187`

```go
// Every violation causes immediate failure — no silent bypass
if strings.Contains(err.Error(), "duplicate") {
    return ErrDuplicateStageID
}
if strings.Contains(err.Error(), "unknown") {
    return ErrUnknownDependency
}
if strings.Contains(err.Error(), "cycle") {
    return ErrCycleDetected
}
```

**Effectiveness**: Prevents malformed orchestration graphs entirely. A model cannot accidentally create a broken execution plan that slips through.

---

### 1.2 Graceful Degradation — `orchestrate.go:17-18`

When task splitting fails (low confidence or single result), the orchestrator falls back to single-agent execution:

```go
// orchestrate.go:17-18
if len(subTasks) <= 1 || confidence < 0.4 {
    // Fall back to single-agent — no crash, just simplified execution
    return singleAgentExecute(ctx, task)
}
```

**Effectiveness**: Prevents complete failure when the model produces a degenerate split. The system degrades gracefully instead of crashing.

---

### 1.3 Dependency Failure Handling — `orchestrate_dag.go:261`

When a stage's dependency fails, the stage is marked as `StatusSkipped` with reason:

```go
// orchestrate_dag.go:261
stage.Status = StatusSkipped
stage.Err = fmt.Errorf("skipped: dep %s failed", failedDep)
```

**Effectiveness**: Prevents cascading failure where one bad result poisons downstream stages. Wrong results are not produced; stages are simply skipped.

---

### 1.4 Field Aliasing — `meta.go:607-614` and `meta.go:642-643`

Argument field aliasing tolerates third-party model variation:

```go
// meta.go:607-614 — argument field aliasing
aliases := map[string]string{
    "input":      "args",
    "arguments":  "args",
    "params":     "args",
}

// meta.go:642-643 — tool name field aliasing
if _, ok := m["tool"]; ok {
    m["name"] = m["tool"] // normalize
}
```

**Effectiveness**: Prevents failures due to semantically equivalent but syntactically different field names from third-party models.

---

## 2. Missing Protection Mechanisms (Security Gaps)

### 2.1 **No Schema Validation for Tool Arguments**

**Problem**: When a model generates a tool call with arguments, there is no enforcement that argument names, types, or required fields match the tool's actual schema.

**Example of failure mode**:
```json
// Model produces (intentionally or by hallucination):
{
  "name": "read_file",
  "args": {
    "path": 12345,        // wrong type: should be string
    "line_start": "five", // wrong type: should be int
    "offset": null        // valid but unexpected
  }
}
```

**Current behavior**: This malformed call passes through to `read_file(path: any)` without validation. Depending on the underlying implementation, this may cause:
- Runtime panic (Go type assertion failure)
- Silent data corruption (offset=null treated as 0)
- Unexpected partial reads

**Code location of gap**: `dfmc/orchestration/meta.go` — the `parseToolCall` function accepts `args map[string]any` without validating structure against `ToolSpec`.

---

### 2.2 **No Runtime Type Checking for Arguments**

**Problem**: Even when argument names are correct, the model may pass values of the wrong type. There is no `ArgType` enforcement at call time.

**Example**:
```json
{
  "name": "glob",
  "args": {
    "pattern": ["*.go", "*.md"],  // should be string, not array
    "path": 12345
  }
}
```

**Current behavior**: The model produces a list where a string was expected. No validation layer catches this before the call reaches the glob implementation.

---

### 2.3 **No Whitelist Enforcement for Available Tools**

**Problem**: A model could (through hallucination or prompt injection) attempt to call a tool that does not exist in the backend registry. There is no whitelist enforcing that only `tool_search`/`tool_help`-discovered tools may be used.

**Example of failure mode**:
```json
{
  "name": "execute_shell_command",  // does not exist — hallucinated name
  "args": { "cmd": "rm -rf /" }
}
```

**Current behavior**: The orchestrator trusts the model-generated `name` field without verifying it against the known tool registry.

---

### 2.4 **No Enforced Discovery Workflow**

**Problem**: The system does not enforce that the model must call `tool_search` or `tool_help` before using a tool. A model could hallucinate a tool name and arguments without ever consulting the registry.

**Current behavior**: `tool_search` exists as a tool but is not a required prerequisite. The model can produce `tool_call` directly without prior discovery.

---

### 2.5 **`force_sequential` Only Applies to Collector Stage**

**Problem**: The `force_sequential` flag is only checked in the collector logic (`orchestrate_dag.go`). If a stage incorrectly sets `force_sequential=true` alongside `parallel=true`, the orchestrator does not detect the contradiction.

**Code location**: `orchestrate_dag.go` — no cross-field validation between `parallel` and `force_sequential` exists.

---

## 3. Recommended Enhancements

### 3.1 Schema Validation Layer

Add a `ValidateToolCall` function that runs before every `tool_call`:

```go
// dfmc/orchestration/tool_validation.go (proposed)

type ArgSpec struct {
    Name        string
    Type        string  // "string", "int", "bool", "array", "object", "any"
    Required    bool
    Default     any
    Description string
}

type ToolSpec struct {
    Name        string
    Description string
    RiskLevel   string  // "read", "write", "execute", "meta"
    Cost        string  // "low", "medium", "high"
    Args        []ArgSpec
    Returns     string
    Rules       []string
}

func ValidateToolCall(call *ToolCall) error {
    spec, ok := GetToolSpec(call.Name)
    if !ok {
        return ErrUnknownTool // hallucinated tool name
    }

    // Check required args
    for _, arg := range spec.Args {
        if arg.Required && call.Args[arg.Name] == nil {
            return ErrMissingRequiredArg
        }
        // Type check
        if call.Args[arg.Name] != nil {
            if !TypeMatches(call.Args[arg.Name], arg.Type) {
                return ErrWrongArgType
            }
        }
    }

    return nil
}
```

**Where to integrate**: `meta.go:parseToolCall` → after parsing, call `ValidateToolCall` before returning.

---

### 3.2 Tool Registry Whitelist

Maintain an internal `toolRegistry` map:

```go
// dfmc/orchestration/tool_registry.go (proposed)

var toolRegistry = map[string]ToolSpec{
    "read_file":      readFileSpec,
    "glob":           globSpec,
    "grep_codebase":  grepCodebaseSpec,
    // ... all registered tools
}

func RegisterTool(name string, spec ToolSpec) {
    toolRegistry[name] = spec
}

func IsRegisteredTool(name string) bool {
    _, ok := toolRegistry[name]
    return ok
}
```

**Enforcement point**: In `tool_call` execution path, check `IsRegisteredTool(name)` before dispatch.

---

### 3.3 Discovery-First Workflow Enforcement

Add a session-level flag tracking tool discovery:

```go
type SessionState struct {
    DiscoveredTools map[string]bool  // tools the model has called tool_search/tool_help for
    DiscoveryRequired bool           // enforce discovery-before-use
}

// In tool_call execution:
func (tc *ToolCall) Execute(ctx context.Context) (any, error) {
    if ctx.SessionState.DiscoveryRequired && !ctx.SessionState.DiscoveredTools[tc.Name] {
        return nil, ErrToolNotDiscovered
    }
    // proceed with call
}
```

**Effectiveness**: Prevents the model from hallucinating tools without first consulting the registry.

---

### 3.4 Parallel + Sequential Conflict Detection

Add cross-field validation in DAG validation:

```go
// orchestrate_dag.go (proposed addition to ValidateDAG)
func ValidateDAGStage(stage *Stage) error {
    if stage.Parallel && stage.ForceSequential {
        return ErrConflictingParallelFlags
    }
    // existing checks...
    return nil
}
```

**Effectiveness**: Prevents contradictory stage configuration that could cause undefined behavior.

---

### 3.5 Argument Type Coercion with Warning

For cases where the model passes a close-but-wrong type (e.g., `"123"` instead of `123`), attempt coercion and log a warning:

```go
// Coerce string to int if possible, emit warning
if expected == "int" && actual == "string" {
    if coerced, err := strconv.Atoi(actual.(string)); err == nil {
        log.Warn("Argument %s was coerced from string to int")
        return coerced
    }
}
```

This prevents hard failures on minor type mismatches while still alerting the system to the discrepancy.

---

## 4. Risk Summary Table

| Mechanism | Current | Recommended | Risk if Not Added |
|-----------|---------|-------------|-------------------|
| DAG validation | ✅ Complete | — | Medium |
| Graceful degradation | ✅ Partial | — | Medium |
| Dependency failure handling | ✅ Complete | — | Low |
| Field aliasing | ✅ Complete | — | Low |
| Schema validation | ❌ Missing | **High priority** | **Critical** |
| Type checking | ❌ Missing | **High priority** | **Critical** |
| Tool whitelist | ❌ Missing | High | High |
| Discovery-first workflow | ❌ Missing | Medium | High |
| Parallel/sequential conflict check | ❌ Missing | Low | Low |
| Type coercion with warning | ❌ Missing | Low | Medium |

---

## 5. Implementation Priority

1. **P0 (Critical)**: Schema validation — prevents hallucinations from causing runtime crashes
2. **P0 (Critical)**: Tool registry whitelist — prevents calls to non-existent tools
3. **P1 (High)**: Type checking — prevents data corruption from type-mismatched arguments
4. **P1 (High)**: Discovery-first enforcement — ensures model always uses registered tools
5. **P2 (Medium)**: Parallel/sequential conflict detection
6. **P3 (Low)**: Type coercion with warning

---

## 6. Related Code Locations

| File | Purpose |
|------|---------|
| `dfmc/orchestration/orchestrate_dag.go:187` | DAG validation |
| `dfmc/orchestration/orchestrate.go:17-18` | Graceful degradation |
| `dfmc/orchestration/meta.go:607-614` | Argument field aliasing |
| `dfmc/orchestration/meta.go:642-643` | Tool name field aliasing |
| `dfmc/orchestration/tool_discovery.go` | Tool discovery (existing) |
| `dfmc/model/tool.go` | Tool call model |
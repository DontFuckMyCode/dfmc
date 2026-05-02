# Engine Fix Review

## Topics Reviewed
- engine
- test
- requireready

## Files
- `internal/tools/engine.go`
- `internal/engine/engine.go`

## Findings

### internal/tools/engine.go
- Engine struct with `mu sync.RWMutex` for registry protection
- Key types: `Request`, `Result`, `Tool` interface
- Constants: `maxReadSnapshots = 256`, `maxRecentFailures = 256`
- Registry map protected by RWMutex
- Recent failures tracking with separate `failureMu sync.Mutex`

### internal/engine/engine.go
- Separate engine package for agent execution
- Agent loop and context management
- `invalidateContextForTool()` handles context invalidation for edit_file, write_file, apply_patch
- Modified files tracked with `modifiedFiles map[string]time.Time`

### test
- `internal/engine/engine_test.go` covers core engine logic
- `internal/tools/engine_test.go` covers tool registry

### requireready
- Pattern `RequireReady()` used to ensure engine initialized before tool execution
- Present in agent loop setup and context initialization paths
- Guard for operations requiring fully initialized engine state

## Recommendation
Both engine implementations have proper mutual exclusion. The tools/engine.go uses sync.RWMutex for registry, and internal/engine/engine.go uses mu.Lock() for context invalidation. The RequireReady pattern provides additional safety for initialization checks.

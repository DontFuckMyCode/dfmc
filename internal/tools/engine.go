package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/mcp"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

type Request struct {
	ProjectRoot string         `json:"project_root"`
	Params      map[string]any `json:"params,omitempty"`
}

type Result struct {
	Success    bool           `json:"success"`
	Output     string         `json:"output,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	Truncated  bool           `json:"truncated,omitempty"`
	DurationMs int64          `json:"duration_ms"`
}

type Tool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, req Request) (Result, error)
}

const (
	maxReadSnapshots  = 256
	maxRecentFailures = 256
)

type Engine struct {
	mu              sync.RWMutex
	registry        map[string]Tool
	cfg             config.Config
	failureMu       sync.Mutex
	recentFailures  map[string]int
	recentFailOrder []string
	readMu          sync.RWMutex
	readSnapshots   map[string]string
	readSnapshotLRU []string
	delegateTool    *DelegateTaskTool
	orchestrateTool *OrchestrateTool

	// reasoningPublisher is the optional callback the higher-level engine
	// installs at construction to receive tool self-narration. Execute()
	// strips the virtual `_reason` arg from every params map and, when
	// non-empty, calls this with (toolName, reason). Nil-safe: when no
	// publisher is installed (tests, embedded use), the field is just
	// stripped silently. Atomic via the mu lock.
	reasoningPublisher ReasoningPublisher

	// taskStore is the bbolt-backed task persistence. Injected via
	// SetTaskStore so the package stays free of an engine-cycle.
	taskStore *taskstore.Store

	// codemap is the project codemap engine. Injected via SetCodemap
	// so the dependency_graph tool can query edges without importing
	// the engine package (which would create a cycle).
	codemap *codemap.Engine

	// mcpBridge exposes MCP server tools. Set by the engine-side MCP
	// bridge adapter after clients are loaded. Nil when no MCP servers
	// are configured.
	mcpBridge mcp.ToolBridge

	// pathLocks serialises concurrent (read-gate-check → write) operations
	// on the same absolute path. Sub-agent fan-out can touch the same file
	// from multiple goroutines; without serialisation the window between
	// EnsureReadBeforeMutation and os.WriteFile is a TOCTOU race.
	pathLocks sync.Map
}

// LockPath returns a release function for the per-path lock covering abs.
// Empty abs is a no-op (returns a nop release). Used by apply_patch and
// write_file to serialise the read-gate → write window per target file.
func (e *Engine) LockPath(abs string) func() {
	if abs == "" {
		return func() {}
	}
	// Load or create the per-path mutex.
	lock, _ := e.pathLocks.LoadOrStore(abs, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	return func() { mu.Unlock() }
}

// ReasoningPublisher is the callback shape the higher-level engine wires
// into tools.Engine to surface tool-call self-narration. The engine
// translates these into `tool:reasoning` events on its EventBus so TUI/
// web/CLI can render the why above each tool result. Kept as a function
// type (not an interface) so the tools package doesn't import the
// engine package — that would create a cycle.
type ReasoningPublisher func(toolName, reason string)

// SetReasoningPublisher installs the self-narration callback. Safe to
// call before or after registration; the publisher is consulted on
// every Execute(). Pass nil to disable.
func (e *Engine) SetReasoningPublisher(pub ReasoningPublisher) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reasoningPublisher = pub
}

func (e *Engine) SetTaskStore(store *taskstore.Store) {
	e.taskStore = store
	t, ok := e.Get("todo_write")
	if !ok {
		return
	}
	if tw, ok := t.(*TodoWriteTool); ok {
		tw.SetStore(store)
	}
}

// TaskStore returns the injected task store, or nil when no store was set.
func (e *Engine) TaskStore() *taskstore.Store {
	return e.taskStore
}

// SetCodemap injects the project codemap engine so the dependency_graph
// tool can query edges. Called by engine.Init after CodeMap is wired.
func (e *Engine) SetCodemap(cm *codemap.Engine) {
	e.codemap = cm
}

// SetMCPBridge installs the MCP bridge after external clients are loaded.
// The bridge exposes tools from one or more MCP servers as native tools.
func (e *Engine) SetMCPBridge(bridge mcp.ToolBridge) {
	e.mcpBridge = bridge
}

func New(cfg config.Config) *Engine {
	e := &Engine{
		registry:        map[string]Tool{},
		cfg:             cfg,
		recentFailures:  map[string]int{},
		recentFailOrder: []string{},
		readSnapshots:   map[string]string{},
		readSnapshotLRU: []string{},
	}
	e.Register(NewReadFileTool())
	e.Register(NewWriteFileTool())
	e.Register(NewEditFileTool())
	e.Register(NewListDirTool())
	e.Register(NewGrepCodebaseTool())
	e.Register(NewGlobTool())
	e.Register(NewThinkTool())
	e.Register(NewTodoWriteTool())
	e.Register(NewWebFetchTool())
	e.Register(NewWebSearchTool())
	e.Register(NewASTQueryTool())
	e.Register(NewFindSymbolTool())
	e.Register(NewCodemapTool())
	e.Register(NewTestDiscoveryTool())
	depTool := NewDependencyGraphTool()
	// SetEngine uses a deferred pattern: it stores the engine and will
	// re-read the codemap field when SetCodemap is called later (from
	// engine.Init). This avoids a circular import while still allowing
	// the engine to inject the codemap after tools.New() returns.
	depTool.SetEngine(e)
	e.Register(depTool)
	apTool := NewApplyPatchTool()
	apTool.SetEngine(e)
	e.Register(apTool)
	e.Register(NewGitStatusTool())
	e.Register(NewGitDiffTool())
	e.Register(NewGitBranchTool())
	e.Register(NewGitLogTool())
	e.Register(NewGitBlameTool())
	e.Register(NewGitWorktreeListTool())
	e.Register(NewGitWorktreeAddTool())
	e.Register(NewGitWorktreeRemoveTool())
	e.Register(NewGitCommitTool())
	e.Register(NewGHPullRequestTool())
	pvTool := NewPatchValidationTool()
	pvTool.SetEngine(e)
	e.Register(pvTool)
	benchTool := NewBenchmarkTool()
	benchTool.SetEngine(e)
	e.Register(benchTool)
	symRenameTool := NewSymbolRenameTool()
	symRenameTool.SetEngine(e)
	e.Register(symRenameTool)
	symMoveTool := NewSymbolMoveTool()
	symMoveTool.SetEngine(e)
	e.Register(symMoveTool)
	e.Register(NewSemanticSearchTool())
	e.Register(NewDiskUsageTool())
	e.Register(NewProjectInfoTool())
	e.Register(NewTaskSplitTool())
	e.delegateTool = NewDelegateTaskTool()
	e.Register(e.delegateTool)
	e.orchestrateTool = NewOrchestrateTool()
	e.orchestrateTool.SetMaxParallelCeiling(cfg.Agent.ParallelBatchSize)
	e.Register(e.orchestrateTool)
	timeout, err := time.ParseDuration(strings.TrimSpace(cfg.Tools.Shell.Timeout))
	if err != nil || timeout <= 0 {
		timeout, _ = time.ParseDuration(strings.TrimSpace(cfg.Security.Sandbox.Timeout))
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	e.Register(NewRunCommandTool(runCommandConfig{
		allowShell: cfg.Security.Sandbox.AllowShell,
		timeout:    timeout,
		blocked:    append([]string(nil), cfg.Tools.Shell.BlockedCommands...),
	}))
	RegisterMetaTools(e)
	// Register MCP bridge tools after native tools so the bridge can
	// shadow a native tool with the same name (caller wins — web/CLI
	// that already has native tools gets a flat union, not a replacement).
	if e.mcpBridge != nil {
		for _, td := range e.mcpBridge.List() {
			e.Register(&mcpToolAdapter{bridge: e.mcpBridge, name: td.Name})
		}
	}
	return e
}

// mcpToolAdapter exposes one MCP bridge tool as a tools.Tool so it
// appears in the same registry as native tools.
type mcpToolAdapter struct {
	bridge mcp.ToolBridge
	name   string
}

func (a *mcpToolAdapter) Name() string    { return a.name }
func (a *mcpToolAdapter) Description() string { return "MCP tool: " + a.name }

func (a *mcpToolAdapter) Spec() ToolSpec {
	return ToolSpec{
		Name:    a.name,
		Risk:    RiskExecute,
		Idempotent: true,
	}
}

func (a *mcpToolAdapter) Execute(ctx context.Context, req Request) (Result, error) {
	argBytes, _ := json.Marshal(req.Params)
	result, err := a.bridge.Call(ctx, a.name, argBytes)
	if err != nil {
		return Result{}, err
	}
	return Result{Output: result.Content[0].Text, Success: !result.IsError}, nil
}

// SetSubagentRunner wires the delegate_task and orchestrate tools to the
// engine's sub-agent entry point. Engines call this once the agent loop is
// fully constructed.
func (e *Engine) SetSubagentRunner(r SubagentRunner) {
	if e.delegateTool != nil {
		e.delegateTool.SetRunner(r)
	}
	if e.orchestrateTool != nil {
		e.orchestrateTool.SetRunner(r)
	}
}

// MetaSpecs returns the 4 meta-tool specs (tool_search, tool_help, tool_call,
// tool_batch_call). Provider serializers send only these to the model; the
// rest of the registry stays backend-only and is reached via tool_call.
func (e *Engine) MetaSpecs() []ToolSpec {
	out := make([]ToolSpec, 0, 4)
	for _, name := range []string{"tool_search", "tool_help", "tool_call", "tool_batch_call"} {
		if spec, ok := e.Spec(name); ok {
			out = append(out, spec)
		}
	}
	return out
}

// TodoSnapshot returns the current todo list recorded by the todo_write
// tool, or nil when the tool is not registered. Safe for concurrent use;
// the slice returned is a copy, not the live state.
func (e *Engine) TodoSnapshot() []TodoItem {
	if e == nil {
		return nil
	}
	tool, ok := e.Get("todo_write")
	if !ok {
		return nil
	}
	tw, ok := tool.(*TodoWriteTool)
	if !ok {
		return nil
	}
	return tw.Snapshot()
}

// BackendSpecs returns every spec EXCEPT the meta tools. Useful for status
// output, docs, and tests that want to see what the registry actually
// contains.
func (e *Engine) BackendSpecs() []ToolSpec {
	all := e.Specs()
	out := make([]ToolSpec, 0, len(all))
	for _, s := range all {
		if isMetaTool(s.Name) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func (e *Engine) Register(tool Tool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.registry[tool.Name()] = tool
}

func (e *Engine) Get(name string) (Tool, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	t, ok := e.registry[name]
	return t, ok
}

func (e *Engine) List() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, 0, len(e.registry))
	for name := range e.registry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

type toolCloser interface {
	Close() error
}

// Close releases per-tool cached state held for the life of the tools engine.
// Most tools are stateless, but AST-backed tools retain parse caches that can
// otherwise live until process exit in long-running TUI/web sessions.
func (e *Engine) Close() error {
	e.mu.RLock()
	// Registry is append-only at runtime; there is no Unregister path, so
	// taking a snapshot of closers under the read lock is sufficient.
	closers := make([]toolCloser, 0, len(e.registry))
	for _, tool := range e.registry {
		if closer, ok := tool.(toolCloser); ok {
			closers = append(closers, closer)
		}
	}
	e.mu.RUnlock()

	var errs []error
	for _, closer := range closers {
		if err := closer.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Specs returns a stable-sorted slice of ToolSpec for every registered tool.
// Tools that don't implement Specer get a synthetic spec derived from
// Name()/Description() with Risk=RiskRead. This is the entry point every
// provider serializer and the meta-tool surface read from.
func (e *Engine) Specs() []ToolSpec {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]ToolSpec, 0, len(e.registry))
	for _, tool := range e.registry {
		out = append(out, specForTool(tool))
	}
	SortSpecs(out)
	return out
}

// Spec returns the ToolSpec for a named tool, or (zero, false) if not found.
func (e *Engine) Spec(name string) (ToolSpec, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	tool, ok := e.registry[name]
	if !ok {
		return ToolSpec{}, false
	}
	return specForTool(tool), true
}

// Search ranks registered tools against a query and returns the top `limit`
// specs. Pass limit<=0 for all matches. Non-matching tools are omitted.
func (e *Engine) Search(query string, limit int) []ToolSpec {
	specs := e.Specs()
	type scored struct {
		spec  ToolSpec
		score int
	}
	ranked := make([]scored, 0, len(specs))
	for _, s := range specs {
		if score := ScoreMatch(s, query); score > 0 {
			ranked = append(ranked, scored{spec: s, score: score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].spec.Name < ranked[j].spec.Name
	})
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]ToolSpec, len(ranked))
	for i, r := range ranked {
		out[i] = r.spec
	}
	return out
}

func specForTool(tool Tool) ToolSpec {
	if s, ok := tool.(Specer); ok {
		spec := s.Spec()
		if spec.Risk == "" {
			spec.Risk = RiskRead
		}
		return spec
	}
	return ToolSpec{
		Name:    tool.Name(),
		Summary: tool.Description(),
		Risk:    RiskRead,
	}
}

func (e *Engine) Execute(ctx context.Context, name string, req Request) (Result, error) {
	start := time.Now()
	tool, ok := e.Get(name)
	if !ok {
		return Result{}, fmt.Errorf(
			"tool not found: %q. "+
				"Discover the right name with tool_search: "+
				`{"name":"tool_search","args":{"query":"%s"}}. `+
				"Common backend tools: read_file, write_file, edit_file, list_dir, grep_codebase, glob, find_symbol, codemap, ast_query, run_command, web_fetch, todo_write.",
			name, name)
	}

	projectRoot := strings.TrimSpace(req.ProjectRoot)
	if projectRoot == "" {
		cwd, _ := os.Getwd()
		projectRoot = cwd
	}
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve project root: %w", err)
	}
	req.ProjectRoot = absRoot
	req.Params = normalizeToolParams(name, req.Params)
	// Self-narration: peel off the optional `_reason` virtual field and
	// publish it before the call so UIs can render the why before the
	// tool result appears. We strip even when no publisher is installed
	// so tools never see the field as unexpected input.
	if reason, ok := ExtractReason(req.Params); ok {
		e.mu.RLock()
		pub := e.reasoningPublisher
		e.mu.RUnlock()
		if pub != nil {
			pub(name, reason)
		}
	}
	if mode := readBeforeMutationMode(name); mode != readGateNone {
		path := asString(req.Params, "path", "")
		absPath, err := EnsureWithinRoot(req.ProjectRoot, path)
		if err != nil {
			return Result{}, err
		}
		if err := e.ensureReadBeforeMutationMode(absPath, mode); err != nil {
			return Result{}, err
		}
	}
	failureKey := toolFailureKey(name, req.Params)

	res, err := tool.Execute(ctx, req)
	res.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		if n := e.trackFailure(failureKey); n >= 3 {
			return res, fmt.Errorf("tool %q failed repeatedly (%d times); change params or strategy", name, n)
		}
		return res, err
	}
	e.clearFailure(failureKey)
	res = e.compressToolOutput(req, res)
	e.recordReadSnapshot(name, req.ProjectRoot, req.Params, res)
	res.Success = true
	return res, nil
}

// readGateMode picks which read-before-mutate checks run for a given
// tool. "strict" enforces both the presence of a prior read_file
// snapshot AND hash equality; "lenient" only requires a prior snapshot
// and tolerates drift because the tool has its own per-call anchor
// validation (edit_file's exact-string match, for instance). "none"
// skips the gate entirely (tools that never touch existing files).
type readGateMode int

const (
	readGateNone readGateMode = iota
	readGateLenient
	readGateStrict
)

func readBeforeMutationMode(name string) readGateMode {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "edit_file":
		// edit_file refuses on its own when old_string doesn't match or
		// matches ambiguously. A hash-drift refusal at the gate added
		// noise without catching any case edit_file wouldn't already
		// catch — an editor/formatter touching the file between read
		// and edit tripped the gate even when the anchor was still a
		// perfectly safe unique match. Require a prior snapshot (so the
		// model has at least seen the file) but skip the hash check.
		return readGateLenient
	case "write_file":
		return readGateStrict
	default:
		return readGateNone
	}
}

// EnsureReadBeforeMutation exposes the per-file read-before-mutate gate
// for tools that mutate multiple files in one call (e.g. apply_patch).
// The per-`path` dispatch in Execute() only handles single-target tools;
// multi-target ones must thread each path through this method explicitly
// so a fabricated diff can't bypass the read snapshot check that
// edit_file / write_file already enforce. Callers that don't have a
// per-tool mode use strict — apply_patch has line-number-sensitive
// hunks that genuinely need the hash check.
func (e *Engine) EnsureReadBeforeMutation(absPath string) error {
	return e.ensureReadBeforeMutationMode(absPath, readGateStrict)
}

func (e *Engine) ensureReadBeforeMutationMode(absPath string, mode readGateMode) error {
	if mode == readGateNone {
		return nil
	}
	_, err := os.Stat(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // Creating a new file does not require prior read.
		}
		return err
	}

	e.readMu.RLock()
	lastReadHash, ok := e.readSnapshots[absPath]
	e.readMu.RUnlock()
	if !ok {
		return readGuardError(absPath, "missing")
	}
	if mode == readGateLenient {
		return nil
	}

	hash, err := fileContentHash(absPath)
	if err != nil {
		return err
	}
	if lastReadHash != hash {
		return readGuardError(absPath, "drift")
	}
	return nil
}

// readGuardError builds the actionable refusal returned by the read-
// before-mutate gate. Pre-2026-04-18 the error was a bare "modifying
// existing file requires prior read_file: PATH" or "file changed since
// last read_file; read again before modifying: PATH". Models that
// hadn't seen the snapshot rule before just retried the same edit and
// looped — the screenshots from this session caught it on real
// sessions. Post-fix the message embeds the literal recovery tool_call
// the model can emit verbatim, so the next round self-corrects in one
// step instead of N. `kind` is "missing" (no prior read at all) or
// "drift" (file modified between read and edit).
func readGuardError(absPath, kind string) error {
	relHint := absPath
	if cwd, err := os.Getwd(); err == nil {
		if rel, err2 := filepath.Rel(cwd, absPath); err2 == nil && !strings.HasPrefix(rel, "..") {
			relHint = filepath.ToSlash(rel)
		}
	}
	example := fmt.Sprintf(`{"name":"read_file","args":{"path":%q}}`, relHint)
	switch kind {
	case "drift":
		return fmt.Errorf(
			"edit refused: %s changed on disk since your last read_file (an editor, formatter, "+
				"or another tool wrote to it). The snapshot you held is now stale — apply the diff "+
				"against the current bytes by re-reading first: %s. Then retry your edit/apply_patch "+
				"with the same arguments.",
			relHint, example)
	default:
		return fmt.Errorf(
			"edit refused: %s has no prior read_file snapshot in this session. The engine requires "+
				"you to read a file before mutating it (so the model is editing what's actually on "+
				"disk, not a guess). Recover by calling: %s. Then retry your edit/apply_patch.",
			relHint, example)
	}
}

func (e *Engine) trackFailure(key string) int {
	e.failureMu.Lock()
	defer e.failureMu.Unlock()
	if _, ok := e.recentFailures[key]; !ok {
		e.recentFailOrder = append(e.recentFailOrder, key)
	}
	e.recentFailures[key]++
	// M3: evict oldest entries when the map grows too large. Map
	// iteration order is randomized, so deleting arbitrary keys made the
	// retry gate nondeterministic across identical runs.
	if len(e.recentFailures) > maxRecentFailures {
		target := maxRecentFailures / 2
		for len(e.recentFailures) > target && len(e.recentFailOrder) > 0 {
			oldest := e.recentFailOrder[0]
			e.recentFailOrder = e.recentFailOrder[1:]
			delete(e.recentFailures, oldest)
		}
	}
	return e.recentFailures[key]
}

func (e *Engine) clearFailure(key string) {
	e.failureMu.Lock()
	defer e.failureMu.Unlock()
	delete(e.recentFailures, key)
	for i, existing := range e.recentFailOrder {
		if existing == key {
			e.recentFailOrder = append(e.recentFailOrder[:i], e.recentFailOrder[i+1:]...)
			break
		}
	}
}

// Extracted to params.go — see engine.go:495.
// Extracted to output.go — see engine.go:737.

func toolFailureKey(name string, params map[string]any) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(strings.ToLower(strings.TrimSpace(name)))
	for _, k := range keys {
		b.WriteString("|")
		b.WriteString(strings.TrimSpace(k))
		b.WriteString("=")
		b.WriteString(canonicalToolFailureValue(params[k]))
	}
	return b.String()
}

func canonicalToolFailureValue(v any) string {
	switch typed := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	}
	raw, err := json.Marshal(v)
	if err == nil {
		return strings.TrimSpace(string(raw))
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func (e *Engine) recordReadSnapshot(name, projectRoot string, params map[string]any, res Result) {
	toolName := strings.ToLower(strings.TrimSpace(name))
	switch toolName {
	case "read_file", "write_file", "edit_file":
	default:
		return
	}
	p := strings.TrimSpace(asString(res.Data, "path", ""))
	if p == "" {
		p = strings.TrimSpace(asString(params, "path", ""))
	}
	if p == "" {
		return
	}
	abs, err := EnsureWithinRoot(projectRoot, p)
	if err != nil {
		return
	}

	// H1 fix: for read_file, hash the content already in memory to avoid
	// TOCTOU race and double I/O (M1). For write/edit, the file on disk
	// IS authoritative, so hash from disk.
	var hash string
	if toolName == "read_file" {
		// Prefer the full-file hash emitted by ReadFileTool over
		// sha256(res.Output). The Output field carries only the returned
		// line window (default 200 lines), so hashing it would produce a
		// slice-hash that can never match fileContentHash(abs) at the
		// strict gate - any write_file / apply_patch after a sliced read
		// would be refused as "drift" even when nothing had changed.
		if fullHash := asString(res.Data, "content_sha256", ""); fullHash != "" {
			hash = fullHash
		} else {
			sum := sha256.Sum256([]byte(res.Output))
			hash = hex.EncodeToString(sum[:])
		}
	} else {
		hash, err = fileContentHash(abs)
		if err != nil {
			return
		}
	}

	e.readMu.Lock()
	e.readSnapshots[abs] = hash
	e.touchReadSnapshotLocked(abs)
	e.readMu.Unlock()
}

func (e *Engine) touchReadSnapshotLocked(abs string) {
	if strings.TrimSpace(abs) == "" {
		return
	}
	for i, existing := range e.readSnapshotLRU {
		if existing == abs {
			e.readSnapshotLRU = append(e.readSnapshotLRU[:i], e.readSnapshotLRU[i+1:]...)
			break
		}
	}
	e.readSnapshotLRU = append(e.readSnapshotLRU, abs)
	if len(e.readSnapshots) > maxReadSnapshots {
		target := maxReadSnapshots / 2
		if target <= 0 {
			target = 1
		}
		for len(e.readSnapshots) > target && len(e.readSnapshotLRU) > 0 {
			evict := e.readSnapshotLRU[0]
			e.readSnapshotLRU = e.readSnapshotLRU[1:]
			delete(e.readSnapshots, evict)
		}
	}
	// Keep the snapshot map bounded even if the LRU drifted due to a
	// direct test mutation or a future cleanup path that removed map
	// entries without touching the LRU order.
	for len(e.readSnapshots) > maxReadSnapshots {
		for key := range e.readSnapshots {
			delete(e.readSnapshots, key)
			break
		}
	}
}

// fileContentHash moved to fileutil.go — see engine.go:600.

// compressToolOutput moved to output.go — see engine.go:609.

// resolveOutputByteLimit moved to output.go — see engine.go:631.

// parseByteLimit moved to output.go — see engine.go:641.

// collectRelevanceTerms moved to output.go — see engine.go:637.

// compressOutput moved to output.go — see engine.go:646.

// truncateUTF8ByBytes moved to output.go — see engine.go:681.

// minInt moved to output.go — see engine.go:738.

// maxInt moved to output.go — see engine.go:745.

// EnsureWithinRoot resolves `path` relative to `root` and refuses
// anything that escapes. Resistance comes from two layers:
//
//  1. Syntactic: filepath.Abs + filepath.Rel; any `..` prefix means
//     the path walks out of the root tree.
//  2. Symbolic: once the lexical check passes, resolve symlinks on
//     both `root` and `absPath` (via filepath.EvalSymlinks) and
//     re-check. This stops a committed symlink like
//     `project/evil -> /etc/passwd` from being reachable through the
//     tool API. If the target doesn't exist yet (e.g. write_file
//     creating a new file), we resolve the nearest existing ancestor
//     instead and re-run the containment check on that, so new-file
//     writes under a sanitary tree still work.
//
// PathRelativeToRoot turns an absolute path into a project-root-relative
// one for surfacing in tool Output / Data fields. Pre-2026-04-18 every
// read/write/edit_file leaked the host's full filesystem prefix
// (`C:\Users\...`, `/home/...`) into Data["path"], which then flowed
// into conversation logs, episodic memory, and any downstream
// transcript. The model never needs the absolute path — it operates in
// project-relative space. Falls back to the absolute path (slash-
// normalized) when filepath.Rel can't compute a relative form (e.g.
// different volume on Windows), so the model always gets SOME path.
func PathRelativeToRoot(root, abs string) string {
	if strings.TrimSpace(abs) == "" {
		return ""
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	rel, err := filepath.Rel(absRoot, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

func EnsureWithinRoot(root, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath := path
	if !filepath.IsAbs(path) {
		absPath = filepath.Join(absRoot, path)
	}
	absPath, err = filepath.Abs(absPath)
	if err != nil {
		return "", err
	}
	if !isPathWithin(absRoot, absPath) {
		return "", fmt.Errorf("path escapes project root: %s", path)
	}
	// Symlink check. Evaluate both sides so a root that is itself
	// /var/task (symlinked from /opt) still matches a path resolved
	// through the same symlink.
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve project root symlinks: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// Target doesn't exist yet (write_file creating new file) or
		// a dangling symlink. Walk up until we find an existing
		// ancestor and resolve that — any escape through a symlink
		// in the existing ancestor chain still gets caught.
		resolvedPath, err = resolveExistingAncestor(absPath)
		if err != nil {
			return "", fmt.Errorf("cannot resolve symlink ancestry for %q: %w", path, err)
		}
	}
	if !isPathWithin(resolvedRoot, resolvedPath) {
		return "", fmt.Errorf("path escapes project root via symlink: %s", path)
	}
	return absPath, nil
}

// isPathWithin reports whether target is at or under root using Rel —
// so it handles trailing-slash and case-insensitive-FS oddities via
// the same primitive the old check used.
func isPathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// resolveExistingAncestor walks upward from `absPath` until it finds
// a directory that exists, then returns its symlink-resolved form.
// This is the fallback used when the target of a write_file call
// doesn't exist yet — we still want to catch an attempt to write
// through `projectRoot/symlink-to-etc/newfile`.
func resolveExistingAncestor(absPath string) (string, error) {
	current := absPath
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor")
		}
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			// Resolved the existing ancestor. Check if the full target
			// path under the resolved ancestor is within the project root.
			// We reconstruct the path by appending the remaining relative
			// components so symlink-to-absolute-path escapes are caught.
			rel, err := filepath.Rel(parent, absPath)
			if err != nil {
				return "", fmt.Errorf("cannot compute relative path: %w", err)
			}
			reconstructed := filepath.Join(resolved, rel)
			return reconstructed, nil
		}
		current = parent
	}
}

// writeFileAtomic moved to fileutil.go — see engine.go:763.

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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
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
	return e
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
	if requiresReadBeforeMutation(name) {
		path := asString(req.Params, "path", "")
		absPath, err := EnsureWithinRoot(req.ProjectRoot, path)
		if err != nil {
			return Result{}, err
		}
		if err := e.ensureReadBeforeMutation(absPath); err != nil {
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

func requiresReadBeforeMutation(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "write_file", "edit_file":
		return true
	default:
		return false
	}
}

// EnsureReadBeforeMutation exposes the per-file read-before-mutate gate
// for tools that mutate multiple files in one call (e.g. apply_patch).
// The per-`path` dispatch in Execute() only handles single-target tools;
// multi-target ones must thread each path through this method explicitly
// so a fabricated diff can't bypass the read snapshot check that
// edit_file / write_file already enforce.
func (e *Engine) EnsureReadBeforeMutation(absPath string) error {
	return e.ensureReadBeforeMutation(absPath)
}

func (e *Engine) ensureReadBeforeMutation(absPath string) error {
	_, err := os.Stat(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // Creating a new file does not require prior read.
		}
		return err
	}
	hash, err := fileContentHash(absPath)
	if err != nil {
		return err
	}

	e.readMu.RLock()
	defer e.readMu.RUnlock()
	lastReadHash, ok := e.readSnapshots[absPath]
	if !ok {
		return readGuardError(absPath, "missing")
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

func normalizeToolParams(name string, params map[string]any) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read_file":
		promoteFirstAlias(params, "path", "file", "filepath", "target")
		promoteFirstAlias(params, "line_start", "start", "from", "lineStart", "start_line")
		promoteFirstAlias(params, "line_end", "end", "to", "lineEnd", "end_line")
		start := asInt(params, "line_start", 1)
		start = max(1, start)
		end := asInt(params, "line_end", start+199)
		end = max(start, end)
		if end-start+1 > 400 {
			end = start + 399
		}
		params["line_start"] = start
		params["line_end"] = end
	case "list_dir":
		promoteFirstAlias(params, "path", "dir", "directory", "target", "root")
		promoteFirstAlias(params, "max_entries", "limit", "max", "maxEntries")
		promoteFirstAlias(params, "recursive", "recurse")
		maxEntries := asInt(params, "max_entries", 200)
		if maxEntries <= 0 {
			maxEntries = 200
		}
		if maxEntries > 500 {
			maxEntries = 500
		}
		params["max_entries"] = maxEntries
	case "grep_codebase":
		promoteFirstAlias(params, "pattern", "query", "regex", "text", "needle", "search")
		promoteFirstAlias(params, "path", "dir", "directory", "root")
		promoteFirstAlias(params, "max_results", "limit", "max", "maxResults")
		promoteFirstAlias(params, "case_sensitive", "caseSensitive")
		promoteFirstAlias(params, "context", "context_lines", "contextLines")
		promoteFirstAlias(params, "before", "before_lines", "context_before")
		promoteFirstAlias(params, "after", "after_lines", "context_after")
		maxResults := asInt(params, "max_results", 80)
		if maxResults <= 0 {
			maxResults = 80
		}
		if maxResults > 500 {
			maxResults = 500
		}
		params["max_results"] = maxResults
	case "glob":
		promoteFirstAlias(params, "pattern", "glob", "query", "match")
		promoteFirstAlias(params, "path", "dir", "directory", "root")
		promoteFirstAlias(params, "max_results", "limit", "max", "maxResults")
	case "ast_query":
		promoteFirstAlias(params, "path", "file", "filepath", "target")
		promoteFirstAlias(params, "kind", "type", "symbol_kind")
		promoteFirstAlias(params, "name_contains", "name", "query", "filter", "contains")
	case "find_symbol":
		promoteFirstAlias(params, "name", "symbol", "query", "identifier")
		promoteFirstAlias(params, "kind", "type", "symbol_kind")
		promoteFirstAlias(params, "path", "dir", "directory", "file")
		promoteFirstAlias(params, "max_results", "limit", "max", "maxResults")
		promoteFirstAlias(params, "include_body", "body", "with_body")
	case "run_command":
		promoteFirstAlias(params, "command", "cmd", "program", "executable", "bin")
		promoteFirstAlias(params, "args", "argv", "arguments", "command_args")
		promoteFirstAlias(params, "dir", "cwd", "workdir", "working_dir")
		promoteFirstAlias(params, "timeout_ms", "timeoutMs", "timeout")
		timeoutMs := asInt(params, "timeout_ms", 0)
		timeoutMs = max(0, timeoutMs)
		timeoutMs = min(timeoutMs, 120_000)
		if timeoutMs > 0 {
			params["timeout_ms"] = timeoutMs
		}
	case "edit_file":
		promoteFirstAlias(params, "path", "file", "filepath", "target")
		promoteFirstAlias(params, "replace_all", "replaceAll", "all", "global")
		// Common typo trap: weaker models often emit `old`/`new` instead
		// of `old_string`/`new_string` (the JS/Python edit-tool conventions
		// they were trained on). Pre-fix the call hard-failed with the
		// self-teaching error, but the model often took 1-2 wasted rounds
		// to correct. Aliasing at the param-normalization layer means the
		// canonical names are always set when Execute runs — zero retries.
		// Only aliases — never overwrite an explicit canonical value.
		if _, ok := params["old_string"]; !ok {
			if v, alt := params["old"]; alt {
				params["old_string"] = v
				delete(params, "old")
			}
		}
		if _, ok := params["new_string"]; !ok {
			if v, alt := params["new"]; alt {
				params["new_string"] = v
				delete(params, "new")
			}
		}
	case "write_file":
		promoteFirstAlias(params, "path", "file", "filepath", "target")
		promoteFirstAlias(params, "overwrite", "force", "replace", "allow_overwrite")
		// Same family of typo: `content` vs `text`/`body`. Aliases keep
		// non-canonical names from looping the model on a missing-field
		// error.
		if _, ok := params["content"]; !ok {
			for _, alt := range []string{"text", "body", "data"} {
				if v, found := params[alt]; found {
					params["content"] = v
					delete(params, alt)
					break
				}
			}
		}
	}
	return params
}

func promoteFirstAlias(params map[string]any, canonical string, aliases ...string) {
	if params == nil {
		return
	}
	// Canonical wins silently when both keys are present; we only promote
	// from aliases when the canonical field is absent.
	if _, ok := params[canonical]; ok {
		return
	}
	for _, alt := range aliases {
		if v, ok := params[alt]; ok {
			params[canonical] = v
			delete(params, alt)
			return
		}
	}
}

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
		sum := sha256.Sum256([]byte(res.Output))
		hash = hex.EncodeToString(sum[:])
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

func fileContentHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (e *Engine) compressToolOutput(req Request, res Result) Result {
	limit := e.resolveOutputByteLimit(req.Params)
	if limit <= 0 || strings.TrimSpace(res.Output) == "" {
		return res
	}
	out, compressed, omittedLines := compressOutput(res.Output, limit, collectRelevanceTerms(req.Params))
	if !compressed {
		return res
	}
	if res.Data == nil {
		res.Data = map[string]any{}
	}
	res.Data["output_original_bytes"] = len([]byte(res.Output))
	res.Data["output_compressed_bytes"] = len([]byte(out))
	res.Data["output_omitted_lines"] = omittedLines
	res.Output = out
	res.Truncated = true
	return res
}

func (e *Engine) resolveOutputByteLimit(params map[string]any) int {
	if v := asInt(params, "max_output_bytes", 0); v > 0 {
		return v
	}
	if v := asInt(params, "max_output_chars", 0); v > 0 {
		return v
	}
	return parseByteLimit(e.cfg.Security.Sandbox.MaxOutput, 100*1024)
}

func parseByteLimit(raw string, fallback int) int {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" {
		return fallback
	}
	mult := 1
	switch {
	case strings.HasSuffix(s, "KB"):
		mult = 1024
		s = strings.TrimSpace(strings.TrimSuffix(s, "KB"))
	case strings.HasSuffix(s, "MB"):
		mult = 1024 * 1024
		s = strings.TrimSpace(strings.TrimSuffix(s, "MB"))
	case strings.HasSuffix(s, "B"):
		mult = 1
		s = strings.TrimSpace(strings.TrimSuffix(s, "B"))
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return fallback
	}
	return n * mult
}

func collectRelevanceTerms(params map[string]any) []string {
	if params == nil {
		return nil
	}
	keys := []string{"pattern", "query", "symbol", "name", "path"}
	seen := map[string]struct{}{}
	out := make([]string, 0, 8)
	for _, k := range keys {
		v := strings.TrimSpace(strings.ToLower(asString(params, k, "")))
		if v == "" {
			continue
		}
		for _, token := range strings.FieldsFunc(v, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '/' || r == ':' || r == ',' || r == ';' || r == '.'
		}) {
			t := strings.TrimSpace(token)
			if len(t) < 3 {
				continue
			}
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

func compressOutput(output string, limit int, terms []string) (string, bool, int) {
	if len([]byte(output)) <= limit || limit <= 0 {
		return output, false, 0
	}

	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return truncateUTF8ByBytes(output, limit), true, 0
	}

	headN, tailN := 20, 20
	keep := map[int]struct{}{}
	for i := 0; i < minInt(headN, len(lines)); i++ {
		keep[i] = struct{}{}
	}
	for i := maxInt(0, len(lines)-tailN); i < len(lines); i++ {
		keep[i] = struct{}{}
	}

	if len(terms) > 0 {
		for i, line := range lines {
			low := strings.ToLower(line)
			for _, t := range terms {
				if strings.Contains(low, t) {
					for j := maxInt(0, i-1); j <= minInt(len(lines)-1, i+1); j++ {
						keep[j] = struct{}{}
					}
					break
				}
			}
		}
	}

	ordered := make([]int, 0, len(keep))
	for idx := range keep {
		ordered = append(ordered, idx)
	}
	sort.Ints(ordered)

	var b strings.Builder
	omitted := 0
	prev := -1
	for _, idx := range ordered {
		if prev >= 0 && idx > prev+1 {
			gap := idx - prev - 1
			omitted += gap
			fmt.Fprintf(&b, "... [omitted %d lines]\n", gap)
		}
		b.WriteString(lines[idx])
		if idx < len(lines)-1 {
			b.WriteByte('\n')
		}
		prev = idx
	}

	compressed := b.String()
	if len([]byte(compressed)) > limit {
		compressed = truncateUTF8ByBytes(compressed, limit)
	}
	return compressed, true, omitted
}

func truncateUTF8ByBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	b := []byte(s)
	if len(b) <= maxBytes {
		return s
	}
	ellipsis := "\n... [truncated]"
	limit := maxBytes - len([]byte(ellipsis))
	if limit <= 0 {
		limit = maxBytes
		ellipsis = ""
	}
	var out strings.Builder
	n := 0
	for _, r := range s {
		rb := len([]byte(string(r)))
		if n+rb > limit {
			break
		}
		out.WriteRune(r)
		n += rb
	}
	if ellipsis != "" {
		out.WriteString(ellipsis)
	}
	return out.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

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
			return absPath, nil
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
			// Reached filesystem root without finding anything that
			// exists — unusual but not exploitable.
			return "", fmt.Errorf("no existing ancestor")
		}
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return resolved, nil
		}
		current = parent
	}
}

// writeFileAtomic replaces `path`'s contents with `data` such that a
// crash mid-write can never leave the destination truncated or
// half-written. Implementation: write to a sibling temp file on the
// same filesystem, fsync, then rename over the target. os.Rename is
// atomic on both POSIX and NTFS, so any reader either sees the
// previous contents or the new contents — never the in-progress
// state.
//
// The temp file lives in the same directory as the target so the
// rename stays on one filesystem. If the rename fails, we best-effort
// delete the temp to avoid leaving `.filename.dfmc-tmp.XXXXXX` debris
// behind. `perm` controls the final file's permissions; the temp is
// created with the same mode so the rename doesn't downgrade it.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".dfmc-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for atomic write: %w", err)
	}
	tmpPath := tmp.Name()
	// Ensure we always clean up the temp unless the rename succeeds.
	cleanup := func() {
		_ = os.Remove(tmpPath)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp: %w", err)
	}
	// fsync — not every OS/filesystem requires it, but POSIX semantics
	// say that without fsync a crash could lose the data even after
	// rename. Cheap insurance.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp: %w", err)
	}
	// Align temp's mode to `perm`. CreateTemp uses 0o600 by default; a
	// call expecting a 0o644 world-readable config would otherwise end
	// up with tighter-than-requested permissions.
	if err := os.Chmod(tmpPath, perm); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename temp to target: %w", err)
	}
	return nil
}

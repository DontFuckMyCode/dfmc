package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

type Engine struct {
	mu             sync.RWMutex
	registry       map[string]Tool
	cfg            config.Config
	failureMu      sync.Mutex
	recentFailures map[string]int
	readMu         sync.Mutex
	readSnapshots  map[string]string
	delegateTool    *DelegateTaskTool
	orchestrateTool *OrchestrateTool
}

func New(cfg config.Config) *Engine {
	e := &Engine{
		registry:       map[string]Tool{},
		cfg:            cfg,
		recentFailures: map[string]int{},
		readSnapshots:  map[string]string{},
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
	e.Register(NewApplyPatchTool())
	e.Register(NewGitStatusTool())
	e.Register(NewGitDiffTool())
	e.Register(NewGitBranchTool())
	e.Register(NewGitLogTool())
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
		return Result{}, fmt.Errorf("tool not found: %s", name)
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
	e.recordReadSnapshot(name, req.Params, res)
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

	e.readMu.Lock()
	defer e.readMu.Unlock()
	lastReadHash, ok := e.readSnapshots[absPath]
	if !ok {
		return fmt.Errorf("modifying existing file requires prior read_file: %s", absPath)
	}
	if lastReadHash != hash {
		return fmt.Errorf("file changed since last read_file; read again before modifying: %s", absPath)
	}
	return nil
}

func (e *Engine) trackFailure(key string) int {
	e.failureMu.Lock()
	defer e.failureMu.Unlock()
	e.recentFailures[key]++
	return e.recentFailures[key]
}

func (e *Engine) clearFailure(key string) {
	e.failureMu.Lock()
	defer e.failureMu.Unlock()
	delete(e.recentFailures, key)
}

func normalizeToolParams(name string, params map[string]any) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read_file":
		start := asInt(params, "line_start", 1)
		if start < 1 {
			start = 1
		}
		end := asInt(params, "line_end", start+199)
		if end < start {
			end = start
		}
		if end-start+1 > 400 {
			end = start + 399
		}
		params["line_start"] = start
		params["line_end"] = end
	case "list_dir":
		maxEntries := asInt(params, "max_entries", 200)
		if maxEntries <= 0 {
			maxEntries = 200
		}
		if maxEntries > 500 {
			maxEntries = 500
		}
		params["max_entries"] = maxEntries
	case "grep_codebase":
		maxResults := asInt(params, "max_results", 80)
		if maxResults <= 0 {
			maxResults = 80
		}
		if maxResults > 500 {
			maxResults = 500
		}
		params["max_results"] = maxResults
	case "run_command":
		timeoutMs := asInt(params, "timeout_ms", 0)
		if timeoutMs < 0 {
			timeoutMs = 0
		}
		if timeoutMs > 120_000 {
			timeoutMs = 120_000
		}
		if timeoutMs > 0 {
			params["timeout_ms"] = timeoutMs
		}
	}
	return params
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
		b.WriteString(strings.TrimSpace(fmt.Sprint(params[k])))
	}
	return b.String()
}

func (e *Engine) recordReadSnapshot(name string, params map[string]any, res Result) {
	toolName := strings.ToLower(strings.TrimSpace(name))
	switch toolName {
	case "read_file":
		p := strings.TrimSpace(asString(res.Data, "path", ""))
		if p == "" {
			p = strings.TrimSpace(asString(params, "path", ""))
		}
		if p == "" {
			return
		}
		hash, err := fileContentHash(p)
		if err != nil {
			return
		}
		e.readMu.Lock()
		e.readSnapshots[p] = hash
		e.readMu.Unlock()
	case "write_file", "edit_file":
		p := strings.TrimSpace(asString(res.Data, "path", ""))
		if p == "" {
			p = strings.TrimSpace(asString(params, "path", ""))
		}
		if p == "" {
			return
		}
		hash, err := fileContentHash(p)
		if err != nil {
			return
		}
		e.readMu.Lock()
		e.readSnapshots[p] = hash
		e.readMu.Unlock()
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
			b.WriteString(fmt.Sprintf("... [omitted %d lines]\n", gap))
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
	if len([]byte(compressed)) > limit {
		compressed = string([]byte(compressed)[:limit])
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
		// Root should exist; if EvalSymlinks fails we just skip the
		// symbolic check. Better to accept a path that passed the
		// lexical check than refuse every call on a weird filesystem.
		return absPath, nil
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
